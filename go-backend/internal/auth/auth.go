// Package auth implements the observable authentication rules of the current
// service: MD5 input from the UI, signed sessions, API keys and IP rules.
package auth

import (
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

var (
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrLoginExpired       = errors.New("登录已失效")
	ErrTooManyAttempts    = errors.New("失败次数过多，已限制登录")
)

type Authenticator struct {
	config    *appconfig.Manager
	mu        sync.Mutex
	sessionID string
	attempts  map[string]attempt
}

type attempt struct {
	count   int
	expires time.Time
}

func New(config *appconfig.Manager) *Authenticator {
	return &Authenticator{config: config, sessionID: "-", attempts: make(map[string]attempt)}
}

func (a *Authenticator) Login(request *http.Request, input model.Login) (string, error) {
	ip := a.ClientIP(request)
	if a.isLimited(ip) {
		return "", ErrTooManyAttempts
	}
	cfg := a.config.Snapshot()
	login, _ := cfg["login"].(map[string]any)
	if strings.TrimSpace(input.Username) == "" || strings.TrimSpace(input.Password) == "" {
		a.recordFailure(ip)
		return "", errors.New("用户名或密码不能为空")
	}
	if subtle.ConstantTimeCompare([]byte(input.Username), []byte(appconfig.String(login, "username"))) != 1 ||
		subtle.ConstantTimeCompare([]byte(input.Password), []byte(appconfig.String(login, "password"))) != 1 {
		a.recordFailure(ip)
		return "", ErrInvalidCredentials
	}
	a.mu.Lock()
	if appconfig.Bool(cfg, "multiLoginForbidden") {
		a.sessionID = newID()
	} else {
		a.sessionID = "-"
	}
	sessionID := a.sessionID
	a.mu.Unlock()
	a.clearFailure(ip)

	expire := int64(0)
	if hours := appconfig.Int(cfg, "loginEffectiveHours"); hours > 0 {
		expire = time.Now().Add(time.Duration(hours) * time.Hour).UnixMilli()
	}
	claims := map[string]any{"sessionId": sessionID, "expireTime": expire, "tokenId": appconfig.String(cfg, "tokenId"), "ip": ip}
	return a.sign(claims, cfg)
}

func (a *Authenticator) Authorized(request *http.Request) bool {
	if a.IsLoginLimited(request) {
		return false
	}
	if a.IPWhitelist(request) || a.APIKey(request) {
		return true
	}
	token := request.Header.Get("Authorization")
	if token == "" {
		if request.URL != nil {
			token = request.URL.Query().Get("s")
		}
	}
	if a.Verify(token, request) {
		return true
	}
	a.recordFailure(a.ClientIP(request))
	return false
}

func (a *Authenticator) IsLoginLimited(request *http.Request) bool {
	return a.isLimited(a.ClientIP(request))
}

func (a *Authenticator) Verify(token string, request *http.Request) bool {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[len("Bearer "):])
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	cfg := a.config.Snapshot()
	key, err := base64.StdEncoding.DecodeString(appconfig.String(cfg, "jwtKey"))
	if err != nil || len(key) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || subtle.ConstantTimeCompare(mac.Sum(nil), provided) != 1 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return false
	}
	if stringClaim(claims, "tokenId") != appconfig.String(cfg, "tokenId") {
		return false
	}
	a.mu.Lock()
	session := a.sessionID
	a.mu.Unlock()
	if stringClaim(claims, "sessionId") != session {
		return false
	}
	if expiry := numberClaim(claims, "expireTime"); expiry > 0 && expiry < time.Now().UnixMilli() {
		return false
	}
	if appconfig.Bool(cfg, "verifyLoginIp") && stringClaim(claims, "ip") != a.ClientIP(request) {
		return false
	}
	return true
}

func (a *Authenticator) IPWhitelist(request *http.Request) bool {
	cfg := a.config.Snapshot()
	if !appconfig.Bool(cfg, "ipWhitelist") {
		return false
	}
	ip := a.ClientIP(request)
	if ip == "" {
		return false
	}
	for _, rule := range strings.Split(appconfig.String(cfg, "ipWhitelistStr"), "\n") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		if rule == ip || wildcardIP(rule, ip) || rangeIP(rule, ip) {
			return true
		}
		if network, err := netip.ParsePrefix(rule); err == nil {
			if address, err := netip.ParseAddr(ip); err == nil && network.Contains(address) {
				return true
			}
		}
	}
	return false
}

func (a *Authenticator) APIKey(request *http.Request) bool {
	key := appconfig.String(a.config.Snapshot(), "apiKey")
	if key == "" {
		return false
	}
	query := make(url.Values)
	if request.URL != nil {
		query = request.URL.Query()
	}
	for _, candidate := range []string{headerValue(request.Header, "api-key"), headerValue(request.Header, "x-api-key"), headerValue(request.Header, "s"), query.Get("api-key"), query.Get("x-api-key"), query.Get("s")} {
		if candidate != "" && subtle.ConstantTimeCompare([]byte(key), []byte(candidate)) == 1 {
			return true
		}
	}
	return false
}

func headerValue(header http.Header, name string) string {
	if value := header.Get(name); value != "" {
		return value
	}
	for key, values := range header {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func (a *Authenticator) ClientIP(request *http.Request) string {
	remote := request.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	cfg := a.config.Snapshot()
	if !appconfig.Bool(cfg, "reverseProxyTrustIpListEnabled") || !contains(appconfig.Strings(cfg, "reverseProxyTrustIpList"), remote) {
		return remote
	}
	forwarded := request.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return remote
}

func (a *Authenticator) InnerIP(request *http.Request) bool {
	ip := net.ParseIP(a.ClientIP(request))
	if ip == nil || ip.To4() == nil {
		return false
	}
	private := []*net.IPNet{}
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16"} {
		_, network, _ := net.ParseCIDR(cidr)
		private = append(private, network)
	}
	for _, network := range private {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *Authenticator) isLimited(ip string) bool {
	if !appconfig.Bool(a.config.Snapshot(), "limitLoginAttempts") {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.attempts[ip]
	if !ok || time.Now().After(entry.expires) {
		delete(a.attempts, ip)
		return false
	}
	return entry.count >= 30
}

func (a *Authenticator) recordFailure(ip string) {
	if !appconfig.Bool(a.config.Snapshot(), "limitLoginAttempts") {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.attempts[ip]
	if time.Now().After(entry.expires) {
		entry = attempt{expires: time.Now().Add(24 * time.Hour)}
	}
	entry.count++
	a.attempts[ip] = entry
}

func (a *Authenticator) clearFailure(ip string) {
	a.mu.Lock()
	delete(a.attempts, ip)
	a.mu.Unlock()
}

func (a *Authenticator) sign(claims map[string]any, cfg model.Config) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	key, err := base64.StdEncoding.DecodeString(appconfig.String(cfg, "jwtKey"))
	if err != nil {
		return "", fmt.Errorf("decode JWT key: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(header + "." + body))
	return header + "." + body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func newID() string {
	b := make([]byte, 16)
	// A session ID need only be unpredictable within the lifetime of the
	// process; crypto/rand failure is represented by a time-based fallback.
	if _, err := cryptorand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func wildcardIP(pattern, ip string) bool {
	patternParts, ipParts := strings.Split(pattern, "."), strings.Split(ip, ".")
	if len(patternParts) != 4 || len(ipParts) != 4 {
		return false
	}
	for i := range patternParts {
		if patternParts[i] != "*" && patternParts[i] != ipParts[i] {
			return false
		}
	}
	return true
}

func rangeIP(rule, ip string) bool {
	parts := strings.Split(rule, "-")
	if len(parts) != 2 {
		return false
	}
	start, err1 := netip.ParseAddr(strings.TrimSpace(parts[0]))
	end, err2 := netip.ParseAddr(strings.TrimSpace(parts[1]))
	target, err3 := netip.ParseAddr(ip)
	return err1 == nil && err2 == nil && err3 == nil && start.Compare(target) <= 0 && target.Compare(end) <= 0
}

func stringClaim(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return value
}

func numberClaim(claims map[string]any, key string) int64 {
	value, ok := claims[key].(float64)
	if ok {
		return int64(value)
	}
	return 0
}
