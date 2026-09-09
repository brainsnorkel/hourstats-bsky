package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/procmem"
)

// memTimelineCheckpoints are the elapsed marks at which the sampler records a
// compact timeline entry. The first sample at or after a checkpoint is kept.
var memTimelineCheckpoints = []time.Duration{
	1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second,
	20 * time.Second, 30 * time.Second, 45 * time.Second, 60 * time.Second,
	90 * time.Second, 120 * time.Second, 180 * time.Second, 300 * time.Second,
	420 * time.Second, 600 * time.Second, 900 * time.Second,
}

// memTimelineMax caps timeline entries so a long cycle cannot grow the slice.
const memTimelineMax = 20

// memStatsCheapWindow is how long a sampler prefers the cheap RSS read to a
// full runtime.ReadMemStats. ReadMemStats stops the world, and the OOM under
// investigation lands inside this window, so during it the full read is only
// paid for on samples that are reported (a tick line, a checkpoint, a guard
// crossing) while RSS — the number the kernel kills on — is read every time.
const memStatsCheapWindow = 300 * time.Second

// memCallbackDrain is how long stop() waits for in-flight guard and checkpoint
// callbacks. A trip is usually followed by the cycle unwinding, and a heap
// profile half-written is worth no more than none at all; a bounded wait keeps
// shutdown inside Fly's 15s kill_timeout regardless.
const memCallbackDrain = time.Second

// goSafe runs fn on its own goroutine, tracked by wg. A panic in evidence
// writing must not take the process down: the guard exists to keep it alive.
func goSafe(wg *sync.WaitGroup, what string, fn func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("memory sampler callback panicked",
					"callback", what,
					"panic", fmt.Sprint(r),
					"stack", string(debug.Stack()),
				)
			}
		}()
		fn()
	}()
}

// waitCallbacks gives in-flight callbacks up to d to finish, then gives up
// rather than hold the cycle open.
func waitCallbacks(wg *sync.WaitGroup, d time.Duration, label string) {
	drained := make(chan struct{})
	go func() {
		wg.Wait()
		close(drained)
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-drained:
	case <-timer.C:
		slog.Warn("memory sampler callbacks still running, abandoning them", "label", label, "waited", d)
	}
}

// memSamplerOptions carries the optional wiring of a sampler run. The zero
// value is the plain sampler: no guard, no checkpoint sink, real RSS.
type memSamplerOptions struct {
	// Guard, when non-nil, is checked against RSS on every sample.
	Guard *memGuard
	// OnCheckpoint receives one sample per timeline checkpoint, so a cycle
	// killed before it can report its peak still leaves a trace.
	OnCheckpoint func(memSample)
	// RSS overrides procmem.RSSBytes. Tests inject a sequence here; procfs
	// does not exist on a macOS dev box, where RSS is always 0.
	RSS func() int64
}

// memSchedule controls when the sampler emits a periodic tick line and when it
// records a timeline entry. It is passed in rather than read from package state
// so tests can drive a compressed schedule without mutating globals.
type memSchedule struct {
	earlyTick   time.Duration // tick cadence while elapsed < earlyWindow
	earlyWindow time.Duration // boundary between the two cadences
	lateTick    time.Duration // tick cadence at or after earlyWindow
	checkpoints []time.Duration
}

// defaultMemSchedule ticks every 5s through the first minute of a cycle — the
// OOM under investigation lands ~30s in — then backs off to every 30s.
var defaultMemSchedule = memSchedule{
	earlyTick:   5 * time.Second,
	earlyWindow: 60 * time.Second,
	lateTick:    30 * time.Second,
	checkpoints: memTimelineCheckpoints,
}

// tickDisabled parks the next tick deadline beyond any real elapsed time.
const tickDisabled = time.Duration(math.MaxInt64)

// advanceTick returns the next tick deadline strictly after elapsed, stepping
// at the early cadence below earlyWindow and the late cadence above it. A
// non-positive step disables further ticks rather than spinning.
func advanceTick(next, elapsed time.Duration, sched memSchedule) time.Duration {
	for next <= elapsed {
		step := sched.lateTick
		if next < sched.earlyWindow {
			step = sched.earlyTick
		}
		if step <= 0 {
			return tickDisabled
		}
		next += step
	}
	return next
}

// memPeak holds the high-water marks observed during one sampler run.
type memPeak struct {
	RSSPeakBytes         int64
	RSSPeakAt            time.Duration
	HeapInusePeakBytes   uint64
	HeapSysMaxBytes      uint64
	HeapReleasedMinBytes uint64
	StackInuseMaxBytes   uint64
	SysMaxBytes          uint64
	GoroutinesPeak       int
	Samples              int
	Timeline             []string
}

// eventDetails renders the peak as a single compact line for stats_events.
func (p memPeak) eventDetails(label string) string {
	return fmt.Sprintf("label=%s rss_peak=%.1fMB at=%.1fs heap_inuse_peak=%.1fMB heap_sys_max=%.1fMB stack_max=%.1fMB goroutines_peak=%d",
		label,
		bytesToMB(uint64(p.RSSPeakBytes)),
		p.RSSPeakAt.Seconds(),
		bytesToMB(p.HeapInusePeakBytes),
		bytesToMB(p.HeapSysMaxBytes),
		bytesToMB(p.StackInuseMaxBytes),
		p.GoroutinesPeak,
	)
}

// startMemSampler polls RSS and runtime memory stats every interval until the
// returned stop function is called or ctx is cancelled. stop is idempotent: it
// halts the sampler, takes a final sample, logs one summary line and returns
// the observed peaks.
func startMemSampler(ctx context.Context, interval time.Duration, label string) func() memPeak {
	return startMemSamplerWithSchedule(ctx, interval, label, defaultMemSchedule)
}

// startMemSamplerWithSchedule is startMemSampler with an explicit tick and
// checkpoint schedule, so tests can exercise the periodic path quickly.
func startMemSamplerWithSchedule(ctx context.Context, interval time.Duration, label string, sched memSchedule) func() memPeak {
	return startMemSamplerWithOptions(ctx, interval, label, sched, memSamplerOptions{})
}

// startMemSamplerWithOptions is startMemSamplerWithSchedule plus the memory
// guard and the checkpoint sink.
func startMemSamplerWithOptions(ctx context.Context, interval time.Duration, label string, sched memSchedule, opts memSamplerOptions) func() memPeak {
	start := time.Now()
	readRSS := opts.RSS
	if readRSS == nil {
		readRSS = procmem.RSSBytes
	}
	// Fire-once state for the guard. Only sample() touches it, and sample()
	// never runs concurrently with itself: the stop closure takes its final
	// sample after the sampler goroutine has returned.
	var guardWarned, guardTripped bool
	// Callbacks run off the sampling goroutine; stop() gives them a bounded
	// chance to finish so a shutdown does not truncate a heap profile.
	var callbacks sync.WaitGroup
	peak := memPeak{
		HeapReleasedMinBytes: math.MaxUint64,
		Timeline:             make([]string, 0, memTimelineMax),
	}

	var (
		mu        sync.Mutex
		nextCheck int
		nextTick  = tickDisabled
	)
	if sched.earlyTick > 0 {
		nextTick = sched.earlyTick
	}

	sample := func(final bool) {
		rss := readRSS()
		elapsed := time.Since(start)

		// The guard reads RSS only, so it is evaluated before anything that
		// stops the world: the crossing must be seen on the tick it happens.
		crossing := opts.Guard.crossed(rss, guardWarned, guardTripped)

		// Peek at the schedule to decide whether this sample will be reported;
		// an unreported sample inside the cheap window skips ReadMemStats.
		tickDue, checkDue := func() (bool, bool) {
			mu.Lock()
			defer mu.Unlock()
			return elapsed >= nextTick,
				nextCheck < len(sched.checkpoints) && elapsed >= sched.checkpoints[nextCheck]
		}()
		readStats := final || tickDue || checkDue || crossing != "" || elapsed >= memStatsCheapWindow

		var ms runtime.MemStats
		if readStats {
			runtime.ReadMemStats(&ms)
		}
		goroutines := runtime.NumGoroutine()

		if crossing != "" {
			guardWarned = true
			if crossing == memGuardTrip {
				guardTripped = true
			}
			// On its own goroutine: writing a heap profile must not stall the
			// sampling cadence at the moment the cadence matters most.
			s := memSample{
				Label:      label,
				Elapsed:    elapsed,
				RSSBytes:   rss,
				HeapInuse:  ms.HeapInuse,
				Goroutines: goroutines,
			}
			guard := opts.Guard
			goSafe(&callbacks, "memory guard "+crossing, func() { guard.fire(crossing, s) })
		}

		// Decide under the lock, log outside it.
		tick, crossed := func() (bool, bool) {
			mu.Lock()
			defer mu.Unlock()

			peak.Samples++
			if rss > peak.RSSPeakBytes {
				peak.RSSPeakBytes = rss
				peak.RSSPeakAt = elapsed
			}
			// ms is only populated on samples that read it; an unread ms is
			// all zeroes, which would drag HeapReleasedMinBytes to 0.
			if readStats {
				if ms.HeapInuse > peak.HeapInusePeakBytes {
					peak.HeapInusePeakBytes = ms.HeapInuse
				}
				if ms.HeapSys > peak.HeapSysMaxBytes {
					peak.HeapSysMaxBytes = ms.HeapSys
				}
				if ms.HeapReleased < peak.HeapReleasedMinBytes {
					peak.HeapReleasedMinBytes = ms.HeapReleased
				}
				if ms.StackInuse > peak.StackInuseMaxBytes {
					peak.StackInuseMaxBytes = ms.StackInuse
				}
				if ms.Sys > peak.SysMaxBytes {
					peak.SysMaxBytes = ms.Sys
				}
			}
			if goroutines > peak.GoroutinesPeak {
				peak.GoroutinesPeak = goroutines
			}

			// Record the first sample at or after each checkpoint, plus the final
			// sample so short cycles still produce a timeline.
			crossed := false
			for nextCheck < len(sched.checkpoints) && elapsed >= sched.checkpoints[nextCheck] {
				nextCheck++
				crossed = true
			}
			if (crossed || final) && len(peak.Timeline) < memTimelineMax {
				peak.Timeline = append(peak.Timeline, fmt.Sprintf("t=%ds rss=%dMB heap=%dMB hsys=%dMB stk=%dMB g=%d",
					int(elapsed.Round(time.Second)/time.Second),
					rss/(1024*1024),
					ms.HeapInuse/(1024*1024),
					ms.HeapSys/(1024*1024),
					ms.StackInuse/(1024*1024),
					goroutines,
				))
			}

			if final || elapsed < nextTick {
				return false, crossed
			}
			nextTick = advanceTick(nextTick, elapsed, sched)
			return true, crossed
		}()

		// The stop closure is the only other place memory is reported, and it
		// never runs when the kernel OOM-kills the process. Emitting as we go
		// means a SIGKILL still leaves the approach to the peak in the logs.
		if tick {
			slog.Info("cycle memory tick",
				"label", label,
				"t_s", math.Round(elapsed.Seconds()*10)/10,
				"rss_mb", bytesToMB(uint64(rss)),
				"heap_inuse_mb", bytesToMB(ms.HeapInuse),
				"heap_sys_mb", bytesToMB(ms.HeapSys),
				"goroutines", goroutines,
			)
		}

		// Logs are lost with the machine; the checkpoint rows are not. Written
		// off the sampling goroutine for the same reason the guard is.
		if crossed && opts.OnCheckpoint != nil {
			s := memSample{
				Label:      label,
				Elapsed:    elapsed,
				RSSBytes:   rss,
				HeapInuse:  ms.HeapInuse,
				Goroutines: goroutines,
			}
			onCheckpoint := opts.OnCheckpoint
			goSafe(&callbacks, "cycle_memory_tick", func() { onCheckpoint(s) })
		}
	}

	samplerCtx, cancelSampler := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-samplerCtx.Done():
				return
			case <-ticker.C:
				sample(false)
			}
		}
	}()

	var (
		stopOnce sync.Once
		result   memPeak
	)
	return func() memPeak {
		stopOnce.Do(func() {
			cancelSampler()
			<-done
			sample(true)
			waitCallbacks(&callbacks, memCallbackDrain, label)

			mu.Lock()
			result = peak
			mu.Unlock()

			if result.HeapReleasedMinBytes == math.MaxUint64 {
				result.HeapReleasedMinBytes = 0
			}

			slog.Info("cycle memory peak",
				"label", label,
				"rss_peak_mb", bytesToMB(uint64(result.RSSPeakBytes)),
				"rss_peak_at_s", math.Round(result.RSSPeakAt.Seconds()*10)/10,
				"heap_inuse_peak_mb", bytesToMB(result.HeapInusePeakBytes),
				"heap_sys_max_mb", bytesToMB(result.HeapSysMaxBytes),
				"heap_released_min_mb", bytesToMB(result.HeapReleasedMinBytes),
				"stack_inuse_max_mb", bytesToMB(result.StackInuseMaxBytes),
				"sys_max_mb", bytesToMB(result.SysMaxBytes),
				"goroutines_peak", result.GoroutinesPeak,
				"samples", result.Samples,
				"timeline", strings.Join(result.Timeline, "; "),
			)
		})
		return result
	}
}

// bytesToMB converts a byte count to megabytes rounded to one decimal place.
func bytesToMB(b uint64) float64 {
	return math.Round(float64(b)/(1024*1024)*10) / 10
}
