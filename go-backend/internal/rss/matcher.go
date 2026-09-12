package rss

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/regexutil"
)

type MatchOptions struct {
	GlobalExclude    []string
	PriorityKeywords []string
	DownloadNew      bool
	SkipHalf         bool
	Offset           int
	DelayedMinutes   int
	Coexist          bool
	CustomEpisode    bool
	CustomEpisodeRE  string
	CustomEpisodeIdx int
}

func Match(items []model.Resource, ani model.Ani, options MatchOptions) []model.Resource {
	result := make([]model.Resource, 0, len(items))
	for _, item := range items {
		if options.DelayedMinutes > 0 && item.PublishedAt != nil && item.PublishedAt.After(time.Now().Add(-time.Duration(options.DelayedMinutes)*time.Minute)) {
			continue
		}
		if options.CustomEpisode {
			if episode, ok := customEpisode(item.Title, options.CustomEpisodeRE, options.CustomEpisodeIdx); ok {
				item.Episode = episode
			} else {
				continue
			}
		}
		item.Episode += float64(options.Offset)
		if item.Episode <= 0 || (options.SkipHalf && item.Episode != float64(int(item.Episode))) {
			continue
		}
		if item.Subgroup == "" {
			item.Subgroup = ani.Subgroup
		}
		if ani.Subgroup != "" && item.Subgroup != ani.Subgroup {
			continue
		}
		if matchesAny(ani.Exclude, item.Title) || (ani.GlobalExclude && matchesAny(options.GlobalExclude, item.Title)) {
			continue
		}
		if len(ani.Match) > 0 && !matchesAll(ani.Match, item.Title) {
			continue
		}
		if containsEpisode(ani.NotDownload, item.Episode) {
			continue
		}
		item.Master = true
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Episode != result[j].Episode {
			return result[i].Episode < result[j].Episode
		}
		priority := func(title string) int {
			for index, keyword := range options.PriorityKeywords {
				if strings.Contains(strings.ToLower(title), strings.ToLower(keyword)) {
					return index
				}
			}
			return len(options.PriorityKeywords)
		}
		if priority(result[i].Title) != priority(result[j].Title) {
			return priority(result[i].Title) < priority(result[j].Title)
		}
		return result[i].PublishedAt != nil && result[j].PublishedAt != nil && result[i].PublishedAt.Before(*result[j].PublishedAt)
	})
	if !options.Coexist && !ani.OVA {
		result = dedupeEpisodes(result)
	}
	if options.DownloadNew && len(result) > 1 {
		latest := result[len(result)-1]
		sameDay := func(item model.Resource) bool {
			return latest.PublishedAt == nil || item.PublishedAt == nil || latest.PublishedAt.Format("2006-01-02") == item.PublishedAt.Format("2006-01-02")
		}
		filtered := []model.Resource{}
		for _, item := range result {
			if sameDay(item) {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) > 0 {
			result = filtered[len(filtered)-1:]
		}
	}
	return result
}

func customEpisode(title, expression string, group int) (float64, bool) {
	if strings.TrimSpace(expression) == "" || group < 1 {
		return 0, false
	}
	// Java's configured patterns commonly use non-capturing groups. Go's RE2
	// syntax does not support (?:...), but replacing them with ordinary groups
	// preserves the configured capture indexes used by the default rule.
	pattern, group, err := regexutil.CompileCapturePattern(expression, group)
	if err != nil {
		return 0, false
	}
	matches := pattern.FindStringSubmatch(title)
	if group >= len(matches) {
		return 0, false
	}
	number := regexp.MustCompile(`[0-9]+([.]5)?`).FindString(matches[group])
	if number == "" {
		return 0, false
	}
	episode, err := strconv.ParseFloat(number, 64)
	return episode, err == nil
}

func dedupeEpisodes(items []model.Resource) []model.Resource {
	result := make([]model.Resource, 0, len(items))
	seen := map[float64]bool{}
	for _, item := range items {
		if seen[item.Episode] {
			continue
		}
		seen[item.Episode] = true
		result = append(result, item)
	}
	return result
}

func matchesAll(patterns []string, title string) bool {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		if ok, err := regexp.MatchString(pattern, title); err != nil || !ok {
			return false
		}
	}
	return true
}
func matchesAny(patterns []string, title string) bool {
	for _, pattern := range patterns {
		if pattern != "" {
			if ok, err := regexp.MatchString(pattern, title); err == nil && ok {
				return true
			}
		}
	}
	return false
}
func containsEpisode(values []float64, target float64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
