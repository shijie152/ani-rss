package subscription_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
	"github.com/shijie152/ani-rss/go-backend/internal/subscription"
)

func TestServiceValidatesDuplicatesAndPersistsEdits(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	service := subscription.NewService(s, m, nil)
	item := model.Ani{ID: "one", Title: "Demo", URL: "https://example.test/rss", Season: 1, Enable: true}
	if err := service.Add(item); err != nil {
		t.Fatal(err)
	}
	if err := service.Add(item); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	item.Title = "Demo 2"
	item.Enable = false
	if err := service.Set(item); err != nil {
		t.Fatal(err)
	}
	items, err := s.LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Demo 2" || items[0].Enable {
		t.Fatalf("persisted items = %#v", items)
	}
	if err := service.BatchEnable(true, []string{"one"}); err != nil {
		t.Fatal(err)
	}
	if !service.Items()[0].Enable {
		t.Fatal("batch enable did not update")
	}
	if err := service.Delete([]string{"one"}, false); err != nil {
		t.Fatal(err)
	}
	if len(service.Items()) != 0 {
		t.Fatal("delete did not remove item")
	}
}

func TestServiceListReturnsUIGroupingAndDownloadPath(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	service := subscription.NewService(s, m, []model.Ani{{ID: "one", Title: "Demo", URL: "https://example.test/rss", Season: 2, ReleaseDate: "2025-03-04"}})
	list := service.List()
	if list.Total != 1 || len(list.WeekList) != 7 || len(list.ReleaseDateList) != 1 {
		t.Fatalf("list = %#v", list)
	}
	path, err := service.DownloadPath(service.Items()[0])
	if err != nil {
		t.Fatal(err)
	}
	if path["downloadPath"] == "" {
		t.Fatal("download path empty")
	}
}

func TestServiceListProvidesPinyinAndJavaReleaseOrdering(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update(model.Config{"sortType": "PINYIN"}); err != nil {
		t.Fatal(err)
	}
	service := subscription.NewService(s, config, []model.Ani{
		{ID: "z", Title: "中文", URL: "https://example.test/z", ReleaseDate: "2025-01-01", Season: 1},
		{ID: "a", Title: "日本", URL: "https://example.test/a", ReleaseDate: "2026-03-01", Season: 1},
	})
	list := service.List()
	if list.ReleaseDateList[0] != "2026-03" || list.ReleaseDateList[1] != "2025-01" {
		t.Fatalf("release dates = %#v", list.ReleaseDateList)
	}
	var first, second model.Ani
	for _, week := range list.WeekList {
		for _, item := range week.Items {
			if item.ID == "a" {
				first = item
			}
			if item.ID == "z" {
				second = item
			}
		}
	}
	if first.Pinyin == "" || first.PinyinInitials == "" || second.Pinyin == "" || second.PinyinInitials == "" {
		t.Fatalf("pinyin fields missing: first=%#v second=%#v", first, second)
	}
}

func TestDownloadPathResolvesJavaTemplateFieldsAndDecemberQuarter(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := config.Update(model.Config{"downloadPathTemplate": filepath.Join(root, "${year}-${monthFormat}-${quarterName}-${seasonFormat}-${tmdbYear}-${tmdbid}-${bgmId}-${letter}-${subgroup}")}); err != nil {
		t.Fatal(err)
	}
	item := model.Ani{ID: "template", Title: "测试", URL: "https://example.test/rss", ReleaseDate: "2025-12-15", Season: 1, Subgroup: "Group", BGMURL: "https://bgm.tv/subject/99", TMDB: map[string]any{"id": "42", "first_air_date": "2025-12-01"}}
	service := subscription.NewService(s, config, []model.Ani{item})
	path, err := service.DownloadPath(item)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "2026-12-冬-01-2025-42-99-cs-Group")
	if got := path["downloadPath"]; got != want {
		t.Fatalf("download path = %q, want %q", got, want)
	}
}

func TestServiceUpdatesTotalEpisodesFromInjectedBangumiResolver(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	service := subscription.NewService(s, config, []model.Ani{{ID: "one", Title: "Demo", URL: "https://example.test/rss", TotalEpisodeNumber: 0}})
	called := false
	err = service.UpdateTotalEpisodes(context.Background(), false, []string{"one"}, func(_ context.Context, item model.Ani) (int, error) {
		called = item.ID == "one"
		return 12, nil
	})
	if err != nil || !called {
		t.Fatalf("update err=%v called=%v", err, called)
	}
	if got := service.Items()[0].TotalEpisodeNumber; got != 12 {
		t.Fatalf("total episodes = %d", got)
	}
	if err := service.UpdateTotalEpisodes(context.Background(), false, []string{"one"}, func(context.Context, model.Ani) (int, error) {
		t.Fatal("resolver called for an existing non-forced total")
		return 0, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceImportAssignsIDAndHonorsConflictPolicy(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	service := subscription.NewService(s, config, []model.Ani{{ID: "existing", Title: "Demo", URL: "https://old.test/rss", Season: 1, Enable: true}})
	if err := service.Import([]model.Ani{{Title: "New", URL: "https://new.test/rss", Season: 1}}, "REPLACE"); err != nil {
		t.Fatal(err)
	}
	if len(service.Items()) != 2 || service.Items()[1].ID == "" {
		t.Fatalf("imported id/items = %#v", service.Items())
	}
	if err := service.Import([]model.Ani{{Title: "Demo", URL: "https://replacement.test/rss", Season: 1, Enable: false}}, "SKIP"); err != nil {
		t.Fatal(err)
	}
	if service.Items()[0].URL != "https://old.test/rss" || !service.Items()[0].Enable {
		t.Fatalf("SKIP changed existing subscription: %#v", service.Items()[0])
	}
}

func TestServiceDeleteFilesRequiresExplicitOptIn(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := config.Update(model.Config{"downloadPathTemplate": filepath.Join(root, "${title}")}); err != nil {
		t.Fatal(err)
	}
	service := subscription.NewService(s, config, nil)
	item := model.Ani{ID: "one", Title: "Demo", URL: "https://example.test/rss", Season: 1}
	if err := service.Add(item); err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(root, "Demo")
	if err := os.MkdirAll(mediaPath, 0o755); err != nil {
		t.Fatal(err)
	}
	mediaFile := filepath.Join(mediaPath, "episode.mkv")
	if err := os.WriteFile(mediaFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete([]string{item.ID}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mediaFile); err != nil {
		t.Fatalf("media was deleted without opt-in: %v", err)
	}
	if err := service.Add(item); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete([]string{item.ID}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mediaPath); !os.IsNotExist(err) {
		t.Fatalf("media directory remains after explicit delete, err=%v", err)
	}
}

func TestServiceUpdatesCurrentEpisodeFromRefreshResults(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	item := model.Ani{ID: "one", Title: "Demo", URL: "https://example.test/rss", Season: 1}
	service := subscription.NewService(s, config, []model.Ani{item})
	resources := []model.Resource{{Episode: 1, Master: true}, {Episode: 2.5, Master: true}, {Episode: 3, Master: false}}
	if err := service.UpdateCurrentEpisode(item.ID, resources); err != nil {
		t.Fatal(err)
	}
	if got := service.Items()[0].CurrentEpisodeNumber; got != 2 {
		t.Fatalf("current episode = %d, want 2", got)
	}
	item.DownloadNew = true
	if err := service.Set(item); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateCurrentEpisode(item.ID, resources); err != nil {
		t.Fatal(err)
	}
	if got := service.Items()[0].CurrentEpisodeNumber; got != 3 {
		t.Fatalf("download-new current episode = %d, want 3", got)
	}
}
