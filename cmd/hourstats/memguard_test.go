package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// rssSequence returns an RSS source that walks seq and then holds the last
// value, so a sampler polling faster than the sequence still sees the peak.
func rssSequence(seq ...int64) func() int64 {
	var mu sync.Mutex
	var i int
	return func() int64 {
		mu.Lock()
		defer mu.Unlock()
		v := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return v
	}
}

// guardTestSchedule turns off ticks and checkpoints: these tests are about the
// guard, not the timeline.
var guardTestSchedule = memSchedule{earlyTick: 0, earlyWindow: 0, lateTick: 0}

func TestMemSamplerGuardFiresWarnThenTripOnce(t *testing.T) {
	dir := t.TempDir()
	warns := make(chan memSample, 8)
	trips := make(chan memSample, 8)

	g := newMemGuard(100*1024*1024, 50, 80, dir)
	g.OnWarn = func(s memSample) { warns <- s }
	g.OnTrip = func(s memSample) { trips <- s }

	// Below warn, over warn twice, then over trip and held there. Only the
	// first crossing of each threshold may fire.
	rss := rssSequence(10<<20, 60<<20, 62<<20, 90<<20)

	stop := startMemSamplerWithOptions(context.Background(), 5*time.Millisecond, "run-guard",
		guardTestSchedule, memSamplerOptions{Guard: g, RSS: rss})

	warn := recvSample(t, warns, "warn")
	trip := recvSample(t, trips, "trip")
	stop()

	if warn.RSSBytes != 60<<20 {
		t.Errorf("warn fired at rss=%d, want %d (the first sample over the threshold)", warn.RSSBytes, 60<<20)
	}
	if trip.RSSBytes != 90<<20 {
		t.Errorf("trip fired at rss=%d, want %d", trip.RSSBytes, 90<<20)
	}
	if warn.Label != "run-guard" || trip.Label != "run-guard" {
		t.Errorf("labels = %q/%q, want run-guard", warn.Label, trip.Label)
	}

	// The sampler kept polling above both thresholds; nothing more may fire.
	time.Sleep(30 * time.Millisecond)
	if n := len(warns); n != 0 {
		t.Errorf("warn fired %d extra times, want 0", n)
	}
	if n := len(trips); n != 0 {
		t.Errorf("trip fired %d extra times, want 0", n)
	}

	for _, suffix := range []string{"warn.pprof", "warn.goroutines.txt", "trip.pprof", "trip.goroutines.txt"} {
		matches, err := filepath.Glob(filepath.Join(dir, "memguard-run-guard-*"+suffix))
		if err != nil {
			t.Fatalf("glob failed: %v", err)
		}
		if len(matches) != 1 {
			t.Errorf("got %d files matching *%s, want 1 (dir: %v)", len(matches), suffix, lsDir(t, dir))
			continue
		}
		info, err := os.Stat(matches[0])
		if err != nil {
			t.Fatalf("stat %s: %v", matches[0], err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", matches[0])
		}
	}
}

func TestMemSamplerGuardStaysQuiet(t *testing.T) {
	tests := []struct {
		name string
		rss  func() int64
	}{
		{name: "below thresholds", rss: rssSequence(10 << 20)},
		// procmem returns 0 wherever /proc/self/statm does not exist.
		{name: "no procfs", rss: rssSequence(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			var fires atomic.Int64
			g := newMemGuard(100*1024*1024, 50, 80, dir)
			g.OnWarn = func(memSample) { fires.Add(1) }
			g.OnTrip = func(memSample) { fires.Add(1) }

			stop := startMemSamplerWithOptions(context.Background(), 2*time.Millisecond, "quiet",
				guardTestSchedule, memSamplerOptions{Guard: g, RSS: tt.rss})
			time.Sleep(40 * time.Millisecond)
			stop()
			time.Sleep(10 * time.Millisecond)

			if n := fires.Load(); n != 0 {
				t.Errorf("guard fired %d times, want 0", n)
			}
			if files := lsDir(t, dir); len(files) != 0 {
				t.Errorf("wrote %v, want no evidence files", files)
			}
		})
	}
}

// TestMemSamplerNilGuard covers the disabled configuration: a nil guard must
// leave the sampler working rather than panic.
func TestMemSamplerNilGuard(t *testing.T) {
	stop := startMemSamplerWithOptions(context.Background(), 2*time.Millisecond, "nil-guard",
		guardTestSchedule, memSamplerOptions{Guard: nil, RSS: rssSequence(900 << 20)})
	time.Sleep(20 * time.Millisecond)
	if peak := stop(); peak.Samples == 0 {
		t.Error("Samples = 0, want > 0")
	}
}

// TestMemGuardForCycleCancels is the trip-to-cancel wiring on its own: a full
// cycle needs a database, a Bluesky session and 100k posts to reach it.
func TestMemGuardForCycleCancels(t *testing.T) {
	var order []string
	var baseCalls int
	base := &memGuard{TotalBytes: 1 << 30, WarnBytes: 500 << 20, TripBytes: 700 << 20}
	base.OnTrip = func(memSample) {
		baseCalls++
		order = append(order, "base")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var tripped atomic.Bool

	cycle := base.forCycle(func(memSample) {
		tripped.Store(true)
		cancel()
		order = append(order, "cancel")
	})
	cycle.OnTrip(memSample{Label: "run-x", RSSBytes: 800 << 20})

	if !tripped.Load() {
		t.Error("cycle hook did not run")
	}
	// The cancel is the only step that stops memory being allocated, so it
	// must not queue behind the stats insert the base sink does.
	if len(order) != 2 || order[0] != "cancel" {
		t.Errorf("trip ran in order %v, want the cycle cancel first", order)
	}
	if baseCalls != 1 {
		t.Errorf("base OnTrip called %d times, want 1", baseCalls)
	}
	if ctx.Err() == nil {
		t.Error("cycle context is still live, want cancelled")
	}
	if base.OnTrip == nil || cycle.TripBytes != base.TripBytes {
		t.Error("forCycle mutated the shared guard instead of copying it")
	}
}

// TestMemGuardFireOrder pins the order inside fire(): the sink (which cancels
// the cycle and records the event) runs before the profile write, which is the
// slowest step and the only one that can be lost without consequence.
func TestMemGuardFireOrder(t *testing.T) {
	dir := t.TempDir()
	g := newMemGuard(100*1024*1024, 50, 80, dir)

	var filesAtSink []string
	g.OnTrip = func(memSample) { filesAtSink = lsDir(t, dir) }

	g.fire(memGuardTrip, memSample{Label: "run-order", RSSBytes: 90 << 20})

	if len(filesAtSink) != 0 {
		t.Errorf("evidence %v was already written when the sink ran, want the sink first", filesAtSink)
	}
	if after := lsDir(t, dir); len(after) != 2 {
		t.Errorf("fire() left %v, want the profile and the goroutine dump", after)
	}
}

// TestMemSamplerCallbackPanicIsContained is the whole point of the guard: a
// bug in the evidence path must not do what the guard exists to prevent.
func TestMemSamplerCallbackPanicIsContained(t *testing.T) {
	dir := t.TempDir()
	g := newMemGuard(100*1024*1024, 50, 80, dir)
	fired := make(chan struct{}, 1)
	g.OnWarn = func(memSample) {
		fired <- struct{}{}
		panic("evidence sink blew up")
	}

	stop := startMemSamplerWithOptions(context.Background(), 2*time.Millisecond, "panic-test",
		guardTestSchedule, memSamplerOptions{Guard: g, RSS: rssSequence(60 << 20)})

	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the warn callback")
	}

	// The sampler survives the panicking callback and still reports.
	time.Sleep(20 * time.Millisecond)
	if peak := stop(); peak.Samples == 0 {
		t.Error("Samples = 0; the sampler did not survive the panic")
	}
}

func TestMemGuardWarnOnly(t *testing.T) {
	base := newMemGuard(1024*1024*1024, 55, 70, "/data")
	base.OnTrip = func(memSample) { t.Error("warn-only guard tripped") }

	g := base.warnOnly()
	if g.TripBytes != 0 || g.OnTrip != nil {
		t.Errorf("warnOnly() kept the trip: bytes=%d nil_sink=%t", g.TripBytes, g.OnTrip == nil)
	}
	if got := g.crossed(int64(g.TotalBytes), false, false); got != memGuardWarn {
		t.Errorf("crossed at full memory = %q, want %q", got, memGuardWarn)
	}
	if base.TripBytes == 0 {
		t.Error("warnOnly() mutated the shared guard")
	}
}

func TestMemGuardNilReceiver(t *testing.T) {
	var g *memGuard
	if got := g.crossed(900<<20, false, false); got != "" {
		t.Errorf("crossed() on a nil guard = %q, want empty", got)
	}
	if g.warnOnly() != nil {
		t.Error("warnOnly() on a nil guard is not nil")
	}
	if g.forCycle(func(memSample) {}) != nil {
		t.Error("forCycle() on a nil guard is not nil")
	}
}

func TestMemGuardCrossed(t *testing.T) {
	g := newMemGuard(1000*1024*1024, 50, 80, "")
	tests := []struct {
		name            string
		rss             int64
		warned, tripped bool
		want            string
	}{
		{name: "under warn", rss: 400 << 20, want: ""},
		{name: "at warn", rss: 500 << 20, want: memGuardWarn},
		{name: "over warn, already warned", rss: 600 << 20, warned: true, want: ""},
		// A jump straight past both thresholds reports the trip only.
		{name: "at trip", rss: 800 << 20, want: memGuardTrip},
		{name: "over trip, already tripped", rss: 900 << 20, warned: true, tripped: true, want: ""},
		{name: "negative rss", rss: -1, want: ""},
	}
	for _, tt := range tests {
		if got := g.crossed(tt.rss, tt.warned, tt.tripped); got != tt.want {
			t.Errorf("%s: crossed(%d, %t, %t) = %q, want %q", tt.name, tt.rss, tt.warned, tt.tripped, got, tt.want)
		}
	}
}

// TestMemSamplerCheckpointEvents covers the reason the ticks are persisted: an
// OOM-killed cycle never reaches the peak event, so every checkpoint must have
// left a row behind.
func TestMemSamplerCheckpointEvents(t *testing.T) {
	var mu sync.Mutex
	var got []memSample

	// The checkpoints are far enough apart that a sampler delayed by the race
	// detector still crosses them on separate samples rather than collapsing
	// both into one call.
	sched := memSchedule{
		earlyTick:   0,
		earlyWindow: time.Second,
		lateTick:    time.Second,
		checkpoints: []time.Duration{20 * time.Millisecond, 400 * time.Millisecond},
	}

	delivered := make(chan memSample, 8)
	stop := startMemSamplerWithOptions(context.Background(), 5*time.Millisecond, "run-ticks", sched,
		memSamplerOptions{
			RSS: rssSequence(120 << 20),
			OnCheckpoint: func(s memSample) {
				mu.Lock()
				got = append(got, s)
				mu.Unlock()
				delivered <- s
			},
		})
	recvSample(t, delivered, "first checkpoint")
	recvSample(t, delivered, "second checkpoint")
	stop()

	mu.Lock()
	defer mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("got %d checkpoint samples, want >= 2: %+v", len(got), got)
	}
	for i, s := range got {
		if s.Label != "run-ticks" {
			t.Errorf("sample %d label = %q, want run-ticks", i, s.Label)
		}
		if s.RSSBytes != 120<<20 {
			t.Errorf("sample %d rss = %d, want %d", i, s.RSSBytes, 120<<20)
		}
		if s.HeapInuse == 0 {
			t.Errorf("sample %d has no heap reading; a checkpoint sample must read MemStats", i)
		}
		if s.Goroutines <= 0 {
			t.Errorf("sample %d goroutines = %d, want > 0", i, s.Goroutines)
		}
		details := s.eventDetails()
		for _, want := range []string{"label=run-ticks", "t=", "rss_mb=120.0", "heap_inuse_mb=", "goroutines="} {
			if !strings.Contains(details, want) {
				t.Errorf("eventDetails() = %q, missing %q", details, want)
			}
		}
	}
}

func TestPruneMemGuardFiles(t *testing.T) {
	dir := t.TempDir()

	// 14 sets of evidence, oldest first, one minute apart.
	base := time.Now().Add(-time.Hour)
	var written []string
	for i := 0; i < 14; i++ {
		path := filepath.Join(dir, memGuardFilePrefix+"run-"+string(rune('a'+i))+"-1s-warn.pprof")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		mod := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
		written = append(written, path)
	}
	// An unrelated neighbour on the same volume must survive.
	keeper := filepath.Join(dir, "hourstats-prod.db")
	if err := os.WriteFile(keeper, []byte("db"), 0o600); err != nil {
		t.Fatalf("write %s: %v", keeper, err)
	}

	pruneMemGuardFiles(dir, memGuardKeepFiles)

	remaining, err := filepath.Glob(filepath.Join(dir, memGuardFilePrefix+"*"))
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if len(remaining) != memGuardKeepFiles {
		t.Fatalf("%d files left, want %d: %v", len(remaining), memGuardKeepFiles, remaining)
	}
	kept := make(map[string]bool, len(remaining))
	for _, p := range remaining {
		kept[p] = true
	}
	for _, p := range written[:len(written)-memGuardKeepFiles] {
		if kept[p] {
			t.Errorf("%s should have been pruned (it is among the oldest)", p)
		}
	}
	for _, p := range written[len(written)-memGuardKeepFiles:] {
		if !kept[p] {
			t.Errorf("%s should have been kept (it is among the newest)", p)
		}
	}
	if _, err := os.Stat(keeper); err != nil {
		t.Errorf("prune removed an unrelated file: %v", err)
	}

	// Under the cap it is a no-op.
	pruneMemGuardFiles(dir, memGuardKeepFiles)
	if again, _ := filepath.Glob(filepath.Join(dir, memGuardFilePrefix+"*")); len(again) != memGuardKeepFiles {
		t.Errorf("second prune left %d files, want %d", len(again), memGuardKeepFiles)
	}
}

// TestPruneMemGuardFilesPrefersTrip covers the tie: evidence written in the
// same instant is ranked by what produced it, and a trip — the cycle that was
// actually aborted — outranks a warn.
func TestPruneMemGuardFilesPrefersTrip(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Now().Add(-time.Hour)

	tripFile := filepath.Join(dir, memGuardFilePrefix+"run-a-20260909T155500-45s-trip.pprof")
	files := []string{tripFile}
	for i := 0; i < memGuardKeepFiles; i++ {
		files = append(files, filepath.Join(dir,
			memGuardFilePrefix+"run-"+string(rune('b'+i))+"-20260909T155500-45s-warn.pprof"))
	}
	for _, path := range files {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}

	pruneMemGuardFiles(dir, memGuardKeepFiles)

	if _, err := os.Stat(tripFile); err != nil {
		t.Errorf("the trip profile was pruned ahead of a warn: %v (left: %v)", err, lsDir(t, dir))
	}
	if remaining := lsDir(t, dir); len(remaining) != memGuardKeepFiles {
		t.Errorf("%d files left, want %d: %v", len(remaining), memGuardKeepFiles, remaining)
	}
}

func TestIsTripEvidence(t *testing.T) {
	tests := map[string]bool{
		"/data/memguard-run-a-20260909T155500-45s-trip.pprof":          true,
		"/data/memguard-run-a-20260909T155500-45s-trip.goroutines.txt": true,
		"/data/memguard-run-a-20260909T155500-45s-warn.pprof":          false,
		"/data/memguard-daily-20260909T155500-45s-warn.goroutines.txt": false,
		"/data/hourstats-prod.db":                                      false,
	}
	for path, want := range tests {
		if got := isTripEvidence(path); got != want {
			t.Errorf("isTripEvidence(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestMemGuardTotalMB(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		wantMB     int
		wantSource string
	}{
		{
			name:       "default when nothing is set",
			wantMB:     memGuardDefaultTotalMB,
			wantSource: "default",
		},
		{
			// Fly sets FLY_VM_MEMORY_MB on every machine, and it is the only
			// source that follows a resize.
			name:       "fly wins over every fallback",
			env:        map[string]string{"FLY_VM_MEMORY_MB": "2048", "MEMORY_GUARD_TOTAL_MB": "512", "HEALTH_CHART_MEMORY_LIMIT_MB": "256"},
			wantMB:     2048,
			wantSource: "FLY_VM_MEMORY_MB",
		},
		{
			name:       "explicit override without fly",
			env:        map[string]string{"MEMORY_GUARD_TOTAL_MB": "512"},
			wantMB:     512,
			wantSource: "MEMORY_GUARD_TOTAL_MB",
		},
		{
			// The health chart's limit is a line drawn on a chart, not a
			// statement about the machine, so it is not a source.
			name:       "health chart limit is ignored",
			env:        map[string]string{"HEALTH_CHART_MEMORY_LIMIT_MB": "256"},
			wantMB:     memGuardDefaultTotalMB,
			wantSource: "default",
		},
		{
			name:       "garbage falls through to the next source",
			env:        map[string]string{"FLY_VM_MEMORY_MB": "not-a-number", "MEMORY_GUARD_TOTAL_MB": "768"},
			wantMB:     768,
			wantSource: "MEMORY_GUARD_TOTAL_MB",
		},
		{
			name:       "garbage everywhere falls back to the default",
			env:        map[string]string{"FLY_VM_MEMORY_MB": "not-a-number", "MEMORY_GUARD_TOTAL_MB": "0"},
			wantMB:     memGuardDefaultTotalMB,
			wantSource: "default",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearMemGuardEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			mb, source := memGuardTotalMB()
			if mb != tt.wantMB || source != tt.wantSource {
				t.Errorf("memGuardTotalMB() = (%d, %q), want (%d, %q)", mb, source, tt.wantMB, tt.wantSource)
			}
		})
	}
}

func TestNewMemGuardFromEnv(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		clearMemGuardEnv(t)
		g := newMemGuardFromEnv("/data")
		if g == nil {
			t.Fatal("guard is nil, want the default configuration")
		}
		if g.TotalBytes != memGuardDefaultTotalMB*1024*1024 {
			t.Errorf("TotalBytes = %d, want %d", g.TotalBytes, memGuardDefaultTotalMB*1024*1024)
		}
		// 55% and 70% of 1024MB: 563.2MB and 716.8MB.
		if want := uint64(1024*1024*1024) * 55 / 100; g.WarnBytes != want {
			t.Errorf("WarnBytes = %d, want %d (55%% of 1024MB)", g.WarnBytes, want)
		}
		if want := uint64(1024*1024*1024) * 70 / 100; g.TripBytes != want {
			t.Errorf("TripBytes = %d, want %d (70%% of 1024MB)", g.TripBytes, want)
		}
		if g.ProfileDir != "/data" {
			t.Errorf("ProfileDir = %q, want /data", g.ProfileDir)
		}
	})

	t.Run("percentages of the fly machine size", func(t *testing.T) {
		clearMemGuardEnv(t)
		t.Setenv("FLY_VM_MEMORY_MB", "2048")
		t.Setenv("MEMORY_GUARD_WARN_PCT", "40")
		t.Setenv("MEMORY_GUARD_TRIP_PCT", "60")

		g := newMemGuardFromEnv("/data")
		total := uint64(2048) * 1024 * 1024
		if want := total * 40 / 100; g.WarnBytes != want {
			t.Errorf("WarnBytes = %d, want %d", g.WarnBytes, want)
		}
		if want := total * 60 / 100; g.TripBytes != want {
			t.Errorf("TripBytes = %d, want %d", g.TripBytes, want)
		}
	})

	t.Run("out of range percentages fall back", func(t *testing.T) {
		clearMemGuardEnv(t)
		t.Setenv("MEMORY_GUARD_WARN_PCT", "0")
		t.Setenv("MEMORY_GUARD_TRIP_PCT", "300")

		g := newMemGuardFromEnv("/data")
		def := newMemGuard(memGuardDefaultTotalMB*1024*1024, memGuardDefaultWarnPct, memGuardDefaultTripPct, "/data")
		if g.WarnBytes != def.WarnBytes || g.TripBytes != def.TripBytes {
			t.Errorf("thresholds = (%d, %d), want the defaults (%d, %d)", g.WarnBytes, g.TripBytes, def.WarnBytes, def.TripBytes)
		}
	})

	// A warn at or above the trip would leave no early evidence: either it
	// never fires, or it fires on the sample that already aborts the cycle.
	t.Run("warn not below trip falls back", func(t *testing.T) {
		def := newMemGuard(memGuardDefaultTotalMB*1024*1024, memGuardDefaultWarnPct, memGuardDefaultTripPct, "/data")
		for _, pair := range [][2]string{{"70", "70"}, {"80", "60"}} {
			clearMemGuardEnv(t)
			t.Setenv("MEMORY_GUARD_WARN_PCT", pair[0])
			t.Setenv("MEMORY_GUARD_TRIP_PCT", pair[1])

			g := newMemGuardFromEnv("/data")
			if g.WarnBytes != def.WarnBytes || g.TripBytes != def.TripBytes {
				t.Errorf("warn=%s trip=%s gave (%d, %d), want the defaults (%d, %d)",
					pair[0], pair[1], g.WarnBytes, g.TripBytes, def.WarnBytes, def.TripBytes)
			}
		}
	})

	t.Run("disabled", func(t *testing.T) {
		clearMemGuardEnv(t)
		t.Setenv("MEMORY_GUARD_ENABLED", "false")
		if g := newMemGuardFromEnv("/data"); g != nil {
			t.Errorf("guard = %+v, want nil when MEMORY_GUARD_ENABLED=false", g)
		}
	})
}

// clearMemGuardEnv empties every variable the guard reads so a developer's
// shell cannot change the result. t.Setenv restores them afterwards.
func clearMemGuardEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"MEMORY_GUARD_ENABLED", "MEMORY_GUARD_WARN_PCT", "MEMORY_GUARD_TRIP_PCT",
		"MEMORY_GUARD_TOTAL_MB", "FLY_VM_MEMORY_MB", "HEALTH_CHART_MEMORY_LIMIT_MB",
	} {
		t.Setenv(key, "")
	}
}

// recvSample waits for one callback sample.
func recvSample(t *testing.T, ch <-chan memSample, kind string) memSample {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for the %s callback", kind)
		return memSample{}
	}
}

// lsDir lists a directory for failure messages.
func lsDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
