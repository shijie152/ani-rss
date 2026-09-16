// Package scheduler owns background cadence and lifecycle. Jobs are isolated
// from one another and never overlap with themselves, while cancellation is
// propagated to the job callback.
package scheduler

import (
	"context"
	"sync"
	"time"
)

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type Clock interface {
	NewTicker(time.Duration) Ticker
}

type realClock struct{}

type realTicker struct{ ticker *time.Ticker }

func (realClock) NewTicker(interval time.Duration) Ticker {
	return realTicker{ticker: time.NewTicker(interval)}
}

func (t realTicker) C() <-chan time.Time { return t.ticker.C }
func (t realTicker) Stop()               { t.ticker.Stop() }

type Job struct {
	Interval time.Duration
	Initial  bool
	Run      func(context.Context)
}

type DomainOwner interface {
	Owner(string) (string, bool)
}

type DomainJob struct {
	Domain string
	Job    Job
}

type Scheduler struct {
	clock Clock
	jobs  []Job
}

func New(jobs ...Job) *Scheduler {
	return &Scheduler{clock: realClock{}, jobs: append([]Job(nil), jobs...)}
}

// NewOwned selects only jobs whose migration domain is owned by this process.
// Ownership is evaluated once when the scheduler starts; the lock manager is
// responsible for preventing another runtime from claiming the same domain.
func NewOwned(owner DomainOwner, jobs ...DomainJob) *Scheduler {
	return New(selectOwned(owner, jobs...)...)
}

// WithClock is intended for deterministic lifecycle tests and embedders that
// already own a clock. Production callers normally use New.
func WithClock(clock Clock, jobs ...Job) *Scheduler {
	if clock == nil {
		clock = realClock{}
	}
	return &Scheduler{clock: clock, jobs: append([]Job(nil), jobs...)}
}

func WithOwnedClock(clock Clock, owner DomainOwner, jobs ...DomainJob) *Scheduler {
	return WithClock(clock, selectOwned(owner, jobs...)...)
}

func selectOwned(owner DomainOwner, jobs ...DomainJob) []Job {
	selected := make([]Job, 0, len(jobs))
	if owner == nil {
		return selected
	}
	for _, domainJob := range jobs {
		if domainJob.Domain != "" {
			if _, owned := owner.Owner(domainJob.Domain); owned {
				selected = append(selected, domainJob.Job)
			}
		}
	}
	return selected
}

// Run blocks until every job has stopped. Each job owns one worker, so a slow
// RSS refresh cannot delay source prewarming and a tick cannot start a second
// invocation of the same job while the first is still running.
func (s *Scheduler) Run(ctx context.Context) {
	if s == nil {
		return
	}
	var workers sync.WaitGroup
	for _, job := range s.jobs {
		if job.Run == nil || job.Interval <= 0 {
			continue
		}
		job := job
		ticker := s.clock.NewTicker(job.Interval)
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer ticker.Stop()
			if job.Initial {
				select {
				case <-ctx.Done():
					return
				default:
				}
				job.Run(ctx)
			}
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C():
					job.Run(ctx)
				}
			}
		}()
	}
	workers.Wait()
}
