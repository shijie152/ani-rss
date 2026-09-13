package store_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

func TestSQLiteStoreImportsLegacyJSONAndPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	legacy, err := store.NewJSONStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	config := model.Config{"futureField": map[string]any{"enabled": true}}
	items := []model.Ani{{ID: "one", Title: "Demo", URL: "https://example.test/rss", Enable: true}}
	resources := []model.Resource{{AniID: "one", InfoHash: "abc", Title: "Demo E01"}}
	tasks := []model.Torrent{{Hash: "abc", Name: "Demo E01", State: "downloading"}}
	if err := legacy.SaveConfig(config); err != nil {
		t.Fatal(err)
	}
	if err := legacy.SaveSubscriptions(items); err != nil {
		t.Fatal(err)
	}
	if err := legacy.SaveResources(resources); err != nil {
		t.Fatal(err)
	}
	if err := legacy.SaveTasks(tasks); err != nil {
		t.Fatal(err)
	}

	database, err := store.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(database.DatabasePath()); err != nil {
		t.Fatal(err)
	}
	loaded, err := database.LoadConfig()
	if err != nil || loaded["futureField"].(map[string]any)["enabled"] != true {
		t.Fatalf("imported config = %#v, err=%v", loaded, err)
	}
	if got, err := database.LoadSubscriptions(); err != nil || len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("imported subscriptions = %#v, err=%v", got, err)
	}
	if got, err := database.LoadResources(); err != nil || len(got) != 1 || got[0].InfoHash != "abc" {
		t.Fatalf("imported resources = %#v, err=%v", got, err)
	}
	if got, err := database.LoadTasks(); err != nil || len(got) != 1 || got[0].Hash != "abc" {
		t.Fatalf("imported tasks = %#v, err=%v", got, err)
	}
	if err := database.SaveConfig(model.Config{"persisted": true}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := store.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if got, err := restarted.LoadConfig(); err != nil || got["persisted"] != true {
		t.Fatalf("restarted config = %#v, err=%v", got, err)
	}
}

func TestSQLiteStoreConcurrentWritesRemainValid(t *testing.T) {
	database, err := store.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			if err := database.SaveSubscriptions([]model.Ani{{ID: string(rune('a' + i)), Title: "Demo", URL: "https://example.test/rss"}}); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}(i)
	}
	group.Wait()
	items, err := database.LoadSubscriptions()
	if err != nil || len(items) != 1 || items[0].Title != "Demo" {
		t.Fatalf("items = %#v, err=%v", items, err)
	}
}

func TestSQLiteStoreReplaceStateIsAtomicAcrossLogicalDocuments(t *testing.T) {
	dir := t.TempDir()
	database, err := store.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.ReplaceStateWithTasks(model.Config{"version": "two"}, []model.Ani{{ID: "two", Title: "Two", URL: "https://example.test/two"}}, []model.Resource{{AniID: "two"}}, []model.Torrent{{Hash: "two"}}); err != nil {
		t.Fatal(err)
	}
	config, _ := database.LoadConfig()
	items, _ := database.LoadSubscriptions()
	resources, _ := database.LoadResources()
	tasks, _ := database.LoadTasks()
	if config["version"] != "two" || len(items) != 1 || items[0].ID != "two" || len(resources) != 1 || resources[0].AniID != "two" || len(tasks) != 1 || tasks[0].Hash != "two" {
		t.Fatalf("replacement state = %#v %#v %#v", config, items, resources)
	}
	if filepath.Base(database.DatabasePath()) != "ani-rss.sqlite" {
		t.Fatalf("database path = %s", database.DatabasePath())
	}
}
