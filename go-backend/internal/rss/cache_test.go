package rss_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/rss"
)

func TestResourceCacheIsolatedAndDeletable(t *testing.T) {
	root := t.TempDir()
	ani := model.Ani{ID: "ani-1", Title: "Demo"}
	other := model.Ani{ID: "ani-2", Title: "Demo"}
	magnet := model.Resource{InfoHash: "ABC", DownloadURL: "magnet:?xt=urn:btih:ABC", Magnet: "magnet:?xt=urn:btih:ABC"}
	if err := rss.SaveResourceCache(context.Background(), nil, root, ani, magnet); err != nil {
		t.Fatal(err)
	}
	if !rss.HasCachedResource(root, ani, magnet) || rss.HasCachedResource(root, other, magnet) {
		t.Fatal("cache was not isolated by subscription")
	}
	if err := rss.DeleteResourceCache(root, ani, map[string]bool{"abc": true}); err != nil {
		t.Fatal(err)
	}
	if rss.HasCachedResource(root, ani, magnet) {
		t.Fatal("cache was not deleted")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "torrent-bytes") }))
	defer server.Close()
	torrentResource := model.Resource{InfoHash: "DEF", TorrentURL: server.URL + "/file.torrent", DownloadURL: server.URL + "/file.torrent"}
	if err := rss.SaveResourceCache(context.Background(), server.Client(), root, ani, torrentResource); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "torrents", ani.ID, "def.torrent"))
	if err != nil || string(data) != "torrent-bytes" {
		t.Fatalf("torrent cache = %q, err=%v", data, err)
	}
}
