// Package cache provides the process-local LRU cache with
// stale-while-revalidate and single-flight semantics shared by the upstream
// clients. Callers supply the key, the freshness window and the loader; the
// package owns eviction, coalescing and background refresh.
package cache

import (
	"context"
	"sync"
	"time"
)

// State describes whether a value could be served without contacting the
// upstream. Stale values are intentionally usable while one background load
// refreshes them.
type State uint8

const (
	Miss State = iota
	Fresh
	Stale
)

type entry[T any] struct {
	value      T
	fetchedAt  time.Time
	lastAccess time.Time
}

type call[T any] struct {
	done  chan struct{}
	value T
	err   error
}

// Cache is a bounded process-local cache. Values are copied on the way in and
// out when the type is a byte slice, so callers never share mutable state.
type Cache[T any] struct {
	mu         sync.Mutex
	entries    map[string]entry[T]
	calls      map[string]*call[T]
	maxEntries int
	now        func() time.Time
	background func(func())
}

// New creates a bounded cache. A zero or negative limit uses a conservative
// default suitable for a single-user desktop service. A nil clock uses
// time.Now.
func New[T any](maxEntries int, now func() time.Time) *Cache[T] {
	if maxEntries <= 0 {
		maxEntries = 256
	}
	if now == nil {
		now = time.Now
	}
	return &Cache[T]{
		entries:    make(map[string]entry[T]),
		calls:      make(map[string]*call[T]),
		maxEntries: maxEntries,
		now:        now,
	}
}

// WithBackground lets an application-owned executor track stale refreshes
// instead of spawning bare goroutines.
func (c *Cache[T]) WithBackground(run func(func())) *Cache[T] {
	c.background = run
	return c
}

// Get returns the value for key, loading it when the cache cannot serve it.
// A fresh entry is returned directly; a stale entry is returned while one
// background load refreshes it. Concurrent callers for the same key share a
// single load.
func (c *Cache[T]) Get(ctx context.Context, key string, fresh, stale time.Duration, load func(context.Context) (T, error)) (T, State, error) {
	if c == nil || key == "" {
		value, err := load(ctx)
		return value, Miss, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if value, state := c.lookup(key, fresh, stale); state != Miss {
		if state == Stale {
			c.refresh(ctx, key, load)
		}
		return value, state, nil
	}
	value, err := c.load(ctx, key, load)
	if err != nil {
		var zero T
		return zero, Miss, err
	}
	return value, Fresh, err
}

func (c *Cache[T]) lookup(key string, freshFor, staleFor time.Duration) (T, State) {
	var zero T
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	stored, ok := c.entries[key]
	if !ok {
		return zero, Miss
	}
	age := now.Sub(stored.fetchedAt)
	if age < 0 {
		age = 0
	}
	if freshFor > 0 && age < freshFor {
		stored.lastAccess = now
		c.entries[key] = stored
		return stored.value, Fresh
	}
	if staleFor > 0 && age < staleFor {
		stored.lastAccess = now
		c.entries[key] = stored
		return stored.value, Stale
	}
	delete(c.entries, key)
	return zero, Miss
}

func (c *Cache[T]) store(key string, value T) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry[T]{value: value, fetchedAt: now, lastAccess: now}
	for len(c.entries) > c.maxEntries {
		oldestKey := ""
		var oldest time.Time
		for candidate, stored := range c.entries {
			if oldestKey == "" || stored.lastAccess.Before(oldest) {
				oldestKey, oldest = candidate, stored.lastAccess
			}
		}
		delete(c.entries, oldestKey)
	}
}

func (c *Cache[T]) begin(key string) (*call[T], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.calls[key]; ok {
		return existing, false
	}
	pending := &call[T]{done: make(chan struct{})}
	c.calls[key] = pending
	return pending, true
}

func (c *Cache[T]) finish(key string, pending *call[T], value T, err error) {
	if err == nil {
		c.store(key, value)
	}
	pending.value = value
	pending.err = err
	c.mu.Lock()
	delete(c.calls, key)
	close(pending.done)
	c.mu.Unlock()
}

func (c *Cache[T]) load(ctx context.Context, key string, load func(context.Context) (T, error)) (T, error) {
	pending, owner := c.begin(key)
	if !owner {
		<-pending.done
		return pending.value, pending.err
	}
	value, err := load(ctx)
	c.finish(key, pending, value, err)
	return value, err
}

// refresh starts one best-effort background load. It returns false when an
// equivalent load is already running.
func (c *Cache[T]) refresh(ctx context.Context, key string, load func(context.Context) (T, error)) bool {
	pending, owner := c.begin(key)
	if !owner {
		return false
	}
	background := c.background
	if background == nil {
		background = func(fn func()) { go fn() }
	}
	background(func() {
		value, err := load(context.WithoutCancel(ctx))
		c.finish(key, pending, value, err)
	})
	return true
}
