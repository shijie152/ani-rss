package metadata

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheDeduplicatesConcurrentLoadsAndReturnsIndependentValues(t *testing.T) {
	cache := NewCache(4)
	var loads atomic.Int32
	start := make(chan struct{})
	release := make(chan struct{})
	loader := func() (map[string]any, error) {
		loads.Add(1)
		close(start)
		<-release
		return map[string]any{"value": "cached"}, nil
	}
	results := make([]map[string]any, 2)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], _ = cache.JSON("https://metadata.test/v0/subjects/1", loader)
		}(index)
	}
	<-start
	close(release)
	wg.Wait()
	if loads.Load() != 1 || results[0]["value"] != "cached" || results[1]["value"] != "cached" {
		t.Fatalf("loads=%d results=%#v", loads.Load(), results)
	}
	results[0]["value"] = "mutated"
	value, err := cache.JSON("https://metadata.test/v0/subjects/1", func() (map[string]any, error) { t.Fatal("fresh cache missed"); return nil, nil })
	if err != nil || value["value"] != "cached" {
		t.Fatalf("cached value was mutable: value=%#v err=%v", value, err)
	}
}

func TestCacheServesStaleWhileRefreshFailureKeepsPreviousValue(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewCache(4)
	cache.now = func() time.Time { return now }
	if _, err := cache.JSON("https://metadata.test/v0/subjects/2", func() (map[string]any, error) {
		return map[string]any{"value": "original"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * time.Hour)
	refreshDone := make(chan struct{})
	value, err := cache.JSON("https://metadata.test/v0/subjects/2", func() (map[string]any, error) {
		defer close(refreshDone)
		return nil, errors.New("upstream unavailable")
	})
	if err != nil || value["value"] != "original" {
		t.Fatalf("stale value=%#v err=%v", value, err)
	}
	select {
	case <-refreshDone:
	case <-time.After(time.Second):
		t.Fatal("stale refresh did not run")
	}
	value, err = cache.JSON("https://metadata.test/v0/subjects/2", func() (map[string]any, error) { return nil, errors.New("still unavailable") })
	if err != nil || value["value"] != "original" {
		t.Fatalf("stale value after failure=%#v err=%v", value, err)
	}
}

func TestCacheStaleRefreshPublishesOnlyAfterBackgroundWorkFinishes(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewCache(4)
	cache.now = func() time.Time { return now }
	var scheduled func()
	cache.WithBackground(func(fn func()) { scheduled = fn })
	if _, err := cache.JSON("https://metadata.test/v0/subjects/3", func() (map[string]any, error) {
		return map[string]any{"value": "original"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * time.Hour)
	var loads int
	loader := func() (map[string]any, error) {
		loads++
		return map[string]any{"value": "refreshed"}, nil
	}
	value, err := cache.JSON("https://metadata.test/v0/subjects/3", loader)
	if err != nil || value["value"] != "original" {
		t.Fatalf("first stale read = %#v, err=%v", value, err)
	}
	value, err = cache.JSON("https://metadata.test/v0/subjects/3", loader)
	if err != nil || value["value"] != "original" {
		t.Fatalf("second stale read = %#v, err=%v", value, err)
	}
	if scheduled == nil || loads != 0 {
		t.Fatalf("refresh scheduling = %v, loads=%d; refresh should still be pending", scheduled != nil, loads)
	}
	scheduled()
	if loads != 1 {
		t.Fatalf("background refresh loads = %d, want 1", loads)
	}
	value, err = cache.JSON("https://metadata.test/v0/subjects/3", func() (map[string]any, error) {
		t.Fatal("fresh cache missed after successful refresh")
		return nil, nil
	})
	if err != nil || value["value"] != "refreshed" {
		t.Fatalf("refreshed read = %#v, err=%v", value, err)
	}
}

func TestCacheRefreshUsesContextWithoutCallerCancellation(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewCache(4)
	cache.now = func() time.Time { return now }
	var scheduled func()
	cache.WithBackground(func(fn func()) { scheduled = fn })
	if _, err := cache.JSON("https://metadata.test/v0/subjects/4", func() (map[string]any, error) {
		return map[string]any{"value": "original"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	var refreshContext context.Context
	value, err := cache.JSONContext(ctx, "https://metadata.test/v0/subjects/4", func(loadCtx context.Context) (map[string]any, error) {
		refreshContext = loadCtx
		return map[string]any{"value": "refreshed"}, nil
	})
	if err != nil || value["value"] != "original" {
		t.Fatalf("stale read = %#v, err=%v", value, err)
	}
	cancel()
	scheduled()
	if refreshContext == nil {
		t.Fatal("background refresh did not receive a context")
	}
	if err := refreshContext.Err(); err != nil {
		t.Fatalf("background refresh context was cancelled: %v", err)
	}
	value, err = cache.JSON("https://metadata.test/v0/subjects/4", func() (map[string]any, error) {
		return map[string]any{"value": "refreshed"}, nil
	})
	if err != nil || value["value"] != "refreshed" {
		t.Fatalf("refreshed read = %#v, err=%v", value, err)
	}
}

func TestCachePolicyUsesFreshAndStaleBoundaries(t *testing.T) {
	if fresh, stale := policy("https://metadata.test/search/subject/demo"); fresh != 15*time.Minute || stale != 24*time.Hour {
		t.Fatalf("search policy = %s/%s", fresh, stale)
	}
	if fresh, stale := policy("https://metadata.test/v0/subjects/42"); fresh != 6*time.Hour || stale != 7*24*time.Hour {
		t.Fatalf("subject policy = %s/%s", fresh, stale)
	}
	if fresh, stale := policy("https://metadata.test/v0/episodes?subject_id=42"); fresh != time.Hour || stale != 24*time.Hour {
		t.Fatalf("episode policy = %s/%s", fresh, stale)
	}
}
