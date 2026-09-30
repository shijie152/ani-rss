package cache_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/cache"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
}

func TestGetReturnsFreshValueWithoutReloading(t *testing.T) {
	timeSource := newClock()
	c := cache.New[string](4, timeSource.Now)
	loads := 0
	load := func(context.Context) (string, error) { loads++; return "第01话", nil }

	first, state, err := c.Get(context.Background(), "key", time.Minute, time.Hour, load)
	if err != nil || first != "第01话" || state != cache.Fresh {
		t.Fatalf("first = %q/%v/%v", first, state, err)
	}
	timeSource.Advance(30 * time.Second)
	second, state, err := c.Get(context.Background(), "key", time.Minute, time.Hour, load)
	if err != nil || second != "第01话" || state != cache.Fresh {
		t.Fatalf("second = %q/%v/%v", second, state, err)
	}
	if loads != 1 {
		t.Fatalf("loads = %d, want 1", loads)
	}
}

func TestGetReturnsStaleValueWhileRefreshingInBackground(t *testing.T) {
	timeSource := newClock()
	c := cache.New[string](4, timeSource.Now)
	// The executor receives the pending refresh; capturing it lets the test run
	// the refresh synchronously instead of racing a goroutine.
	var pending func()
	c.WithBackground(func(fn func()) { pending = fn })
	value := "第01话"

	if _, _, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return value, nil
	}); err != nil {
		t.Fatal(err)
	}
	timeSource.Advance(2 * time.Minute)
	value = "第02话"

	stale, state, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return value, nil
	})
	if err != nil || stale != "第01话" || state != cache.Stale {
		t.Fatalf("stale = %q/%v/%v", stale, state, err)
	}
	if pending == nil {
		t.Fatal("stale read must schedule a background refresh")
	}
	pending()
	refreshed, state, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return value, nil
	})
	if err != nil || refreshed != "第02话" || state != cache.Fresh {
		t.Fatalf("refreshed = %q/%v/%v", refreshed, state, err)
	}
}

func TestGetCoalescesConcurrentLoads(t *testing.T) {
	c := cache.New[string](4, nil)
	release := make(chan struct{})
	loads := 0
	load := func(context.Context) (string, error) {
		loads++
		<-release
		return "第01话", nil
	}

	var wg sync.WaitGroup
	results := make([]string, 3)
	for index := range results {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			value, _, err := c.Get(context.Background(), "key", time.Minute, time.Hour, load)
			if err != nil {
				t.Errorf("get: %v", err)
			}
			results[slot] = value
		}(index)
	}
	close(release)
	wg.Wait()

	if loads != 1 {
		t.Fatalf("loads = %d, want 1", loads)
	}
	for _, value := range results {
		if value != "第01话" {
			t.Fatalf("results = %#v", results)
		}
	}
}

func TestGetFailureKeepsStaleValueAndDoesNotStore(t *testing.T) {
	timeSource := newClock()
	c := cache.New[string](4, timeSource.Now)
	if _, _, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "第01话", nil
	}); err != nil {
		t.Fatal(err)
	}
	timeSource.Advance(2 * time.Hour)
	if _, _, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "", errors.New("upstream down")
	}); err == nil {
		t.Fatal("expired key with failing loader must return the error")
	}
	if _, state, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "第02话", nil
	}); err != nil || state != cache.Fresh {
		t.Fatalf("reload after failure = %v/%v", state, err)
	}
}

// 以下三条语义原先由 source.Cache 的测试守着，迁移到通用实现后必须继续守：
// 同一 key 只允许一次后台刷新、刷新不受调用方取消影响、刷新失败保留旧值。
func TestStaleReadStartsOnlyOneBackgroundRefresh(t *testing.T) {
	timeSource := newClock()
	c := cache.New[string](4, timeSource.Now)
	scheduled := 0
	c.WithBackground(func(func()) { scheduled++ })
	if _, _, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "第01话", nil
	}); err != nil {
		t.Fatal(err)
	}
	timeSource.Advance(2 * time.Minute)

	for range 3 {
		if _, state, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
			return "第02话", nil
		}); err != nil || state != cache.Stale {
			t.Fatalf("state = %v, err = %v", state, err)
		}
	}
	if scheduled != 1 {
		t.Fatalf("scheduled refreshes = %d, want 1", scheduled)
	}
}

func TestBackgroundRefreshIgnoresCancelledCallerContext(t *testing.T) {
	timeSource := newClock()
	c := cache.New[string](4, timeSource.Now)
	var pending func()
	c.WithBackground(func(fn func()) { pending = fn })
	if _, _, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "第01话", nil
	}); err != nil {
		t.Fatal(err)
	}
	timeSource.Advance(2 * time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	if _, _, err := c.Get(ctx, "key", time.Minute, time.Hour, func(loadCtx context.Context) (string, error) {
		return "第02话", loadCtx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	pending()

	value, state, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "never", nil
	})
	if err != nil || state != cache.Fresh || value != "第02话" {
		t.Fatalf("after refresh: %q/%v/%v", value, state, err)
	}
}

func TestFailedRefreshKeepsStaleValueUntilItExpires(t *testing.T) {
	timeSource := newClock()
	c := cache.New[string](4, timeSource.Now)
	var pending func()
	c.WithBackground(func(fn func()) { pending = fn })
	if _, _, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "第01话", nil
	}); err != nil {
		t.Fatal(err)
	}
	timeSource.Advance(2 * time.Minute)
	if _, state, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "", errors.New("upstream down")
	}); err != nil || state != cache.Stale {
		t.Fatalf("stale read = %v/%v", state, err)
	}
	pending()

	value, state, err := c.Get(context.Background(), "key", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "never", nil
	})
	if err != nil || state != cache.Stale || value != "第01话" {
		t.Fatalf("after failed refresh: %q/%v/%v", value, state, err)
	}
}

func TestGetEvictsLeastRecentlyUsedEntry(t *testing.T) {
	c := cache.New[string](2, nil)
	store := func(key, value string) {
		if _, _, err := c.Get(context.Background(), key, time.Minute, time.Hour, func(context.Context) (string, error) {
			return value, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	store("a", "A")
	store("b", "B")
	// Touch "a" so "b" becomes the least recently used entry.
	if _, _, err := c.Get(context.Background(), "a", time.Minute, time.Hour, func(context.Context) (string, error) {
		return "A", nil
	}); err != nil {
		t.Fatal(err)
	}
	store("c", "C")

	loads := 0
	if _, _, err := c.Get(context.Background(), "b", time.Minute, time.Hour, func(context.Context) (string, error) {
		loads++
		return "B", nil
	}); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("evicted key reloads = %d, want 1", loads)
	}
}
