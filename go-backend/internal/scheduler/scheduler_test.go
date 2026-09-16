package scheduler_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/scheduler"
)

type fakeClock struct{ ready chan *fakeTicker }

type fakeOwner map[string]bool

func (o fakeOwner) Owner(domain string) (string, bool) {
	return "go", o[domain]
}

type fakeTicker struct {
	ch      chan time.Time
	stopped atomic.Bool
}

func (c *fakeClock) NewTicker(time.Duration) scheduler.Ticker {
	ticker := &fakeTicker{ch: make(chan time.Time, 8)}
	c.ready <- ticker
	return ticker
}
func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               { t.stopped.Store(true) }

func TestSchedulerRunsInitialAndPeriodicJobsWithoutOverlap(t *testing.T) {
	clock := &fakeClock{ready: make(chan *fakeTicker, 1)}
	var active, maxActive, runs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		scheduler.WithClock(clock, scheduler.Job{Interval: time.Minute, Initial: true, Run: func(context.Context) {
			current := active.Add(1)
			if current > maxActive.Load() {
				maxActive.Store(current)
			}
			runs.Add(1)
			active.Add(-1)
		}}).Run(ctx)
		close(done)
	}()
	deadline := time.After(time.Second)
	var ticker *fakeTicker
	select {
	case ticker = <-clock.ready:
	case <-deadline:
		t.Fatal("scheduler did not create ticker")
	}
	for runs.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	ticker.ch <- time.Now()
	ticker.ch <- time.Now()
	for runs.Load() < 3 {
		time.Sleep(time.Millisecond)
	}
	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent runs = %d", maxActive.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	if !ticker.stopped.Load() {
		t.Fatal("ticker was not stopped")
	}
}

func TestSchedulerRunsJobsIndependently(t *testing.T) {
	clock := &multiClock{ready: make(chan *multiTicker, 2)}
	var source, rss atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		scheduler.WithClock(clock,
			scheduler.Job{Interval: time.Minute, Initial: true, Run: func(context.Context) { source.Add(1) }},
			scheduler.Job{Interval: 2 * time.Minute, Initial: true, Run: func(context.Context) { rss.Add(1) }},
		).Run(ctx)
		close(done)
	}()
	deadline := time.After(time.Second)
	for source.Load() == 0 || rss.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("jobs did not run independently")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	first := <-clock.ready
	second := <-clock.ready
	intervals := map[time.Duration]bool{first.interval: true, second.interval: true}
	if !intervals[time.Minute] || !intervals[2*time.Minute] || len(intervals) != 2 {
		t.Fatalf("job intervals = %s and %s", first.interval, second.interval)
	}
	cancel()
	<-done
}

func TestOwnedSchedulerFiltersJobsByDomain(t *testing.T) {
	clock := &fakeClock{ready: make(chan *fakeTicker, 2)}
	var ownedRuns atomic.Int32
	var unownedRuns atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		scheduler.WithOwnedClock(clock, fakeOwner{"rss": true},
			scheduler.DomainJob{Domain: "rss", Job: scheduler.Job{Interval: time.Minute, Initial: true, Run: func(context.Context) { ownedRuns.Add(1) }}},
			scheduler.DomainJob{Domain: "sources", Job: scheduler.Job{Interval: time.Minute, Initial: true, Run: func(context.Context) { unownedRuns.Add(1) }}},
		).Run(ctx)
		close(done)
	}()
	select {
	case <-clock.ready:
	case <-time.After(time.Second):
		t.Fatal("owned scheduler did not create ticker")
	}
	deadline := time.After(time.Second)
	for ownedRuns.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("owned job did not run")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if unownedRuns.Load() != 0 {
		t.Fatalf("unowned job ran %d times", unownedRuns.Load())
	}
	select {
	case <-clock.ready:
		t.Fatal("unowned job created a ticker")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("owned scheduler did not stop")
	}
}

type multiClock struct {
	ready chan *multiTicker
}
type multiTicker struct {
	interval time.Duration
	ch       chan time.Time
}

func (c *multiClock) NewTicker(interval time.Duration) scheduler.Ticker {
	ticker := &multiTicker{interval: interval, ch: make(chan time.Time)}
	c.ready <- ticker
	return ticker
}
func (t *multiTicker) C() <-chan time.Time { return t.ch }
func (t *multiTicker) Stop()               {}
