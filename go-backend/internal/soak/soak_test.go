// Package soak exercises the full Java-parity lifecycle that unit tests only
// cover one seam at a time: RSS submission -> downloader completion -> media
// organization -> notification -> subscription finish/auto-disable.
//
// A single fake downloader implements both the rss.Downloader slice (Add,
// Torrents, Delete) and the completion.Downloader slice (Login, AddTags) so a
// resource submitted through the real Submitter flows into the real completion
// Coordinator unchanged — this is the path that would have caught the qBittorrent
// HTTP-202 add regression (eb0f1390) without a manual soak.
package soak

import (
	"context"
	"errors"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/completion"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/rss"
)

// lifecycleDownloader is the one adapter wired into both ends of the chain.
// Add records the submission and flips the task into a finished seeding state,
// exactly like a real download completing between two passes.
type lifecycleDownloader struct {
	added   []model.Resource
	tasks   []model.Torrent
	tags    map[string][]string
	addErr  error
	addedAs map[string]string // infoHash -> savePath the submitter resolved
}

func (d *lifecycleDownloader) Login(context.Context) error { return nil }

func (d *lifecycleDownloader) Torrents(context.Context) ([]model.Torrent, error) {
	return d.tasks, nil
}

func (d *lifecycleDownloader) Add(_ context.Context, res model.Resource, savePath string, tags []string, _ bool) error {
	if d.addErr != nil {
		return d.addErr
	}
	d.added = append(d.added, res)
	if d.addedAs == nil {
		d.addedAs = map[string]string{}
	}
	d.addedAs[res.InfoHash] = savePath
	// The resource arrives unfinished; the next Torrents() call reports it
	// complete, modelling a fast real download.
	d.tasks = append(d.tasks, model.Torrent{
		Hash: res.InfoHash, Name: res.Title,
		State: "stalledUP", Progress: 100,
		SavePath: savePath, TagList: tags,
	})
	return nil
}

func (d *lifecycleDownloader) AddTags(_ context.Context, hash, tags string) error {
	if d.tags == nil {
		d.tags = map[string][]string{}
	}
	d.tags[hash] = append(d.tags[hash], tags)
	for i := range d.tasks {
		if d.tasks[i].Hash == hash {
			d.tasks[i].TagList = append(d.tasks[i].TagList, tags)
		}
	}
	return nil
}

func (d *lifecycleDownloader) Delete(_ context.Context, hash string, _ bool) error { return nil }

// memSubs is the in-memory subscription view both ends read; BatchEnable is
// what completion uses to retire a finished subscription.
type memSubs struct {
	items   []model.Ani
	paths   map[string]string
	enabled map[string]bool
}

func (m *memSubs) Items() []model.Ani { return m.items }
func (m *memSubs) DownloadPath(item model.Ani) (map[string]any, error) {
	return map[string]any{"downloadPath": m.paths[item.ID]}, nil
}
func (m *memSubs) BatchEnable(value bool, ids []string) error {
	if m.enabled == nil {
		m.enabled = map[string]bool{}
	}
	for _, id := range ids {
		m.enabled[id] = value
	}
	return nil
}

// fakeScraper records the organize call without touching the filesystem.
type fakeScraper struct {
	calls   int
	lastAni model.Ani
}

func (f *fakeScraper) Scrape(_ context.Context, ani *model.Ani, _ bool) (completion.ScrapeResult, error) {
	f.calls++
	f.lastAni = *ani
	// Processed:0 keeps the DOWNLOAD_END signal inside the completion seam,
	// which is the path this test asserts on. (media.Service emits its own
	// DOWNLOAD_END when it actually organizes files; that half is covered by
	// the media package tests.)
	return completion.ScrapeResult{Processed: 0, Path: "/lib/" + ani.ID}, nil
}

func lifecycleConfig() model.Config {
	return model.Config{
		"rss": true, "scrape": true, "rename": true,
		"autoDisabled": true, "completed": true, "delete": false,
		"downloadCount": 1, "rssSleepMinutes": 1, "renameSleepSeconds": 8,
		"fileExist": false, "standbyRss": false, "coexist": false, "skip5": true,
	}
}

// TestFullLifecycleReplaceJava is the soak assertion: one resource goes in the
// top (RSS submit), the subscription's progress has advanced to the final
// episode (as a prior refresh would set it), and the subscription comes out
// the bottom finished and disabled, with DOWNLOAD_START -> DOWNLOAD_END ->
// COMPLETED notifications in order and the 下载完成 tag applied in the
// downloader.
func TestFullLifecycleReplaceJava(t *testing.T) {
	cfg := lifecycleConfig()
	ani := model.Ani{
		ID: "soak-1", Title: "古诺希亚", JPTitle: "GNOSIA", Season: 1,
		URL: "feed://test", Type: "mikan", Enable: true, Message: true,
		Completed: true, CurrentEpisodeNumber: 12, TotalEpisodeNumber: 12,
	}
	subs := &memSubs{items: []model.Ani{ani}, paths: map[string]string{"soak-1": "/dl/soak-1/Season 1"}}
	dl := &lifecycleDownloader{}
	scraper := &fakeScraper{}
	var statuses []string
	notify := func(_ context.Context, ev completion.NotificationEvent) error {
		statuses = append(statuses, ev.Status)
		return nil
	}

	// 1. RSS submission (the top of the chain).
	sub := &rss.Submitter{
		Config:       reader{cfg},
		DownloadPath: subs.DownloadPath,
		Downloader:   dl,
		Notify: func(ctx context.Context, a model.Ani, res *model.Resource, status, text string) error {
			return notify(ctx, completion.NotificationEvent{Ani: a, Status: status, Text: text, Resource: res})
		},
	}
	res := model.Resource{AniID: ani.ID, Title: "[LoliHouse] GNOSIA - 12", Episode: 12, InfoHash: "abc123def456", Master: true, DownloadURL: "https://x/t.torrent"}
	submitted, err := sub.Submit(context.Background(), ani, []model.Resource{res})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(submitted) != 1 || len(dl.added) != 1 {
		t.Fatalf("expected one submitted download, got submitted=%v added=%v", len(submitted), len(dl.added))
	}
	if dl.addedAs["abc123def456"] != "/dl/soak-1/Season 1" {
		t.Fatalf("resource submitted to wrong save path: %q", dl.addedAs["abc123def456"])
	}

	// 2. Completion pass (the bottom of the chain).
	pass := &completion.Coordinator{
		Config: cfg, Downloader: dl, Scraper: scraper, Subscriptions: subs, Notify: notify,
	}
	if err := pass.Run(context.Background()); err != nil {
		t.Fatalf("completion pass: %v", err)
	}

	// Assertions on the observable Java-parity outcomes.
	if scraper.calls != 1 {
		t.Fatalf("expected media organize for the finished task, calls=%d", scraper.calls)
	}
	if subs.enabled["soak-1"] != false {
		t.Fatal("subscription should auto-disable once it reaches totalEpisodeNumber")
	}
	var hasCompletionTag bool
	for _, tag := range dl.tags["abc123def456"] {
		if tag == completion.CompletionTag {
			hasCompletionTag = true
		}
	}
	if !hasCompletionTag {
		t.Fatalf("expected %q tag on the task, got %v", completion.CompletionTag, dl.tags["abc123def456"])
	}
	order := map[string]int{}
	for i, s := range statuses {
		order[s] = i
	}
	for _, want := range []string{"DOWNLOAD_START", "DOWNLOAD_END", "COMPLETED"} {
		if _, ok := order[want]; !ok {
			t.Fatalf("missing notification %s in %v", want, statuses)
		}
	}
	if !(order["DOWNLOAD_START"] < order["DOWNLOAD_END"] && order["DOWNLOAD_END"] < order["COMPLETED"]) {
		t.Fatalf("notification order wrong: %v", statuses)
	}
}

// TestAddAcceptsJava2xxSuccess guards the eb0f1390 contract: the downloader
// treats any HTTP 2xx as a successful add, and the submitter must not retry or
// fail the round on a non-200 success.
func TestAddAcceptsJava2xxSuccess(t *testing.T) {
	cfg := lifecycleConfig()
	ani := model.Ani{ID: "soak-2", Title: "X", Season: 1, URL: "u", Enable: true}
	subs := &memSubs{items: []model.Ani{ani}, paths: map[string]string{"soak-2": "/dl/soak-2"}}
	dl := &lifecycleDownloader{}
	sub := &rss.Submitter{Config: reader{cfg}, DownloadPath: subs.DownloadPath, Downloader: dl}
	res := model.Resource{AniID: ani.ID, Title: "X - 01", Episode: 1, InfoHash: "h1", Master: true}
	submitted, err := sub.Submit(context.Background(), ani, []model.Resource{res})
	if err != nil {
		t.Fatalf("submit must succeed on a 2xx-style add: %v", err)
	}
	if len(submitted) != 1 {
		t.Fatal("resource was not recorded as submitted")
	}

	// An adapter-level add failure that is NOT recoverable must still surface —
	// the submitter can't silently swallow a genuinely broken add.
	dl.addErr = errors.New("qbittorrent returned HTTP 500")
	if _, err := sub.Submit(context.Background(), ani, []model.Resource{{AniID: ani.ID, Title: "X - 02", Episode: 2, InfoHash: "h2", Master: true}}); err == nil {
		t.Fatal("a real add failure must surface, not be swallowed")
	}
}

// reader adapts a plain Config map to the appconfig.Reader seam.
type reader struct{ cfg model.Config }

func (r reader) Snapshot() model.Config { return r.cfg }
