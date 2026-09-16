package rss

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// Feed describes one primary or standby RSS input. It is deliberately small
// so the same intake seam can be used by refresh, preview and conversion.
type Feed struct {
	URL      string
	Subgroup string
	Offset   int
	Master   bool
}

// Intake owns the feed -> parse -> subgroup -> match pipeline. Submission is
// intentionally outside this type, so preview and conversion cannot mutate
// downloader or history state by accident.
type Intake struct {
	Config     appconfig.Reader
	HTTPClient *http.Client
	Retry      int
}

func (i *Intake) Feeds(item model.Ani) []Feed {
	feeds := []Feed{{URL: item.URL, Subgroup: item.Subgroup, Offset: item.Offset, Master: true}}
	if i != nil && i.Config != nil && appconfig.Bool(i.Config.Snapshot(), "standbyRss") {
		for _, standby := range item.StandbyRSSList {
			feeds = append(feeds, Feed{URL: standby.URL, Subgroup: standby.Label, Offset: standby.Offset})
		}
	}
	return feeds
}

// Collect gets and matches every configured feed, preserving the legacy
// partial-failure behavior: successful feeds remain visible alongside a
// joined error for failed feeds.
func (i *Intake) Collect(ctx context.Context, item model.Ani) ([]model.Resource, error) {
	if i == nil {
		return nil, errors.New("RSS intake 未配置")
	}
	resources := make([]model.Resource, 0)
	var failures []string
	for _, feed := range i.Feeds(item) {
		if strings.TrimSpace(feed.URL) == "" {
			continue
		}
		values, err := i.CollectFeed(ctx, item, feed)
		if err != nil {
			failures = append(failures, feed.URL+": "+err.Error())
			continue
		}
		resources = append(resources, values...)
	}
	resources = dedupeFeeds(resources, item, i.coexist())
	if len(failures) > 0 {
		return resources, errors.New(strings.Join(failures, "; "))
	}
	return resources, nil
}

func (i *Intake) CollectFeed(ctx context.Context, item model.Ani, feed Feed) ([]model.Resource, error) {
	client := i.HTTPClient
	if client == nil {
		timeout := 20 * time.Second
		if i.Config != nil && appconfig.Int(i.Config.Snapshot(), "rssTimeout") > 0 {
			timeout = time.Duration(appconfig.Int(i.Config.Snapshot(), "rssTimeout")) * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	retries := i.Retry
	if retries < 1 && i.Config != nil {
		retries = appconfig.Int(i.Config.Snapshot(), "downloadRetry")
	}
	if retries < 1 {
		retries = 1
	}
	body, err := Fetch(ctx, client, feed.URL, retries)
	if err != nil {
		return nil, err
	}
	resources, err := Parse(body, feed.Subgroup, feed.URL)
	if err != nil {
		return nil, err
	}
	if feed.Subgroup == "未知字幕组" {
		if subgroup := InferSubgroup(resources); subgroup != "" {
			feed.Subgroup = subgroup
			for index := range resources {
				resources[index].Subgroup = subgroup
			}
		}
	}
	matchItem := item
	if feed.Subgroup != "" {
		matchItem.Subgroup = feed.Subgroup
	}
	options := i.matchOptions(item, feed.Offset)
	resources = Match(resources, matchItem, options)
	for index := range resources {
		resources[index].Master = feed.Master
		resources[index].AniID = item.ID
	}
	return resources, nil
}

func (i *Intake) matchOptions(item model.Ani, offset int) MatchOptions {
	options := MatchOptions{Offset: offset, DownloadNew: item.DownloadNew, CustomEpisode: item.CustomEpisode, CustomEpisodeRE: item.CustomEpisodeStr, CustomEpisodeIdx: item.CustomEpisodeGroupIndex}
	if i.Config == nil {
		return options
	}
	cfg := i.Config.Snapshot()
	options.GlobalExclude = appconfig.Strings(cfg, "exclude")
	options.SkipHalf = appconfig.Bool(cfg, "skip5")
	options.DelayedMinutes = appconfig.Int(cfg, "delayedDownload")
	options.Coexist = appconfig.Bool(cfg, "coexist")
	if item.CustomPriorityKeywordsEnable {
		options.PriorityKeywords = item.CustomPriorityKeywords
	} else if appconfig.Bool(cfg, "priorityKeywordsEnable") {
		options.PriorityKeywords = appconfig.Strings(cfg, "priorityKeywords")
	}
	return options
}

func (i *Intake) coexist() bool {
	return i != nil && i.Config != nil && appconfig.Bool(i.Config.Snapshot(), "coexist")
}

// InferSubgroup follows the Java conversion convention: the first bracketed
// prefix is the publisher, including when the title is a path-like value.
func InferSubgroup(resources []model.Resource) string {
	return inferSubgroup(resources)
}

func inferSubgroup(resources []model.Resource) string {
	for _, resource := range resources {
		name := strings.TrimSpace(resource.Title)
		if subgroup := bracketPrefix(name); subgroup != "" {
			return subgroup
		}
		if subgroup := bracketPrefix(nameAfterLastSlash(name)); subgroup != "" {
			return subgroup
		}
	}
	return ""
}

func bracketPrefix(value string) string {
	if !strings.HasPrefix(value, "[") {
		return ""
	}
	end := strings.IndexByte(value, ']')
	if end <= 1 {
		return ""
	}
	return strings.TrimSpace(value[1:end])
}

func nameAfterLastSlash(value string) string {
	if index := strings.LastIndexAny(value, "/\\"); index >= 0 {
		return value[index+1:]
	}
	return value
}
