package completion

import (
	"context"
	"errors"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// fakeDownloader records the calls the completion pass makes against the
// downloader boundary. It is the deterministic seam for scheduler tests.
type fakeDownloader struct {
	loginErr   error
	tasks      []model.Torrent
	torrentErr error
	addedTags  map[string][]string
	deleted    map[string]bool
	loginCalls int
}

func (f *fakeDownloader) Login(context.Context) error {
	f.loginCalls++
	return f.loginErr
}
func (f *fakeDownloader) Torrents(context.Context) ([]model.Torrent, error) {
	return f.tasks, f.torrentErr
}
func (f *fakeDownloader) AddTags(_ context.Context, hash, tags string) error {
	if f.addedTags == nil {
		f.addedTags = map[string][]string{}
	}
	f.addedTags[hash] = append(f.addedTags[hash], tags)
	return nil
}
func (f *fakeDownloader) Delete(_ context.Context, hash string, deleteFiles bool) error {
	if f.deleted == nil {
		f.deleted = map[string]bool{}
	}
	f.deleted[hash] = deleteFiles
	return nil
}

type fakeScraper struct {
	calls   int
	lastAni model.Ani
	result  ScrapeResult
	err     error
}

func (f *fakeScraper) Scrape(_ context.Context, ani *model.Ani, force bool) (ScrapeResult, error) {
	f.calls++
	f.lastAni = *ani
	return f.result, f.err
}

type fakeSubs struct {
	items   []model.Ani
	paths   map[string]string
	enabled map[string]bool
}

func (f *fakeSubs) Items() []model.Ani { return f.items }
func (f *fakeSubs) DownloadPath(item model.Ani) (map[string]any, error) {
	if f.paths == nil {
		return map[string]any{"downloadPath": "/media/" + item.ID}, nil
	}
	return map[string]any{"downloadPath": f.paths[item.ID]}, nil
}
func (f *fakeSubs) BatchEnable(value bool, ids []string) error {
	if f.enabled == nil {
		f.enabled = map[string]bool{}
	}
	for _, id := range ids {
		f.enabled[id] = value
	}
	return nil
}

func baseConfig() model.Config {
	return model.Config{
		"rename": true, "scrape": true, "delete": false, "autoDisabled": false,
		"renameSleepSeconds": 10,
	}
}

func finishedTask(hash, savePath string) model.Torrent {
	return model.Torrent{Hash: hash, Name: "Some Anime - 01", State: "stoppedUP", Progress: 100, SavePath: savePath}
}

func TestSkipsUnfinishedAndAlreadyTagged(t *testing.T) {
	dl := &fakeDownloader{tasks: []model.Torrent{
		{Hash: "a", State: "downloading", Progress: 40, SavePath: "/media/ani-1"},
		finishedTask("b", "/media/ani-1"), // already tagged
	}}
	dl.tasks[1].TagList = []string{"下载完成"}
	scraper := &fakeScraper{}
	c := &Coordinator{
		Config: baseConfig(), Downloader: dl, Scraper: scraper,
		Subscriptions: &fakeSubs{items: []model.Ani{{ID: "ani-1", Title: "A", Enable: true}}},
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if scraper.calls != 0 {
		t.Fatalf("expected no scrape for unfinished/tagged tasks, got %d", scraper.calls)
	}
}

func TestCompletesFinishedTaskScrapesAndTags(t *testing.T) {
	dl := &fakeDownloader{tasks: []model.Torrent{finishedTask("h1", "/media/ani-1")}}
	scraper := &fakeScraper{result: ScrapeResult{Processed: 1}}
	var events []string
	c := &Coordinator{
		Config: baseConfig(), Downloader: dl, Scraper: scraper,
		Subscriptions: &fakeSubs{items: []model.Ani{{ID: "ani-1", Title: "A", Enable: true, Message: true}}},
		Notify: func(_ context.Context, ev NotificationEvent) error {
			events = append(events, ev.Status)
			return nil
		},
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if scraper.calls != 1 {
		t.Fatalf("expected one scrape, got %d", scraper.calls)
	}
	if len(dl.addedTags["h1"]) == 0 {
		t.Fatal("expected completion tag to be applied")
	}
}

func TestResolvesSubscriptionBySavePath(t *testing.T) {
	dl := &fakeDownloader{tasks: []model.Torrent{
		finishedTask("x", "/other/path"), // no subscription matches
	}}
	scraper := &fakeScraper{}
	c := &Coordinator{
		Config: baseConfig(), Downloader: dl, Scraper: scraper,
		Subscriptions: &fakeSubs{items: []model.Ani{{ID: "ani-1", Title: "A"}}},
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if scraper.calls != 0 {
		t.Fatalf("unmatched save path must not scrape, got %d", scraper.calls)
	}
}

func TestAutoDisabledCompletesSubscription(t *testing.T) {
	cfg := baseConfig()
	cfg["autoDisabled"] = true
	cfg["completed"] = true
	ani := model.Ani{ID: "ani-1", Title: "A", Enable: true, Message: true, Completed: true, TotalEpisodeNumber: 1, CurrentEpisodeNumber: 1}
	dl := &fakeDownloader{tasks: []model.Torrent{finishedTask("h", "/media/ani-1")}}
	subs := &fakeSubs{items: []model.Ani{ani}}
	scraper := &fakeScraper{result: ScrapeResult{Processed: 1}}
	var statuses []string
	c := &Coordinator{
		Config: cfg, Downloader: dl, Scraper: scraper, Subscriptions: subs,
		Notify: func(_ context.Context, ev NotificationEvent) error {
			statuses = append(statuses, ev.Status)
			return nil
		},
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if subs.enabled["ani-1"] != false {
		t.Fatal("expected subscription to be disabled on completion")
	}
	found := false
	for _, s := range statuses {
		if s == "COMPLETED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected COMPLETED notification, got %v", statuses)
	}
}

func TestDownloaderLoginFailureAbortsRound(t *testing.T) {
	dl := &fakeDownloader{loginErr: errors.New("no login")}
	scraper := &fakeScraper{}
	c := &Coordinator{Config: baseConfig(), Downloader: dl, Scraper: scraper, Subscriptions: &fakeSubs{}}
	if err := c.Run(context.Background()); err == nil {
		t.Fatal("expected login failure to surface")
	}
	if scraper.calls != 0 {
		t.Fatal("no work should happen when downloader login fails")
	}
}

func TestDeleteRespectsToggles(t *testing.T) {
	dl := &fakeDownloader{tasks: []model.Torrent{finishedTask("h", "/media/ani-1")}}
	c := &Coordinator{
		Config:        model.Config{"scrape": false, "delete": true, "deleteStandbyRSSOnly": true},
		Downloader:    dl,
		Subscriptions: &fakeSubs{items: []model.Ani{{ID: "ani-1", Title: "A"}}},
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok := dl.deleted["h"]; ok {
		t.Fatal("deleteStandbyRSSOnly must skip deletion entirely (Java RenameTask continue)")
	}
}

func TestAwaitStalledUPRequiresStoppedUP(t *testing.T) {
	seeding := model.Torrent{Hash: "h", State: "stalledUP", Progress: 100, SavePath: "/media/ani-1"}
	dl := &fakeDownloader{tasks: []model.Torrent{seeding}}
	c := &Coordinator{
		Config:        model.Config{"scrape": false, "delete": true, "awaitStalledUP": true},
		Downloader:    dl,
		Subscriptions: &fakeSubs{items: []model.Ani{{ID: "ani-1", Title: "A"}}},
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok := dl.deleted["h"]; ok {
		t.Fatal("awaitStalledUP must only delete fully-seeded stoppedUP tasks")
	}
}

func TestFinishedStateSet(t *testing.T) {
	for state, want := range map[string]bool{
		"queuedUP": true, "uploading": true, "stalledUP": true, "stoppedUP": true,
		"downloading": false, "stalledDL": false, "pausedUP": false, "forcedUP": true, "error": false,
	} {
		if got := (model.Torrent{State: state, Progress: 100}).Finished(); got != want {
			t.Errorf("state %s: got %v want %v", state, got, want)
		}
	}
	if (model.Torrent{State: "stoppedUP", Progress: 40}).Finished() {
		t.Error("progress<100 must not be finished")
	}
}
