// Package metadata contains replaceable TMDB and Bangumi metadata clients.
// Responses are kept as raw maps because the existing UI stores the complete
// TMDB object and newer UI versions may add fields before Go does.
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

type Client struct {
	Config     model.Config
	HTTPClient *http.Client
}

// Keep the public fallback used by the Java release. Users can still replace
// it with their own key in settings, while a fresh installation retains the
// legacy behavior of resolving TMDB during subscription creation.
const defaultTMDBAPIKey = "450e4f651e1c93e31383e20f8e731e5f"

func New(config model.Config, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{Config: config, HTTPClient: client}
}

// Lookup obtains a TMDB object by the stored id, or searches by the
// subscription title when no id is stored. Bangumi is used as a fallback for
// subscriptions created from a Bangumi subject when TMDB is disabled.
func (c *Client) Lookup(ctx context.Context, ani model.Ani) (model.Metadata, map[string]any, error) {
	tmdbEnabled, tmdbConfigured := c.Config["tmdb"].(bool)
	if tmdbConfigured && !tmdbEnabled {
		if subjectID := subjectID(ani.BGMURL); subjectID != "" {
			return c.lookupBangumi(ctx, subjectID)
		}
		return model.Metadata{}, nil, errors.New("未启用 TMDB 且订阅没有 Bangumi subject")
	}
	if rawID := objectString(ani.TMDB, "id"); rawID != "" {
		return c.lookupTMDBID(ctx, rawID, ani.OVA, ani.Season)
	}
	if title := strings.TrimSpace(ani.TheMovieDBName); title != "" {
		if metadata, raw, err := c.searchTMDB(ctx, title, ani.OVA, ani.Season); err == nil {
			return metadata, raw, nil
		}
	}
	if metadata, raw, err := c.searchTMDB(ctx, stripIdentifiers(ani.Title), ani.OVA, ani.Season); err == nil {
		return metadata, raw, nil
	}
	if subjectID := subjectID(ani.BGMURL); subjectID != "" {
		return c.lookupBangumi(ctx, subjectID)
	}
	return model.Metadata{}, nil, errors.New("未获取到元数据")
}

func (c *Client) SearchTMDB(ctx context.Context, title string, ova bool) (model.Metadata, map[string]any, error) {
	return c.searchTMDB(ctx, title, ova, 0)
}

// LookupTMDB implements the standalone /getThemoviedbName operation. Unlike
// subscription scraping, this endpoint is explicitly TMDB-only: the global
// `tmdb` switch must not turn a title lookup into a Bangumi fallback.
func (c *Client) LookupTMDB(ctx context.Context, title, id string, ova bool) (model.Metadata, map[string]any, error) {
	if strings.TrimSpace(id) != "" {
		return c.lookupTMDBID(ctx, strings.TrimSpace(id), ova, 0)
	}
	title = renameDelForLookup(title)
	if strings.TrimSpace(title) == "" {
		return model.Metadata{}, nil, errors.New("TmdbId 或 标题 不能为空")
	}
	return c.searchTMDB(ctx, title, ova, 0)
}

func (c *Client) lookupTMDBID(ctx context.Context, id string, ova bool, season int) (model.Metadata, map[string]any, error) {
	base := c.tmdbBase()
	if base == "" {
		return model.Metadata{}, nil, errors.New("TMDB API 未配置")
	}
	typeName := "tv"
	if ova {
		typeName = "movie"
	}
	query := c.tmdbQuery()
	query.Set("append_to_response", "credits,videos,images")
	raw, err := c.getJSON(ctx, base+"/"+typeName+"/"+url.PathEscape(id), query)
	if err != nil {
		return model.Metadata{}, nil, err
	}
	metadata := normalizeTMDB(raw, ova)
	if !ova && season > 0 {
		metadata.EpisodeInfo = c.lookupSeason(ctx, base, id, season, query)
	}
	return metadata, raw, nil
}

func (c *Client) searchTMDB(ctx context.Context, title string, ova bool, season int) (model.Metadata, map[string]any, error) {
	base := c.tmdbBase()
	if base == "" {
		return model.Metadata{}, nil, errors.New("TMDB API 未配置")
	}
	typeName := "tv"
	if ova {
		typeName = "movie"
	}
	query := c.tmdbQuery()
	query.Set("query", title)
	search, err := c.getJSON(ctx, base+"/search/"+typeName, query)
	if err != nil {
		return model.Metadata{}, nil, err
	}
	results, _ := search["results"].([]any)
	if len(results) == 0 {
		return model.Metadata{}, nil, errors.New("未获取到 TMDB")
	}
	first, ok := results[0].(map[string]any)
	if !ok {
		return model.Metadata{}, nil, errors.New("TMDB 返回数据异常")
	}
	id := objectString(first, "id")
	if id == "" {
		return model.Metadata{}, nil, errors.New("TMDB 返回缺少 id")
	}
	return c.lookupTMDBID(ctx, id, ova, season)
}

func (c *Client) lookupSeason(ctx context.Context, base, id string, season int, query url.Values) []model.EpisodeMeta {
	raw, err := c.getJSON(ctx, base+"/tv/"+url.PathEscape(id)+"/season/"+strconv.Itoa(season), query)
	if err != nil {
		return nil
	}
	values, _ := raw["episodes"].([]any)
	result := make([]model.EpisodeMeta, 0, len(values))
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		result = append(result, model.EpisodeMeta{
			Number:   number(item["episode_number"]),
			Name:     stringValue(item["name"]),
			Overview: stringValue(item["overview"]),
			AirDate:  stringValue(item["air_date"]),
			Still:    stringValue(item["still_path"]),
		})
	}
	return result
}

func (c *Client) lookupBangumi(ctx context.Context, id string) (model.Metadata, map[string]any, error) {
	base := strings.TrimRight(stringValue(c.Config["bgmApi"]), "/")
	if base == "" {
		return model.Metadata{}, nil, errors.New("Bangumi API 未配置")
	}
	raw, err := c.getJSON(ctx, base+"/v0/subjects/"+url.PathEscape(id), nil)
	if err != nil {
		return model.Metadata{}, nil, err
	}
	metadata := normalizeBangumi(raw)
	return metadata, raw, nil
}

func (c *Client) SearchBangumi(ctx context.Context, title string) ([]map[string]any, error) {
	base := strings.TrimRight(stringValue(c.Config["bgmApi"]), "/")
	if base == "" {
		return nil, errors.New("Bangumi API 未配置")
	}
	query := url.Values{"type": {"2"}, "max_results": {"25"}, "responseGroup": {"small"}}
	raw, err := c.getJSON(ctx, base+"/search/subject/"+url.PathEscape(title), query)
	if err != nil {
		return nil, err
	}
	values, _ := raw["list"].([]any)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result, nil
}

func (c *Client) Groups(ctx context.Context, id string) ([]map[string]any, error) {
	base := c.tmdbBase()
	if base == "" || strings.TrimSpace(id) == "" {
		return nil, errors.New("TMDB id 不能为空")
	}
	raw, err := c.getJSON(ctx, base+"/tv/"+url.PathEscape(id)+"/episode_groups", c.tmdbQuery())
	if err != nil {
		return nil, err
	}
	values, _ := raw["results"].([]any)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result, nil
}

func (c *Client) tmdbBase() string {
	base := strings.TrimRight(stringValue(c.Config["tmdbApi"]), "/")
	if base == "" {
		return ""
	}
	if strings.HasSuffix(base, "/3") {
		return base
	}
	return base + "/3"
}

func (c *Client) tmdbQuery() url.Values {
	query := url.Values{}
	key := stringValue(c.Config["tmdbApiKey"])
	if key == "" {
		key = defaultTMDBAPIKey
	}
	query.Set("api_key", key)
	if language := stringValue(c.Config["tmdbLanguage"]); language != "" {
		query.Set("language", language)
	}
	return query
}

func (c *Client) getJSON(ctx context.Context, target string, query url.Values) (map[string]any, error) {
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "ani-rss-go")
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("metadata service returned HTTP %d", response.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func normalizeTMDB(raw map[string]any, ova bool) model.Metadata {
	title := stringValue(raw["name"])
	original := stringValue(raw["original_name"])
	date := stringValue(raw["first_air_date"])
	if ova {
		title = stringValue(raw["title"])
		original = stringValue(raw["original_title"])
		date = stringValue(raw["release_date"])
	}
	genres := []string{}
	if values, ok := raw["genres"].([]any); ok {
		for _, value := range values {
			if item, ok := value.(map[string]any); ok && stringValue(item["name"]) != "" {
				genres = append(genres, stringValue(item["name"]))
			}
		}
	}
	return model.Metadata{ID: objectString(raw, "id"), Title: title, OriginalTitle: original, Overview: stringValue(raw["overview"]), Year: year(date), Episodes: number(raw["number_of_episodes"]), Score: decimal(raw["vote_average"]), Poster: stringValue(raw["poster_path"]), Backdrop: stringValue(raw["backdrop_path"]), AirDate: date, Genres: genres, Raw: raw}
}

func normalizeBangumi(raw map[string]any) model.Metadata {
	name := stringValue(raw["nameCn"])
	if name == "" {
		name = stringValue(raw["name_cn"])
	}
	if name == "" {
		name = stringValue(raw["name"])
	}
	rating, _ := raw["rating"].(map[string]any)
	images, _ := raw["images"].(map[string]any)
	date := stringValue(raw["date"])
	return model.Metadata{ID: objectString(raw, "id"), Title: name, OriginalTitle: stringValue(raw["name"]), Overview: stringValue(raw["summary"]), Year: year(date), Season: number(raw["season"]), Episodes: number(raw["eps"]), Score: decimal(rating["score"]), Poster: stringValue(images["large"]), AirDate: date, Raw: raw}
}

func objectString(value map[string]any, key string) string {
	return stringValue(value[key])
}

func stringValue(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(value), 'f', -1, 32)
	case int:
		return strconv.Itoa(value)
	default:
		return ""
	}
}

func number(value any) int {
	parsed, _ := strconv.Atoi(stringValue(value))
	return parsed
}

func decimal(value any) float64 {
	parsed, _ := strconv.ParseFloat(stringValue(value), 64)
	return parsed
}

func year(value string) int {
	if len(value) < 4 {
		return 0
	}
	parsed, _ := strconv.Atoi(value[:4])
	return parsed
}

func subjectID(value string) string {
	parsed, err := url.Parse(value)
	if err == nil {
		for _, key := range []string{"subject", "id", "bgmId"} {
			if result := parsed.Query().Get(key); result != "" {
				return result
			}
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) > 1 && parts[len(parts)-2] == "subject" {
			return parts[len(parts)-1]
		}
	}
	return ""
}

func stripIdentifiers(value string) string {
	value = strings.TrimSpace(value)
	for _, pattern := range []string{"[tmdbid=", "{tmdb-"} {
		if index := strings.Index(strings.ToLower(value), pattern); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
	}
	return value
}

func renameDelForLookup(value string) string {
	value = strings.TrimSpace(value)
	value = regexp.MustCompile(` ?(\[tmdbid=\d+\]|\{tmdb-\d+\})`).ReplaceAllString(value, "")
	value = regexp.MustCompile(` ?\((?:19|20)\d{2}\)`).ReplaceAllString(value, "")
	return strings.TrimSpace(value)
}
