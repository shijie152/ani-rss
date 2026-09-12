package rss_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/rss"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
	"github.com/shijie152/ani-rss/go-backend/internal/subscription"
)

func TestParseAndMatchRSSRules(t *testing.T) {
	feed := `<rss><channel><item><title>[Group] Demo - 01 [1080p]</title><guid>abc123</guid><description>summary</description><pubDate>Mon, 01 Jan 2024 00:00:00 +0000</pubDate><enclosure url="magnet:?xt=urn:btih:ABC123&amp;dn=demo" length="2048"/></item><item><title>[Group] Demo - 02</title><enclosure url="https://example.test/02.torrent" length="4096"/></item></channel></rss>`
	items, err := rss.Parse([]byte(feed), "Group", "https://example.test/rss")
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %#v, err=%v", items, err)
	}
	if items[0].Episode != 1 || items[0].InfoHash != "abc123" || items[0].Magnet == "" {
		t.Fatalf("parsed item = %#v", items[0])
	}
	ani := model.Ani{Title: "Demo", Subgroup: "Group", Match: []string{"1080p"}, Exclude: []string{"02"}, GlobalExclude: true}
	matched := rss.Match(items, ani, rss.MatchOptions{GlobalExclude: []string{"bad"}, PriorityKeywords: []string{"1080p"}})
	if len(matched) != 1 || matched[0].Episode != 1 {
		t.Fatalf("matched = %#v", matched)
	}
	newTime := time.Now()
	items[0].PublishedAt = &newTime
	if got := rss.Match(items[:1], model.Ani{Subgroup: "Group"}, rss.MatchOptions{DelayedMinutes: 5}); len(got) != 0 {
		t.Fatal("delayed resource was not filtered")
	}
}

func TestCoordinatorSubmitsOnceAndSurvivesRestart(t *testing.T) {
	var adds int
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/add" {
			adds++
			_, _ = w.Write([]byte("Ok"))
			return
		}
		if r.URL.Path == "/api/v2/app/version" {
			_, _ = w.Write([]byte("v4"))
			return
		}
		http.NotFound(w, r)
	}))
	defer qb.Close()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><item><title>[Group] Demo E01</title><enclosure url="magnet:?xt=urn:btih:UNIQUE" length="100"/></item></channel></rss>`))
	}))
	defer feed.Close()
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	services := subscription.NewService(s, m, []model.Ani{{ID: "one", Title: "Demo", URL: feed.URL, Subgroup: "Group", Enable: true}})
	c := &rss.Coordinator{Config: m, Subscriptions: services, History: s, QB: &downloader.QBittorrent{Host: qb.URL, APIKey: "qbt_test"}}
	if _, err := c.Refresh(context.Background(), services.Items()[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Refresh(context.Background(), services.Items()[0]); err != nil {
		t.Fatal(err)
	}
	if adds != 1 {
		t.Fatalf("duplicate adds = %d", adds)
	}
	history, err := s.LoadResources()
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %#v, %v", history, err)
	}
	if !strings.Contains(history[0].InfoHash, "unique") {
		t.Fatalf("history key = %#v", history[0])
	}
	if got := services.Items()[0].CurrentEpisodeNumber; got != 1 {
		t.Fatalf("current episode after first submission = %d", got)
	}
	// A duplicate-only refresh must not look like a new download or rewrite
	// progress based solely on the RSS contents.
	servicesAgain := subscription.NewService(s, m, services.Items())
	coordinatorAgain := &rss.Coordinator{Config: m, Subscriptions: servicesAgain, History: s, QB: &downloader.QBittorrent{Host: qb.URL, APIKey: "qbt_test"}}
	if _, err := coordinatorAgain.Refresh(context.Background(), servicesAgain.Items()[0]); err != nil {
		t.Fatal(err)
	}
	if got := servicesAgain.Items()[0].CurrentEpisodeNumber; got != 1 {
		t.Fatalf("current episode after duplicate refresh = %d", got)
	}
}

func TestCoordinatorLogsInAndStartsRSSTask(t *testing.T) {
	var loggedIn, paused bool
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			loggedIn = true
			_, _ = w.Write([]byte("v4"))
		case "/api/v2/torrents/add":
			if r.ParseForm() != nil {
				t.Fatal(r.Form)
			}
			paused = r.Form.Get("paused") == "true" || r.Form.Get("stopped") == "true"
			_, _ = w.Write([]byte("Ok"))
		case "/api/v2/torrents/info":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><item><title>[Group] Demo E02</title><enclosure url="magnet:?xt=urn:btih:START" length="100"/></item></channel></rss>`))
	}))
	defer feed.Close()
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	services := subscription.NewService(s, m, []model.Ani{{ID: "one", Title: "Demo", URL: feed.URL, Subgroup: "Group", Enable: true}})
	c := &rss.Coordinator{Config: m, Subscriptions: services, History: s, QB: &downloader.QBittorrent{Host: qb.URL, APIKey: "qbt_test"}}
	if _, err := c.Refresh(context.Background(), services.Items()[0]); err != nil {
		t.Fatal(err)
	}
	if !loggedIn || paused {
		t.Fatalf("loggedIn=%v paused=%v", loggedIn, paused)
	}
}

func TestCoordinatorUsesStandbyRSSAndKeepsOtherSubscriptionsMoving(t *testing.T) {
	var addedTags []string
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = w.Write([]byte("v4"))
		case "/api/v2/torrents/info":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v2/torrents/add":
			if err := r.ParseForm(); err != nil {
				t.Errorf("add form: %v", err)
			}
			addedTags = append(addedTags, r.Form.Get("tags"))
			_, _ = w.Write([]byte("Ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer primary.Close()
	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><item><title>[Backup] Demo E01</title><guid>backup-1</guid><enclosure url="magnet:?xt=urn:btih:BACKUP1" length="100"/></item></channel></rss>`))
	}))
	defer standby.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><item><title>[Group] Other E01</title><guid>good-1</guid><enclosure url="magnet:?xt=urn:btih:GOOD1" length="100"/></item></channel></rss>`))
	}))
	defer good.Close()

	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update(model.Config{"standbyRss": true, "downloadPathTemplate": filepath.Join(t.TempDir(), "${title}")}); err != nil {
		t.Fatal(err)
	}
	services := subscription.NewService(s, config, []model.Ani{
		{ID: "fallback", Title: "Demo", URL: primary.URL, Subgroup: "Group", StandbyRSSList: []model.StandbyRSS{{Label: "Backup", URL: standby.URL}}, Enable: true},
		{ID: "healthy", Title: "Other", URL: good.URL, Subgroup: "Group", Enable: true},
	})
	coordinator := &rss.Coordinator{Config: config, Subscriptions: services, History: s, QB: &downloader.QBittorrent{Host: qb.URL, APIKey: "qbt_test"}, Retry: 1}

	result, err := coordinator.RefreshAll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("RefreshAll error = %v", err)
	}
	if len(result["healthy"]) != 1 || len(addedTags) != 2 {
		t.Fatalf("result=%#v addedTags=%#v", result, addedTags)
	}
	if addedTags[0] != "ani-rss,Backup,备用RSS" {
		t.Fatalf("standby tags = %q", addedTags[0])
	}
}

func TestCoordinatorWashesSameEpisodeStandbyTaskBeforePrimarySubmit(t *testing.T) {
	var deletedHash string
	var deletedFiles string
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = w.Write([]byte("v4"))
		case "/api/v2/torrents/info":
			_, _ = w.Write([]byte(`[{"hash":"standby-hash","name":"Demo S01E01","state":"downloading","progress":0.2,"size":100,"amount_left":80,"save_path":"` + "/tmp/" + `Demo","category":"ani-rss","tags":"ani-rss,备用RSS"},{"hash":"other-hash","name":"Other S01E01","state":"downloading","progress":0.2,"size":100,"amount_left":80,"save_path":"/tmp/Other","category":"ani-rss","tags":"ani-rss,备用RSS"}]`))
		case "/api/v2/torrents/delete":
			if err := r.ParseForm(); err != nil {
				t.Errorf("delete form: %v", err)
			}
			deletedHash, deletedFiles = r.Form.Get("hashes"), r.Form.Get("deleteFiles")
			_, _ = w.Write([]byte("Ok"))
		case "/api/v2/torrents/add":
			_, _ = w.Write([]byte("Ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><item><title>[Group] Demo E01 [1080p]</title><guid>primary-1</guid><enclosure url="magnet:?xt=urn:btih:PRIMARY1" length="100"/></item></channel></rss>`))
	}))
	defer feed.Close()

	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update(model.Config{"delete": true, "deleteStandbyRSSOnly": true, "standbyRss": true, "downloadPathTemplate": "/tmp/Demo"}); err != nil {
		t.Fatal(err)
	}
	services := subscription.NewService(s, config, []model.Ani{{ID: "one", Title: "Demo", URL: feed.URL, Subgroup: "Group", Enable: true}})
	coordinator := &rss.Coordinator{Config: config, Subscriptions: services, History: s, QB: &downloader.QBittorrent{Host: qb.URL, APIKey: "qbt_test"}, Retry: 1}
	if _, err := coordinator.Refresh(context.Background(), services.Items()[0]); err != nil {
		t.Fatal(err)
	}
	if deletedHash != "standby-hash" || deletedFiles != "true" {
		t.Fatalf("standby delete = hash %q files %q", deletedHash, deletedFiles)
	}
}

func TestCustomEpisodeRuleAndHalfEpisodeFiltering(t *testing.T) {
	items := []model.Resource{{Title: "Demo [12]", Episode: 0}, {Title: "Demo [12.5]", Episode: 0}}
	matched := rss.Match(items, model.Ani{CustomEpisode: true, CustomEpisodeStr: `([0-9]+)`, CustomEpisodeGroupIndex: 1}, rss.MatchOptions{SkipHalf: true, CustomEpisode: true, CustomEpisodeRE: `([0-9]+)`, CustomEpisodeIdx: 1})
	if len(matched) != 1 || matched[0].Episode != 12 {
		t.Fatalf("matched = %#v", matched)
	}
}

func TestCustomEpisodeRulePreservesJavaCaptureIndexAcrossNonCapturingGroups(t *testing.T) {
	items := []model.Resource{{Title: "Demo - 12", Episode: 0}}
	matched := rss.Match(items, model.Ani{CustomEpisode: true, CustomEpisodeStr: `(?:Demo - )([0-9]+)`, CustomEpisodeGroupIndex: 1}, rss.MatchOptions{CustomEpisode: true, CustomEpisodeRE: `(?:Demo - )([0-9]+)`, CustomEpisodeIdx: 1})
	if len(matched) != 1 || matched[0].Episode != 12 {
		t.Fatalf("matched = %#v", matched)
	}
}
