package metadata

import (
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
