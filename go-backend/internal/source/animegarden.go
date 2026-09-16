package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// AnimeGardenAdapter owns AnimeGarden's subject/resource envelopes and its
// fansub RSS URL convention.
type AnimeGardenAdapter struct {
	runtime *runtime
	bgm     subjectProvider
}

type subjectProvider interface {
	Subject(string) (map[string]any, error)
}

func newAnimeGardenAdapter(rt *runtime, bgm subjectProvider) *AnimeGardenAdapter {
	return &AnimeGardenAdapter{runtime: rt, bgm: bgm}
}

func (a *AnimeGardenAdapter) ResolveRSSSubscription(rssURL, bgmURL, subgroup string) (model.Ani, error) {
	parsed, err := url.Parse(strings.TrimSpace(rssURL))
	if err != nil {
		return model.Ani{}, fmt.Errorf("RSS地址格式异常: %w", err)
	}
	resolved := model.Ani{BGMURL: bgmURL, Subgroup: subgroup}
	if values, exists := parsed.Query()["subject"]; exists && len(values) > 0 {
		resolved.BGMURL = "https://bgm.tv/subject/" + values[0]
	} else {
		resolved.BGMURL = ""
	}
	if values, exists := parsed.Query()["fansub"]; exists && len(values) > 0 {
		resolved.Subgroup = values[0]
	}
	return resolved, nil
}

func (a *AnimeGardenAdapter) List(bgmURL string) ([]map[string]any, error) {
	c := a.runtime
	if bgmURL != "" {
		id := subjectID(bgmURL)
		subject := map[string]any{"id": id, "exists": true}
		if info, err := a.bgm.Subject(id); err == nil {
			subject["name"] = firstNonEmpty(stringValue(info["nameCn"]), stringValue(info["name_cn"]), stringValue(info["name"]))
			if images, ok := info["images"].(map[string]any); ok {
				subject["cover"] = firstNonEmpty(stringValue(images["small"]), stringValue(images["large"]))
			}
		}
		return []map[string]any{{"weekLabel": "搜索", "subjects": []any{subject}}}, nil
	}
	target := c.gardenHost + "/subjects"
	value, err := c.cachedJSON("animegarden:catalog:"+target, 24*time.Hour, 7*24*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
		if getErr != nil {
			return nil, getErr
		}
		var envelope struct {
			Subjects []map[string]any `json:"subjects"`
		}
		if decodeErr := json.Unmarshal(body, &envelope); decodeErr != nil {
			return nil, fmt.Errorf("decode AnimeGarden subjects: %w", decodeErr)
		}
		return envelope.Subjects, nil
	})
	if err != nil {
		return nil, err
	}
	subjects := mapListValue(value)
	// AnimeGarden intentionally does not include a cover in /subjects. The
	// Java service overlays this response with the public Bangumi cover cache;
	// keep the overlay best-effort so a cache outage does not blank the page.
	var bgmCovers map[string]any
	if len(subjects) > 0 {
		bgmCovers = a.bangumiCoverCache()
	}
	weeks := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	grouped := map[string][]any{}
	for _, subject := range subjects {
		id := stringValue(subject["id"])
		if cover := bangumiCoverURL(bgmCovers[id]); cover != "" {
			subject["cover"] = cover
		}
		subject["exists"] = c.hasBGMSubject(id)
		week := weekLabel(subject["activedAt"], weeks)
		subject["weekLabel"] = week
		grouped[week] = append(grouped[week], subject)
	}
	result := make([]map[string]any, 0)
	for _, week := range weeks {
		if subjects := grouped[week]; len(subjects) > 0 {
			result = append(result, map[string]any{"weekLabel": week, "subjects": subjects})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return sourceWeekdayIndex(stringValue(result[i]["weekLabel"])) < sourceWeekdayIndex(stringValue(result[j]["weekLabel"]))
	})
	return result, nil
}

func (a *AnimeGardenAdapter) bangumiCoverCache() map[string]any {
	c := a.runtime
	if c.bgmCoverURL == "" {
		return nil
	}
	value, err := c.cachedJSON("bangumi:covers:"+c.bgmCoverURL, 6*time.Hour, 7*24*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, c.bgmCoverURL)
		if getErr != nil {
			return nil, getErr
		}
		var result map[string]any
		if decodeErr := json.Unmarshal(body, &result); decodeErr != nil {
			return nil, decodeErr
		}
		return result, nil
	})
	if err != nil {
		return nil
	}
	return mapValue(value)
}

func weekLabel(value any, labels []string) string {
	raw := stringValue(value)
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return ""
	}
	return labels[int(parsed.In(time.Local).Weekday())]
}

func bangumiCoverURL(value any) string {
	images, ok := value.(map[string]any)
	if !ok {
		return stringValue(value)
	}
	return firstNonEmpty(
		stringValue(images["small"]),
		stringValue(images["medium"]),
		stringValue(images["large"]),
		stringValue(images["common"]),
		stringValue(images["grid"]),
	)
}

func (a *AnimeGardenAdapter) Group(bgmID string) ([]map[string]any, error) {
	c := a.runtime
	values := url.Values{"subject": []string{bgmID}, "pageSize": []string{"200"}, "duplicate": []string{"false"}}
	target := c.gardenHost + "/resources?" + values.Encode()
	value, err := c.cachedJSON("animegarden:resources:"+target, time.Hour, 6*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
		if getErr != nil {
			return nil, getErr
		}
		var envelope struct {
			Resources []map[string]any `json:"resources"`
		}
		if decodeErr := json.Unmarshal(body, &envelope); decodeErr != nil {
			return nil, fmt.Errorf("decode AnimeGarden resources: %w", decodeErr)
		}
		groups := map[string]map[string]any{}
		for _, item := range envelope.Resources {
			fansub, _ := item["fansub"].(map[string]any)
			if fansub == nil {
				continue
			}
			id, name := stringValue(fansub["id"]), stringValue(fansub["name"])
			group := groups[id]
			if group == nil {
				// Match the Java endpoint's URL contract. It escapes the ampersand
				// separator inside a fansub name, but leaves spaces as-is.
				group = map[string]any{"id": id, "name": name, "bgmId": bgmID, "rss": fmt.Sprintf("%s/feed.xml?subject=%s&fansub=%s", c.gardenHost, bgmID, strings.ReplaceAll(name, "&", "%26")), "items": []any{}}
				// The Java endpoint exposes createdAt. fetchedAt is only the time
				// the API ingested a resource and must not affect group ordering.
				group["lastUpdatedAt"] = itemTime(item, "createdAt")
				groups[id] = group
			} else if newer(itemTime(item, "createdAt"), group["lastUpdatedAt"]) {
				group["lastUpdatedAt"] = itemTime(item, "createdAt")
			}
			items := group["items"].([]any)
			item["formatSize"] = formatSize(number64(item["size"]))
			group["items"] = append(items, item)
		}
		result := make([]map[string]any, 0, len(groups))
		for _, group := range groups {
			group["groupRegex"] = buildGroupRegex(groupTitles(group["items"]))
			result = append(result, group)
		}
		sort.SliceStable(result, func(i, j int) bool {
			return newer(result[i]["lastUpdatedAt"], result[j]["lastUpdatedAt"])
		})
		groupsValue := make([]any, 0, len(result))
		for _, group := range result {
			groupsValue = append(groupsValue, group)
		}
		return groupsValue, nil
	})
	return mapListValue(value), err
}
