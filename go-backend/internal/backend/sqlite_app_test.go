package backend_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

func TestAppUsesSQLiteAsItsOnlyLogicalStateStore(t *testing.T) {
	dir := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"runtime", "subscriptions"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, ok := app.Store().(*store.SQLiteStore); !ok {
		t.Fatalf("app store type = %T, want *store.SQLiteStore", app.Store())
	}
	if err := app.Config().Update(model.Config{"futureField": "kept"}); err != nil {
		t.Fatal(err)
	}
	if err := app.Store().SaveSubscriptions([]model.Ani{{ID: "one", Title: "Demo", URL: "https://example.test/rss"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.v2.json")); !os.IsNotExist(err) {
		t.Fatalf("application wrote legacy config JSON: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ani.v2.json")); !os.IsNotExist(err) {
		t.Fatalf("application wrote legacy subscription JSON: %v", err)
	}
}
