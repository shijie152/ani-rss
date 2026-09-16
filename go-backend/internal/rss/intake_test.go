package rss_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/rss"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

func TestIntakeCollectUsesTheSamePipelineForPrimaryAndStandbyFeeds(t *testing.T) {
	var primary, standby atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/primary" {
			primary.Add(1)
			_, _ = io.WriteString(w, `<rss><channel><item><title>[Primary] Demo E01</title><enclosure url="magnet:?xt=urn:btih:PRIMARY" length="1"/></item></channel></rss>`)
			return
		}
		standby.Add(1)
		_, _ = io.WriteString(w, `<rss><channel><item><title>[Backup] Demo E02</title><enclosure url="magnet:?xt=urn:btih:BACKUP" length="1"/></item></channel></rss>`)
	}))
	defer server.Close()

	legacy, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update(model.Config{"standbyRss": true}); err != nil {
		t.Fatal(err)
	}
	intake := &rss.Intake{Config: config, HTTPClient: server.Client(), Retry: 1}
	items, err := intake.Collect(context.Background(), model.Ani{ID: "ani", Title: "Demo", URL: server.URL + "/primary", Subgroup: "Primary", StandbyRSSList: []model.StandbyRSS{{URL: server.URL + "/standby", Label: "Backup"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Master != true || items[1].Master != false || items[1].Subgroup != "Backup" {
		t.Fatalf("collected items = %#v", items)
	}
	if primary.Load() != 1 || standby.Load() != 1 {
		t.Fatalf("fetch counts primary=%d standby=%d", primary.Load(), standby.Load())
	}
}

func TestIntakeCollectPreservesPartialResultsAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `<rss><channel><item><title>Demo E01</title><enclosure url="magnet:?xt=urn:btih:GOOD" length="1"/></item></channel></rss>`)
	}))
	defer server.Close()
	legacy, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update(model.Config{"standbyRss": true}); err != nil {
		t.Fatal(err)
	}
	intake := &rss.Intake{Config: config, HTTPClient: server.Client(), Retry: 1}
	items, err := intake.Collect(context.Background(), model.Ani{Title: "Demo", URL: server.URL + "/bad", StandbyRSSList: []model.StandbyRSS{{URL: server.URL + "/good", Label: ""}}})
	if err == nil || len(items) != 1 || items[0].InfoHash != "good" {
		t.Fatalf("partial intake items=%#v err=%v", items, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := intake.Collect(ctx, model.Ani{Title: "Demo", URL: server.URL + "/good"}); err == nil {
		t.Fatal("cancelled intake unexpectedly succeeded")
	}
}
