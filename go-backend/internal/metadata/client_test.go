package metadata_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/metadata"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func TestClientLooksUpTMDBSeasonAndBangumiFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/3/tv/42":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "name": "Demo", "original_name": "Demo JP", "first_air_date": "2026-01-01", "number_of_episodes": 12, "vote_average": 8.8, "poster_path": "/poster.jpg"})
		case "/3/tv/42/season/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"episodes": []any{map[string]any{"episode_number": 1, "name": "Pilot", "overview": "intro", "air_date": "2026-01-01", "still_path": "/still.jpg"}}})
		case "/v0/subjects/7":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "name": "Demo JP", "name_cn": "Demo CN", "summary": "summary", "date": "2026-01-01", "eps": 12, "rating": map[string]any{"score": 8.1}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := metadata.New(model.Config{"tmdbApi": server.URL, "tmdbApiKey": "test", "bgmApi": server.URL}, server.Client())
	value, raw, err := client.Lookup(context.Background(), model.Ani{TMDB: map[string]any{"id": "42"}, Season: 1})
	if err != nil || raw["id"] == nil || value.Title != "Demo" || len(value.EpisodeInfo) != 1 || value.EpisodeInfo[0].Still != "/still.jpg" {
		t.Fatalf("tmdb value=%#v raw=%#v err=%v", value, raw, err)
	}
	value, _, err = client.Lookup(context.Background(), model.Ani{BGMURL: "https://bgm.tv/subject/7", Title: "No TMDB"})
	if err != nil || value.Title != "Demo CN" || value.Episodes != 12 {
		t.Fatalf("bangumi value=%#v err=%v", value, err)
	}
}

func TestClientUsesBangumiWhenTMDBIsDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/subjects/42" {
			t.Fatalf("unexpected metadata path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2024-01-01","images":{"large":"https://img.test/poster.jpg"}}`)
	}))
	defer server.Close()
	client := metadata.New(model.Config{"tmdb": false, "bgmApi": server.URL}, server.Client())
	value, _, err := client.Lookup(context.Background(), model.Ani{Title: "Demo", BGMURL: "https://bgm.tv/subject/42"})
	if err != nil {
		t.Fatal(err)
	}
	if value.Title != "Demo CN" || value.Episodes != 12 || value.Poster != "https://img.test/poster.jpg" {
		t.Fatalf("Bangumi metadata = %#v", value)
	}
}
