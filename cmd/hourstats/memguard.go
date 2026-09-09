package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The kernel OOM-killer leaves nothing behind: the process dies between two
// samples and the peak the memory sampler would have logged at the end of the
// cycle is never written. The guard closes that gap. It watches RSS — the
// number the killer acts on — and, well before the ceiling, dumps a heap
// profile and a goroutine dump to the data volume where they survive the
// restart. Past a second threshold it cancels the cycle so the process lives.

// Threshold kinds. They name the evidence files and the stats events.
const (
	memGuardWarn = "warn"
	memGuardTrip = "trip"
)

// memGuardDefaultTotalMB is the assumed machine size when no source of truth
// is available: the size of the Fly VMs this bot has always run on.
const memGuardDefaultTotalMB = 1024

// memGuardKeepFiles caps how many memguard-* files are retained in the profile
// directory. They share the SQLite volume, so they cannot be allowed to grow.
const memGuardKeepFiles = 10

// memGuardFilePrefix is both the file name prefix and the prune glob.
const memGuardFilePrefix = "memguard-"

// memGuardEventBudget bounds the stats_events insert a crossing triggers. The
// write pool's busy timeout is 30s; a guard event is not worth waiting that
// long for, least of all while the machine is running out of memory.
const memGuardEventBudget = 5 * time.Second

// memSample is one memory reading, handed to the guard callbacks and to the
// sampler's checkpoint sink.
type memSample struct {
	Label      string
	Elapsed    time.Duration
	RSSBytes   int64
	HeapInuse  uint64
	Goroutines int
}

// eventDetails renders the sample for a stats_events row.
func (s memSample) eventDetails() string {
	rss := uint64(0)
	if s.RSSBytes > 0 {
		rss = uint64(s.RSSBytes)
	}
	return fmt.Sprintf("label=%s t=%d rss_mb=%.1f heap_inuse_mb=%.1f goroutines=%d",
		s.Label,
		int(s.Elapsed.Round(time.Second)/time.Second),
		bytesToMB(rss),
		bytesToMB(s.HeapInuse),
		s.Goroutines,
	)
}

// memGuard holds the RSS thresholds a cycle is watched against and the sinks
// that record a crossing. It is built once and shared by every cycle; the
// fire-once state lives in the sampler, so each cycle gets one warn and one
// trip.
type memGuard struct {
	TotalBytes uint64
	WarnBytes  uint64
	TripBytes  uint64
	ProfileDir string
	OnWarn     func(memSample)
	OnTrip     func(memSample)
}

// newMemGuard builds a guard for a machine of totalBytes, with warn and trip
// expressed as percentages of it. Out-of-range percentages fall back to the
// defaults rather than disabling the guard.
func newMemGuard(totalBytes uint64, warnPct, tripPct int, profileDir string) *memGuard {
	warnPct = clampPct("MEMORY_GUARD_WARN_PCT", warnPct, memGuardDefaultWarnPct)
	tripPct = clampPct("MEMORY_GUARD_TRIP_PCT", tripPct, memGuardDefaultTripPct)
	// A warn at or above the trip is not a configuration anyone means: the warn
	// would either never fire or fire on the same sample that aborts the cycle,
	// leaving no early evidence at all.
	if warnPct >= tripPct {
		slog.Warn("memory guard warn threshold is not below the trip threshold, using the defaults",
			"warn_pct", warnPct, "trip_pct", tripPct,
			"default_warn_pct", memGuardDefaultWarnPct, "default_trip_pct", memGuardDefaultTripPct)
		warnPct, tripPct = memGuardDefaultWarnPct, memGuardDefaultTripPct
	}
	return &memGuard{
		TotalBytes: totalBytes,
		WarnBytes:  totalBytes * uint64(warnPct) / 100,
		TripBytes:  totalBytes * uint64(tripPct) / 100,
		ProfileDir: profileDir,
	}
}

// Default thresholds. Warn sits above a healthy cycle's peak (~360MB of 1GB)
// and well below the ceiling; trip is the last point at which cancelling the
// cycle reliably beats the kernel to the kill.
const (
	memGuardDefaultWarnPct = 55
	memGuardDefaultTripPct = 70
)

// clampPct keeps a percentage in 1..100, warning once at startup if it is not.
func clampPct(name string, v, fallback int) int {
	if v <= 0 || v > 100 {
		slog.Warn("memory guard percentage out of range, using default", "env", name, "value", v, "default", fallback)
		return fallback
	}
	return v
}

// newMemGuardFromEnv resolves the guard configuration and logs it. It returns
// nil when MEMORY_GUARD_ENABLED is false, which disables every guard check.
func newMemGuardFromEnv(profileDir string) *memGuard {
	if !envBool("MEMORY_GUARD_ENABLED", true) {
		slog.Info("memory guard disabled", "env", "MEMORY_GUARD_ENABLED=false")
		return nil
	}
	totalMB, source := memGuardTotalMB()
	g := newMemGuard(uint64(totalMB)*1024*1024,
		envInt("MEMORY_GUARD_WARN_PCT", memGuardDefaultWarnPct),
		envInt("MEMORY_GUARD_TRIP_PCT", memGuardDefaultTripPct),
		profileDir,
	)
	slog.Info("memory guard configured",
		"total_mb", totalMB,
		"total_source", source,
		"warn_mb", bytesToMB(g.WarnBytes),
		"trip_mb", bytesToMB(g.TripBytes),
		"profile_dir", g.ProfileDir,
	)
	return g
}

// memGuardTotalMB resolves the machine memory the thresholds are a share of,
// and names the source it came from. FLY_VM_MEMORY_MB is set by Fly on every
// machine and is the only source that follows a resize, so it wins;
// MEMORY_GUARD_TOTAL_MB is the manual override. Nothing else describes the
// machine — HEALTH_CHART_MEMORY_LIMIT_MB is a line on a chart, not a size —
// so the last resort is the VM size this bot has always run on, which is worth
// a warning: thresholds derived from a guess are only as good as the guess.
func memGuardTotalMB() (int, string) {
	for _, key := range []string{"FLY_VM_MEMORY_MB", "MEMORY_GUARD_TOTAL_MB"} {
		raw := os.Getenv(key)
		if raw == "" {
			continue
		}
		mb, err := strconv.Atoi(raw)
		if err != nil || mb <= 0 {
			slog.Warn("memory guard total is not a positive integer, trying the next source", "env", key, "value", raw)
			continue
		}
		return mb, key
	}
	slog.Warn("memory guard has no machine size to work from, assuming the historical VM",
		"assumed_mb", memGuardDefaultTotalMB,
		"set_one_of", "FLY_VM_MEMORY_MB, MEMORY_GUARD_TOTAL_MB")
	return memGuardDefaultTotalMB, "default"
}

// warnOnly returns a copy of g that can never trip. The daily job uses it:
// aborting a backup or an aggregation halfway costs more than the peak it
// would save, but the evidence is still worth having.
func (g *memGuard) warnOnly() *memGuard {
	if g == nil {
		return nil
	}
	cp := *g
	cp.TripBytes = 0
	cp.OnTrip = nil
	return &cp
}

// forCycle returns a copy of g whose OnTrip also runs onTrip. It is how a
// per-cycle cancel is attached to the process-wide guard configuration.
//
// onTrip runs first, and everything downstream of it is ordered on the same
// principle: the cancel is the only step that actually stops memory being
// allocated, so it must not queue behind a database insert or a heap profile
// write. The evidence is worth having, but not at the cost of the kill it
// exists to prevent.
func (g *memGuard) forCycle(onTrip func(memSample)) *memGuard {
	if g == nil {
		return nil
	}
	cp := *g
	base := g.OnTrip
	cp.OnTrip = func(s memSample) {
		onTrip(s)
		if base != nil {
			base(s)
		}
	}
	return &cp
}

// crossed reports which threshold this reading crosses, given what has already
// fired during the current cycle, or "" for none. An RSS of 0 means procfs is
// unavailable (any non-Linux host), where the guard is a no-op. A reading past
// the trip threshold reports only the trip: its evidence supersedes the warn's.
func (g *memGuard) crossed(rss int64, warned, tripped bool) string {
	if g == nil || rss <= 0 {
		return ""
	}
	if !tripped && g.TripBytes > 0 && uint64(rss) >= g.TripBytes {
		return memGuardTrip
	}
	if !warned && g.WarnBytes > 0 && uint64(rss) >= g.WarnBytes {
		return memGuardWarn
	}
	return ""
}

// sink returns the callback for a threshold kind.
func (g *memGuard) sink(kind string) func(memSample) {
	if kind == memGuardTrip {
		return g.OnTrip
	}
	return g.OnWarn
}

// fire logs the crossing, hands the sample to the caller's sink and then
// writes the evidence. The sampler runs it on its own goroutine: a heap
// profile on a loaded volume takes long enough to skew the 500ms sampling
// cadence.
//
// The sink comes before the evidence deliberately. On a trip the sink cancels
// the cycle and records the stats row; the profile write is the slowest step
// and the only one that can be lost without consequence, so it goes last.
func (g *memGuard) fire(kind string, s memSample) {
	rss := uint64(0)
	if s.RSSBytes > 0 {
		rss = uint64(s.RSSBytes)
	}
	attrs := []any{
		"label", s.Label,
		"kind", kind,
		"rss_mb", bytesToMB(rss),
		"heap_inuse_mb", bytesToMB(s.HeapInuse),
		"goroutines", s.Goroutines,
		"elapsed_s", int(s.Elapsed.Round(time.Second) / time.Second),
		"warn_mb", bytesToMB(g.WarnBytes),
		"trip_mb", bytesToMB(g.TripBytes),
		"total_mb", bytesToMB(g.TotalBytes),
	}
	if kind == memGuardTrip {
		slog.Error("memory guard tripped, cancelling the cycle", attrs...)
	} else {
		slog.Warn("memory guard warn threshold crossed", attrs...)
	}

	if sink := g.sink(kind); sink != nil {
		sink(s)
	}

	g.writeEvidence(kind, s)
}

// writeEvidence dumps a heap profile and a goroutine dump next to the database,
// then prunes older sets. Every failure is logged and swallowed: losing the
// evidence must never cost the cycle.
func (g *memGuard) writeEvidence(kind string, s memSample) {
	if g.ProfileDir == "" {
		return
	}
	// The wall-clock stamp is what separates one day's evidence from the next:
	// the daily job's label is the constant "daily", so without it a second
	// warn at the same elapsed second would overwrite the first.
	base := filepath.Join(g.ProfileDir, fmt.Sprintf("%s%s-%s-%ds-%s",
		memGuardFilePrefix,
		sanitizeFileLabel(s.Label),
		time.Now().UTC().Format("20060102T150405"),
		int(s.Elapsed/time.Second),
		kind))

	// pprof.WriteHeapProfile does not collect first — it writes the profile as
	// of the last GC, which under GOGC=75 during a burst can be well out of
	// date. Collect explicitly so the profile describes the live set that
	// crossed the threshold. On a trip, FreeOSMemory does that and also hands
	// the freed pages back to the kernel, which is the point of tripping.
	if kind == memGuardTrip {
		debug.FreeOSMemory()
	} else {
		runtime.GC()
	}

	if err := writeProfileFile(base+".pprof", pprof.WriteHeapProfile); err != nil {
		slog.Warn("memory guard heap profile failed", "error", err, "label", s.Label)
	}
	if err := writeProfileFile(base+".goroutines.txt", func(w io.Writer) error {
		return pprof.Lookup("goroutine").WriteTo(w, 1)
	}); err != nil {
		slog.Warn("memory guard goroutine dump failed", "error", err, "label", s.Label)
	}

	pruneMemGuardFiles(g.ProfileDir, memGuardKeepFiles)
}

// writeProfileFile creates path and hands it to write.
func writeProfileFile(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	slog.Info("memory guard evidence written", "path", path)
	return nil
}

// sanitizeFileLabel reduces a sampler label to characters that are safe in a
// file name; the label reaches here from a run ID, so it is never trusted.
func sanitizeFileLabel(label string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, label)
	if safe == "" {
		return "unlabelled"
	}
	return safe
}

// memGuardPruneMu serialises pruning. A cycle guard and the daily job's guard
// can fire at the same time on the same directory, and two interleaved passes
// would each decide what to keep from a listing the other is deleting from.
var memGuardPruneMu sync.Mutex

// isTripEvidence reports whether a file came from a trip rather than a warn.
// The kind is the last dash-separated field of the base name, before the
// ".pprof" or ".goroutines.txt" suffix.
func isTripEvidence(path string) bool {
	name := filepath.Base(path)
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	return strings.HasSuffix(name, "-"+memGuardTrip)
}

// pruneMemGuardFiles keeps the keep newest memguard-* files in dir and deletes
// the rest, so a run of trips cannot fill the volume the database lives on.
func pruneMemGuardFiles(dir string, keep int) {
	memGuardPruneMu.Lock()
	defer memGuardPruneMu.Unlock()

	matches, err := filepath.Glob(filepath.Join(dir, memGuardFilePrefix+"*"))
	if err != nil {
		slog.Warn("memory guard prune glob failed", "error", err, "dir", dir)
		return
	}
	if len(matches) <= keep {
		return
	}

	type entry struct {
		path string
		mod  time.Time
	}
	entries := make([]entry, 0, len(matches))
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		entries = append(entries, entry{path: path, mod: info.ModTime()})
	}
	// Newest first. Within the same instant a trip outranks a warn — the trip
	// is the cycle that was actually aborted — and name order breaks the rest,
	// so the choice is deterministic.
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].mod.Equal(entries[j].mod) {
			return entries[i].mod.After(entries[j].mod)
		}
		if iTrip, jTrip := isTripEvidence(entries[i].path), isTripEvidence(entries[j].path); iTrip != jTrip {
			return iTrip
		}
		return entries[i].path > entries[j].path
	})
	if keep > len(entries) {
		return
	}
	for _, e := range entries[keep:] {
		if err := os.Remove(e.path); err != nil {
			slog.Warn("memory guard prune failed", "error", err, "path", e.path)
			continue
		}
		slog.Info("memory guard evidence pruned", "path", e.path)
	}
}
