package rss

import (
	"context"
	"errors"
	"net/http"
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
		Reserve:      c.reserve,
		Release:      c.release,
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
