package main

import (
	"context"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Background jobs (daily, yearly) must not park the scheduler loop
// ---------------------------------------------------------------------------

// TestSchedulerStaysResponsiveDuringDailyWait models the scheduler loop with an
// analysis cycle that never finishes: the daily tick arrives, parks waiting for
// that cycle, and a SIGTERM must still be served promptly. Waiting inline in the
// select blocked the loop for up to jobCycleWait, so Fly SIGKILLed the process
// at kill_timeout before the drain ever ran.
func TestSchedulerStaysResponsiveDuringDailyWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cycles := &cycleGuard{}
	jobs := &cycleGuard{}

	// An analysis cycle that never finishes on its own.
	cycleRelease := make(chan struct{})
	defer close(cycleRelease)
	cycleStarted := make(chan struct{})
	cycles.TryStart(func() {
		close(cycleStarted)
		<-cycleRelease
	})
	<-cycleStarted

	var dailyRan atomic.Bool
	var rejected atomic.Bool
	backupCh := make(chan time.Time, 1)
	sigCh := make(chan os.Signal, 1)
	shutdownReached := make(chan struct{})
	loopDone := make(chan struct{})

	go func() {
		defer close(loopDone)
		for {
			select {
			case <-backupCh:
				if !startAfterCycle(ctx, jobs, cycles, "daily", jobCycleWait, func() {
					dailyRan.Store(true)
				}) {
					rejected.Store(true)
				}
			case <-sigCh:
				close(shutdownReached)
				return
			}
		}
	}()

	backupCh <- time.Now()

	// Let the daily job reach its wait on the in-flight cycle.
	deadline := time.Now().Add(time.Second)
	for !jobs.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !jobs.Running() {
		t.Fatal("daily job never started")
	}
	if rejected.Load() {
		t.Fatal("daily job was unexpectedly rejected")
	}

	start := time.Now()
	sigCh <- syscall.SIGTERM
	select {
	case <-shutdownReached:
	case <-time.After(time.Second):
		t.Fatal("scheduler loop did not serve SIGTERM while a daily job was waiting on a cycle")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("SIGTERM took %v to be served, want well under 1s", elapsed)
	}
	<-loopDone

	// The parked job must also release on cancellation rather than holding
	// shutdown for the full jobCycleWait.
	cancel()
	if !jobs.Wait(context.Background(), 2*time.Second) {
		t.Error("daily job did not exit after ctx cancellation")
	}
	if dailyRan.Load() {
		t.Error("daily body ran even though shutdown was already under way")
	}
}

func TestStartAfterCycleRunsBodyOnceCycleFinishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cycles := &cycleGuard{}
	jobs := &cycleGuard{}

	cycleRelease := make(chan struct{})
	cycleStarted := make(chan struct{})
	cycles.TryStart(func() {
		close(cycleStarted)
		<-cycleRelease
	})
	<-cycleStarted

	bodyRan := make(chan struct{})
	if !startAfterCycle(ctx, jobs, cycles, "daily", time.Minute, func() { close(bodyRan) }) {
		t.Fatal("startAfterCycle returned false on an idle job guard")
	}

	select {
	case <-bodyRan:
		t.Fatal("job body ran while the analysis cycle was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(cycleRelease)
	select {
	case <-bodyRan:
	case <-time.After(2 * time.Second):
		t.Fatal("job body never ran after the cycle finished")
	}
}

func TestStartAfterCycleRunsImmediatelyWithoutACycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobs := &cycleGuard{}
	bodyRan := make(chan struct{})

	if !startAfterCycle(ctx, jobs, &cycleGuard{}, "yearly", time.Minute, func() { close(bodyRan) }) {
		t.Fatal("startAfterCycle returned false on an idle job guard")
	}
	select {
	case <-bodyRan:
	case <-time.After(2 * time.Second):
		t.Fatal("job body never ran with no cycle in flight")
	}
}

// TestStartAfterCycleRejectsOverlappingJobs covers the yearly-beside-daily
// case: both render charts, and stacking their peaks is what pushes the 1GB VM
// towards an OOM.
func TestStartAfterCycleRejectsOverlappingJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobs := &cycleGuard{}
	idle := &cycleGuard{}

	release := make(chan struct{})
	defer close(release)
	firstStarted := make(chan struct{})

	if !startAfterCycle(ctx, jobs, idle, "daily", time.Minute, func() {
		close(firstStarted)
		<-release
	}) {
		t.Fatal("first job was rejected")
	}
	<-firstStarted

	var secondRan atomic.Bool
	if startAfterCycle(ctx, jobs, idle, "yearly", time.Minute, func() { secondRan.Store(true) }) {
		t.Error("overlapping job returned true, expected the tick to be skipped")
	}
	if secondRan.Load() {
		t.Error("overlapping job ran its body")
	}
}

func TestStartAfterCycleSkipsBodyWhenAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	jobs := &cycleGuard{}
	var ran atomic.Bool

	if !startAfterCycle(ctx, jobs, &cycleGuard{}, "daily", time.Minute, func() { ran.Store(true) }) {
		t.Fatal("startAfterCycle returned false on an idle job guard")
	}
	if !jobs.Wait(context.Background(), 2*time.Second) {
		t.Fatal("job goroutine did not exit")
	}
	if ran.Load() {
		t.Error("job body ran despite ctx already being cancelled")
	}
}

func TestCycleGuardWaitHonoursContextCancellation(t *testing.T) {
	var g cycleGuard
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})

	g.TryStart(func() {
		close(started)
		<-release
	})
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if g.Wait(ctx, time.Hour) {
		t.Error("Wait returned true for a cycle that never finished")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Wait took %v to observe cancellation, want immediate", elapsed)
	}
}
