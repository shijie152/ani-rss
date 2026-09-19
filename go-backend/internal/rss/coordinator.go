package rss

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
	"github.com/shijie152/ani-rss/go-backend/internal/subscription"
)

type Coordinator struct {
	Config        *appconfig.Manager
	Subscriptions *subscription.Service
	History       store.HistoryStore
	HTTPClient    *http.Client
	QB            downloader.Adapter
	Notify        func(context.Context, model.Ani, *model.Resource, string, string) error
	Retry         int
	ConfigDir     string
	Logger        *slog.Logger
	mu            sync.Mutex
	inFlight      map[string]struct{}
}

func (c *Coordinator) Refresh(ctx context.Context, item model.Ani) ([]model.Resource, error) {
	if c.QB == nil {
		return nil, errors.New("下载器未配置")
	}
	all, intakeErr := c.intake().Collect(ctx, item)
	var failures []string
	if intakeErr != nil {
		failures = append(failures, intakeErr.Error())
	}
	// Side-channel checks that mirror Java's ItemsUtil.omit/procrastinating.
	// They observe the matched resource set and only emit notifications; they
	// never influence matching, submission, or progress.
	c.checkOmit(ctx, item, all)
	c.checkProcrastinating(ctx, item, all)
	submitted, submitErr := c.submit(ctx, item, all)
	progressResources := all
	if submitErr != nil {
		all = submitted
		progressResources = submitted
		failures = append(failures, submitErr.Error())
	}
	// A successful standby feed may still be submitted when the primary feed
	// failed. Persist progress whenever at least one resource was actually
	// accepted by the downloader, while avoiding progress updates for a fully
	// failed or duplicate-only refresh.
	if len(submitted) > 0 {
		if progressErr := c.Subscriptions.UpdateCurrentEpisode(item.ID, progressResources); progressErr != nil {
			failures = append(failures, progressErr.Error())
		}
	}
	if len(failures) > 0 {
		return all, errors.New(strings.Join(failures, "; "))
	}
	return all, nil
}

// Preview returns the same parsed and matched resources as Refresh without
// contacting the downloader or mutating resource history.
func (c *Coordinator) Preview(ctx context.Context, item model.Ani) ([]model.Resource, error) {
	return c.intake().Collect(ctx, item)
}

// PreviewResult is shaped for the existing PreviewView component.
func (c *Coordinator) PreviewResult(ctx context.Context, item model.Ani) (map[string]any, error) {
	resources, err := c.Preview(ctx, item)
	pathData, pathErr := c.Subscriptions.DownloadPath(item)
	if pathErr != nil {
		return nil, pathErr
	}
	history := []model.Resource{}
	if c.History != nil {
		history, _ = c.History.LoadResources()
	}
	historyKeys := map[string]bool{}
	for _, value := range history {
		historyKeys[resourceKey(value)] = true
	}
	items := make([]model.Item, 0, len(resources))
	for _, value := range resources {
		cached := historyKeys[resourceKey(value)]
		if c.ConfigDir != "" {
			cached = cached || HasCachedResource(c.ConfigDir, item, value)
		}
		items = append(items, model.Item{Title: value.Title, ReName: value.Title, Torrent: value.DownloadURL, InfoHash: value.InfoHash, Episode: value.Episode, FormatSize: value.FormatSize, Length: value.Size, HasDownloaded: cached, Master: value.Master, Subgroup: value.Subgroup, PubDate: value.PublishedAt, Source: value.Source, Description: value.Description})
	}
	return map[string]any{"downloadPath": pathData["downloadPath"], "items": items, "omitList": omitEpisodes(items)}, err
}

func omitEpisodes(items []model.Item) []int {
	seen := map[int]bool{}
	max := 0
	min := 0
	for _, item := range items {
		if item.Episode != float64(int(item.Episode)) || item.Episode <= 0 {
			continue
		}
		episode := int(item.Episode)
		seen[episode] = true
		if min == 0 || episode < min {
			min = episode
		}
		if episode > max {
			max = episode
		}
	}
	result := []int{}
	for episode := min; episode <= max && episode > 0; episode++ {
		if !seen[episode] {
			result = append(result, episode)
		}
	}
	return result
}

func (c *Coordinator) refreshFeed(ctx context.Context, item model.Ani, feedURL, subgroup string, offset int, master bool) ([]model.Resource, error) {
	return c.intake().CollectFeed(ctx, item, Feed{URL: feedURL, Subgroup: subgroup, Offset: offset, Master: master})
}

func (c *Coordinator) intake() *Intake {
	return &Intake{Config: c.Config, HTTPClient: c.HTTPClient, Retry: c.Retry}
}

func (c *Coordinator) RefreshAll(ctx context.Context) (map[string][]model.Resource, error) {
	result := map[string][]model.Resource{}
	var failures []string
	for _, item := range c.Subscriptions.Items() {
		if !item.Enable {
			continue
		}
		resources, err := c.Refresh(ctx, item)
		if err != nil {
			failures = append(failures, item.ID+": "+err.Error())
			continue
		}
		result[item.ID] = resources
	}
	if len(failures) > 0 {
		return result, errors.New(strings.Join(failures, "; "))
	}
	return result, nil
}

// WaitForCompletion keeps the RSS coordinator as the boundary for the full
// refresh-to-download lifecycle. Callers do not need to know which downloader
// protocol supplies the status events.
func (c *Coordinator) WaitForCompletion(ctx context.Context, hash string, interval time.Duration) (model.Torrent, error) {
	if c.QB == nil {
		return model.Torrent{}, errors.New("下载器未配置")
	}
	if err := c.QB.Login(ctx); err != nil {
		return model.Torrent{}, err
	}
	return c.QB.WaitForCompletion(ctx, hash, interval)
}

func (c *Coordinator) submit(ctx context.Context, ani model.Ani, resources []model.Resource) ([]model.Resource, error) {
	s := &Submitter{
		Config:       c.Config,
		DownloadPath: c.Subscriptions.DownloadPath,
		SaveCache:    SaveResourceCache,
		History:      c.History,
		Downloader:   c.QB,
		Notify:       c.Notify,
		ConfigDir:    c.ConfigDir,
		HTTPClient:   c.HTTPClient,
		LocalExists:  c.localFileExists,
		SaveExist: func(ctx context.Context, a model.Ani, r model.Resource) {
			// Mirror Java's saveTorrent-on-hit so the fileExist check becomes a
			// cheap cache lookup on subsequent refreshes.
			if err := SaveResourceCache(ctx, c.HTTPClient, c.ConfigDir, a, r); err != nil {
				// A cache miss must not fail the refresh; the existence check
				// still protected this pass.
				if c.Logger != nil {
					c.Logger.Warn("fileExist cache mark failed", "error", err)
				}
			}
		},
		Reserve: c.reserve,
		Release: c.release,
	}
	return s.Submit(ctx, ani, resources)
}

func (c *Coordinator) reserve(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inFlight == nil {
		c.inFlight = make(map[string]struct{})
	}
	if _, exists := c.inFlight[key]; exists {
		return false
	}
	c.inFlight[key] = struct{}{}
	return true
}

func (c *Coordinator) release(key string) {
	c.mu.Lock()
	delete(c.inFlight, key)
	c.mu.Unlock()
}

func dedupeFeeds(items []model.Resource, ani model.Ani, coexist bool) []model.Resource {
	if coexist || ani.OVA {
		return items
	}
	result := make([]model.Resource, 0, len(items))
	positions := map[float64]int{}
	for _, item := range items {
		if position, ok := positions[item.Episode]; ok {
			// A master feed wins over a standby feed for the same episode.
			if item.Master && !result[position].Master {
				result[position] = item
			}
			continue
		}
		positions[item.Episode] = len(result)
		result = append(result, item)
	}
	return result
}

// localFileExists implements the Java fileExist check: a resource is treated
// as already downloaded when the subscription's download directory already
// holds a video file for the same season+episode (or any video for OVA).
func (c *Coordinator) localFileExists(ani model.Ani, resource model.Resource) bool {
	if c.Subscriptions == nil {
		return false
	}
	data, err := c.Subscriptions.DownloadPath(ani)
	if err != nil {
		return false
	}
	path, _ := data["downloadPath"].(string)
	if path == "" {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !isVideoName(entry.Name()) {
			continue
		}
		if ani.OVA {
			return true
		}
		season, episode, ok := seasonEpisodeFor(entry.Name())
		if ok && season == ani.Season && episode == resource.Episode {
			return true
		}
	}
	return false
}

var seasonEpisodeRE = regexp.MustCompile(`[Ss](\d+)[Ee](\d+(\.5)?)`)

// seasonEpisodeFor extracts S{season}E{episode} from a media filename using the
// same SEASON_REG the Java fileExist check applies.
func seasonEpisodeFor(name string) (int, float64, bool) {
	match := seasonEpisodeRE.FindStringSubmatch(name)
	if len(match) < 3 {
		return 0, 0, false
	}
	season, serr := strconv.Atoi(match[1])
	episode, eerr := strconv.ParseFloat(match[2], 64)
	return season, episode, serr == nil && eerr == nil
}

func isVideoName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".avi", ".wmv", ".ts", ".m4v", ".mov", ".flv":
		return true
	}
	return false
}

// checkOmit emits a single OMIT notification when the matched resources leave
// gaps in the episode run, mirroring ItemsUtil.omit: global omit + the
// subscription's own omit flag, non-OVA, and at most 10 missing episodes.
// The dispatcher's 24h dedupe supplies Java's per-day repeat suppression.
func (c *Coordinator) checkOmit(ctx context.Context, ani model.Ani, resources []model.Resource) {
	if c.Notify == nil || c.Config == nil {
		return
	}
	cfg := c.Config.Snapshot()
	if !appconfig.Bool(cfg, "omit") || !ani.Omit || ani.OVA {
		return
	}
	missing := omittedEpisodes(resources)
	if len(missing) == 0 || len(missing) > 10 {
		return
	}
	parts := make([]string, 0, len(missing))
	for _, ep := range missing {
		parts = append(parts, "缺少集数 "+ani.Title+" S"+pad2(ani.Season)+"E"+pad2(ep))
	}
	if err := c.Notify(ctx, ani, nil, "OMIT", strings.Join(parts, "\n")); err != nil && c.Logger != nil {
		c.Logger.Warn("omit notification failed", "subscription", ani.ID, "error", err)
	}
}

// checkProcrastinating mirrors ItemsUtil.procrastinating: when the newest
// (optionally master-only) resource is older than procrastinatingDay days, a
// single PROCRASTINATING notice is emitted; dispatcher dedupe repeats daily.
func (c *Coordinator) checkProcrastinating(ctx context.Context, ani model.Ani, resources []model.Resource) {
	if c.Notify == nil || c.Config == nil {
		return
	}
	cfg := c.Config.Snapshot()
	if !appconfig.Bool(cfg, "procrastinating") || !ani.Procrastinating {
		return
	}
	masterOnly := appconfig.Bool(cfg, "procrastinatingMasterOnly")
	var latest *time.Time
	for _, resource := range resources {
		if masterOnly && !resource.Master {
			continue
		}
		if resource.PublishedAt == nil {
			continue
		}
		if latest == nil || resource.PublishedAt.After(*latest) {
			stamp := *resource.PublishedAt
			latest = &stamp
		}
	}
	if latest == nil || latest.After(time.Now()) {
		return
	}
	days := int(time.Since(*latest).Hours() / 24)
	limit := appconfig.Int(cfg, "procrastinatingDay")
	if limit < 1 {
		limit = 14
	}
	if days < limit {
		return
	}
	text := "检测到" + ani.Title + ", 已摸鱼" + strconv.Itoa(days) + "天"
	if err := c.Notify(ctx, ani, nil, "PROCRASTINATING", text); err != nil && c.Logger != nil {
		c.Logger.Warn("procrastinating notification failed", "subscription", ani.ID, "error", err)
	}
}

// omittedEpisodes returns the integer episode numbers missing between the
// lowest and highest matched episode, the same set PreviewResult.omitList
// reports to the UI.
func omittedEpisodes(resources []model.Resource) []int {
	seen := map[int]bool{}
	min, max := 0, 0
	for _, resource := range resources {
		if resource.Episode <= 0 || resource.Episode != float64(int(resource.Episode)) {
			continue
		}
		ep := int(resource.Episode)
		seen[ep] = true
		if min == 0 || ep < min {
			min = ep
		}
		if ep > max {
			max = ep
		}
	}
	missing := []int{}
	for ep := min; ep <= max && ep > 0; ep++ {
		if !seen[ep] {
			missing = append(missing, ep)
		}
	}
	return missing
}

func pad2(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}
