package main

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// cycleGuard
// ---------------------------------------------------------------------------

func TestCycleGuardRunsOffCallerGoroutine(t *testing.T) {
	var g cycleGuard
	ran := make(chan struct{})

	if !g.TryStart(func() { close(ran) }) {
		t.Fatal("TryStart on an idle guard returned false")
	}

	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("cycle function never ran")
	}

	if !g.Wait(context.Background(), time.Second) {
		t.Fatal("Wait did not observe the cycle finishing")
	}
	if g.Running() {
		t.Error("Running() is true after the cycle finished")
	}
}

func TestCycleGuardRejectsOverlap(t *testing.T) {
	var g cycleGuard
	release := make(chan struct{})
	started := make(chan struct{})

	if !g.TryStart(func() {
		close(started)
		<-release
	}) {
		t.Fatal("first TryStart returned false")
	}
	<-started

	if !g.Running() {
		t.Error("Running() is false while a cycle is in flight")
	}

	secondRan := false
	if g.TryStart(func() { secondRan = true }) {
		t.Error("overlapping TryStart returned true, expected the tick to be skipped")
	}
	if secondRan {
		t.Error("overlapping TryStart ran the cycle function")
	}

	// The guard must not stay latched once the first cycle drains.
	close(release)
	if !g.Wait(context.Background(), time.Second) {
		t.Fatal("Wait timed out after the cycle was released")
	}
	if !g.TryStart(func() {}) {
		t.Error("TryStart returned false after the previous cycle finished")
	}
	if !g.Wait(context.Background(), time.Second) {
		t.Fatal("Wait timed out on the follow-up cycle")
	}
}

func TestCycleGuardWaitTimesOut(t *testing.T) {
	var g cycleGuard
	release := make(chan struct{})
	started := make(chan struct{})

	g.TryStart(func() {
		close(started)
		<-release
	})
	<-started

	if g.Wait(context.Background(), 20*time.Millisecond) {
		t.Error("Wait returned true while the cycle was still in flight")
	}

	close(release)
	if !g.Wait(context.Background(), time.Second) {
		t.Fatal("Wait timed out after release")
	}
}

func TestCycleGuardWaitOnIdleGuard(t *testing.T) {
	var g cycleGuard
	if !g.Wait(context.Background(), 0) {
		t.Error("Wait on an idle guard returned false")
	}
}

// ---------------------------------------------------------------------------
// Shutdown ordering
// ---------------------------------------------------------------------------

func recordingHooks(order *[]string, waitResult bool) shutdownHooks {
	add := func(name string) { *order = append(*order, name) }
	return shutdownHooks{
		Cancel:       func() { add("cancel") },
		WaitFlusher:  func(time.Duration) bool { add("flusher"); return waitResult },
		WaitConsumer: func(time.Duration) bool { add("consumer"); return waitResult },
		WaitCycle:    func(time.Duration) bool { add("cycle"); return waitResult },
		WaitJob:      func(time.Duration) bool { add("job"); return waitResult },
		Snapshot:     func() { add("snapshot") },
		StopStatsAPI: func() { add("statsapi") },
		CloseStore:   func() error { add("close"); return nil },
	}
}

func TestRunShutdownOrder(t *testing.T) {
	var order []string
	runShutdown(recordingHooks(&order, true))

	want := []string{"cancel", "flusher", "consumer", "cycle", "job", "snapshot", "statsapi", "close"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("shutdown order = %v, want %v", order, want)
	}
}

func TestRunShutdownClosesStoreEvenWhenWaitsTimeOut(t *testing.T) {
	var order []string
	runShutdown(recordingHooks(&order, false))

	if len(order) == 0 || order[len(order)-1] != "close" {
		t.Errorf("store close is not the last step: %v", order)
	}
}

func TestRunShutdownReportsCloseError(t *testing.T) {
	// A failing close must not abort the sequence or panic.
	runShutdown(shutdownHooks{
		Cancel:       func() {},
		WaitFlusher:  func(time.Duration) bool { return true },
		WaitConsumer: func(time.Duration) bool { return true },
		WaitCycle:    func(time.Duration) bool { return true },
		WaitJob:      func(time.Duration) bool { return true },
		Snapshot:     func() {},
		StopStatsAPI: func() {},
		CloseStore:   func() error { return errors.New("boom") },
	})
}

// TestRunShutdownDrainsBeforeStoreClose reproduces the deploy-time failure the
// ordering exists to prevent: the flusher's drain and the consumer's cursor
// write must both land while the store is still open, or they fail with
// "database is closed" and the buffered posts are lost.
func TestRunShutdownDrainsBeforeStoreClose(t *testing.T) {
	var (
		mu          sync.Mutex
		storeClosed bool
		lateWrites  []string
	)

	// writeToStore models a store write that fails once Close has run.
	writeToStore := func(who string) {
		mu.Lock()
		defer mu.Unlock()
		if storeClosed {
			lateWrites = append(lateWrites, who)
		}
	}

	cancelled := make(chan struct{})

	flusherDone := make(chan struct{})
	go func() {
		defer close(flusherDone)
		<-cancelled
		time.Sleep(20 * time.Millisecond) // drain takes a moment
		writeToStore("flusher drain")
	}()

	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		<-cancelled
		time.Sleep(30 * time.Millisecond) // cursor persist takes a moment
		writeToStore("cursor persist")
	}()

	runShutdown(shutdownHooks{
		Cancel:       func() { close(cancelled) },
		WaitFlusher:  func(d time.Duration) bool { return waitClosed(flusherDone, d) },
		WaitConsumer: func(d time.Duration) bool { return waitClosed(consumerDone, d) },
		WaitCycle:    func(time.Duration) bool { return true },
		WaitJob:      func(time.Duration) bool { return true },
		Snapshot:     func() {},
		StopStatsAPI: func() {},
		CloseStore: func() error {
			mu.Lock()
			storeClosed = true
			mu.Unlock()
			return nil
		},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(lateWrites) > 0 {
		t.Errorf("writes reached a closed store: %v", lateWrites)
	}
	if !storeClosed {
		t.Error("store was never closed")
	}
}

func TestShutdownBudgetsFitKillTimeout(t *testing.T) {
	// flyKillTimeout mirrors fly.prod.toml / fly.staging.toml; see its
	// declaration in scheduling.go.
	if shutdownBudget >= flyKillTimeout {
		t.Errorf("shutdownBudget %v leaves no margin under kill_timeout %v", shutdownBudget, flyKillTimeout)
	}

	steps := shutdownFlusherBudget + shutdownConsumerBudget + shutdownCycleBudget +
		shutdownJobBudget + shutdownSnapshotBudget + shutdownStatsAPIBudget
	if steps > shutdownBudget {
		t.Errorf("shutdown step budgets total %v, over the %v overall budget", steps, shutdownBudget)
	}

	if drainBudget >= shutdownFlusherBudget {
		t.Errorf("drainBudget %v must be under shutdownFlusherBudget %v so the flusher returns before the wait expires",
			drainBudget, shutdownFlusherBudget)
	}
}

func TestWaitClosed(t *testing.T) {
	open := make(chan struct{})
	if waitClosed(open, 0) {
		t.Error("waitClosed on an open channel with no budget returned true")
	}
	if waitClosed(open, 10*time.Millisecond) {
		t.Error("waitClosed on an open channel returned true")
	}

	closed := make(chan struct{})
	close(closed)
	if !waitClosed(closed, 0) {
		t.Error("waitClosed on a closed channel with no budget returned false")
	}
	if !waitClosed(closed, time.Second) {
		t.Error("waitClosed on a closed channel returned false")
	}
}

// ---------------------------------------------------------------------------
// Backpressure and drop accounting
// ---------------------------------------------------------------------------

func TestSendPostAcceptsWhenBufferHasRoom(t *testing.T) {
	ch := make(chan store.PendingWrite, 1)
	if !sendPost(context.Background(), ch, store.PendingWrite{}, time.Second) {
		t.Fatal("sendPost returned false with a free buffer slot")
	}
	if len(ch) != 1 {
		t.Errorf("buffer length = %d, want 1", len(ch))
	}
}

func TestSendPostBlocksThenSucceedsWhenReaderCatchesUp(t *testing.T) {
	ch := make(chan store.PendingWrite, 1)
	ch <- store.PendingWrite{} // buffer full

	go func() {
		time.Sleep(20 * time.Millisecond)
		<-ch
	}()

	if !sendPost(context.Background(), ch, store.PendingWrite{Post: store.Post{URI: "at://late"}}, time.Second) {
		t.Fatal("sendPost dropped a post the reader made room for")
	}
}

func TestSendPostDropsOnlyAfterTimeout(t *testing.T) {
	ch := make(chan store.PendingWrite, 1)
	ch <- store.PendingWrite{} // buffer full, nobody reading

	start := time.Now()
	if sendPost(context.Background(), ch, store.PendingWrite{}, 50*time.Millisecond) {
		t.Fatal("sendPost accepted a post into a permanently full buffer")
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("sendPost gave up after %v, expected it to block for the timeout", elapsed)
	}
}

func TestSendPostGivesUpOnContextCancel(t *testing.T) {
	ch := make(chan store.PendingWrite, 1)
	ch <- store.PendingWrite{} // buffer full

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if sendPost(ctx, ch, store.PendingWrite{}, 10*time.Second) {
		t.Fatal("sendPost accepted a post after ctx was cancelled")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("sendPost took %v to observe cancellation", elapsed)
	}
}

func TestDropLimiterCollapsesBurstIntoOneWarning(t *testing.T) {
	d := &dropLimiter{window: 5 * time.Second}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if n := d.record(base); n != 1 {
		t.Errorf("first drop reported %d, want 1", n)
	}

	// 999 more drops inside the window must stay silent.
	for i := 1; i < 1000; i++ {
		if n := d.record(base.Add(time.Duration(i) * time.Millisecond)); n != 0 {
			t.Fatalf("drop %d reported %d, want 0 (suppressed)", i, n)
		}
	}

	// The next window carries the whole suppressed count.
	if n := d.record(base.Add(6 * time.Second)); n != 1000 {
		t.Errorf("post-window drop reported %d, want 1000", n)
	}

	// Counter resets after reporting.
	if n := d.record(base.Add(6*time.Second + time.Millisecond)); n != 0 {
		t.Errorf("drop right after a warning reported %d, want 0", n)
	}
}

func TestDropLimiterIsConcurrencySafe(t *testing.T) {
	d := &dropLimiter{window: time.Hour} // suppress everything after the first
	var wg sync.WaitGroup
	var mu sync.Mutex
	reported := 0

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if n := d.record(time.Now()); n > 0 {
				mu.Lock()
				reported += n
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if reported != 1 {
		t.Errorf("reported %d drops across the window, want exactly 1", reported)
	}
}

// ---------------------------------------------------------------------------
// consumerHandle
// ---------------------------------------------------------------------------

func TestConsumerHandleForceReconnectWithoutConsumer(t *testing.T) {
	h := &consumerHandle{}
	if h.forceReconnect() {
		t.Error("forceReconnect reported success with no consumer set")
	}

	h.set(nil)
	if h.forceReconnect() {
		t.Error("forceReconnect reported success after the consumer was cleared")
	}
}
