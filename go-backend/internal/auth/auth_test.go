package auth_test

import (
	"crypto/md5"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/auth"
	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

func TestAuthenticatorSupportsLegacyMD5LoginAndBearerToken(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultConfig()
	cfg["login"] = map[string]any{"username": "admin", "password": fmt.Sprintf("%x", md5.Sum([]byte("secret")))}
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.New(m)
	r := &http.Request{RemoteAddr: "192.168.1.8:1234"}
	token, err := a.Login(r, model.Login{Username: "admin", Password: fmt.Sprintf("%x", md5.Sum([]byte("secret")))})
	if err != nil {
		t.Fatal(err)
	}
	r.Header = http.Header{"Authorization": []string{"Bearer " + token}}
	if !a.Authorized(r) {
		t.Fatal("valid token was rejected")
	}
	if a.Authorized(&http.Request{RemoteAddr: r.RemoteAddr, Header: http.Header{"Authorization": []string{"Bearer invalid"}}}) {
		t.Fatal("invalid token accepted")
	}
}

func TestAuthenticatorSupportsAPIKeyAndIPRules(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultConfig()
	cfg["apiKey"] = "key"
	cfg["ipWhitelist"] = true
	cfg["ipWhitelistStr"] = "10.0.*.*\n192.168.1.1-192.168.1.9"
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.New(m)
	if !a.Authorized(&http.Request{RemoteAddr: "10.0.2.3:123"}) {
		t.Fatal("wildcard IP rejected")
	}
	if !a.Authorized(&http.Request{RemoteAddr: "192.168.1.5:123"}) {
		t.Fatal("range IP rejected")
	}
	if !a.Authorized(&http.Request{RemoteAddr: "8.8.8.8:123", Header: http.Header{"X-API-Key": []string{"key"}}}) {
		t.Fatal("API key rejected")
	}
}

func TestAuthenticatorRevokesOldTokenAndLimitsThirtyFirstFailure(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultConfig()
	cfg["login"] = map[string]any{"username": "admin", "password": fmt.Sprintf("%x", md5.Sum([]byte("secret")))}
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m, err = appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.New(m)
	request := &http.Request{RemoteAddr: "192.168.1.9:1234"}
	password := fmt.Sprintf("%x", md5.Sum([]byte("secret")))
	if _, err := a.Login(request, model.Login{Username: "admin", Password: password}); err != nil {
		t.Fatal(err)
	}
	token, err := a.Login(request, model.Login{Username: "admin", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Update(model.Config{"login": map[string]any{"password": fmt.Sprintf("%x", md5.Sum([]byte("changed")))}}); err != nil {
		t.Fatal(err)
	}
	if a.Verify(token, request) {
		t.Fatal("token remained valid after password change")
	}

	limitedStore, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	limitedManager, err := appconfig.NewManager(limitedStore)
	if err != nil {
		t.Fatal(err)
	}
	limited := auth.New(limitedManager)
	wrong := model.Login{Username: "admin", Password: "wrong"}
	for index := 0; index < 30; index++ {
		if _, err := limited.Login(request, wrong); err == nil || errors.Is(err, auth.ErrTooManyAttempts) {
			t.Fatalf("failure %d returned %v", index+1, err)
		}
	}
	if _, err := limited.Login(request, wrong); !errors.Is(err, auth.ErrTooManyAttempts) {
		t.Fatalf("31st failure = %v", err)
	}
}
