package rss

import (
	"context"
	"testing"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

// newManager builds a config Manager on a throwaway store and overlays the
// supplied keys, giving tests the concrete *appconfig.Manager the
// Coordinator/Submitter seams require.
func newManager(t *testing.T, overlay model.Config) *appconfig.Manager {
	t.Helper()
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	if len(overlay) > 0 {
		if err := m.Update(overlay); err != nil {
			t.Fatalf("update: %v", err)
		}
	}
	return m
}

// --- downloadCount -------------------------------------------------------

type recordingDownloader struct {
	added  []model.Resource
	tasks  []model.Torrent
	addErr error
}

func (r *recordingDownloader) Login(context.Context) error { return nil }
func (r *recordingDownloader) Add(_ context.Context, res model.Resource, _ string, _ []string, _ bool) error {
	r.added = append(r.added, res)
	return r.addErr
}
func (r *recordingDownloader) Torrents(context.Context) ([]model.Torrent, error) { return r.tasks, nil }
func (r *recordingDownloader) Delete(context.Context, string, bool) error        { return nil }

func submitterFor(cfg *appconfig.Manager, dl *recordingDownloader) *Submitter {
	return &Submitter{
		Config: cfg,
		DownloadPath: func(model.Ani) (map[string]any, error) {
			return map[string]any{"downloadPath": "/media/x"}, nil
		},
		Downloader: dl,
	}
}

func res(title string, ep float64, master bool) model.Resource {
	return model.Resource{Title: title, Episode: ep, Master: master, InfoHash: title}
}

func TestDownloadCountCapsConcurrentSubmissions(t *testing.T) {
	cfg := newManager(t, model.Config{"downloadCount": 2})
	dl := &recordingDownloader{tasks: []model.Torrent{
		{Hash: "u1", State: "downloading", Progress: 30}, // 1 unfinished already
	}}
	s := submitterFor(cfg, dl)
	resources := []model.Resource{res("e1", 1, true), res("e2", 2, true), res("e3", 3, true)}
	got, err := s.Submit(context.Background(), model.Ani{ID: "a"}, resources)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// unfinished=1, limit=2 -> only one more master integer episode may go out.
	if len(dl.added) != 1 {
		t.Fatalf("expected 1 submission under downloadCount, got %d", len(dl.added))
	}
	_ = got
}

func TestDownloadCountIgnoresHalfAndStandby(t *testing.T) {
	cfg := newManager(t, model.Config{"downloadCount": 1})
	dl := &recordingDownloader{}
	s := submitterFor(cfg, dl)
	resources := []model.Resource{res("half", 1.5, true), res("standby", 2, false), res("main", 3, true), res("extra", 4, true)}
	if _, err := s.Submit(context.Background(), model.Ani{ID: "a"}, resources); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// .5 and standby are free; exactly one master integer episode fits the cap.
	masters := 0
	for _, r := range dl.added {
		if r.Master && r.Episode == float64(int(r.Episode)) {
			masters++
		}
	}
	if masters != 1 {
		t.Fatalf("expected 1 counted master submission, got %d (added %d total)", masters, len(dl.added))
	}
}

// --- fileExist -----------------------------------------------------------

func TestFileExistSkipsResourceOnDisk(t *testing.T) {
	cfg := newManager(t, model.Config{"fileExist": true})
	dl := &recordingDownloader{}
	s := submitterFor(cfg, dl)
	marked := 0
	s.LocalExists = func(_ model.Ani, r model.Resource) bool { return r.Episode == 2 }
	s.SaveExist = func(context.Context, model.Ani, model.Resource) { marked++ }
	resources := []model.Resource{res("e1", 1, true), res("e2", 2, true)}
	if _, err := s.Submit(context.Background(), model.Ani{ID: "a"}, resources); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(dl.added) != 1 || dl.added[0].Episode != 1 {
		t.Fatalf("expected only ep1 submitted, got %+v", dl.added)
	}
	if marked != 1 {
		t.Fatalf("expected the on-disk resource to be marked once, got %d", marked)
	}
}

func TestSeasonEpisodeFor(t *testing.T) {
	season, ep, ok := seasonEpisodeFor("Some Show S02E07.mkv")
	if !ok || season != 2 || ep != 7 {
		t.Fatalf("got %d %v %v", season, ep, ok)
	}
	if _, _, ok := seasonEpisodeFor("no-episode.mkv"); ok {
		t.Fatal("expected no match")
	}
	if _, ep, ok := seasonEpisodeFor("Show S01E02.5.mkv"); !ok || ep != 2.5 {
		t.Fatalf("half episode: %v %v", ep, ok)
	}
}

// --- omit ----------------------------------------------------------------

func TestOmittedEpisodesFindsGaps(t *testing.T) {
	got := omittedEpisodes([]model.Resource{res("a", 1, true), res("b", 2, true), res("c", 5, true)})
	if len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("expected [3 4], got %v", got)
	}
	if len(omittedEpisodes([]model.Resource{res("a", 3, true)})) != 0 {
		t.Fatal("single resource has no gaps")
	}
}

func TestCheckOmitNotifiesWithinLimit(t *testing.T) {
	var got []string
	c := &Coordinator{
		Config: newManager(t, model.Config{"omit": true}),
		Notify: func(_ context.Context, _ model.Ani, _ *model.Resource, status, text string) error {
			got = append(got, status+":"+text)
			return nil
		},
	}
	ani := model.Ani{ID: "a", Title: "Show", Season: 1, Omit: true}
	// episodes 1..5 present except 3 -> one OMIT event naming the gap.
	c.checkOmit(context.Background(), ani, []model.Resource{res("a", 1, true), res("b", 2, true), res("d", 4, true), res("e", 5, true)})
	if len(got) != 1 || got[0][:4] != "OMIT" {
		t.Fatalf("expected one OMIT, got %v", got)
	}
	// >10 missing -> suppressed as a likely false positive.
	got = nil
	big := []model.Resource{res("a", 1, true), res("z", 30, true)}
	c.checkOmit(context.Background(), ani, big)
	if len(got) != 0 {
		t.Fatalf("expected suppression for >10 gaps, got %v", got)
	}
}

func TestCheckOmitRespectsFlags(t *testing.T) {
	c := &Coordinator{Config: newManager(t, model.Config{"omit": false}), Notify: func(context.Context, model.Ani, *model.Resource, string, string) error {
		t.Fatal("must not notify when omit disabled")
		return nil
	}}
	c.checkOmit(context.Background(), model.Ani{Omit: true}, []model.Resource{res("a", 1, true), res("c", 4, true)})
}

// --- procrastinating -----------------------------------------------------

func TestCheckProcrastinatingNotifiesWhenStale(t *testing.T) {
	old := time.Now().Add(-20 * 24 * time.Hour)
	var got []string
	c := &Coordinator{
		Config: newManager(t, model.Config{"procrastinating": true, "procrastinatingDay": 14}),
		Notify: func(_ context.Context, _ model.Ani, _ *model.Resource, status, text string) error {
			got = append(got, status)
			return nil
		},
	}
	ani := model.Ani{ID: "a", Title: "Show", Procrastinating: true}
	c.checkProcrastinating(context.Background(), ani, []model.Resource{{Title: "x", Episode: 5, Master: true, PublishedAt: &old}})
	if len(got) != 1 || got[0] != "PROCRASTINATING" {
		t.Fatalf("expected PROCRASTINATING, got %v", got)
	}
}

func TestCheckProcrastinatingSkipsFresh(t *testing.T) {
	fresh := time.Now().Add(-2 * 24 * time.Hour)
	c := &Coordinator{
		Config: newManager(t, model.Config{"procrastinating": true, "procrastinatingDay": 14}),
		Notify: func(context.Context, model.Ani, *model.Resource, string, string) error {
			t.Fatal("must not notify for fresh resource")
			return nil
		},
	}
	c.checkProcrastinating(context.Background(), model.Ani{Procrastinating: true}, []model.Resource{{PublishedAt: &fresh, Master: true}})
}
