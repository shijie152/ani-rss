package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

func TestJSONStoreCreatesDefaultsAndPreservesUnknownConfigFields(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg["futureField"] = map[string]any{"enabled": true}
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded["futureField"].(map[string]any)["enabled"] != true {
		t.Fatalf("unknown field lost: %#v", loaded["futureField"])
	}
	if _, err := os.Stat(filepath.Join(s.Directory(), "config.v2.json")); err != nil {
		t.Fatal(err)
	}
}

func TestJSONStoreRejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ani.v2.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewJSONStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadSubscriptions(); err == nil || !strings.Contains(err.Error(), "ani.v2.json") {
		t.Fatalf("error = %v", err)
	}
}

func TestJSONStoreAcceptsAnEmptySubscriptionArray(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSubscriptions([]model.Ani{}); err != nil {
		t.Fatal(err)
	}
	items, err := s.LoadSubscriptions()
	if err != nil || len(items) != 0 {
		t.Fatalf("empty subscriptions = %#v, err=%v", items, err)
	}
}

func TestJSONStoreConcurrentWritesRemainValid(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			if err := s.SaveSubscriptions([]model.Ani{{ID: string(rune('a' + i)), Title: "demo"}}); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}(i)
	}
	group.Wait()
	items, err := s.LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "demo" {
		t.Fatalf("items = %#v", items)
	}
}
