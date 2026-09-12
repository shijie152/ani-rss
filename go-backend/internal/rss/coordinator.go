package rss

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	QB            *downloader.QBittorrent
	Retry         int
	mu            sync.Mutex
}

func (c *Coordinator) Refresh(ctx context.Context, item model.Ani) ([]model.Resource, error) {
	if c.QB == nil {
		return nil, errors.New("qBittorrent is not configured")
	}
	feeds := []struct {
		url, subgroup string
		offset        int
		master        bool
	}{{item.URL, item.Subgroup, item.Offset, true}}
	if appconfig.Bool(c.Config.Snapshot(), "standbyRss") {
		for _, standby := range item.StandbyRSSList {
			feeds = append(feeds, struct {
				url, subgroup string
				offset        int
				master        bool
			}{standby.URL, standby.Label, standby.Offset, false})
		}
	}
	all := []model.Resource{}
	var failures []string
	for _, feed := range feeds {
		if feed.url == "" {
			continue
		}
		resources, err := c.refreshFeed(ctx, item, feed.url, feed.subgroup, feed.offset, feed.master)
		if err != nil {
			failures = append(failures, feed.url+": "+err.Error())
			continue
		}
		all = append(all, resources...)
	}
	all = dedupeFeeds(all, item, appconfig.Bool(c.Config.Snapshot(), "coexist"))
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
	feeds := []struct {
		url, subgroup string
		offset        int
		master        bool
	}{{item.URL, item.Subgroup, item.Offset, true}}
	if appconfig.Bool(c.Config.Snapshot(), "standbyRss") {
		for _, standby := range item.StandbyRSSList {
			feeds = append(feeds, struct {
				url, subgroup string
				offset        int
				master        bool
			}{standby.URL, standby.Label, standby.Offset, false})
		}
	}
	resources := []model.Resource{}
	var failures []string
	for _, feed := range feeds {
		if feed.url == "" {
			continue
		}
		values, err := c.fetchFeed(ctx, item, feed.url, feed.subgroup, feed.offset, feed.master)
		if err != nil {
			failures = append(failures, feed.url+": "+err.Error())
			continue
		}
		resources = append(resources, values...)
	}
	resources = dedupeFeeds(resources, item, appconfig.Bool(c.Config.Snapshot(), "coexist"))
	if len(failures) > 0 {
		return resources, errors.New(strings.Join(failures, "; "))
	}
	return resources, nil
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
		items = append(items, model.Item{Title: value.Title, ReName: value.Title, Torrent: value.DownloadURL, InfoHash: value.InfoHash, Episode: value.Episode, FormatSize: value.FormatSize, Length: value.Size, HasDownloaded: historyKeys[resourceKey(value)], Master: value.Master, Subgroup: value.Subgroup, PubDate: value.PublishedAt, Source: value.Source, Description: value.Description})
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
	return c.fetchFeed(ctx, item, feedURL, subgroup, offset, master)
}

func (c *Coordinator) fetchFeed(ctx context.Context, item model.Ani, feedURL, subgroup string, offset int, master bool) ([]model.Resource, error) {
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: time.Duration(appconfig.Int(c.Config.Snapshot(), "rssTimeout")) * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "ani-rss-go")
	attempts := c.Retry
	if attempts < 1 {
		attempts = appconfig.Int(c.Config.Snapshot(), "downloadRetry")
	}
	if attempts < 1 {
		attempts = 1
	}
	var body []byte
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		response, requestErr := client.Do(request)
		if requestErr == nil {
			body, lastErr = readRSSResponse(response)
			if lastErr == nil {
				break
			}
		} else {
			lastErr = requestErr
		}
		if attempt+1 < attempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	resources, err := Parse(body, subgroup, feedURL)
	if err != nil {
		return nil, err
	}
	feedItem := item
	feedItem.Subgroup = subgroup
	options := MatchOptions{GlobalExclude: appconfig.Strings(c.Config.Snapshot(), "exclude"), DownloadNew: item.DownloadNew, SkipHalf: appconfig.Bool(c.Config.Snapshot(), "skip5"), Offset: offset, DelayedMinutes: appconfig.Int(c.Config.Snapshot(), "delayedDownload"), CustomEpisode: item.CustomEpisode, CustomEpisodeRE: item.CustomEpisodeStr, CustomEpisodeIdx: item.CustomEpisodeGroupIndex}
	if item.CustomPriorityKeywordsEnable {
		options.PriorityKeywords = item.CustomPriorityKeywords
	} else if appconfig.Bool(c.Config.Snapshot(), "priorityKeywordsEnable") {
		options.PriorityKeywords = appconfig.Strings(c.Config.Snapshot(), "priorityKeywords")
	}
	options.Coexist = appconfig.Bool(c.Config.Snapshot(), "coexist")
	resources = Match(resources, feedItem, options)
	for index := range resources {
		resources[index].Master = master
	}
	return resources, nil
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
		return model.Torrent{}, errors.New("qBittorrent is not configured")
	}
	if err := c.QB.Login(ctx); err != nil {
		return model.Torrent{}, err
	}
	return c.QB.WaitForCompletion(ctx, hash, interval)
}

func (c *Coordinator) submit(ctx context.Context, ani model.Ani, resources []model.Resource) ([]model.Resource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.QB.Login(ctx); err != nil {
		return nil, err
	}
	var history []model.Resource
	if c.History != nil {
		history, _ = c.History.LoadResources()
	}
	existing := map[string]bool{}
	for _, item := range history {
		existing[resourceKey(item)] = true
	}
	// History is the durable fast path. The downloader is also consulted so a
	// manually restored qBittorrent task cannot be submitted a second time.
	tasks, taskErr := c.QB.Torrents(ctx)
	if taskErr == nil {
		for _, task := range tasks {
			if task.Hash != "" {
				existing[strings.ToLower(task.Hash)] = true
			}
		}
	}
	pathData, err := c.Subscriptions.DownloadPath(ani)
	if err != nil {
		return nil, err
	}
	savePath := pathData["downloadPath"].(string)
	var failures []string
	if deleteErr := c.deleteStandbyTasks(ctx, savePath, resources, tasks); deleteErr != nil {
		// Keep submitting the primary resources even when cleanup of an old
		// standby task fails. This matches Java's best-effort wash behavior and
		// keeps a stale downloader task from blocking the new primary task.
		failures = append(failures, deleteErr.Error())
	}
	newResources := []model.Resource{}
	for _, resource := range resources {
		key := resourceKey(resource)
		if existing[key] {
			continue
		}
		duplicateTask := false
		for _, task := range tasks {
			if (resource.InfoHash != "" && strings.EqualFold(task.Hash, resource.InfoHash)) || strings.EqualFold(task.Name, resource.Title) {
				duplicateTask = true
				break
			}
		}
		if duplicateTask {
			existing[key] = true
			continue
		}
		tags := []string{"ani-rss"}
		if resource.Subgroup != "" {
			tags = append(tags, resource.Subgroup)
		}
		if !resource.Master {
			tags = append(tags, "备用RSS")
		}
		// RSS resources are submitted by URL/magnet. Java starts these tasks
		// immediately; rename is performed after completion, so pausing here
		// would leave a newly submitted task idle forever.
		if err := c.QB.Add(ctx, resource, savePath, tags, false); err != nil {
			failures = append(failures, resource.Title+": "+err.Error())
			continue
		}
		history = append(history, resource)
		newResources = append(newResources, resource)
		existing[key] = true
	}
	if c.History != nil {
		if err := c.History.SaveResources(history); err != nil {
			return newResources, err
		}
	}
	if len(failures) > 0 {
		return newResources, errors.New(strings.Join(failures, "; "))
	}
	return newResources, nil
}

// deleteStandbyTasks performs the explicit wash step used by the Java
// downloader: when a primary resource for an episode arrives, remove an old
// standby task for the same subscription. It is deliberately opt-in because
// deleting downloader tasks/files is user-visible and irreversible.
func (c *Coordinator) deleteStandbyTasks(ctx context.Context, savePath string, resources []model.Resource, tasks []model.Torrent) error {
	if !appconfig.Bool(c.Config.Snapshot(), "delete") || !appconfig.Bool(c.Config.Snapshot(), "deleteStandbyRSSOnly") || !appconfig.Bool(c.Config.Snapshot(), "standbyRss") || appconfig.Bool(c.Config.Snapshot(), "coexist") {
		return nil
	}
	primaryEpisodes := map[float64]bool{}
	for _, resource := range resources {
		if resource.Master && resource.Episode > 0 {
			primaryEpisodes[resource.Episode] = true
		}
	}
	if len(primaryEpisodes) == 0 {
		return nil
	}
	var failures []string
	for _, task := range tasks {
		if task.Hash == "" || task.SavePath != savePath || !hasTorrentTag(task.TagList, "备用RSS") || !primaryEpisodes[Episode(task.Name)] {
			continue
		}
		if err := c.QB.Delete(ctx, task.Hash, true); err != nil {
			failures = append(failures, task.Hash+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New("备用 RSS 清理失败: " + strings.Join(failures, "; "))
	}
	return nil
}

func hasTorrentTag(tags []string, target string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), target) {
			return true
		}
	}
	return false
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

func readRSSResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("RSS returned HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 16<<20))
}

func resourceKey(resource model.Resource) string {
	if resource.InfoHash != "" {
		return strings.ToLower(resource.InfoHash)
	}
	sum := sha256.Sum256([]byte(resource.DownloadURL + "\x00" + resource.Title))
	return hex.EncodeToString(sum[:])
}
