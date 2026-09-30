package backend

import (
	"context"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func TestComponentsExposeMediaMetadataAndSourceModules(t *testing.T) {
	app, err := New(Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "media", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	components := app.Components()

	metadataClient, err := components.MetadataClient()
	if err != nil {
		t.Fatal(err)
	}
	if metadataClient.Cache != app.metadataCache {
		t.Fatal("metadata client must share the app's metadata cache")
	}

	mediaService, err := components.MediaService()
	if err != nil {
		t.Fatal(err)
	}
	if mediaService.ConfigDir != app.configDir {
		t.Fatalf("media ConfigDir = %q, want %q", mediaService.ConfigDir, app.configDir)
	}
	if mediaService.Notify == nil || mediaService.ResolveOther == nil {
		t.Fatal("media service side effects must be wired")
	}

	sourceClient, err := components.CatalogueDiscovery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sourceClient == nil {
		t.Fatal("catalogue discovery must be assembled")
	}
}

// The assembly seam must be reachable without an HTTP route, and every call
// must reflect the configuration snapshot at call time: a stale coordinator
// would keep retrying with values the user already changed.
func TestComponentsAssembleFromLatestConfigSnapshot(t *testing.T) {
	app, err := New(Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "rss"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	first, err := app.Components().Coordinator()
	if err != nil {
		t.Fatal(err)
	}
	if first.Retry != 3 {
		t.Fatalf("default retry = %d, want 3", first.Retry)
	}
	if first.Config != app.config || first.Subscriptions != app.subscriptions {
		t.Fatal("coordinator must be wired to the app's config and subscriptions")
	}

	if err := app.config.Update(model.Config{"downloadRetry": 7}); err != nil {
		t.Fatal(err)
	}
	second, err := app.Components().Coordinator()
	if err != nil {
		t.Fatal(err)
	}
	if second.Retry != 7 {
		t.Fatalf("retry after config change = %d, want 7", second.Retry)
	}
}
