package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseStatmRSS(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		pageSize int
		want     int64
		wantErr  bool
	}{
		{
			name:     "realistic statm line",
			content:  "150000 148000 3000 1 0 120000 0\n",
			pageSize: 4096,
			want:     148000 * 4096,
		},
		{
			name:     "16k pages",
			content:  "150000 148000 3000 1 0 120000 0",
			pageSize: 16384,
			want:     148000 * 16384,
		},
		{
			name:     "too few fields",
			content:  "150000\n",
			pageSize: 4096,
			wantErr:  true,
		},
		{
			name:     "empty",
			content:  "",
			pageSize: 4096,
			wantErr:  true,
		},
		{
			name:     "non-numeric resident field",
			content:  "150000 not-a-number 3000",
			pageSize: 4096,
			wantErr:  true,
		},
		{
			name:     "negative resident field",
			content:  "150000 -5 3000",
			pageSize: 4096,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStatmRSS(tt.content, tt.pageSize)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseStatmRSS(%q) = %d, want error", tt.content, got)
				}
				if got != 0 {
					t.Errorf("parseStatmRSS(%q) = %d on error, want 0", tt.content, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStatmRSS(%q) unexpected error: %v", tt.content, err)
			}
			if got != tt.want {
				t.Errorf("parseStatmRSS(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}

func TestStartMemSampler(t *testing.T) {
	stop := startMemSampler(context.Background(), 5*time.Millisecond, "test-cycle")

	// Touch every page so the allocation is resident, not just reserved.
	block := make([]byte, 32<<20)
	for i := 0; i < len(block); i += 4096 {
		block[i] = 1
	}
	time.Sleep(50 * time.Millisecond)

	peak := stop()
	runtime.KeepAlive(block)

	if peak.Samples == 0 {
		t.Error("Samples = 0, want > 0")
	}
	if peak.HeapInusePeakBytes < 32<<20 {
		t.Errorf("HeapInusePeakBytes = %d, want >= %d", peak.HeapInusePeakBytes, 32<<20)
	}
	if len(peak.Timeline) == 0 {
		t.Error("Timeline is empty, want at least the final entry")
	}
	if len(peak.Timeline) > memTimelineMax {
		t.Errorf("Timeline has %d entries, want <= %d", len(peak.Timeline), memTimelineMax)
	}
	if !strings.HasPrefix(peak.Timeline[0], "t=") {
		t.Errorf("Timeline[0] = %q, want a t=... entry", peak.Timeline[0])
	}
	if peak.GoroutinesPeak <= 0 {
		t.Errorf("GoroutinesPeak = %d, want > 0", peak.GoroutinesPeak)
	}
	if peak.SysMaxBytes == 0 {
		t.Error("SysMaxBytes = 0, want > 0")
	}
	// RSS is 0 on platforms without /proc (macOS); never negative.
	if peak.RSSPeakBytes < 0 {
		t.Errorf("RSSPeakBytes = %d, want >= 0", peak.RSSPeakBytes)
	}

	// stop() is idempotent and returns the same result.
	again := stop()
	if again.Samples != peak.Samples || again.HeapInusePeakBytes != peak.HeapInusePeakBytes {
		t.Errorf("second stop() = %+v, want same as first %+v", again, peak)
	}

	details := peak.eventDetails("test-cycle")
	for _, want := range []string{"label=test-cycle", "rss_peak=", "at=", "heap_inuse_peak=", "heap_sys_max=", "stack_max=", "goroutines_peak="} {
		if !strings.Contains(details, want) {
			t.Errorf("eventDetails() = %q, missing %q", details, want)
		}
	}
}

func TestStartMemSamplerContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := startMemSampler(ctx, 5*time.Millisecond, "cancelled")
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(10 * time.Millisecond)

	peak := stop()
	if peak.Samples == 0 {
		t.Error("Samples = 0, want > 0 (final sample is always taken)")
	}
	if len(peak.Timeline) == 0 {
		t.Error("Timeline is empty, want the final entry")
	}
}

func TestStartMemSamplerTimelineCheckpoints(t *testing.T) {
	// Compressed checkpoints exercise the timeline path without sleeping for
	// whole seconds. Passed as a parameter so no package state is mutated.
	sched := memSchedule{
		earlyTick:   0, // ticks off; this test is about the timeline
		earlyWindow: time.Second,
		lateTick:    time.Second,
		checkpoints: []time.Duration{10 * time.Millisecond, 50 * time.Millisecond},
	}

	stop := startMemSamplerWithSchedule(context.Background(), 5*time.Millisecond, "checkpoints", sched)
	time.Sleep(80 * time.Millisecond)
	peak := stop()

	// At least one checkpoint entry plus the forced final entry. Checkpoints
	// collapse into a single entry if a sample is late, so don't require both.
	if len(peak.Timeline) < 2 {
		t.Errorf("Timeline = %v, want >= 2 entries (checkpoint + final)", peak.Timeline)
	}
	for i, entry := range peak.Timeline {
		for _, want := range []string{"t=", "rss=", "heap=", "hsys=", "stk=", "g="} {
			if !strings.Contains(entry, want) {
				t.Errorf("Timeline[%d] = %q, missing %q", i, entry, want)
			}
		}
	}
}

// lockedBuffer is a concurrency-safe io.Writer for capturing slog output.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestStartMemSamplerPeriodicTicks covers the SIGKILL case: the sampler must
// emit progress while it runs, not only from the stop closure, because an
// OOM-killed process never reaches stop.
func TestStartMemSamplerPeriodicTicks(t *testing.T) {
	var out lockedBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	sched := memSchedule{
		earlyTick:   10 * time.Millisecond,
		earlyWindow: 40 * time.Millisecond,
		lateTick:    25 * time.Millisecond,
		checkpoints: []time.Duration{10 * time.Millisecond},
	}

	stop := startMemSamplerWithSchedule(context.Background(), 2*time.Millisecond, "tick-test", sched)
	time.Sleep(200 * time.Millisecond)
	stop()

	var ticks []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", line, err)
		}
		if rec["msg"] == "cycle memory tick" {
			ticks = append(ticks, rec)
		}
	}

	if len(ticks) < 3 {
		t.Fatalf("got %d tick lines, want >= 3\nlog:\n%s", len(ticks), out.String())
	}

	first := ticks[0]
	for _, key := range []string{"label", "t_s", "rss_mb", "heap_inuse_mb", "heap_sys_mb", "goroutines"} {
		if _, ok := first[key]; !ok {
			t.Errorf("tick line missing %q: %v", key, first)
		}
	}
	if first["label"] != "tick-test" {
		t.Errorf("label = %v, want tick-test", first["label"])
	}

	// t_s must advance across ticks rather than repeating the same instant.
	firstT, _ := ticks[0]["t_s"].(float64)
	lastT, _ := ticks[len(ticks)-1]["t_s"].(float64)
	if !(lastT > firstT) {
		t.Errorf("t_s did not advance: first=%v last=%v", firstT, lastT)
	}
}

// TestStartMemSamplerTicksDisabled guards the advanceTick loop against a
// non-positive cadence, which must disable ticks instead of spinning.
func TestStartMemSamplerTicksDisabled(t *testing.T) {
	sched := memSchedule{earlyTick: 0, earlyWindow: 0, lateTick: 0}
	stop := startMemSamplerWithSchedule(context.Background(), 2*time.Millisecond, "no-ticks", sched)
	time.Sleep(20 * time.Millisecond)
	peak := stop()
	if peak.Samples == 0 {
		t.Error("Samples = 0, want > 0")
	}

	if got := advanceTick(10*time.Millisecond, 50*time.Millisecond, sched); got != tickDisabled {
		t.Errorf("advanceTick with zero cadence = %v, want tickDisabled", got)
	}
}

func TestAdvanceTick(t *testing.T) {
	sched := memSchedule{
		earlyTick:   5 * time.Second,
		earlyWindow: 60 * time.Second,
		lateTick:    30 * time.Second,
	}

	tests := []struct {
		next, elapsed, want time.Duration
	}{
		// Inside the early window: step by 5s.
		{5 * time.Second, 5 * time.Second, 10 * time.Second},
		{10 * time.Second, 12 * time.Second, 15 * time.Second},
		// A stalled sampler skips ahead past elapsed in one call.
		{5 * time.Second, 32 * time.Second, 35 * time.Second},
		// At the boundary the late cadence takes over.
		{60 * time.Second, 61 * time.Second, 90 * time.Second},
		{90 * time.Second, 95 * time.Second, 120 * time.Second},
	}
	for _, tt := range tests {
		if got := advanceTick(tt.next, tt.elapsed, sched); got != tt.want {
			t.Errorf("advanceTick(next=%v, elapsed=%v) = %v, want %v", tt.next, tt.elapsed, got, tt.want)
		}
	}
}
