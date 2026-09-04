package stats

import (
	"context"
	"log/slog"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/jetstream"
	"github.com/christophergentle/hourstats-bsky/internal/procmem"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// defaultSnapshotWindowMinutes is the posts-per-minute divisor used for the
// first snapshot, before there is a previous snapshot to measure against.
const defaultSnapshotWindowMinutes = 30.0

// ConsumerStatsProvider is satisfied by *jetstream.Consumer
type ConsumerStatsProvider interface {
	GetStatsReport() jetstream.StatsReport
}

// StatsStore is the subset of store.Store we need
type StatsStore interface {
	InsertStatsSnapshot(ctx context.Context, s *store.StatsSnapshot) error
	InsertStatsEvent(ctx context.Context, e *store.StatsEvent) error
}

// Collector aggregates statistics from multiple sources and persists snapshots.
type Collector struct {
	store    StatsStore
	provider ConsumerStatsProvider // may be nil during consumer restart
	mu       sync.RWMutex          // protects provider, lastAnalysis, and health fields
	dbPath   string

	// Traffic counters (incremented from OnPost callback via atomic)
	englishPosts atomic.Int64
	rootPosts    atomic.Int64
	replyPosts   atomic.Int64

	// Firehose counter (replaces the global firehosePostCount in main.go).
	// Monotonic for the life of the process: nothing ever resets it. Two
	// independent consumers read it through their own cursor so neither can
	// steal counts from the other.
	firehosePosts        atomic.Int64
	lastSnapshotFirehose atomic.Int64 // cursor for TakeSnapshot
	lastAnalysisFirehose atomic.Int64 // cursor for FirehoseSinceAnalysis
	lastPostReceived     atomic.Int64 // Unix timestamp of last post received

	// Per-language firehose counts since the last analysis cycle consumed
	// them, keyed by primary language subtag ("en", "pt", "und").
	langMu     sync.Mutex
	langCounts map[string]int64

	// Dropped-post counter — incremented when the write buffer is full
	droppedPosts atomic.Int64

	// Health metric counters (hs-21g)
	slowFlushCount atomic.Int64
	slowFlushMaxMs atomic.Int64
	writeChDepthFn func() int // returns len(writeCh); nil-safe

	// lastSeen holds the previous snapshot's raw counter values. It is only
	// read and written by TakeSnapshot, which the stats ticker calls from a
	// single goroutine.
	lastSeen struct {
		eventsReceived    int64
		postsProcessed    int64
		eventsSkipped     int64
		reconnects        int64
		errors            int64
		endpointRotations int64
		earlyRejected     int64
		gcPauseTotalNs    uint64
		gcCount           uint32
		snapshotAt        time.Time // zero until the first snapshot
	}

	lastAnalysis struct {
		ran                bool
		postsConsidered    int
		postsHydrated      int
		hydrationErrors    int
		sentimentResult    string
		postingSkipped     bool
		cycleDurationMs    int64
		trendingDurationMs int64
	}
}

// New creates a new Collector with the given store and database path.
func New(store StatsStore, dbPath string) *Collector {
	return &Collector{
		store:  store,
		dbPath: dbPath,
	}
}

// SetWriteChannelFunc registers a function that returns the current write channel depth.
func (c *Collector) SetWriteChannelFunc(fn func() int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeChDepthFn = fn
}

// IncrementSlowFlush records a slow flush event, tracking count and max duration.
func (c *Collector) IncrementSlowFlush(durationMs int64) {
	c.slowFlushCount.Add(1)
	for {
		cur := c.slowFlushMaxMs.Load()
		if durationMs <= cur {
			break
		}
		if c.slowFlushMaxMs.CompareAndSwap(cur, durationMs) {
			break
		}
	}
}

// RecordCycleDuration records the duration of the most recent analysis cycle.
func (c *Collector) RecordCycleDuration(ms int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastAnalysis.cycleDurationMs = ms
}

// RecordTrendingDuration records the duration of the most recent trending analysis.
func (c *Collector) RecordTrendingDuration(ms int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastAnalysis.trendingDurationMs = ms
}

// SetConsumer updates the consumer provider (thread-safe).
// Called when runJetstream creates a new consumer, or with nil when consumer exits.
func (c *Collector) SetConsumer(provider ConsumerStatsProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = provider
}

// IncrementFirehosePost increments the firehose post counter and records the timestamp.
// Called from the OnPost callback for ALL posts (before English filter).
func (c *Collector) IncrementFirehosePost() {
	c.firehosePosts.Add(1)
	c.lastPostReceived.Store(time.Now().Unix())
}

// maxLanguageKeys bounds the per-cycle language map; tags beyond it fold
// into the undetermined bucket so a stream of junk tags cannot grow memory.
// "und" is chosen over "other" because the chart synthesises its own
// "other" series and must not collide with a stored key.
const maxLanguageKeys = 256

// overflowLanguage is where tags past maxLanguageKeys are counted.
const overflowLanguage = "und"

// IncrementLanguage counts one firehose post under lang.
func (c *Collector) IncrementLanguage(lang string) {
	c.langMu.Lock()
	defer c.langMu.Unlock()
	if c.langCounts == nil {
		c.langCounts = make(map[string]int64, 64)
	}
	if _, ok := c.langCounts[lang]; !ok && len(c.langCounts) >= maxLanguageKeys {
		lang = overflowLanguage
	}
	c.langCounts[lang]++
}

// RestoreLanguages merges counts back after a failed store so the next cycle
// carries them instead of losing them.
func (c *Collector) RestoreLanguages(counts map[string]int64) {
	c.langMu.Lock()
	defer c.langMu.Unlock()
	if c.langCounts == nil {
		c.langCounts = make(map[string]int64, len(counts)+16)
	}
	for lang, n := range counts {
		c.langCounts[lang] += n
	}
}

// LanguagesSinceAnalysis returns the per-language counts accumulated since
// the previous call and starts a fresh map. Only the analysis cycle consumes
// these, so a swap is safe.
func (c *Collector) LanguagesSinceAnalysis() map[string]int64 {
	c.langMu.Lock()
	defer c.langMu.Unlock()
	out := c.langCounts
	c.langCounts = nil
	if out == nil {
		out = map[string]int64{}
	}
	return out
}

// IncrementEnglishPost increments the English post counter and either root or reply counter.
// Called from the OnPost callback after English filter passes.
func (c *Collector) IncrementEnglishPost(isReply bool) {
	c.englishPosts.Add(1)
	if isReply {
		c.replyPosts.Add(1)
	} else {
		c.rootPosts.Add(1)
	}
}

// GetFirehoseCount returns the current firehose post count.
// Used by analysis cycle to get the current firehose count.
func (c *Collector) GetFirehoseCount() int64 {
	return c.firehosePosts.Load()
}

// FirehoseSinceAnalysis returns the number of firehose posts counted since the
// previous call and advances the analysis cursor. It does not touch the
// underlying counter, so the snapshot path keeps seeing every post.
func (c *Collector) FirehoseSinceAnalysis() int64 {
	current := c.firehosePosts.Load()
	return current - c.lastAnalysisFirehose.Swap(current)
}

// SwapFirehoseCount is a deprecated alias for FirehoseSinceAnalysis.
//
// Deprecated: the name describes the old behaviour, where the analysis cycle
// reset the shared counter to zero and silently discarded every post counted
// between the last snapshot and the reset. Call FirehoseSinceAnalysis instead.
func (c *Collector) SwapFirehoseCount() int64 {
	return c.FirehoseSinceAnalysis()
}

// LastPostReceived returns the time of the last post received.
// Used for stall detection.
func (c *Collector) LastPostReceived() time.Time {
	ts := c.lastPostReceived.Load()
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// IncrementDroppedPosts adds n to the dropped-post counter.
func (c *Collector) IncrementDroppedPosts(n int) {
	c.droppedPosts.Add(int64(n))
}

// SwapDroppedPosts returns the current dropped-post count and resets it to zero.
func (c *Collector) SwapDroppedPosts() int64 {
	return c.droppedPosts.Swap(0)
}

// RecordAnalysis records the results from the last analysis cycle (thread-safe).
func (c *Collector) RecordAnalysis(postsConsidered, postsHydrated, hydrationErrors int, sentiment string, skipped bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastAnalysis.ran = true
	c.lastAnalysis.postsConsidered = postsConsidered
	c.lastAnalysis.postsHydrated = postsHydrated
	c.lastAnalysis.hydrationErrors = hydrationErrors
	c.lastAnalysis.sentimentResult = sentiment
	c.lastAnalysis.postingSkipped = skipped
}

// TakeSnapshot captures the current state and persists a StatsSnapshot.
func (c *Collector) TakeSnapshot(ctx context.Context) error {
	// Read consumer stats (with lock to safely access provider)
	c.mu.RLock()
	provider := c.provider
	c.mu.RUnlock()

	var report jetstream.StatsReport
	var activeEndpoint string
	var uptimeSeconds int
	var deltaEvents, deltaPosts, deltaSkipped, deltaReconnects, deltaErrors, deltaRotations, deltaEarlyRejected int64

	if provider != nil {
		report = provider.GetStatsReport()

		// Compute deltas from last snapshot. runJetstream builds a fresh
		// Consumer after a fatal error, so these counters restart at zero
		// with no warning; counterDelta absorbs that.
		deltaEvents = counterDelta(report.EventsReceived, c.lastSeen.eventsReceived)
		deltaPosts = counterDelta(report.PostsProcessed, c.lastSeen.postsProcessed)
		deltaSkipped = counterDelta(report.EventsSkipped, c.lastSeen.eventsSkipped)
		deltaReconnects = counterDelta(report.Reconnects, c.lastSeen.reconnects)
		deltaErrors = counterDelta(report.Errors, c.lastSeen.errors)
		deltaRotations = counterDelta(report.EndpointRotations, c.lastSeen.endpointRotations)
		deltaEarlyRejected = counterDelta(report.EarlyRejectedNonEnglish, c.lastSeen.earlyRejected)

		// Update last-seen values
		c.lastSeen.eventsReceived = report.EventsReceived
		c.lastSeen.postsProcessed = report.PostsProcessed
		c.lastSeen.eventsSkipped = report.EventsSkipped
		c.lastSeen.reconnects = report.Reconnects
		c.lastSeen.errors = report.Errors
		c.lastSeen.endpointRotations = report.EndpointRotations
		c.lastSeen.earlyRejected = report.EarlyRejectedNonEnglish

		activeEndpoint = report.ActiveEndpoint
		uptimeSeconds = int(report.ConnectionUptime.Seconds())
	} else {
		slog.Warn("consumer provider is nil, using zeros for consumer stats")
	}

	// Read and reset traffic counters (these are only read by TakeSnapshot, so Swap is safe)
	englishDelta := c.englishPosts.Swap(0)
	rootDelta := c.rootPosts.Swap(0)
	replyDelta := c.replyPosts.Swap(0)

	// Compute firehose delta against this consumer's own cursor. The counter
	// is monotonic, so the analysis cycle advancing its cursor cannot take
	// posts away from the snapshot delta.
	currentFirehose := c.firehosePosts.Load()
	firehoseDelta := currentFirehose - c.lastSnapshotFirehose.Swap(currentFirehose)

	// Read and reset dropped-post counter
	droppedDelta := c.droppedPosts.Swap(0)

	// Read and reset slow flush counters
	slowFlushCount := c.slowFlushCount.Swap(0)
	slowFlushMaxMs := c.slowFlushMaxMs.Swap(0)

	// Sample write channel depth
	var writeChDepth int
	c.mu.RLock()
	if c.writeChDepthFn != nil {
		writeChDepth = c.writeChDepthFn()
	}
	c.mu.RUnlock()

	// Sample runtime memory and GC stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	gcPauseDelta := memStats.PauseTotalNs - uint64(c.lastSeen.gcPauseTotalNs)
	gcCountDelta := memStats.NumGC - c.lastSeen.gcCount
	c.lastSeen.gcPauseTotalNs = memStats.PauseTotalNs
	c.lastSeen.gcCount = memStats.NumGC

	// Sample WAL file size
	var walSize int64
	if c.dbPath != "" {
		if fi, err := os.Stat(c.dbPath + "-wal"); err == nil {
			walSize = fi.Size()
		}
	}

	goroutineCount := runtime.NumGoroutine()

	// Compute posts per minute over the time actually elapsed since the last
	// snapshot. Snapshots are also taken off-schedule (e.g. at the end of an
	// analysis cycle), so a fixed 30-minute divisor overstates or understates
	// the rate. The first snapshot has no predecessor, so it falls back to the
	// nominal window.
	now := time.Now().UTC()
	elapsedMinutes := defaultSnapshotWindowMinutes
	if !c.lastSeen.snapshotAt.IsZero() {
		if m := now.Sub(c.lastSeen.snapshotAt).Minutes(); m > 0 {
			elapsedMinutes = m
		}
	}
	c.lastSeen.snapshotAt = now
	postsPerMinute := float64(englishDelta) / elapsedMinutes

	// Read last analysis results (with lock)
	c.mu.RLock()
	analysisRan := c.lastAnalysis.ran
	postsConsidered := c.lastAnalysis.postsConsidered
	postsHydrated := c.lastAnalysis.postsHydrated
	hydrationErrors := c.lastAnalysis.hydrationErrors
	sentimentResult := c.lastAnalysis.sentimentResult
	postingSkipped := c.lastAnalysis.postingSkipped
	cycleDurationMs := c.lastAnalysis.cycleDurationMs
	trendingDurationMs := c.lastAnalysis.trendingDurationMs
	c.mu.RUnlock()

	// Build snapshot
	snap := &store.StatsSnapshot{
		SnapshotTime:            now,
		ActiveEndpoint:          activeEndpoint,
		EndpointRotations:       int(deltaRotations),
		ReconnectCount:          int(deltaReconnects),
		ConnectionUptimeSeconds: uptimeSeconds,
		EventsReceived:          int(deltaEvents),
		PostsProcessed:          int(deltaPosts),
		EventsSkipped:           int(deltaSkipped),
		ConsumerErrors:          int(deltaErrors),
		TotalFirehosePosts:      int(firehoseDelta),
		EarlyRejectedNonEnglish: int(deltaEarlyRejected),
		EnglishPostsStored:      int(englishDelta),
		RootPosts:               int(rootDelta),
		ReplyPosts:              int(replyDelta),
		PostsPerMinuteAvg:       postsPerMinute,
		AnalysisRan:             boolToInt(analysisRan),
		PostsConsidered:         postsConsidered,
		PostsHydrated:           postsHydrated,
		HydrationErrors:         hydrationErrors,
		SentimentResult:         sentimentResult,
		PostingSkipped:          boolToInt(postingSkipped),
		DroppedPosts:            int(droppedDelta),
		HeapInuseBytes:          int64(memStats.HeapInuse),
		HeapSysBytes:            int64(memStats.HeapSys),
		SysBytes:                int64(memStats.Sys),
		HeapReleasedBytes:       int64(memStats.HeapReleased),
		StackInuseBytes:         int64(memStats.StackInuse),
		RSSBytes:                procmem.RSSBytes(),
		GCPauseTotalNs:          int64(gcPauseDelta),
		GCCount:                 int64(gcCountDelta),
		GCCPUFraction:           memStats.GCCPUFraction,
		SlowFlushCount:          int(slowFlushCount),
		SlowFlushMaxMs:          slowFlushMaxMs,
		WriteChannelDepth:       writeChDepth,
		WALSizeBytes:            walSize,
		GoroutineCount:          goroutineCount,
		CycleDurationMs:         cycleDurationMs,
		TrendingDurationMs:      trendingDurationMs,
	}

	// Persist snapshot
	if err := c.store.InsertStatsSnapshot(ctx, snap); err != nil {
		return err
	}

	// Reset analysis state after snapshot
	c.mu.Lock()
	c.lastAnalysis.ran = false
	c.lastAnalysis.cycleDurationMs = 0
	c.lastAnalysis.trendingDurationMs = 0
	c.mu.Unlock()

	slog.Info("stats snapshot taken",
		"snapshot_id", snap.ID,
		"english_posts", englishDelta,
		"firehose_posts", firehoseDelta,
		"early_rejected_non_english", deltaEarlyRejected,
		"elapsed_minutes", math.Round(elapsedMinutes*10)/10,
		"posts_per_minute", postsPerMinute,
	)

	return nil
}

// LogEvent creates and persists a StatsEvent. Non-blocking (logs error but doesn't propagate).
func (c *Collector) LogEvent(ctx context.Context, eventType, details string) error {
	event := &store.StatsEvent{
		EventTime: time.Now().UTC(),
		EventType: eventType,
		Details:   details,
	}

	if err := c.store.InsertStatsEvent(ctx, event); err != nil {
		slog.Warn("failed to log stats event", "error", err, "event_type", eventType)
		return err
	}

	return nil
}

// counterDelta returns current-lastSeen for a monotonically increasing
// counter. A current value below lastSeen means the counter's owner was
// recreated and restarted at zero, in which case the whole current value is
// the delta. jetstream.StatsReport carries no generation number, so the
// ordering is the only signal available.
func counterDelta(current, lastSeen int64) int64 {
	if current < lastSeen {
		return current
	}
	return current - lastSeen
}

// boolToInt converts bool to int (1 for true, 0 for false)
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
