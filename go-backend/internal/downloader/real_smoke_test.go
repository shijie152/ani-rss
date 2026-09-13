package downloader_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// TestConfiguredDownloaderSmoke is opt-in because the repository must not
// mutate a user's real downloader during ordinary CI. When credentials are
// supplied, it exercises the actual service's login and task inventory, which
// catches protocol/version differences that fake-server tests cannot.
func TestConfiguredDownloaderSmoke(t *testing.T) {
	cases := []struct {
		name, typeName, host, username, password, provider string
	}{
		{"Transmission", "Transmission", "ANI_RSS_TRANSMISSION_URL", "ANI_RSS_TRANSMISSION_USER", "ANI_RSS_TRANSMISSION_PASSWORD", ""},
		{"Aria2", "Aria2", "ANI_RSS_ARIA2_URL", "", "ANI_RSS_ARIA2_TOKEN", ""},
		{"OpenList", "OpenList", "ANI_RSS_OPENLIST_URL", "", "ANI_RSS_OPENLIST_TOKEN", "ANI_RSS_OPENLIST_PROVIDER"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			host := os.Getenv(testCase.host)
			password := os.Getenv(testCase.password)
			if host == "" || password == "" {
				t.Skipf("set %s and %s to run against a real %s service", testCase.host, testCase.password, testCase.name)
			}
			cfg := model.Config{"downloadToolType": testCase.typeName, "downloadToolHost": host, "downloadToolPassword": password}
			if testCase.username != "" {
				cfg["downloadToolUsername"] = os.Getenv(testCase.username)
			}
			if testCase.provider != "" {
				cfg["provider"] = os.Getenv(testCase.provider)
			}
			adapter, err := downloader.New(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := adapter.Login(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Torrents(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
