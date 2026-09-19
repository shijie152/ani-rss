package source

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// MikanAdapter owns Mikan's HTML protocol, URL conventions and local
// subscription marker semantics behind a small source-specific interface.
type MikanAdapter struct{ runtime *runtime }

var (
	mikanIDLinePattern      = regexp.MustCompile(`^id: ([0-9]+)$`)
	mikanBangumiPathPattern = regexp.MustCompile(`(?i)/Home/Bangumi/[0-9]+/?$`)
	mikanTrailingIDPattern  = regexp.MustCompile(`([0-9]+)$`)
)

func newMikanAdapter(rt *runtime) *MikanAdapter { return &MikanAdapter{runtime: rt} }

func (a *MikanAdapter) ResolveSubscription(rssURL string) (model.Ani, error) {
	return a.resolveMikanSubscription(rssURL)
}

func (a *MikanAdapter) ResolveRSSSubscription(rssURL, bgmURL, subgroup string) (model.Ani, error) {
	if _, err := url.Parse(strings.TrimSpace(rssURL)); err != nil {
		return model.Ani{}, fmt.Errorf("RSS地址格式异常: %w", err)
	}
	resolved := model.Ani{BGMURL: bgmURL, Subgroup: subgroup}
	// The legacy batch-add flow only follows Mikan's detail page when both
	// optional values are absent. The regular add flow supplies them already.
	if strings.TrimSpace(bgmURL) != "" || strings.TrimSpace(subgroup) != "" {
		return resolved, nil
	}
	value, err := a.ResolveSubscription(rssURL)
	if err != nil {
		return model.Ani{}, err
	}
	return value, nil
}

func (a *MikanAdapter) parseList(target string, body []byte) map[string]any {
	return a.parseMikanList(target, body)
}

func (a *MikanAdapter) Mikan(text string, season map[string]any) (map[string]any, error) {
	c := a.runtime
	if c.mikanHost == "" {
		return nil, errors.New("Mikan host is not configured")
	}
	trimmedText := strings.TrimSpace(text)
	if match := mikanIDLinePattern.FindStringSubmatch(trimmedText); len(match) == 2 {
		target := c.mikanHost + "/Home/Bangumi/" + url.PathEscape(match[1])
		value, err := c.cachedJSON("mikan:detail:"+target, 15*time.Minute, 6*time.Hour, func(loadCtx context.Context) (any, error) {
			body, getErr := c.get(loadCtx, target)
			if getErr != nil {
				return nil, getErr
			}
			return a.mikanDetail(target, body)
		})
		result := mapValue(value)
		if result != nil {
			a.overlayMikanExists(result)
		}
		return result, err
	}
	// The legacy UI uses the Mikan home page for an unfiltered request. That
	// page contains the current season and seasonal catalogue; /Home/Search
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
			query.Set("year", strconvInt(year))
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
	freshFor, staleFor := 6*time.Hour, 48*time.Hour
	if trimmedText != "" {
		freshFor, staleFor = 30*time.Minute, 24*time.Hour
	}
	value, err := c.cachedJSON("mikan:catalog:"+target, freshFor, staleFor, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
		if getErr != nil {
			return nil, getErr
		}
		return a.parseMikanList(target, body), nil
	})
	result := mapValue(value)
	if result != nil {
		a.overlayMikanExists(result)
	}
	return result, err
}

func (a *MikanAdapter) Group(target string) ([]map[string]any, error) {
	c := a.runtime
	if target == "" {
		return nil, errors.New("Mikan URL is empty")
	}
	value, err := c.cachedJSON("mikan:detail:"+target, 15*time.Minute, 6*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
		if getErr != nil {
			return nil, getErr
		}
		return a.mikanDetail(target, body)
	})
	result := mapValue(value)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("Mikan detail response is empty")
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

func (a *MikanAdapter) mikanDetail(target string, body []byte) (map[string]any, error) {
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

func (a *MikanAdapter) parseMikanList(target string, body []byte) map[string]any {
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
		result["weeks"] = []any{map[string]any{"weekLabel": "Search", "items": a.firstMikanResultList(document, target)}}
	} else {
		weeks := []any{}
		for _, block := range blocks {
			label := ""
			for _, child := range children(block) {
				if child.Type == html.ElementNode {
					label = text(child)
					break
				}
			}
			items := a.parseMikanItems(block, target)
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

func (a *MikanAdapter) parseMikanItems(root *html.Node, target string) []any {
	result := []any{}
	for _, li := range allTag(root, "li") {
		anchors := allTag(li, "a")
		if len(anchors) == 0 {
			continue
		}
		anchor := anchors[0]
		href := absolute(target, attr(anchor, "href"))
		if !isMikanBangumiLink(href) {
			continue
		}
		title := text(anchor)
		image := ""
		if span := firstTag(li, "span"); span != nil {
			image = absolute(target, attr(span, "data-src"))
		}
		id := trailingDigits(href)
		result = append(result, map[string]any{"bgmId": id, "cover": image, "url": href, "exists": a.hasMikanSubject(id), "score": 0, "title": title})
	}
	return result
}

func (a *MikanAdapter) firstMikanResultList(document *html.Node, target string) []any {
	for _, list := range allClass(document, "an-ul") {
		if items := a.parseMikanItems(list, target); len(items) > 0 {
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
	return mikanBangumiPathPattern.MatchString(parsed.Path)
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

func (a *MikanAdapter) hasMikanSubject(id string) bool {
	c := a.runtime
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

func (a *MikanAdapter) overlayMikanExists(result map[string]any) {
	weeks, _ := result["weeks"].([]any)
	for _, rawWeek := range weeks {
		week, _ := rawWeek.(map[string]any)
		items, _ := week["items"].([]any)
		for _, rawItem := range items {
			item, _ := rawItem.(map[string]any)
			id := stringValue(item["bgmId"])
			if id == "" {
				id = trailingDigits(stringValue(item["url"]))
			}
			if id != "" {
				item["exists"] = a.hasMikanSubject(id)
			}
		}
	}
}

func (a *MikanAdapter) resolveMikanSubscription(rssURL string) (model.Ani, error) {
	c := a.runtime
	parsed, err := url.Parse(strings.TrimSpace(rssURL))
	if err != nil {
		return model.Ani{}, fmt.Errorf("解析 Mikan RSS: %w", err)
	}
	mikanID := parsed.Query().Get("bangumiId")
	if mikanID == "" {
		return model.Ani{}, errors.New("Mikan RSS 缺少 bangumiId")
	}
	target := c.mikanHost + "/Home/Bangumi/" + url.PathEscape(mikanID)
	value, err := c.cachedJSON("mikan:detail:"+target, 15*time.Minute, 6*time.Hour, func(loadCtx context.Context) (any, error) {
		body, getErr := c.get(loadCtx, target)
		if getErr != nil {
			return nil, getErr
		}
		return a.mikanDetail(target, body)
	})
	if err != nil {
		return model.Ani{}, err
	}
	detail := mapValue(value)
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

func mikanID(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("bangumiId")
}

func trailingDigits(value string) string {
	match := mikanTrailingIDPattern.FindStringSubmatch(strings.TrimRight(value, "/"))
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

func strconvInt(value float64) string {
	return strconv.FormatInt(int64(value), 10)
}
