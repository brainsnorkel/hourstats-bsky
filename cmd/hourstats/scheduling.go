package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Analysis cycle re-entrancy guard
// ---------------------------------------------------------------------------

// cycleGuard runs at most one analysis cycle at a time off the main scheduler
// loop, and lets other schedulers (the daily branch, shutdown) wait for an
// in-flight cycle. Running cycles in the main select blocked the WAL
// checkpoint, stats snapshot and stall-detection tickers for the 5-18 minutes
// a cycle takes.
type cycleGuard struct {
	mu   sync.Mutex
	done chan struct{} // non-nil while a cycle is in flight
}

// TryStart runs fn in its own goroutine and reports true. It reports false
// without running fn when a cycle is already in flight.
func (g *cycleGuard) TryStart(fn func()) bool {
	g.mu.Lock()
	if g.done != nil {
		g.mu.Unlock()
		return false
	}
	done := make(chan struct{})
	g.done = done
	g.mu.Unlock()

	go func() {
		defer func() {
			g.mu.Lock()
			g.done = nil
			g.mu.Unlock()
			close(done)
		}()
		fn()
	}()
	return true
}

// Running reports whether a cycle is currently in flight.
func (g *cycleGuard) Running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.done != nil
}

// Wait blocks until the cycle in flight when Wait was called has finished, or
// until timeout elapses, or until ctx is cancelled. It reports true only when
// no cycle is left in flight. Honouring ctx matters because a waiter parked on
// a cycle that will not finish must still release promptly at shutdown.
func (g *cycleGuard) Wait(ctx context.Context, timeout time.Duration) bool {
	g.mu.Lock()
	done := g.done
	g.mu.Unlock()
	if done == nil {
		return true
	}
	if timeout <= 0 {
		return false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// startAfterCycle runs body off the caller's goroutine once any in-flight
// analysis cycle has finished, reporting false when another background job is
// already running. The caller must not block: the scheduler loop has to stay
// responsive to SIGTERM and to the WAL, stats and stall tickers, so both the
// wait and the work happen inside the spawned goroutine.
func startAfterCycle(ctx context.Context, jobs, cycles *cycleGuard, name string, maxWait time.Duration, body func()) bool {
	return jobs.TryStart(func() {
		if cycles.Running() {
			waitStart := time.Now()
			slog.Info("background job waiting for in-flight analysis cycle",
				"job", name, "max_wait", maxWait)
			finished := cycles.Wait(ctx, maxWait)
			waited := time.Since(waitStart).Round(time.Second)
			switch {
			case ctx.Err() != nil:
				slog.Warn("background job abandoned during shutdown", "job", name, "waited", waited)
				return
			case !finished:
				slog.Error("background job wait timed out, running without the final analysis cycle",
					"job", name, "waited", waited)
			default:
				slog.Info("background job resumed", "job", name, "waited", waited)
			}
		}
		if ctx.Err() != nil {
			slog.Warn("background job skipped during shutdown", "job", name)
			return
		}
		body()
	})
}

// waitClosed reports whether ch was closed within timeout.
func waitClosed(ch <-chan struct{}, timeout time.Duration) bool {
	if timeout <= 0 {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

// ---------------------------------------------------------------------------
// Shutdown sequencing
// ---------------------------------------------------------------------------

const (
	// flyKillTimeout mirrors `kill_timeout` in fly.prod.toml and
	// fly.staging.toml. Fly sends SIGKILL after this, so every shutdown step
	// has to finish inside it. Keep in sync if those files change.
	flyKillTimeout = 15 * time.Second

	// shutdownBudget is flyKillTimeout minus a margin. Overrunning it means
	// SIGKILL, which skips every step below.
	shutdownBudget = 12 * time.Second

	// The step budgets must sum to no more than shutdownBudget; see
	// TestShutdownBudgetsFitKillTimeout.
	shutdownFlusherBudget  = 5 * time.Second
	shutdownConsumerBudget = 2 * time.Second
	shutdownCycleBudget    = 2 * time.Second
	shutdownJobBudget      = 1 * time.Second
	shutdownSnapshotBudget = 1 * time.Second
	shutdownStatsAPIBudget = 1 * time.Second

	// jobCycleWait bounds how long a background job (daily, yearly) waits for
	// an in-flight analysis cycle before running without it.
	jobCycleWait = 15 * time.Minute
)

// shutdownHooks are the shutdown steps, injected so the ordering can be tested
// without a real store, consumer or flusher.
type shutdownHooks struct {
	Cancel       func()                   // stop producers and in-flight work
	WaitFlusher  func(time.Duration) bool // drain buffered writes into the open store
	WaitConsumer func(time.Duration) bool // let the consumer persist its cursor
	WaitCycle    func(time.Duration) bool // let an in-flight analysis cycle bail out
	WaitJob      func(time.Duration) bool // let an in-flight daily/yearly job bail out
	Snapshot     func()                   // final stats snapshot
	StopStatsAPI func()                   // stop serving /stats
	CloseStore   func() error             // MUST be last
}

// runShutdown executes the shutdown steps in the only safe order: cancel
// producers, then let the write flusher drain and the consumer persist its
// cursor while the store is still open, then give an in-flight analysis cycle a
// bounded moment to observe cancellation, and only then close the store.
// Closing the store first is what produced "database is closed" on every
// rolling deploy, losing the buffered posts and the cursor.
func runShutdown(h shutdownHooks) {
	start := time.Now()
	deadline := start.Add(shutdownBudget)

	// remaining caps a step's budget by what is left of the overall budget, so
	// a slow early step cannot push the total past kill_timeout.
	remaining := func(step time.Duration) time.Duration {
		left := time.Until(deadline)
		if left <= 0 {
			return 0
		}
		if step < left {
			return step
		}
		return left
	}

	h.Cancel()

	if !h.WaitFlusher(remaining(shutdownFlusherBudget)) {
		slog.Warn("shutdown: write flusher did not finish draining in time, buffered posts may be lost")
	}
	if !h.WaitConsumer(remaining(shutdownConsumerBudget)) {
		slog.Warn("shutdown: jetstream consumer did not exit in time, cursor may be stale")
	}
	if !h.WaitCycle(remaining(shutdownCycleBudget)) {
		slog.Warn("shutdown: analysis cycle still in flight, closing store anyway")
	}
	// A daily/yearly job parked on the cycle wait releases as soon as Cancel
	// runs, so this normally returns immediately.
	if !h.WaitJob(remaining(shutdownJobBudget)) {
		slog.Warn("shutdown: background job still in flight, closing store anyway")
	}

	h.Snapshot()
	h.StopStatsAPI()

	if err := h.CloseStore(); err != nil {
		slog.Warn("shutdown: store close failed", "error", err)
	}
	slog.Info("shutdown complete", "elapsed", time.Since(start).Round(time.Millisecond))
}

// newWallClockTicker returns a channel that fires at wall-clock aligned UTC
// boundaries. For example, a 30m interval fires at :00 and :30 past the hour;
// a 3h interval fires at 00:00, 03:00, 06:00, etc. This ensures deploys and
// restarts don't shift the posting schedule.
func newDailyTickerAtHour(hour int) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
			if !next.After(now) {
				next = next.AddDate(0, 0, 1)
			}
			delay := next.Sub(now)
			slog.Info("daily ticker scheduled",
				"hour_utc", hour,
				"next_fire", next.Format(time.RFC3339),
				"delay", delay.Round(time.Second),
			)
			timer := time.NewTimer(delay)
			<-timer.C
			ch <- time.Now()
		}
	}()
	return ch
}

func newWallClockTicker(interval, offset time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		for {
			now := time.Now().UTC()
			next := now.Truncate(interval).Add(offset)
			if !next.After(now) {
				next = next.Add(interval)
			}
			delay := next.Sub(now)
			slog.Info("wall-clock ticker scheduled",
				"interval", interval,
				"offset", offset,
				"next_fire", next.Format(time.RFC3339),
				"delay", delay.Round(time.Second),
			)
			timer := time.NewTimer(delay)
			<-timer.C
			ch <- time.Now()
		}
	}()
	return ch
}
