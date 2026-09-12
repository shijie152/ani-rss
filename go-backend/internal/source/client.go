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
	HTTPClient      *http.Client
	Subscriptions   func() []model.Ani
	Retries         int
}

type Client struct {
	mikanHost, aniBTHost, gardenHost, bangumiAPI string
	httpClient                                   *http.Client
	subscriptions                                func() []model.Ani
	retries                                      int
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
		httpClient:    httpClient,
		subscriptions: options.Subscriptions,
		retries:       retries,
	}
}

func (c *Client) Mikan(text string, season map[string]any) (map[string]any, error) {
	if c.mikanHost == "" {
		return nil, errors.New("Mikan host is not configured")
	}
	if strings.HasPrefix(strings.TrimSpace(text), "id: ") {
		id := strings.TrimSpace(strings.TrimPrefix(text, "id: "))
		target := c.mikanHost + "/Home/Bangumi/" + url.PathEscape(id)
		body, err := c.get(target)
		if err != nil {
			return nil, err
		}
		return c.mikanDetail(target, body)
	}
	target := c.mikanHost + "/Home/Search"
	query := url.Values{}
	if strings.TrimSpace(text) != "" {
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
				anime["exists"] = c.hasSubject(stringValue(anime["bgmId"]))
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
	weeks := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	grouped := map[string][]any{}
	for _, subject := range envelope.Subjects {
		id := stringValue(subject["id"])
		subject["exists"] = c.hasSubject(id)
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
	return result, nil
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
			group = map[string]any{"id": id, "name": name, "bgmId": bgmID, "rss": fmt.Sprintf("%s/feed.xml?subject=%s&fansub=%s", c.gardenHost, url.QueryEscape(bgmID), url.QueryEscape(name)), "items": []any{}}
			group["lastUpdatedAt"] = latestItemTime(item, "createdAt", "fetchedAt")
			groups[id] = group
		} else if newer(latestItemTime(item, "createdAt", "fetchedAt"), group["lastUpdatedAt"]) {
			group["lastUpdatedAt"] = latestItemTime(item, "createdAt", "fetchedAt")
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
	sort.SliceStable(result, func(i, j int) bool { return stringValue(result[i]["name"]) < stringValue(result[j]["name"]) })
	return result, nil
}

func (c *Client) SearchBangumi(name string) ([]map[string]any, error) {
	target := c.bangumiAPI + "/search/subject/" + url.PathEscape(strings.ReplaceAll(name, "1/2", "½")) + "?type=2&max_results=25&responseGroup=small"
	body, err := c.get(target)
	if err != nil {
		return nil, err
	}
	var response struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode Bangumi search: %w", err)
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
	return episodes, nil
}

func (c *Client) SubscriptionFromSubject(id string) (model.Ani, error) {
	info, err := c.BangumiSubject(id)
	if err != nil {
		return model.Ani{}, err
	}
	name := stringValue(info["nameCn"])
	if name == "" {
		name = stringValue(info["name_cn"])
	}
	if name == "" {
		name = stringValue(info["name"])
	}
	images, _ := info["images"].(map[string]any)
	return model.Ani{ID: newID(), Title: name, JPTitle: stringValue(info["name"]), URL: c.bangumiAPI + "/v0/subjects/" + id, BGMURL: "https://bgm.tv/subject/" + id, Image: stringValue(images["large"]), Season: int(number(info["season"])), TotalEpisodeNumber: int(number(info["eps"])), Enable: true, CustomDownloadPath: true, Type: "other"}, nil
}

func (c *Client) BGMTitle(item model.Ani) (string, error) {
	id := subjectID(item.BGMURL)
	if id == "" {
		return "", errors.New("bgmUrl 不能为空")
	}
	info, err := c.BangumiSubject(id)
	if err != nil {
		return "", err
	}
	name := stringValue(info["nameCn"])
	if name == "" {
		name = stringValue(info["name_cn"])
	}
	if name == "" {
		name = stringValue(info["name"])
	}
	if name == "" {
		name = "无标题"
	}
	return name, nil
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
	if node := firstClass(document, "bangumi-info"); node != nil {
		if anchor := firstTag(node, "a"); anchor != nil {
			info["bgmUrl"] = attr(anchor, "href")
		}
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
				seasons = append(seasons, map[string]any{"year": number(year), "season": season, "seasonLabel": label, "select": selectedLabel == label})
			}
		}
	}
	result["seasons"] = seasons
	blocks := allClass(document, "sk-bangumi")
	if len(blocks) == 0 {
		result["weeks"] = []any{map[string]any{"weekLabel": "Search", "items": c.parseMikanItems(document, target)}}
	} else {
		weeks := []any{}
		for _, block := range blocks {
			label := ""
			if len(children(block)) > 0 {
				label = text(children(block)[0])
			}
			weeks = append(weeks, map[string]any{"weekLabel": label, "items": c.parseMikanItems(block, target)})
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
		title := text(anchor)
		image := ""
		if span := firstTag(li, "span"); span != nil {
			image = absolute(target, attr(span, "data-src"))
		}
		id := trailingDigits(href)
		result = append(result, map[string]any{"bgmId": id, "cover": image, "url": href, "exists": c.hasSubject(id), "score": 0, "title": title})
	}
	return result
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

func (c *Client) hasSubject(id string) bool {
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
	return labels[int(parsed.Weekday())]
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
func number64(value any) int64     { return int64(number(value)) }
func stringValue(value any) string { result, _ := value.(string); return strings.TrimSpace(result) }
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
