package metadata

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/cache"
)

// Cache is the metadata-facing view of the shared cache module: it owns the
// per-key freshness policy while eviction, coalescing and background refresh
// stay in the shared implementation.
type Cache struct {
	values *cache.Cache[map[string]any]
}

func NewCache(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = 512
	}
	return &Cache{values: cache.New[map[string]any](maxEntries, nil)}
}

// newCacheWithClock lets tests drive the freshness windows without sleeping.
func newCacheWithClock(maxEntries int, now func() time.Time) *Cache {
	return &Cache{values: cache.New[map[string]any](maxEntries, now)}
}

// WithBackground lets an application-owned executor track stale refreshes
// during shutdown. A nil callback restores the default goroutine executor.
func (c *Cache) WithBackground(run func(func())) *Cache {
	c.values.WithBackground(run)
	return c
}

func (c *Cache) JSON(key string, loader func() (map[string]any, error)) (map[string]any, error) {
	return c.JSONContext(context.Background(), key, func(context.Context) (map[string]any, error) {
		return loader()
	})
}

// JSONContext uses ctx for a foreground load. If a stale value is returned,
// the refresh deliberately uses a copy without cancellation so an HTTP
// request ending does not abort the cache update immediately.
func (c *Cache) JSONContext(ctx context.Context, key string, loader func(context.Context) (map[string]any, error)) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fresh, stale := policy(key)
	value, _, err := c.values.Get(ctx, key, fresh, stale, func(loadCtx context.Context) (map[string]any, error) {
		loaded, loadErr := loader(loadCtx)
		if loadErr != nil {
			return nil, loadErr
		}
		// Cached metadata is shared across requests: hand every caller its own
		// copy so a caller mutating the result cannot corrupt the cache.
		return clone(loaded), nil
	})
	if err != nil {
		return nil, err
	}
	return clone(value), nil
}

func clone(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var copied map[string]any
	if err := json.Unmarshal(raw, &copied); err != nil {
		return value
	}
	return copied
}

func policy(key string) (time.Duration, time.Duration) {
	if strings.Contains(key, "/search/") {
		return 15 * time.Minute, 24 * time.Hour
	}
	if strings.Contains(key, "/v0/subjects/") || strings.Contains(key, "/tv/") {
		return 6 * time.Hour, 7 * 24 * time.Hour
	}
	return time.Hour, 24 * time.Hour
}
