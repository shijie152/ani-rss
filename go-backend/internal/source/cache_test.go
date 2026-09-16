package source

import (
	"testing"
	"time"
)

func TestCacheFreshStaleAndExpiredStates(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cache := newCacheWithClock(4, func() time.Time { return now })

	if value, err := cache.load("catalog", func() ([]byte, error) { return []byte("old"), nil }); err != nil || string(value) != "old" {
		t.Fatalf("initial cache load = %q, err=%v", value, err)
	}
	if value, state := cache.lookup("catalog", time.Hour, 3*time.Hour); state != cacheFresh || string(value) != "old" {
		t.Fatalf("fresh lookup = %q, state=%d", value, state)
	}

	now = now.Add(2 * time.Hour)
	if value, state := cache.lookup("catalog", time.Hour, 3*time.Hour); state != cacheStale || string(value) != "old" {
		t.Fatalf("stale lookup = %q, state=%d", value, state)
	}
	if started := cache.refresh("catalog", func() ([]byte, error) { return []byte("new"), nil }, func(fn func()) { fn() }); !started {
		t.Fatal("stale cache refresh did not start")
	}
	if value, state := cache.lookup("catalog", time.Hour, 3*time.Hour); state != cacheFresh || string(value) != "new" {
		t.Fatalf("refreshed lookup = %q, state=%d", value, state)
	}

	now = now.Add(4 * time.Hour)
	if value, state := cache.lookup("catalog", time.Hour, 3*time.Hour); state != cacheMiss || value != nil {
		t.Fatalf("expired lookup = %q, state=%d", value, state)
	}
}

func TestCacheCoalescesConcurrentLoads(t *testing.T) {
	cache := NewCache(4)
	owner, first := cache.beginCall("same")
	if !first {
		t.Fatal("failed to create the in-flight cache call")
	}
	waiter, second := cache.beginCall("same")
	if second || waiter != owner {
		t.Fatal("second cache call did not join the in-flight request")
	}
	result := make(chan struct {
		value string
		err   error
	})
	go func() {
		<-waiter.done
		value, err := waiter.value, waiter.err
		result <- struct {
			value string
			err   error
		}{string(value), err}
	}()
	// Only the owner runs the upstream loader. A joined caller observes its
	// result after the owner publishes it and never invokes another loader.
	cache.finishCall("same", owner, []byte("shared"), nil)
	loaded := <-result
	if loaded.value != "shared" || loaded.err != nil {
		t.Fatalf("coalesced value = %q, err=%v", loaded.value, loaded.err)
	}
}
