package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// BangumiAdapter owns Bangumi search, subject normalization and subscription
// construction. Metadata clients are used when the application provides one;
// the direct API path remains available to standalone source clients.
type BangumiAdapter struct {
	runtime *runtime
	mikan   mikanSubscriptionResolver
}

type mikanSubscriptionResolver interface {
	ResolveSubscription(string) (model.Ani, error)
}

func newBangumiAdapter(rt *runtime, mikan mikanSubscriptionResolver) *BangumiAdapter {
	return &BangumiAdapter{runtime: rt, mikan: mikan}
}

func (a *BangumiAdapter) Search(name string) ([]map[string]any, error) {
	return a.searchBangumi(name)
}

func (a *BangumiAdapter) Subject(id string) (map[string]any, error) {
	return a.bangumiSubject(id)
}

func (a *BangumiAdapter) EpisodeCount(id string) (int, error) {
	return a.subjectEpisodeCountPublic(id)
}

func (a *BangumiAdapter) Subscription(id string) (model.Ani, error) {
	return a.subscriptionFromSubject(id)
}

func (a *BangumiAdapter) Title(item model.Ani) (string, error) {
	return a.bgmTitle(item)
}

func (a *BangumiAdapter) searchBangumi(name string) ([]map[string]any, error) {
	c := a.runtime
	if strings.TrimSpace(name) == "" {
		return []map[string]any{}, nil
	}
	if c.metadataClient != nil {
		result, err := c.metadataClient.SearchBangumi(c.context, name)
		if err != nil {
			return []map[string]any{}, nil
		}
		return result, nil
	}
	target := c.bangumiAPI + "/search/subject/" + url.PathEscape(strings.ReplaceAll(name, "1/2", "½")) + "?type=2&max_results=25&responseGroup=small"
	value, err := c.cachedJSON("bangumi:search:"+target, time.Hour, 24*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
		if getErr != nil {
			return nil, getErr
		}
		var response struct {
			List []map[string]any `json:"list"`
		}
		if decodeErr := json.Unmarshal(body, &response); decodeErr != nil {
			return nil, decodeErr
		}
		if response.List == nil {
			return []map[string]any{}, nil
		}
		return response.List, nil
	})
	if err != nil {
		// BgmUtil.search deliberately treats an unavailable search result as an
		// empty list. The UI uses this endpoint as an optional lookup.
		return []map[string]any{}, nil
	}
	return mapListValue(value), nil
}

func (a *BangumiAdapter) bangumiSubject(id string) (map[string]any, error) {
	c := a.runtime
	if c.metadataClient != nil {
		return c.metadataClient.Subject(c.context, id)
	}
	target := c.bangumiAPI + "/v0/subjects/" + url.PathEscape(id)
	value, err := c.cachedJSON("bangumi:subject:"+target, 6*time.Hour, 7*24*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
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
		return nil, err
	}
	return mapValue(value), nil
}

func (a *BangumiAdapter) subjectEpisodeCountPublic(id string) (int, error) {
	info, err := a.bangumiSubject(id)
	if err != nil {
		return 0, err
	}
	episodes := int(number(info["eps"]))
	if episodes < 1 {
		return 0, errors.New("Bangumi subject 缺少 eps")
	}
	if actual, episodeErr := a.subjectEpisodeCount(id); episodeErr == nil && actual > 0 {
		episodes = actual
	}
	return episodes, nil
}

func (a *BangumiAdapter) subscriptionFromSubject(id string) (model.Ani, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.Ani{}, errors.New("Bangumi subject 不能为空")
	}
	info, err := a.bangumiSubject(id)
	if err != nil {
		return model.Ani{}, err
	}
	name := a.bangumiTitle(info)
	images, _ := info["images"].(map[string]any)
	season := inferBangumiSeason(info)
	if season < 1 {
		season = int(number(info["season"]))
	}
	if season < 1 {
		season = 1
	}
	platform := strings.ToUpper(stringValue(info["platform"]))
	date := stringValue(info["date"])
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	rating, _ := info["rating"].(map[string]any)
	return model.Ani{
		ID:                 newID(),
		Title:              name,
		JPTitle:            stringValue(info["name"]),
		BGMURL:             "https://bgm.tv/subject/" + id,
		Image:              a.bangumiImage(images),
		ReleaseDate:        date,
		Season:             season,
		Offset:             0,
		TotalEpisodeNumber: episodeCountOrDefault(a, id, int(number(info["eps"]))),
		Score:              numberFloat(rating["score"]),
		OVA:                platform == "OVA" || platform == "剧场版",
		Enable:             true,
	}, nil
}

func (a *BangumiAdapter) bgmTitle(item model.Ani) (string, error) {
	c := a.runtime
	id := subjectID(item.BGMURL)
	if id == "" && strings.EqualFold(strings.TrimSpace(item.Type), "mikan") {
		resolved, err := a.mikan.ResolveSubscription(item.URL)
		if err != nil {
			return "", err
		}
		id = subjectID(resolved.BGMURL)
	}
	if id == "" {
		return "", errors.New("bgmUrl 不能为空")
	}
	info, err := a.bangumiSubject(id)
	if err != nil {
		return "", err
	}
	name := a.bangumiTitle(info)
	if name == "" {
		name = "无标题"
	}
	if boolValue(c.config["titleYear"]) {
		year := yearFromTMDB(item.TMDB)
		if year == 0 {
			year = yearFromDate(stringValue(info["date"]))
		}
		if year > 0 {
			name = stripYearSuffix(name) + fmt.Sprintf(" (%d)", year)
		}
	}
	if tmdbID := stringValue(item.TMDB["id"]); tmdbID != "" && boolValue(c.config["tmdbId"]) {
		if boolValue(c.config["tmdbIdPlexMode"]) {
			name += " {tmdb-" + tmdbID + "}"
		} else {
			name += " [tmdbid=" + tmdbID + "]"
		}
	}
	return name, nil
}

func (a *BangumiAdapter) subjectEpisodeCount(id string) (int, error) {
	c := a.runtime
	if c.metadataClient != nil {
		return c.metadataClient.EpisodeCount(c.context, id)
	}
	target := c.bangumiAPI + "/v0/episodes?subject_id=" + url.QueryEscape(id) + "&type=0&limit=1000&offset=0"
	value, err := c.cachedJSON("bangumi:episodes:"+target, time.Hour, 24*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
		if getErr != nil {
			return nil, getErr
		}
		var response struct {
			Data []map[string]any `json:"data"`
		}
		if decodeErr := json.Unmarshal(body, &response); decodeErr != nil {
			return nil, decodeErr
		}
		return len(response.Data), nil
	})
	if err != nil {
		return 0, err
	}
	return int(number(value)), nil
}

func episodeCountOrDefault(a *BangumiAdapter, id string, fallback int) int {
	if fallback < 1 {
		return fallback
	}
	if actual, err := a.subjectEpisodeCount(id); err == nil && actual > 0 {
		return actual
	}
	return fallback
}

func (a *BangumiAdapter) bangumiTitle(info map[string]any) string {
	if boolValue(a.runtime.config["bgmJpName"]) {
		return stringValue(info["name"])
	}
	return firstNonEmpty(stringValue(info["nameCn"]), stringValue(info["name_cn"]), stringValue(info["name"]))
}

func (a *BangumiAdapter) bangumiImage(images map[string]any) string {
	requested := strings.ToLower(strings.TrimSpace(stringValue(a.runtime.config["bgmImageSize"])))
	if requested == "" {
		requested = "medium"
	}
	for _, size := range append([]string{requested}, "medium", "large", "small", "common", "grid") {
		if image := stringValue(images[size]); image != "" {
			return image
		}
	}
	return ""
}

func inferBangumiSeason(info map[string]any) int {
	if tags, ok := info["tags"].([]any); ok {
		for _, raw := range tags {
			if tag, ok := raw.(map[string]any); ok {
				if season := seasonFromName(stringValue(tag["name"])); season > 1 {
					return season
				}
			}
		}
	}
	for _, key := range []string{"nameCn", "name_cn", "name"} {
		if season := seasonFromName(stringValue(info[key])); season > 1 {
			return season
		}
	}
	if infobox, ok := info["infobox"].([]any); ok {
		for _, raw := range infobox {
			entry, ok := raw.(map[string]any)
			if !ok || stringValue(entry["key"]) != "别名" {
				continue
			}
			if values, ok := entry["value"].([]any); ok {
				for _, value := range values {
					if alias, ok := value.(map[string]any); ok {
						if season := seasonFromName(stringValue(alias["v"])); season > 1 {
							return season
						}
					}
				}
			}
		}
	}
	return 0
}

var bangumiYearSuffix = regexp.MustCompile(`\s*\((?:19|20)\d{2}\)\s*$`)

var seasonPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)第\s*([一二三四五六七八九十百千万]+)\s*[季期]`),
	regexp.MustCompile(`(?i)[Ss]eason\s*(\d+)`),
	regexp.MustCompile(`(?i)(\d+)(?:st|nd|rd|th)\s*[Ss]eason`),
	regexp.MustCompile(`(?i)[Ss](\d+)$`),
}

func seasonFromName(name string) int {
	for _, pattern := range seasonPatterns {
		match := pattern.FindStringSubmatch(name)
		if len(match) < 2 {
			continue
		}
		if value, err := strconv.Atoi(match[1]); err == nil && value > 1 {
			return value
		}
		if value := chineseNumber(match[1]); value > 1 {
			return value
		}
	}
	return 0
}

func chineseNumber(value string) int {
	digits := map[rune]int{'一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9, '零': 0}
	if len([]rune(value)) == 1 {
		return digits[[]rune(value)[0]]
	}
	result, section, number := 0, 0, 0
	for _, valueRune := range value {
		if digit, ok := digits[valueRune]; ok {
			number = digit
			continue
		}
		switch valueRune {
		case '十':
			if number == 0 {
				number = 1
			}
			section += number * 10
			number = 0
		case '百':
			if number == 0 {
				number = 1
			}
			section += number * 100
			number = 0
		case '千':
			if number == 0 {
				number = 1
			}
			section += number * 1000
			number = 0
		}
	}
	return result + section + number
}

func boolValue(value any) bool {
	result, ok := value.(bool)
	return ok && result
}

func yearFromTMDB(value map[string]any) int {
	for _, key := range []string{"date", "first_air_date", "release_date"} {
		if year := yearFromDate(stringValue(value[key])); year > 0 {
			return year
		}
	}
	return 0
}

func yearFromDate(value string) int {
	if len(value) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(value[:4])
	return year
}

func stripYearSuffix(value string) string {
	return bangumiYearSuffix.ReplaceAllString(strings.TrimSpace(value), "")
}

func newID() string { return fmt.Sprintf("ani-%d", time.Now().UnixNano()) }
