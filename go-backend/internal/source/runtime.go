// Package source contains replaceable clients for the resource discovery
// services used by the existing UI. The runtime owns shared transport and
// cache policy; source-specific protocol knowledge lives in adapters.
package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/metadata"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/regexutil"
)

// Options controls the shared runtime used by all source adapters.
type Options struct {
	Context         context.Context
	MikanHost       string
	AniBTHost       string
	AnimeGardenHost string
	BangumiAPI      string
	// Cache is shared by clients created for separate HTTP requests. The
	// backend creates a new Client per request so configuration snapshots stay
	// current; the cache keeps upstream responses reusable across those clients.
	Cache *Cache
	// Background schedules stale-while-revalidate work. It is optional for
	// standalone source-client users and tests.
	Background func(func())
	// BGMCoverURL is the optional cache endpoint used by the legacy
	// AnimeGarden page to fill Bangumi covers.
	BGMCoverURL   string
	Config        model.Config
	HTTPClient    *http.Client
	Subscriptions func() []model.Ani
	// Metadata is the shared metadata authority used by the application.
	Metadata *metadata.Client
	Retries  int
}

// runtime is deliberately source-agnostic. It contains only policies shared
// by adapters: transport, retries, cache lifecycle, request context and local
// subscription access.
type runtime struct {
	mikanHost, aniBTHost, gardenHost, bangumiAPI, bgmCoverURL string
	config                                                    model.Config
	httpClient                                                *http.Client
	subscriptions                                             func() []model.Ani
	retries                                                   int
	cache                                                     *Cache
	background                                                func(func())
	metadataClient                                            *metadata.Client
	context                                                   context.Context
}

func newRuntime(options Options) *runtime {
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	retries := options.Retries
	if retries < 1 {
		retries = 3
	}
	requestContext := options.Context
	if requestContext == nil {
		requestContext = context.Background()
	}
	return &runtime{
		mikanHost:      strings.TrimRight(options.MikanHost, "/"),
		aniBTHost:      strings.TrimRight(options.AniBTHost, "/"),
		gardenHost:     strings.TrimRight(options.AnimeGardenHost, "/"),
		bangumiAPI:     strings.TrimRight(options.BangumiAPI, "/"),
		bgmCoverURL:    strings.TrimRight(options.BGMCoverURL, "/"),
		config:         options.Config,
		httpClient:     httpClient,
		subscriptions:  options.Subscriptions,
		retries:        retries,
		cache:          options.Cache,
		background:     options.Background,
		metadataClient: options.Metadata,
		context:        requestContext,
	}
}

func (c *runtime) cachedJSON(key string, freshFor, staleFor time.Duration, loader func(context.Context) (any, error)) (any, error) {
	load := func(ctx context.Context) ([]byte, error) {
		value, err := loader(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	if c.cache == nil {
		return loader(c.context)
	}
	if raw, state := c.cache.lookup(key, freshFor, staleFor); state != cacheMiss {
		var value any
		if err := json.Unmarshal(raw, &value); err == nil {
			if state == cacheStale {
				_ = c.cache.refreshContext(key, load, c.context, c.background)
			}
			return value, nil
		}
	}
	raw, err := c.cache.loadContext(key, load, c.context)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode cached source response: %w", err)
	}
	return value, nil
}

func mapValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func mapListValue(value any) []map[string]any {
	switch values := value.(type) {
	case []map[string]any:
		return values
	case []any:
		result := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if item, ok := value.(map[string]any); ok {
				result = append(result, item)
			}
		}
		return result
	}
	return []map[string]any{}
}

func (c *runtime) hasBGMSubject(id string) bool {
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

func (c *runtime) get(ctx context.Context, target string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	for attempt := 0; attempt < c.retries; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
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
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
			}
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

func itemTime(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := item[key]; ok && strings.TrimSpace(stringValue(value)) != "" {
			return value
		}
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

func numberFloat(value any) float64 { return number(value) }

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
			matched, err := regexutil.MatchString(pattern, title)
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

// SubjectID extracts a Bangumi subject id from any of the source URL forms
// used by the unchanged HTTP and MCP contracts.
var trailingIDPattern = regexp.MustCompile(`/([0-9]+)(?:/)?$`)

func SubjectID(value string) string { return subjectID(value) }

func subjectID(value string) string {
	if parsed, err := url.Parse(value); err == nil {
		for _, key := range []string{"bgmId", "subject", "bangumiId", "id"} {
			if candidate := parsed.Query().Get(key); candidate != "" {
				return candidate
			}
		}
	}
	match := trailingIDPattern.FindStringSubmatch(strings.TrimRight(value, "/"))
	if len(match) > 1 {
		return match[1]
	}
	return ""
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
