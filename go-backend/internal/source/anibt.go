package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// AniBTAdapter owns AniBT's JSON envelope, query parameters and group RSS
// conventions. It deliberately exposes domain-shaped results to the facade.
type AniBTAdapter struct{ runtime *runtime }

func newAniBTAdapter(rt *runtime) *AniBTAdapter { return &AniBTAdapter{runtime: rt} }

func (a *AniBTAdapter) ResolveRSSSubscription(rssURL, bgmURL, subgroup string) (model.Ani, error) {
	parsed, err := url.Parse(strings.TrimSpace(rssURL))
	if err != nil {
		return model.Ani{}, fmt.Errorf("RSS地址格式异常: %w", err)
	}
	resolved := model.Ani{BGMURL: bgmURL, Subgroup: subgroup}
	if values, exists := parsed.Query()["bgmId"]; exists && len(values) > 0 {
		resolved.BGMURL = "https://bgm.tv/subject/" + values[0]
	} else {
		resolved.BGMURL = ""
	}
	if strings.TrimSpace(resolved.Subgroup) == "" {
		if values, exists := parsed.Query()["groupSlug"]; exists && len(values) > 0 {
			resolved.Subgroup = values[0]
		}
	}
	return resolved, nil
}

func (a *AniBTAdapter) List(query map[string]any) (map[string]any, error) {
	c := a.runtime
	if c.aniBTHost == "" {
		return nil, errors.New("AniBT host is not configured")
	}
	values := url.Values{}
	title := stringValue(query["title"])
	season := stringValue(query["season"])
	bgmID := subjectID(stringValue(query["bgmUrl"]))
	if title != "" {
		// The Java client treats a title query as a global search and clears the
		// season/subject filters.
		season, bgmID = "", ""
	}
	values.Set("season", season)
	values.Set("bgmId", bgmID)
	values.Set("query", title)
	target := c.aniBTHost + "/api/seasons/anime?" + values.Encode()
	value, err := c.cachedJSON("anibt:catalog:"+target, 10*time.Minute, 24*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
		if getErr != nil {
			return nil, getErr
		}
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if decodeErr := json.Unmarshal(body, &envelope); decodeErr != nil {
			return nil, fmt.Errorf("decode AniBT response: %w", decodeErr)
		}
		if envelope.Data == nil {
			return nil, errors.New("AniBT response has no data")
		}
		if weekdays, ok := envelope.Data["byWeekday"].([]any); ok {
			filteredWeeks := make([]any, 0, len(weekdays))
			for _, raw := range weekdays {
				weekday, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				animes, _ := weekday["animes"].([]any)
				filtered := make([]any, 0, len(animes))
				for _, animeRaw := range animes {
					anime, ok := animeRaw.(map[string]any)
					if !ok {
						continue
					}
					if title == "" && number(anime["rssReleaseCount"]) <= 0 {
						continue
					}
					filtered = append(filtered, anime)
				}
				sort.SliceStable(filtered, func(i, j int) bool {
					left, _ := filtered[i].(map[string]any)
					right, _ := filtered[j].(map[string]any)
					return numberFloat(left["rating"]) > numberFloat(right["rating"])
				})
				weekday["animes"] = filtered
				if len(filtered) > 0 {
					filteredWeeks = append(filteredWeeks, weekday)
				}
			}
			envelope.Data["byWeekday"] = filteredWeeks
			sortSourceWeekdays(filteredWeeks, "weekdayLabel")
		}
		return envelope.Data, nil
	})
	result := mapValue(value)
	if result != nil {
		a.overlayExists(result)
	}
	return result, err
}

func (a *AniBTAdapter) Group(bgmID string) ([]map[string]any, error) {
	c := a.runtime
	target := c.aniBTHost + "/api/anime/groups?bgmId=" + url.QueryEscape(bgmID)
	value, err := c.cachedJSON("anibt:detail:"+target, 15*time.Minute, 6*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
		if getErr != nil {
			return nil, getErr
		}
		var envelope struct {
			Data struct {
				Groups []map[string]any `json:"groups"`
			} `json:"data"`
		}
		if decodeErr := json.Unmarshal(body, &envelope); decodeErr != nil {
			return nil, fmt.Errorf("decode AniBT groups: %w", decodeErr)
		}
		for _, group := range envelope.Data.Groups {
			slug := stringValue(group["slug"])
			group["bgmId"] = bgmID
			group["rss"] = fmt.Sprintf("%s/rss/anime.xml?bgmId=%s&groupSlug=%s", c.aniBTHost, url.QueryEscape(bgmID), url.QueryEscape(slug))
			if _, exists := group["items"]; !exists {
				group["items"] = []any{}
			}
			if items, ok := group["items"].([]any); ok {
				for _, raw := range items {
					if item, ok := raw.(map[string]any); ok {
						item["formatSize"] = formatSize(number64(item["size"]))
					}
				}
				group["groupRegex"] = buildGroupRegex(groupTitles(items))
			} else {
				group["groupRegex"] = map[string]any{"regexList": []any{}, "tags": []string{}}
			}
		}
		groups := make([]any, 0, len(envelope.Data.Groups))
		for _, group := range envelope.Data.Groups {
			groups = append(groups, group)
		}
		return groups, nil
	})
	return mapListValue(value), err
}

func (a *AniBTAdapter) overlayExists(result map[string]any) {
	c := a.runtime
	weeks, _ := result["byWeekday"].([]any)
	for _, rawWeek := range weeks {
		week, _ := rawWeek.(map[string]any)
		animes, _ := week["animes"].([]any)
		for _, rawAnime := range animes {
			anime, _ := rawAnime.(map[string]any)
			anime["exists"] = c.hasBGMSubject(stringValue(anime["bgmId"]))
		}
	}
}

// sortSourceWeekdays mirrors the Java WeekComparator: the current weekday is
// shown first, followed by earlier weekdays and then the remaining days in
// reverse order.
func sortSourceWeekdays(values []any, field string) {
	order := sourceWeekOrder(time.Now().Weekday())
	indices := make(map[string]int, len(order))
	for index, label := range order {
		indices[label] = index
	}
	sort.SliceStable(values, func(i, j int) bool {
		left, _ := values[i].(map[string]any)
		right, _ := values[j].(map[string]any)
		leftIndex, leftOK := indices[sourceWeekLabel(stringValue(left[field]))]
		rightIndex, rightOK := indices[sourceWeekLabel(stringValue(right[field]))]
		if !leftOK {
			leftIndex = len(order)
		}
		if !rightOK {
			rightIndex = len(order)
		}
		return leftIndex < rightIndex
	})
}

func sourceWeekOrder(today time.Weekday) []string {
	labels := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	result := make([]string, 0, len(labels))
	for day := int(today); day >= 0; day-- {
		result = append(result, labels[day])
	}
	for day := 6; day > int(today); day-- {
		result = append(result, labels[day])
	}
	return result
}

func sourceWeekLabel(value string) string {
	for _, label := range []string{"日", "一", "二", "三", "四", "五", "六"} {
		if strings.Contains(value, "星期"+label) || strings.Contains(value, "周"+label) {
			return "星期" + label
		}
	}
	return value
}

func sourceWeekdayIndex(value string) int {
	canonical := sourceWeekLabel(value)
	for index, label := range sourceWeekOrder(time.Now().Weekday()) {
		if label == canonical {
			return index
		}
	}
	return 7
}
