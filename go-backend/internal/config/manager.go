// Package config owns configuration defaults, migration of the legacy JSON
// shape, validation and normalization. Business code never opens config files.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

type Manager struct {
	store store.Store
	mu    sync.RWMutex
	cfg   model.Config
}

// Reader is the read-only configuration boundary used by business modules.
type Reader interface {
	Snapshot() model.Config
}

func NewManager(s store.Store) (*Manager, error) {
	if s == nil {
		return nil, errors.New("config store is nil")
	}
	raw, err := s.LoadConfig()
	if err != nil {
		return nil, err
	}
	cfg := merge(model.DefaultConfig(), raw)
	if err := Normalize(cfg); err != nil {
		return nil, err
	}
	return &Manager{store: s, cfg: cfg}, nil
}

func (m *Manager) Store() store.Store { return m.store }

func (m *Manager) Snapshot() model.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return clone(m.cfg)
}

// MergeConfig applies a null-ignoring configuration patch to a base snapshot.
// It is exported for backup/import validation; business modules should use
// Manager.Update for ordinary live changes.
func MergeConfig(base, patch model.Config) model.Config { return merge(base, patch) }

// Reload makes an imported backup visible to the running process without
// changing the persisted data again. Callers should reload dependent domains
// (subscriptions, caches) after this method succeeds.
func (m *Manager) Reload() error {
	raw, err := m.store.LoadConfig()
	if err != nil {
		return err
	}
	cfg := merge(model.DefaultConfig(), raw)
	if err := Normalize(cfg); err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	return nil
}

// PublicSnapshot removes secrets exactly where the current ConfigService does:
// the UI can display and edit the rest of the configuration without learning
// implementation details of the Go runtime.
func (m *Manager) PublicSnapshot() model.Config {
	cfg := m.Snapshot()
	// runtimeOwnership is an internal cutover/locking detail. It is persisted
	// so the gateway can keep migration state, but Java never exposed it from
	// /api/config and the UI has no field for it.
	delete(cfg, "runtimeOwnership")
	if login, ok := cfg["login"].(map[string]any); ok {
		login["password"] = ""
	}
	cfg["jwtKey"] = ""
	return cfg
}

// Update applies a JSON object as a null-ignoring patch, matching the Java
// BeanUtil copy semantics used by setConfig. Unknown fields are retained.
func (m *Manager) Update(input model.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := clone(m.cfg)
	next := merge(m.cfg, input)
	// The current UI receives a redacted password and posts that empty value
	// back with ordinary settings. An empty credential therefore means
	// "unchanged", while a non-empty value is an intentional replacement.
	if incomingLogin, ok := input["login"].(map[string]any); ok {
		storedLogin, _ := m.cfg["login"].(map[string]any)
		if strings.TrimSpace(stringValue(incomingLogin["password"])) == "" {
			nextLogin, _ := next["login"].(map[string]any)
			nextLogin["password"] = storedLogin["password"]
		}
		if strings.TrimSpace(stringValue(incomingLogin["username"])) == "" {
			nextLogin, _ := next["login"].(map[string]any)
			nextLogin["username"] = storedLogin["username"]
		}
	}
	if err := Normalize(next); err != nil {
		return err
	}
	oldLogin := objectString(previous["login"], "username") + "\x00" + objectString(previous["login"], "password")
	newLogin := objectString(next["login"], "username") + "\x00" + objectString(next["login"], "password")
	if oldLogin != newLogin {
		next["tokenId"] = newID()
	}
	// jwtKey is never accepted from the browser; preserving the old key keeps
	// existing sessions valid when ordinary settings are saved.
	if key, ok := previous["jwtKey"]; ok {
		if _, supplied := input["jwtKey"]; supplied {
			next["jwtKey"] = key
		}
	}
	if err := m.store.SaveConfig(next); err != nil {
		return err
	}
	m.cfg = next
	return nil
}

func Normalize(cfg model.Config) error {
	// Java is no longer a runtime option after the final cutover. Preserve the
	// field for older UI/config exports, but make the active owner unambiguous.
	cfg["runtimeOwnership"] = map[string]any{"rss": "go", "rename": "go", "maintenance": "go"}
	for _, key := range []string{"mikanHost", "tmdbApi", "tmdbImage", "bgmApi", "downloadToolHost"} {
		if value, ok := cfg[key].(string); ok {
			normalized, err := normalizeURL(value)
			if err != nil {
				return fmt.Errorf("%s 地址异常: %w", key, err)
			}
			cfg[key] = normalized
		}
	}
	for _, key := range []string{"downloadPathTemplate", "ovaDownloadPathTemplate", "completedPathTemplate"} {
		if value, ok := cfg[key].(string); ok && strings.TrimSpace(value) != "" {
			path, err := filepath.Abs(filepath.Clean(value))
			if err != nil {
				return fmt.Errorf("normalize %s: %w", key, err)
			}
			cfg[key] = path
		}
	}
	if template, ok := cfg["notificationTemplate"].(string); ok {
		// ConfigUtil.format() trims the global template before it is exposed.
		cfg["notificationTemplate"] = strings.TrimSpace(template)
	}
	normalizeNotificationConfigs(cfg)
	if enabled, _ := cfg["proxy"].(bool); enabled {
		host := stringValue(cfg["proxyHost"])
		port := intValue(cfg["proxyPort"])
		if host == "" || port < 1 || port > 65535 {
			return errors.New("代理参数不完整")
		}
	}
	if timeout := intValue(cfg["rssTimeout"]); timeout < 1 {
		cfg["rssTimeout"] = 20
	}
	if interval := intValue(cfg["rssSleepMinutes"]); interval < 1 {
		cfg["rssSleepMinutes"] = 1
	}
	if interval := intValue(cfg["renameSleepSeconds"]); interval < 1 {
		cfg["renameSleepSeconds"] = 1
	}
	if login, ok := cfg["login"].(map[string]any); ok {
		if strings.TrimSpace(objectString(login, "username")) == "" {
			login["username"] = "admin"
		}
		if strings.TrimSpace(objectString(login, "password")) == "" {
			return errors.New("登录密码不能为空")
		}
	}
	return nil
}

// normalizeNotificationConfigs fills fields omitted by older notification
// configuration files. Java's ConfigUtil.format() copies non-null fields from
// NotificationConfig.createNotificationConfig() with override=false; merging
// the defaults underneath each object is the equivalent JSON operation.
func normalizeNotificationConfigs(cfg model.Config) {
	raw, exists := cfg["notificationConfigList"]
	if !exists || raw == nil {
		cfg["notificationConfigList"] = []any{}
		return
	}
	list, ok := raw.([]any)
	if !ok {
		if typed, typedOK := raw.([]map[string]any); typedOK {
			list = make([]any, 0, len(typed))
			for _, item := range typed {
				list = append(list, item)
			}
		} else {
			return
		}
	}
	defaults := defaultNotificationConfig()
	normalized := make([]any, 0, len(list))
	for _, rawItem := range list {
		item, ok := rawItem.(map[string]any)
		if !ok {
			normalized = append(normalized, rawItem)
			continue
		}
		normalized = append(normalized, merge(defaults, item))
	}
	cfg["notificationConfigList"] = normalized
}

func defaultNotificationConfig() model.Config {
	return model.Config{
		"enable": true, "retry": 3, "sort": 10, "comment": "", "notificationTemplate": "${notification}",
		"notificationType": "TELEGRAM", "statusList": []any{"DOWNLOAD_START", "OMIT", "ERROR"},
		"mailSMTPHost": "smtp.qq.com", "mailSMTPPort": 465, "mailFrom": "", "mailPassword": "", "mailSSLEnable": true, "mailTLSEnable": false, "mailAddressee": "", "mailImage": true,
		"serverChanType": "SERVER_CHAN", "serverChanSendKey": "", "serverChan3ApiUrl": "", "serverChanTitleAction": true,
		"telegramBotToken": "", "telegramChatId": "", "telegramTopicId": -1, "telegramApiHost": "https://api.telegram.org", "telegramImage": true, "telegramFormat": "",
		"webHookMethod": "POST", "webHookUrl": "", "webHookHeader": "", "webHookBody": "",
		// embyHost is intentionally omitted here. Java's default
		// NotificationConfig leaves it null and Gson omits null fields.
		// Existing non-empty embyHost values are still preserved by merge.
		"embyRefresh": false, "embyApiKey": "", "embyRefreshViewIds": []any{}, "embyDelayed": 0,
		"shell": "", "aliveLimit": 10,
		"fileMoveTarget": "/CD2/115/Media/番剧/${title}/Season ${season}", "fileMoveOvaTarget": "/CD2/115/Media/剧场版/${title}", "fileMoveDeleteOldEpisode": false, "fileMoveCopyModel": false,
		"openListUploadHost": "http://127.0.0.1:5244", "openListUploadApiKey": "", "openListUploadPath": "/115/Media/番剧/${title}/Season ${season}", "openListUploadOvaPath": "/115/Media/剧场版/${title}", "openListUploadDeleteLocalFile": false, "openListUploadDeleteOldEpisode": false,
		"barkServerUrl": "https://api.day.app", "barkDeviceKeys": []any{}, "barkGroup": "ani-rss", "barkUseMarkdown": false, "barkLevel": "active", "barkVolume": 5,
	}
}

func ProxyURL(cfg model.Config) (*url.URL, error) {
	if enabled, _ := cfg["proxy"].(bool); !enabled {
		return nil, nil
	}
	host := stringValue(cfg["proxyHost"])
	port := intValue(cfg["proxyPort"])
	if host == "" || port < 1 || port > 65535 {
		return nil, errors.New("代理参数不完整")
	}
	scheme := "http"
	if strings.Contains(host, "://") {
		scheme = ""
	}
	address := host
	if scheme != "" {
		address = scheme + "://" + host
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, fmt.Errorf("parse proxy: %w", err)
	}
	if parsed.Port() == "" {
		parsed.Host = fmt.Sprintf("%s:%d", parsed.Host, port)
	}
	if username := stringValue(cfg["proxyUsername"]); username != "" {
		parsed.User = url.UserPassword(username, stringValue(cfg["proxyPassword"]))
	}
	return parsed, nil
}

func String(cfg model.Config, key string) string { return stringValue(cfg[key]) }

func Bool(cfg model.Config, key string) bool {
	v, ok := cfg[key].(bool)
	return ok && v
}

func Int(cfg model.Config, key string) int { return intValue(cfg[key]) }

func Strings(cfg model.Config, key string) []string {
	values, ok := cfg[key].([]any)
	if !ok {
		if typed, ok := cfg[key].([]string); ok {
			return append([]string(nil), typed...)
		}
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if stringValue(value) != "" {
			result = append(result, stringValue(value))
		}
	}
	return result
}

func merge(base, patch model.Config) model.Config {
	result := clone(base)
	for key, value := range patch {
		if value == nil {
			continue
		}
		if patchObject, ok := value.(map[string]any); ok {
			if baseObject, ok := result[key].(map[string]any); ok {
				result[key] = merge(baseObject, patchObject)
				continue
			}
		}
		result[key] = value
	}
	return result
}

// clone deep-copies the config map. Snapshot() is on the hot path (HTTP
// handlers, per-resource refresh, per-file scrape), so this must not round-trip
// through JSON — a recursive map/slice copy is O(n) with no marshal overhead
// and preserves the same shape the JSON round-trip produced (all values in a
// Config are already JSON-compatible maps, slices, strings, numbers, bools).
func clone(value any) model.Config {
	src, _ := value.(model.Config)
	result := make(model.Config, len(src))
	for key, item := range src {
		result[key] = cloneValue(item)
	}
	return result
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = cloneValue(v)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = cloneValue(v)
		}
		return out
	default:
		return typed
	}
}

func objectString(value any, key string) string {
	object, _ := value.(map[string]any)
	return stringValue(object[key])
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		v, _ := typed.Int64()
		return int(v)
	default:
		return 0
	}
}

func normalizeURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return value, nil
	}
	if !strings.HasPrefix(strings.ToLower(value), "http://") && !strings.HasPrefix(strings.ToLower(value), "https://") {
		value = "http://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("必须是有效的 HTTP/HTTPS 地址")
	}
	return strings.TrimRight(value, "/"), nil
}

func newID() string {
	data := make([]byte, 16)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}
