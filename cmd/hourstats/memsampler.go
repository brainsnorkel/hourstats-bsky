package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime"
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
	start := time.Now()
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
		rss := procmem.RSSBytes()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		goroutines := runtime.NumGoroutine()
		elapsed := time.Since(start)

		// Decide under the lock, log outside it.
		tick := func() bool {
			mu.Lock()
			defer mu.Unlock()

			peak.Samples++
			if rss > peak.RSSPeakBytes {
				peak.RSSPeakBytes = rss
				peak.RSSPeakAt = elapsed
			}
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
				return false
			}
			nextTick = advanceTick(nextTick, elapsed, sched)
			return true
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
