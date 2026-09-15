// Package source contains replaceable clients for the resource discovery
// services used by the existing UI. The clients accept base URLs so tests and
// migration environments never need live third-party services.
package source

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

type Options struct {
	MikanHost       string
	AniBTHost       string
	AnimeGardenHost string
	BangumiAPI      string
	// BGMCoverURL is the optional cache endpoint used by the legacy
	// AnimeGarden page to fill Bangumi covers. It is injected by the
	// application so source-client tests do not need a live cache service.
	BGMCoverURL   string
	Config        model.Config
	HTTPClient    *http.Client
	Subscriptions func() []model.Ani
	Retries       int
}

type Client struct {
	mikanHost, aniBTHost, gardenHost, bangumiAPI, bgmCoverURL string
	config                                                    model.Config
	httpClient                                                *http.Client
	subscriptions                                             func() []model.Ani
	retries                                                   int
}

func New(options Options) *Client {
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	retries := options.Retries
	if retries < 1 {
		retries = 3
	}
	return &Client{
		mikanHost:     strings.TrimRight(options.MikanHost, "/"),
		aniBTHost:     strings.TrimRight(options.AniBTHost, "/"),
		gardenHost:    strings.TrimRight(options.AnimeGardenHost, "/"),
		bangumiAPI:    strings.TrimRight(options.BangumiAPI, "/"),
		bgmCoverURL:   strings.TrimRight(options.BGMCoverURL, "/"),
		config:        options.Config,
		httpClient:    httpClient,
		subscriptions: options.Subscriptions,
		retries:       retries,
	}
}

func (c *Client) Mikan(text string, season map[string]any) (map[string]any, error) {
	if c.mikanHost == "" {
		return nil, errors.New("Mikan host is not configured")
	}
	trimmedText := strings.TrimSpace(text)
	if match := regexp.MustCompile(`^id: ([0-9]+)$`).FindStringSubmatch(trimmedText); len(match) == 2 {
		id := match[1]
		target := c.mikanHost + "/Home/Bangumi/" + url.PathEscape(id)
		body, err := c.get(target)
		if err != nil {
			return nil, err
		}
		return c.mikanDetail(target, body)
	}
	// The legacy UI uses the Mikan home page for an unfiltered request. That
	// page contains the current season and the seasonal catalogue; /Home/Search
	// without a search term is intentionally an empty search result page.
	target := c.mikanHost
	if trimmedText != "" {
		target += "/Home/Search"
	}
	query := url.Values{}
	if trimmedText != "" {
		query.Set("searchstr", text)
	} else {
		if year := number(season["year"]); year != 0 {
			query.Set("year", strconv.Itoa(int(year)))
		}
		if value := stringValue(season["season"]); value != "" {
			query.Set("seasonStr", value)
		}
		if len(query) > 0 {
			target = c.mikanHost + "/Home/BangumiCoverFlowByDayOfWeek"
		}
	}
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	body, err := c.get(target)
	if err != nil {
		return nil, err
	}
	return c.parseMikanList(target, body), nil
}

func (c *Client) MikanGroup(target string) ([]map[string]any, error) {
	if target == "" {
		return nil, errors.New("Mikan URL is empty")
	}
	body, err := c.get(target)
	if err != nil {
		return nil, err
	}
	result, err := c.mikanDetail(target, body)
	if err != nil {
		return nil, err
	}
	weeks, _ := result["weeks"].([]any)
	if len(weeks) == 0 {
		return []map[string]any{}, nil
	}
	info, _ := weeks[0].(map[string]any)
	items, _ := info["items"].([]any)
	if len(items) == 0 {
		return []map[string]any{}, nil
	}
	item, _ := items[0].(map[string]any)
	groups, _ := item["groups"].([]any)
	bgmURL := stringValue(item["bgmUrl"])
	resultGroups := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		if value, ok := group.(map[string]any); ok {
			value["bgmUrl"] = bgmURL
			resultGroups = append(resultGroups, value)
		}
	}
	return resultGroups, nil
}

func (c *Client) AniBT(query map[string]any) (map[string]any, error) {
	if c.aniBTHost == "" {
		return nil, errors.New("AniBT host is not configured")
	}
	values := url.Values{}
	title := stringValue(query["title"])
	season := stringValue(query["season"])
	bgmID := subjectID(stringValue(query["bgmUrl"]))
	if title != "" {
		// The Java client treats a title query as a global search and clears
		// the season/subject filters.
		season, bgmID = "", ""
	}
	values.Set("season", season)
	values.Set("bgmId", bgmID)
	values.Set("query", title)
	body, err := c.get(c.aniBTHost + "/api/seasons/anime?" + values.Encode())
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode AniBT response: %w", err)
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
				anime["exists"] = c.hasBGMSubject(stringValue(anime["bgmId"]))
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
}

func (c *Client) AniBTGroup(bgmID string) ([]map[string]any, error) {
	body, err := c.get(c.aniBTHost + "/api/anime/groups?bgmId=" + url.QueryEscape(bgmID))
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data struct {
			Groups []map[string]any `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode AniBT groups: %w", err)
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
	return envelope.Data.Groups, nil
}

func (c *Client) AnimeGardenList(bgmURL string) ([]map[string]any, error) {
	if bgmURL != "" {
		id := subjectID(bgmURL)
		subject := map[string]any{"id": id, "exists": true}
		if info, err := c.BangumiSubject(id); err == nil {
			subject["name"] = firstNonEmpty(stringValue(info["nameCn"]), stringValue(info["name_cn"]), stringValue(info["name"]))
			if images, ok := info["images"].(map[string]any); ok {
				subject["cover"] = firstNonEmpty(stringValue(images["small"]), stringValue(images["large"]))
			}
		}
		return []map[string]any{{"weekLabel": "搜索", "subjects": []any{subject}}}, nil
	}
	body, err := c.get(c.gardenHost + "/subjects")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Subjects []map[string]any `json:"subjects"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode AnimeGarden subjects: %w", err)
	}
	// AnimeGarden intentionally does not include a cover in /subjects. The
	// Java service overlays this response with the public Bangumi cover cache;
	// keep the overlay best-effort so a cache outage does not blank the page.
	bgmCovers := c.bangumiCoverCache()
	weeks := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	grouped := map[string][]any{}
	for _, subject := range envelope.Subjects {
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

func (c *Client) bangumiCoverCache() map[string]any {
	if c.bgmCoverURL == "" {
		return nil
	}
	body, err := c.get(c.bgmCoverURL)
	if err != nil {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil
	}
	return result
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

func (c *Client) AnimeGardenGroup(bgmID string) ([]map[string]any, error) {
	values := url.Values{"subject": []string{bgmID}, "pageSize": []string{"200"}, "duplicate": []string{"false"}}
	body, err := c.get(c.gardenHost + "/resources?" + values.Encode())
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Resources []map[string]any `json:"resources"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode AnimeGarden resources: %w", err)
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
	return result, nil
}

// sortSourceWeekdays mirrors the Java WeekComparator: the current weekday is
// shown first, followed by earlier weekdays and then the remaining days in
// reverse order. This is intentionally applied only to source APIs whose Java
// services sort the returned weekday buckets; Mikan preserves its page order.
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

func (c *Client) SearchBangumi(name string) ([]map[string]any, error) {
	if strings.TrimSpace(name) == "" {
		return []map[string]any{}, nil
	}
	target := c.bangumiAPI + "/search/subject/" + url.PathEscape(strings.ReplaceAll(name, "1/2", "½")) + "?type=2&max_results=25&responseGroup=small"
	body, err := c.get(target)
	if err != nil {
		// BgmUtil.search deliberately treats an unavailable search result as an
		// empty list. The UI uses this endpoint as an optional lookup and should
		// not turn a 404 or transient upstream response into a page-level error.
		return []map[string]any{}, nil
	}
	var response struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return []map[string]any{}, nil
	}
	if response.List == nil {
		return []map[string]any{}, nil
	}
	return response.List, nil
}

func (c *Client) BangumiSubject(id string) (map[string]any, error) {
	body, err := c.get(c.bangumiAPI + "/v0/subjects/" + url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// SubjectEpisodeCount returns Bangumi's eps field for manual progress updates.
func (c *Client) SubjectEpisodeCount(id string) (int, error) {
	info, err := c.BangumiSubject(id)
	if err != nil {
		return 0, err
	}
	episodes := int(number(info["eps"]))
	if episodes < 1 {
		return 0, errors.New("Bangumi subject 缺少 eps")
	}
	if actual, episodeErr := c.subjectEpisodeCount(id); episodeErr == nil && actual > 0 {
		episodes = actual
	}
	return episodes, nil
}

func (c *Client) SubscriptionFromSubject(id string) (model.Ani, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.Ani{}, errors.New("Bangumi subject 不能为空")
	}
	info, err := c.BangumiSubject(id)
	if err != nil {
		return model.Ani{}, err
	}
	name := c.bangumiTitle(info)
	images, _ := info["images"].(map[string]any)
	season := inferBangumiSeason(info)
	if season < 1 {
		season = int(number(info["season"]))
	}
	if season < 1 {
		// Bangumi does not provide a season number for every subject. The
		// legacy createAni() default is the first season.
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
		Image:              c.bangumiImage(images),
		ReleaseDate:        date,
		Season:             season,
		Offset:             0,
		TotalEpisodeNumber: episodeCountOrDefault(c, id, int(number(info["eps"]))),
		Score:              numberFloat(rating["score"]),
		OVA:                platform == "OVA" || platform == "剧场版",
		Enable:             true,
	}, nil
}

// ResolveMikanSubscription follows Mikan's bangumi id to the Bangumi subject
// linked on the detail page. Mikan bangumi ids and Bangumi subject ids are
// different namespaces; using the RSS bangumiId as a Bangumi id produces a
// valid but unrelated subscription.
func (c *Client) ResolveMikanSubscription(rssURL string) (model.Ani, error) {
	parsed, err := url.Parse(strings.TrimSpace(rssURL))
	if err != nil {
		return model.Ani{}, fmt.Errorf("解析 Mikan RSS: %w", err)
	}
	mikanID := parsed.Query().Get("bangumiId")
	if mikanID == "" {
		return model.Ani{}, errors.New("Mikan RSS 缺少 bangumiId")
	}
	target := c.mikanHost + "/Home/Bangumi/" + url.PathEscape(mikanID)
	body, err := c.get(target)
	if err != nil {
		return model.Ani{}, err
	}
	detail, err := c.mikanDetail(target, body)
	if err != nil {
		return model.Ani{}, err
	}
	var mikanInfo map[string]any
	if weeks, ok := detail["weeks"].([]any); ok && len(weeks) > 0 {
		if week, ok := weeks[0].(map[string]any); ok {
			if items, ok := week["items"].([]any); ok && len(items) > 0 {
				mikanInfo, _ = items[0].(map[string]any)
			}
		}
	}
	bgmURL := stringValue(mikanInfo["bgmUrl"])
	if bgmURL == "" {
		return model.Ani{}, errors.New("Mikan 番剧缺少 Bangumi 链接")
	}
	resolved := model.Ani{BGMURL: bgmURL, MikanTitle: stringValue(mikanInfo["title"])}
	requestedGroup := parsed.Query().Get("subgroupid")
	if groups, ok := mikanInfo["groups"].([]any); ok {
		for _, raw := range groups {
			group, ok := raw.(map[string]any)
			if ok && stringValue(group["subgroupId"]) == requestedGroup {
				resolved.Subgroup = stringValue(group["label"])
				break
			}
		}
	}
	return resolved, nil
}

func (c *Client) BGMTitle(item model.Ani) (string, error) {
	id := subjectID(item.BGMURL)
	if id == "" && strings.EqualFold(strings.TrimSpace(item.Type), "mikan") {
		// Mikan RSS URLs carry a Mikan bangumiId, not a Bangumi subject id.
		// The Java implementation follows that id through the detail page when
		// the subscription's bgmUrl has not been filled in yet.
		resolved, err := c.ResolveMikanSubscription(item.URL)
		if err != nil {
			return "", err
		}
		id = subjectID(resolved.BGMURL)
	}
	if id == "" {
		return "", errors.New("bgmUrl 不能为空")
	}
	info, err := c.BangumiSubject(id)
	if err != nil {
		return "", err
	}
	name := c.bangumiTitle(info)
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
	if tmdbID := stringValue(item.TMDB["id"]); tmdbID != "" {
		if boolValue(c.config["tmdbId"]) {
			if boolValue(c.config["tmdbIdPlexMode"]) {
				name += " {tmdb-" + tmdbID + "}"
			} else {
				name += " [tmdbid=" + tmdbID + "]"
			}
		}
	}
	return name, nil
}

func (c *Client) subjectEpisodeCount(id string) (int, error) {
	target := c.bangumiAPI + "/v0/episodes?subject_id=" + url.QueryEscape(id) + "&type=0&limit=1000&offset=0"
	body, err := c.get(target)
	if err != nil {
		return 0, err
	}
	var response struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, err
	}
	return len(response.Data), nil
}

func episodeCountOrDefault(c *Client, id string, fallback int) int {
	if fallback < 1 {
		return fallback
	}
	if actual, err := c.subjectEpisodeCount(id); err == nil && actual > 0 {
		return actual
	}
	return fallback
}

func (c *Client) bangumiTitle(info map[string]any) string {
	if boolValue(c.config["bgmJpName"]) {
		return stringValue(info["name"])
	}
	name := stringValue(info["nameCn"])
	if name == "" {
		name = stringValue(info["name_cn"])
	}
	if name == "" {
		name = stringValue(info["name"])
	}
	return name
}

func (c *Client) bangumiImage(images map[string]any) string {
	requested := strings.ToLower(strings.TrimSpace(stringValue(c.config["bgmImageSize"])))
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
	return regexp.MustCompile(`\s*\((?:19|20)\d{2}\)\s*$`).ReplaceAllString(strings.TrimSpace(value), "")
}

func (c *Client) get(target string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < c.retries; attempt++ {
		request, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", "ani-rss-go")
		response, err := c.httpClient.Do(request)
		if err == nil {
			body, readErr := readSourceResponse(response)
			if readErr == nil {
				return body, nil
			}
			lastErr = readErr
		} else {
			lastErr = err
		}
		if attempt+1 < c.retries {
			time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
		}
	}
	return nil, lastErr
}

func readSourceResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("source returned HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 16<<20))
}

func (c *Client) mikanDetail(target string, body []byte) (map[string]any, error) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	info := map[string]any{"url": target, "groups": []any{}}
	if node := firstClass(document, "content"); node != nil {
		if image := firstTag(node, "img"); image != nil {
			info["cover"] = absolute(target, attr(image, "src"))
		}
	}
	if node := firstClass(document, "bangumi-title"); node != nil {
		info["title"] = text(node)
	}
	for _, node := range allClass(document, "bangumi-info") {
		if !strings.Contains(text(node), "Bangumi番组计划链接") {
			continue
		}
		if anchor := firstTag(node, "a"); anchor != nil {
			info["bgmUrl"] = attr(anchor, "href")
		}
		break
	}
	groups := make([]any, 0)
	for _, left := range allClass(document, "leftbar-item") {
		nameNode := firstClass(left, "subgroup-name")
		if nameNode == nil {
			continue
		}
		label, anchor := text(nameNode), attr(nameNode, "data-anchor")
		section := firstID(document, strings.TrimPrefix(anchor, "#"))
		group := map[string]any{"label": label, "subgroupId": strings.TrimPrefix(anchor, "#"), "rss": "", "items": []any{}}
		if date := firstClass(left, "date"); date != nil {
			group["updateDay"] = text(date)
		}
		if section != nil {
			if rss := firstClass(section, "mikan-rss"); rss != nil {
				group["rss"] = absolute(target, attr(rss, "href"))
			}
			if table := nextElement(section); table != nil {
				group["items"] = parseTableItems(target, table)
			}
		}
		group["groupRegex"] = buildGroupRegex(groupTitles(group["items"]))
		groups = append(groups, group)
	}
	for _, raw := range groups {
		if group, ok := raw.(map[string]any); ok {
			group["bgmUrl"] = stringValue(info["bgmUrl"])
		}
	}
	info["groups"] = groups
	return map[string]any{"totalItems": 1, "seasons": []any{}, "weeks": []any{map[string]any{"weekLabel": "Search", "items": []any{info}}}}, nil
}

func (c *Client) parseMikanList(target string, body []byte) map[string]any {
	document, _ := html.Parse(strings.NewReader(string(body)))
	result := map[string]any{"seasons": []any{}, "weeks": []any{}}
	seasons := []any{}
	selectedLabel := text(firstClass(document, "date-text"))
	for _, node := range allClass(document, "date-select") {
		for _, anchor := range allTag(node, "a") {
			year, season := attr(anchor, "data-year"), attr(anchor, "data-season")
			if year != "" && season != "" {
				label := year + " " + season
				seasons = append(seasons, map[string]any{"year": number(year), "season": season, "seasonLabel": label, "select": strings.HasPrefix(selectedLabel, label)})
			}
		}
	}
	result["seasons"] = seasons
	blocks := allClass(document, "sk-bangumi")
	if len(blocks) == 0 {
		// Java selects the result list (.an-ul) here. Scanning every <li> in
		// the document also picks up the season dropdown and can manufacture
		// fake anime cards when a search has no results.
		result["weeks"] = []any{map[string]any{"weekLabel": "Search", "items": c.firstMikanResultList(document, target)}}
	} else {
		weeks := []any{}
		for _, block := range blocks {
			label := ""
			// net/html keeps indentation as text nodes, while Jsoup's
			// children() used by Java returns element children only. Select
			// the first element so real Mikan pages do not produce blank tab
			// labels merely because the page is pretty-printed.
			for _, child := range children(block) {
				if child.Type == html.ElementNode {
					label = text(child)
					break
				}
			}
			items := c.parseMikanItems(block, target)
			if len(items) == 0 {
				continue
			}
			weeks = append(weeks, map[string]any{"weekLabel": label, "items": items})
		}
		result["weeks"] = weeks
	}
	count := 0
	for _, raw := range result["weeks"].([]any) {
		if week, ok := raw.(map[string]any); ok {
			if items, ok := week["items"].([]any); ok {
				count += len(items)
			}
		}
	}
	result["totalItems"] = count
	return result
}

func (c *Client) parseMikanItems(root *html.Node, target string) []any {
	result := []any{}
	for _, li := range allTag(root, "li") {
		anchors := allTag(li, "a")
		if len(anchors) == 0 {
			continue
		}
		anchor := anchors[0]
		href := absolute(target, attr(anchor, "href"))
		// Mikan pages have historically reused .an-ul for navigation and
		// auxiliary lists. Only a link to a numbered Bangumi page represents
		// an anime card; without this guard those lists become cards titled
		// "主页", "订阅" and "列表".
		if !isMikanBangumiLink(href) {
			continue
		}
		title := text(anchor)
		image := ""
		if span := firstTag(li, "span"); span != nil {
			image = absolute(target, attr(span, "data-src"))
		}
		id := trailingDigits(href)
		result = append(result, map[string]any{"bgmId": id, "cover": image, "url": href, "exists": c.hasMikanSubject(id), "score": 0, "title": title})
	}
	return result
}

func (c *Client) firstMikanResultList(document *html.Node, target string) []any {
	for _, list := range allClass(document, "an-ul") {
		if items := c.parseMikanItems(list, target); len(items) > 0 {
			return items
		}
	}
	return []any{}
}

func isMikanBangumiLink(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	return regexp.MustCompile(`(?i)/Home/Bangumi/[0-9]+/?$`).MatchString(parsed.Path)
}

func parseTableItems(target string, table *html.Node) []any {
	result := []any{}
	for _, tr := range allTag(table, "tr") {
		anchors, cells := allTag(tr, "a"), allTag(tr, "td")
		if len(anchors) < 3 || len(cells) < 4 {
			continue
		}
		result = append(result, map[string]any{"title": text(anchors[0]), "magnet": attr(anchors[1], "data-clipboard-text"), "formatSize": text(cells[2]), "createdAt": text(cells[3]), "torrent": absolute(target, attr(anchors[2], "href"))})
	}
	return result
}

func (c *Client) hasMikanSubject(id string) bool {
	if c.subscriptions == nil || id == "" {
		return false
	}
	for _, item := range c.subscriptions() {
		if mikanID(item.URL) == id {
			return true
		}
	}
	return false
}

func (c *Client) hasBGMSubject(id string) bool {
	if c.subscriptions == nil || id == "" {
		return false
	}
	for _, item := range c.subscriptions() {
		if subjectID(item.BGMURL) == id {
			return true
		}
	}
	return false
}

func mikanID(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("bangumiId")
}

func itemTime(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := item[key]; ok && strings.TrimSpace(stringValue(value)) != "" {
			return value
		}
	}
	return nil
}

func latestItemTime(item map[string]any, keys ...string) any {
	var latest any
	for _, key := range keys {
		candidate := itemTime(item, key)
		if newer(candidate, latest) {
			latest = candidate
		}
	}
	if latest != nil {
		return latest
	}
	return nil
}

func newer(candidate, current any) bool {
	left, leftOK := parseItemTime(candidate)
	right, rightOK := parseItemTime(current)
	return leftOK && (!rightOK || left.After(right))
}

func parseItemTime(value any) (time.Time, bool) {
	raw := strings.TrimSpace(stringValue(value))
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, time.RFC1123Z, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func numberFloat(value any) float64 {
	return number(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func groupTitles(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, raw := range items {
		if item, ok := raw.(map[string]any); ok {
			if title := stringValue(item["title"]); title != "" {
				result = append(result, title)
			}
		}
	}
	return result
}

func buildGroupRegex(titles []string) map[string]any {
	patterns := []string{"1920[Xx]1080", "3840[Xx]2160", "1080[Pp]", "720[Pp]", "4[Kk]", "繁", "简", "日", "内嵌", "内封", "外挂", "cht|Cht|CHT", "chs|Chs|CHS", "avc|Avc|AVC", "hevc|Hevc|HEVC", "h264|H264", "h265|H265", "10bit|10Bit|10BIT", "mp4|MP4", "mkv|MKV"}
	regexList := make([][]map[string]any, 0)
	tags := make([]string, 0, 5)
	seen := map[string]bool{}
	for _, title := range titles {
		items := make([]map[string]any, 0)
		for _, pattern := range patterns {
			matched, err := regexp.MatchString(pattern, title)
			if err != nil || !matched {
				continue
			}
			label := regexp.MustCompile(pattern).FindString(title)
			items = append(items, map[string]any{"regex": pattern, "label": label})
			if len(tags) < 5 && !seen[label] {
				tags = append(tags, label)
				seen[label] = true
			}
		}
		if len(items) > 0 {
			regexList = append(regexList, items)
		}
	}
	return map[string]any{"regexList": regexList, "tags": tags}
}
func SubjectID(value string) string { return subjectID(value) }

func subjectID(value string) string {
	if parsed, err := url.Parse(value); err == nil {
		for _, key := range []string{"bgmId", "subject", "bangumiId", "id"} {
			if candidate := parsed.Query().Get(key); candidate != "" {
				return candidate
			}
		}
	}
	match := regexp.MustCompile(`/([0-9]+)(?:/)?$`).FindStringSubmatch(strings.TrimRight(value, "/"))
	if len(match) > 1 {
		return match[1]
	}
	return ""
}
func trailingDigits(value string) string {
	match := regexp.MustCompile(`([0-9]+)$`).FindStringSubmatch(strings.TrimRight(value, "/"))
	if len(match) > 1 {
		return match[1]
	}
	return ""
}
func absolute(base, value string) string {
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	origin, err := url.Parse(base)
	if err != nil {
		return value
	}
	return origin.ResolveReference(parsed).String()
}
func formatSize(size int64) string {
	value, suffix := float64(size), "B"
	for _, next := range []string{"KiB", "MiB", "GiB", "TiB"} {
		if value < 1024 {
			break
		}
		value /= 1024
		suffix = next
	}
	return fmt.Sprintf("%.2f %s", value, suffix)
}
func weekLabel(value any, labels []string) string {
	raw := stringValue(value)
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return ""
	}
	return labels[int(parsed.In(time.Local).Weekday())]
}
func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case json.Number:
		v, _ := typed.Float64()
		return v
	case string:
		v, _ := strconv.ParseFloat(typed, 64)
		return v
	}
	return 0
}
func number64(value any) int64 { return int64(number(value)) }
func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		if typed == float32(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	default:
		return ""
	}
}
func children(node *html.Node) []*html.Node {
	result := []*html.Node{}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		result = append(result, child)
	}
	return result
}
func text(node *html.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == html.TextNode {
		return strings.TrimSpace(node.Data)
	}
	parts := []string{}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if value := text(child); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}
func attr(node *html.Node, key string) string {
	if node == nil {
		return ""
	}
	for _, item := range node.Attr {
		if item.Key == key {
			return item.Val
		}
	}
	return ""
}
func firstTag(node *html.Node, tag string) *html.Node {
	for _, item := range allTag(node, tag) {
		return item
	}
	return nil
}
func allTag(node *html.Node, tag string) []*html.Node {
	result := []*html.Node{}
	if node == nil {
		return result
	}
	if node.Type == html.ElementNode && node.Data == tag {
		result = append(result, node)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		result = append(result, allTag(child, tag)...)
	}
	return result
}
func hasClass(node *html.Node, class string) bool {
	for _, item := range node.Attr {
		if item.Key == "class" {
			for _, value := range strings.Fields(item.Val) {
				if value == class {
					return true
				}
			}
		}
	}
	return false
}
func allClass(node *html.Node, class string) []*html.Node {
	result := []*html.Node{}
	if node == nil {
		return result
	}
	if node.Type == html.ElementNode && hasClass(node, class) {
		result = append(result, node)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		result = append(result, allClass(child, class)...)
	}
	return result
}
func firstClass(node *html.Node, class string) *html.Node {
	items := allClass(node, class)
	if len(items) > 0 {
		return items[0]
	}
	return nil
}
func firstID(node *html.Node, id string) *html.Node {
	if node == nil {
		return nil
	}
	if attr(node, "id") == id {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if result := firstID(child, id); result != nil {
			return result
		}
	}
	return nil
}
func nextElement(node *html.Node) *html.Node {
	for sibling := node.NextSibling; sibling != nil; sibling = sibling.NextSibling {
		if sibling.Type == html.ElementNode {
			return sibling
		}
	}
	return nil
}

func newID() string { return fmt.Sprintf("ani-%d", time.Now().UnixNano()) }
