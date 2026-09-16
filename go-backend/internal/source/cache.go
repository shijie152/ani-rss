package source

import (
	"context"
	"sync"
	"time"
)

// cacheState describes whether a cached value can be used without contacting
// the upstream source. Stale values are intentionally usable while one
// background request refreshes them.
type cacheState uint8

const (
	cacheMiss cacheState = iota
	cacheFresh
	cacheStale
)

type cacheEntry struct {
	value      []byte
	fetchedAt  time.Time
	lastAccess time.Time
}

type cacheCall struct {
	done  chan struct{}
	value []byte
	err   error
}

// Cache is a small process-local LRU cache for parsed source responses. The
// payload is stored as JSON bytes so callers never share mutable maps or
// slices across requests.
type Cache struct {
	mu         sync.Mutex
	entries    map[string]cacheEntry
	calls      map[string]*cacheCall
	maxEntries int
	now        func() time.Time
}

// NewCache creates a bounded source response cache. A zero or negative limit
// uses a conservative default suitable for a single-user desktop service.
func NewCache(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = 256
	}
	return &Cache{
		entries:    make(map[string]cacheEntry),
		calls:      make(map[string]*cacheCall),
		maxEntries: maxEntries,
		now:        time.Now,
	}
}

func newCacheWithClock(maxEntries int, now func() time.Time) *Cache {
	cache := NewCache(maxEntries)
	if now != nil {
		cache.now = now
	}
	return cache
}

func (c *Cache) lookup(key string, freshFor, staleFor time.Duration) ([]byte, cacheState) {
	if c == nil || key == "" {
		return nil, cacheMiss
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, cacheMiss
	}
	age := now.Sub(entry.fetchedAt)
	if age < 0 {
		age = 0
	}
	if freshFor > 0 && age < freshFor {
		entry.lastAccess = now
		c.entries[key] = entry
		return append([]byte(nil), entry.value...), cacheFresh
	}
	if staleFor > 0 && age < staleFor {
		entry.lastAccess = now
		c.entries[key] = entry
		return append([]byte(nil), entry.value...), cacheStale
	}
	delete(c.entries, key)
	return nil, cacheMiss
}

func (c *Cache) store(key string, value []byte) {
	if c == nil || key == "" || len(value) == 0 {
		return
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{value: append([]byte(nil), value...), fetchedAt: now, lastAccess: now}
	for len(c.entries) > c.maxEntries {
		oldestKey := ""
		var oldest time.Time
		for candidate, entry := range c.entries {
			if oldestKey == "" || entry.lastAccess.Before(oldest) {
				oldestKey, oldest = candidate, entry.lastAccess
			}
		}
		delete(c.entries, oldestKey)
	}
}

func (c *Cache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = make(map[string]cacheEntry)
	c.mu.Unlock()
}

func (c *Cache) beginCall(key string) (*cacheCall, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if call, ok := c.calls[key]; ok {
		return call, false
	}
	call := &cacheCall{done: make(chan struct{})}
	c.calls[key] = call
	return call, true
}

func (c *Cache) finishCall(key string, call *cacheCall, value []byte, err error) {
	call.value = append([]byte(nil), value...)
	call.err = err
	if err == nil {
		c.store(key, value)
	}
	c.mu.Lock()
	delete(c.calls, key)
	close(call.done)
	c.mu.Unlock()
}

func (c *Cache) load(key string, loader func() ([]byte, error)) ([]byte, error) {
	return c.loadContext(key, func(context.Context) ([]byte, error) { return loader() }, context.Background())
}

func (c *Cache) loadContext(key string, loader func(context.Context) ([]byte, error), ctx context.Context) ([]byte, error) {
	if c == nil {
		return loader(ctx)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	call, owner := c.beginCall(key)
	if !owner {
		<-call.done
		return append([]byte(nil), call.value...), call.err
	}
	value, err := loader(ctx)
	c.finishCall(key, call, value, err)
	return value, err
}

// refresh starts one best-effort background load. It returns false when an
// equivalent request is already running.
func (c *Cache) refresh(key string, loader func() ([]byte, error), run func(func())) bool {
	return c.refreshContext(key, func(context.Context) ([]byte, error) { return loader() }, context.Background(), run)
}

func (c *Cache) refreshContext(key string, loader func(context.Context) ([]byte, error), ctx context.Context, run func(func())) bool {
	if c == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	call, owner := c.beginCall(key)
	if !owner {
		return false
	}
	background := run
	if background == nil {
		background = func(fn func()) { go fn() }
	}
	background(func() {
		value, err := loader(context.WithoutCancel(ctx))
		c.finishCall(key, call, value, err)
	})
	return true
}
