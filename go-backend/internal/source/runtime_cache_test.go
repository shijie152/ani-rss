package source

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimeCachedJSONServesStaleAndRefreshesInBackground(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cache := newCacheWithClock(4, func() time.Time { return now })
	var scheduled func()
	runtime := newRuntime(Options{
		Cache:      cache,
		Context:    context.Background(),
		Background: func(fn func()) { scheduled = fn },
	})
	loads := 0
	load := func(context.Context) (any, error) {
		loads++
		return map[string]any{"value": loads}, nil
	}
	value, err := runtime.cachedJSON("catalog", time.Hour, 3*time.Hour, load)
	if err != nil || value.(map[string]any)["value"] != float64(1) {
		t.Fatalf("initial value = %#v, err=%v", value, err)
	}
	now = now.Add(2 * time.Hour)
	value, err = runtime.cachedJSON("catalog", time.Hour, 3*time.Hour, load)
	if err != nil || value.(map[string]any)["value"] != float64(1) {
		t.Fatalf("stale value = %#v, err=%v", value, err)
	}
	if scheduled == nil || loads != 1 {
		t.Fatalf("scheduled=%v loads=%d; stale read must defer refresh", scheduled != nil, loads)
	}
	scheduled()
	if loads != 2 {
		t.Fatalf("refresh loads = %d, want 2", loads)
	}
	value, err = runtime.cachedJSON("catalog", time.Hour, 3*time.Hour, load)
	if err != nil || value.(map[string]any)["value"] != float64(2) {
		t.Fatalf("refreshed value = %#v, err=%v", value, err)
	}
}

func TestRuntimeCachedJSONKeepsOldValueWhenRefreshFails(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cache := newCacheWithClock(4, func() time.Time { return now })
	var scheduled func()
	runtime := newRuntime(Options{Cache: cache, Background: func(fn func()) { scheduled = fn }})
	if _, err := runtime.cachedJSON("catalog", time.Hour, 3*time.Hour, func(context.Context) (any, error) {
		return map[string]any{"value": "old"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	value, err := runtime.cachedJSON("catalog", time.Hour, 3*time.Hour, func(context.Context) (any, error) {
		return nil, errors.New("upstream unavailable")
	})
	if err != nil || value.(map[string]any)["value"] != "old" {
		t.Fatalf("stale value = %#v, err=%v", value, err)
	}
	scheduled()
	value, err = runtime.cachedJSON("catalog", time.Hour, 3*time.Hour, func(context.Context) (any, error) {
		return nil, errors.New("still unavailable")
	})
	if err != nil || value.(map[string]any)["value"] != "old" {
		t.Fatalf("value after failed refresh = %#v, err=%v", value, err)
	}
}
