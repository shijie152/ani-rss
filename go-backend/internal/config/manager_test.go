package config_test

import (
	"strings"
	"testing"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

func TestManagerPreservesRedactedPasswordAndNormalizesSettings(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	originalTokenID := appconfig.String(m.Snapshot(), "tokenId")
	if err := m.Update(model.Config{
		"login":                map[string]any{"username": "admin", "password": ""},
		"mikanHost":            "https://example.test/",
		"downloadPathTemplate": "./library/${title}",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := m.Snapshot()
	login := cfg["login"].(map[string]any)
	if login["password"] == "" {
		t.Fatal("redacted password was cleared")
	}
	if appconfig.String(cfg, "mikanHost") != "https://example.test" {
		t.Fatalf("mikan host = %q", cfg["mikanHost"])
	}
	if !strings.HasPrefix(appconfig.String(cfg, "downloadPathTemplate"), "/") {
		t.Fatalf("path was not normalized: %q", cfg["downloadPathTemplate"])
	}
	if appconfig.String(cfg, "tokenId") != originalTokenID {
		t.Fatal("ordinary setting changed token ID")
	}
}

func TestManagerRejectsIncompleteProxy(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Update(model.Config{"proxy": true, "proxyHost": "", "proxyPort": 8080}); err == nil || !strings.Contains(err.Error(), "代理参数不完整") {
		t.Fatalf("proxy error = %v", err)
	}
	if _, err := appconfig.ProxyURL(model.Config{"proxy": true, "proxyHost": "127.0.0.1", "proxyPort": 8080, "proxyUsername": "u", "proxyPassword": "p"}); err != nil {
		t.Fatal(err)
	}
}

func TestManagerRejectsMalformedHTTPURL(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Update(model.Config{"mikanHost": "http://"}); err == nil || !strings.Contains(err.Error(), "地址异常") {
		t.Fatalf("invalid URL error = %v", err)
	}
}
