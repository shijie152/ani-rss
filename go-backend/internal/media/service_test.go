package media_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/media"
	"github.com/shijie152/ani-rss/go-backend/internal/metadata"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

func TestScrapeRenamesFilesMatchesSubtitlesAndWritesMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/3/tv/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 42, "name": "Demo Show", "original_name": "Demo Show JP", "overview": "overview",
				"first_air_date": "2024-01-01", "number_of_episodes": 1, "vote_average": 8.5,
				"poster_path": "/poster.jpg", "backdrop_path": "/fanart.jpg",
			})
		case r.URL.Path == "/3/tv/42/season/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"episodes": []any{map[string]any{"episode_number": 1, "name": "Pilot", "overview": "pilot", "air_date": "2024-01-01"}}})
		case strings.HasPrefix(r.URL.Path, "/t/p/original/"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("image"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dataStore, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(dataStore)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update(model.Config{"tmdbApi": server.URL, "tmdbImage": server.URL, "tmdbApiKey": "test"}); err != nil {
		t.Fatal(err)
	}
	path := t.TempDir()
	video := filepath.Join(path, "[Group] Demo - 01 [1080p].mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "[Group] Demo - 01 [1080p].chs.ass"), []byte("subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	ani := model.Ani{ID: "demo", Title: "Demo", URL: "https://example.test/rss", Season: 1, Subgroup: "Group", TMDB: map[string]any{"id": "42"}}
	client := metadata.New(config.Snapshot(), server.Client())
	service := media.New(config, client, server.Client(), func(model.Ani) (string, error) { return path, nil })
	service.ConfigDir = t.TempDir()
	result, err := service.Scrape(context.Background(), &ani, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Processed != 1 || ani.TheMovieDBName == "" {
		t.Fatalf("result = %#v, ani = %#v", result, ani)
	}
	if _, err := os.Stat(filepath.Join(path, "[Group] Demo S01E01.mkv")); err != nil {
		t.Fatalf("renamed video missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "[Group] Demo S01E01.chs.ass")); err != nil {
		t.Fatalf("renamed subtitle missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "tvshow.nfo")); err != nil {
		t.Fatalf("tvshow nfo missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "season.nfo")); err != nil {
		t.Fatalf("season nfo missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "[Group] Demo S01E01.nfo")); err != nil {
		t.Fatalf("episode nfo missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "poster.jpg")); err != nil {
		t.Fatalf("poster missing: %v", err)
	}
	files, err := service.List(path)
	if err != nil || len(files) != 1 || len(files[0].Subtitles) != 1 {
		t.Fatalf("files = %#v, err = %v", files, err)
	}
}

func TestRefreshCoverUsesConfigFilesAndDoesNotOverwriteUnlessRequested(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("cover")) }))
	defer server.Close()
	config := &fakeConfig{value: model.Config{}}
	service := media.New(config, nil, server.Client(), nil)
	service.ConfigDir = t.TempDir()
	first, err := service.RefreshCover(context.Background(), server.URL+"/cover.png", false)
	if err != nil || first == "" {
		t.Fatalf("first cover = %q, err = %v", first, err)
	}
	if _, err := os.Stat(filepath.Join(service.ConfigDir, "files", filepath.FromSlash(first))); err != nil {
		t.Fatal(err)
	}
	second, err := service.RefreshCover(context.Background(), server.URL+"/cover.png", false)
	if err != nil || second != first {
		t.Fatalf("second cover = %q, err = %v", second, err)
	}
}

func TestMediaUsesSupportedFormatsAndPlaybackSizePolicy(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"one.mp4", "two.avi", "three.webm", "four.srt", "five.mks", "six.jpg"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("small"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := (&media.Service{}).List(directory)
	if err != nil || len(files) != 2 {
		t.Fatalf("files=%#v err=%v", files, err)
	}
	if media.IsVideo("three.webm") || !media.IsSupported("five.mks") || media.IsSupported("six.txt") {
		t.Fatal("format policy does not match Java contract")
	}
	playable, err := (&media.Service{}).PlaybackList(directory)
	if err != nil || len(playable) != 0 {
		t.Fatalf("playable=%#v err=%v", playable, err)
	}
}

type fakeConfig struct{ value model.Config }

func (f *fakeConfig) Snapshot() model.Config { return f.value }
