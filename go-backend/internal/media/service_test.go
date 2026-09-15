package media_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/at-wat/ebml-go"
	"github.com/at-wat/ebml-go/webm"

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
			_ = json.NewEncoder(w).Encode(map[string]any{"episodes": []any{map[string]any{"episode_number": 1, "name": "Pilot", "overview": "pilot", "air_date": "2024-01-01", "still_path": "/still.jpg"}}})
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
	if _, err := os.Stat(filepath.Join(path, "[Group] Demo S01E01-thumb.jpg")); err != nil {
		t.Fatalf("episode still missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "season01-poster.jpg")); err != nil {
		t.Fatalf("season poster missing: %v", err)
	}
	files, err := service.List(path)
	if err != nil || len(files) != 1 || len(files[0].Subtitles) != 1 {
		t.Fatalf("files = %#v, err = %v", files, err)
	}
	if _, err := service.Scrape(context.Background(), &ani, false); err != nil {
		t.Fatalf("idempotent scrape failed: %v", err)
	}
	duplicate := filepath.Join(path, "[Group] Demo - 01 [1080p].copy.mkv")
	if err := os.WriteFile(duplicate, []byte("duplicate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Scrape(context.Background(), &ani, false); err == nil {
		t.Fatal("existing target was silently overwritten")
	}
	if _, err := os.Stat(duplicate); err != nil {
		t.Fatalf("duplicate source was lost: %v", err)
	}

	completedSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(completedSource, "[Group] Demo 01.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	completedTarget := t.TempDir()
	if err := config.Update(model.Config{"autoDisabled": true, "completed": true}); err != nil {
		t.Fatal(err)
	}
	completedService := media.New(config, client, server.Client(), func(model.Ani) (string, error) { return completedSource, nil })
	completedService.ConfigDir = t.TempDir()
	completedService.ResolveOther = func(model.Ani, string) (string, error) { return completedTarget, nil }
	completedAni := ani
	completedAni.Completed = true
	completedAni.Enable = false
	completedAni.CurrentEpisodeNumber = 1
	completedAni.TotalEpisodeNumber = 1
	completedResult, err := completedService.Scrape(context.Background(), &completedAni, true)
	if err != nil {
		t.Fatalf("completed scrape failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(completedTarget, "[Group] Demo S01E01.mkv")); err != nil {
		t.Fatalf("completed media was not moved: %v result=%#v source=%s target=%s", err, completedResult, completedSource, completedTarget)
	}
	if _, err := os.Stat(completedSource); !os.IsNotExist(err) {
		t.Fatalf("completed source directory still exists, err=%v", err)
	}
}

func TestSubtitlesForExposesOnlyBrowserPlayableSidecars(t *testing.T) {
	directory := t.TempDir()
	video := filepath.Join(directory, "Demo S01E01.mkv")
	for _, name := range []string{"Demo S01E01.chs.ass", "Demo S01E01.eng.srt", "Demo S01E01.jpn.ssa", "Demo S01E01.raw.sup"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("subtitle"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	items := media.SubtitlesFor(video)
	if len(items) != 2 || items[0].Type != "ass" || items[1].Type != "srt" {
		t.Fatalf("playable sidecars = %#v", items)
	}
}

func TestEmbeddedMatroskaTextSubtitlesAreReturnedAsVTT(t *testing.T) {
	var data bytes.Buffer
	document := struct {
		Segment webm.Segment `ebml:"Segment"`
	}{Segment: webm.Segment{
		Tracks:  webm.Tracks{TrackEntry: []webm.TrackEntry{{TrackNumber: 1, TrackType: 17, CodecID: "S_TEXT/UTF8", Name: "简体中文"}}},
		Cluster: []webm.Cluster{{SimpleBlock: []ebml.Block{{TrackNumber: 1, Data: [][]byte{[]byte("你好")}}}}},
	}}
	if err := ebml.Marshal(&document, &data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "embedded.mkv")
	if err := os.WriteFile(path, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	subtitles, err := media.EmbeddedSubtitles(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(subtitles) != 1 || subtitles[0].Name != "简体中文" || !strings.Contains(subtitles[0].Content, "WEBVTT") || !strings.Contains(subtitles[0].Content, "你好") {
		t.Fatalf("embedded subtitles = %#v", subtitles)
	}
}

func TestScrapeRecoversAfterTransientAssetFailure(t *testing.T) {
	posterAttempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/3/tv/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 42, "name": "Demo", "first_air_date": "2024-01-01",
				"number_of_episodes": 1, "poster_path": "/poster.jpg",
			})
		case "/3/tv/42/season/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"episodes": []any{}})
		case "/t/p/original/poster.jpg":
			posterAttempts++
			if posterAttempts == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("poster"))
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
	if err := os.WriteFile(filepath.Join(path, "[Group] Demo 01.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	ani := model.Ani{ID: "recovery", Title: "Demo", URL: "https://example.test/rss", Season: 1, Subgroup: "Group", TMDB: map[string]any{"id": "42"}}
	service := media.New(config, metadata.New(config.Snapshot(), server.Client()), server.Client(), func(model.Ani) (string, error) { return path, nil })
	service.ConfigDir = t.TempDir()
	if _, err := service.Scrape(context.Background(), &ani, true); err == nil {
		t.Fatal("transient asset failure was hidden")
	}
	if _, err := os.Stat(filepath.Join(path, "[Group] Demo S01E01.mkv")); err != nil {
		t.Fatalf("renamed media was not retained for retry: %v", err)
	}
	if _, err := service.Scrape(context.Background(), &ani, true); err != nil {
		t.Fatalf("recovery scrape failed: %v", err)
	}
	if posterAttempts < 2 {
		t.Fatalf("poster attempts = %d", posterAttempts)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "poster.jpg")); err != nil {
		t.Fatalf("poster was not recovered: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file leaked: %s", entry.Name())
		}
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

func TestCustomEpisodeRenameTemplateAndFilenameLimit(t *testing.T) {
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/3/tv/42":
			_, _ = w.Write([]byte(`{"id":42,"name":"Demo","first_air_date":"2024-01-01","number_of_episodes":7}`))
		case "/3/tv/42/season/2":
			_, _ = w.Write([]byte(`{"episodes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer metadataServer.Close()

	config := &fakeConfig{value: model.Config{
		"tmdb": true, "tmdbApi": metadataServer.URL, "tmdbApiKey": "test", "tmdbImage": metadataServer.URL,
		"renameTemplate": "${title}-${subgroup}-${seasonFormat}-${episodeFormat}-${resolution}-${language}",
	}}
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "Episode-07 [1080p] chs.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	ani := model.Ani{ID: "custom", Title: "Show", URL: "https://example.test/rss", Season: 2, Subgroup: "Group", TMDB: map[string]any{"id": "42"}, CustomEpisode: true, CustomEpisodeStr: `(?:Episode-)([0-9]+)`, CustomEpisodeGroupIndex: 1}
	service := media.New(config, metadata.New(config.Snapshot(), metadataServer.Client()), metadataServer.Client(), func(model.Ani) (string, error) { return path, nil })
	if _, err := service.Scrape(context.Background(), &ani, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "Show-Group-02-07-1080p-chs.mkv")); err != nil {
		t.Fatalf("custom template output missing: %v", err)
	}

	limitedConfig := &fakeConfig{value: model.Config{
		"tmdb": true, "tmdbApi": metadataServer.URL, "tmdbApiKey": "test", "tmdbImage": metadataServer.URL,
		"renameTemplate": "${title}-${episode}", "maxFileNameLength": 8,
	}}
	limitedPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(limitedPath, "Episode-07.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	limited := media.New(limitedConfig, metadata.New(limitedConfig.Snapshot(), metadataServer.Client()), metadataServer.Client(), func(model.Ani) (string, error) { return limitedPath, nil })
	limitedAni := model.Ani{ID: "limited", Title: "LongTitle", URL: "https://example.test/rss", Season: 2, TMDB: map[string]any{"id": "42"}, CustomEpisode: true, CustomEpisodeStr: `(?:Episode-)([0-9]+)`, CustomEpisodeGroupIndex: 1}
	if _, err := limited.Scrape(context.Background(), &limitedAni, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(limitedPath, "LongTitl.mkv")); err != nil {
		t.Fatalf("filename limit output missing: %v", err)
	}
}

type fakeConfig struct{ value model.Config }

func (f *fakeConfig) Snapshot() model.Config { return f.value }
