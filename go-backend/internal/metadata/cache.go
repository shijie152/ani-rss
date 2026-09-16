package metadata

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
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

// Cache is a bounded stale-while-revalidate cache for metadata responses.
// Search responses expire sooner than subject/details; callers choose policy
// by key prefix while the cache keeps transport concerns centralized.
type Cache struct {
	mu         sync.Mutex
	entries    map[string]cacheEntry
	calls      map[string]*cacheCall
	maxEntries int
	now        func() time.Time
	background func(func())
}

func NewCache(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = 512
	}
	return &Cache{entries: map[string]cacheEntry{}, calls: map[string]*cacheCall{}, maxEntries: maxEntries, now: time.Now, background: func(fn func()) { go fn() }}
}

// WithBackground lets an application-owned executor track stale refreshes
// during shutdown. A nil callback restores the default goroutine executor.
func (c *Cache) WithBackground(run func(func())) *Cache {
	if run == nil {
		run = func(fn func()) { go fn() }
	}
	c.background = run
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
	if value, ok, isStale := c.lookup(key, fresh, stale); ok {
		if isStale {
			c.refresh(key, func() (map[string]any, error) {
				return loader(context.WithoutCancel(ctx))
			})
		}
		return value, nil
	}
	call, owner := c.begin(key)
	if !owner {
		<-call.done
		return decode(call.value, call.err)
	}
	value, err := loader(ctx)
	var raw []byte
	if err == nil {
		raw, err = json.Marshal(value)
	}
	c.finish(key, call, raw, err)
	return decode(raw, err)
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

func (c *Cache) lookup(key string, fresh, stale time.Duration) (map[string]any, bool, bool) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	if ok {
		age := c.now().Sub(entry.fetchedAt)
		if age < 0 {
			age = 0
		}
		if age < stale {
			entry.lastAccess = c.now()
			c.entries[key] = entry
			c.mu.Unlock()
			value, err := decode(entry.value, nil)
			return value, err == nil, age >= fresh
		}
		delete(c.entries, key)
	}
	c.mu.Unlock()
	return nil, false, false
}

func decode(raw []byte, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (c *Cache) begin(key string) (*cacheCall, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if call, ok := c.calls[key]; ok {
		return call, false
	}
	call := &cacheCall{done: make(chan struct{})}
	c.calls[key] = call
	return call, true
}

func (c *Cache) finish(key string, call *cacheCall, value []byte, err error) {
	call.value, call.err = append([]byte(nil), value...), err
	c.mu.Lock()
	if err == nil && len(value) > 0 {
		now := c.now()
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
	delete(c.calls, key)
	close(call.done)
	c.mu.Unlock()
}

func (c *Cache) refresh(key string, loader func() (map[string]any, error)) {
	call, owner := c.begin(key)
	if !owner {
		return
	}
	run := c.background
	if run == nil {
		run = func(fn func()) { go fn() }
	}
	run(func() {
		value, err := loader()
		var raw []byte
		if err == nil {
			raw, err = json.Marshal(value)
		}
		c.finish(key, call, raw, err)
	})
}
