package stats

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/jetstream"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// --- Mocks ---

type mockStatsStore struct {
	snapshots []*store.StatsSnapshot
	events    []*store.StatsEvent
	snapErr   error
	eventErr  error
}

func (m *mockStatsStore) InsertStatsSnapshot(_ context.Context, s *store.StatsSnapshot) error {
	if m.snapErr != nil {
		return m.snapErr
	}
	s.ID = int64(len(m.snapshots) + 1)
	m.snapshots = append(m.snapshots, s)
	return nil
}

func (m *mockStatsStore) InsertStatsEvent(_ context.Context, e *store.StatsEvent) error {
	if m.eventErr != nil {
		return m.eventErr
	}
	e.ID = int64(len(m.events) + 1)
	m.events = append(m.events, e)
	return nil
}

type mockConsumerProvider struct {
	report jetstream.StatsReport
}

func (m *mockConsumerProvider) GetStatsReport() jetstream.StatsReport {
	return m.report
}

// --- Tests ---

func TestNew(t *testing.T) {
	s := &mockStatsStore{}
	c := New(s, "")
	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.store != s {
		t.Error("store not set correctly")
	}
}

func TestIncrementFirehosePost(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	if got := c.GetFirehoseCount(); got != 0 {
		t.Fatalf("initial firehose count = %d, want 0", got)
	}

	c.IncrementFirehosePost()
	c.IncrementFirehosePost()
	c.IncrementFirehosePost()

	if got := c.GetFirehoseCount(); got != 3 {
		t.Fatalf("firehose count = %d, want 3", got)
	}
}

func TestFirehoseSinceAnalysis(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.IncrementFirehosePost()
	c.IncrementFirehosePost()

	if got := c.FirehoseSinceAnalysis(); got != 2 {
		t.Fatalf("FirehoseSinceAnalysis = %d, want 2", got)
	}
	// The underlying counter is cumulative: reading it must not reset it.
	if after := c.GetFirehoseCount(); after != 2 {
		t.Fatalf("after analysis read, firehose count = %d, want 2", after)
	}
	// A second read with no traffic in between yields nothing.
	if got := c.FirehoseSinceAnalysis(); got != 0 {
		t.Fatalf("second FirehoseSinceAnalysis = %d, want 0", got)
	}

	c.IncrementFirehosePost()
	if got := c.FirehoseSinceAnalysis(); got != 1 {
		t.Fatalf("third FirehoseSinceAnalysis = %d, want 1", got)
	}
}

// TestSwapFirehoseCountDelegates pins the deprecated wrapper to the new
// cursor-based behaviour, since cmd/hourstats/analysis.go still calls it.
func TestSwapFirehoseCountDelegates(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	for i := 0; i < 5; i++ {
		c.IncrementFirehosePost()
	}
	if got := c.SwapFirehoseCount(); got != 5 {
		t.Fatalf("SwapFirehoseCount = %d, want 5", got)
	}
	if after := c.GetFirehoseCount(); after != 5 {
		t.Fatalf("after SwapFirehoseCount, firehose count = %d, want 5 (counter must stay cumulative)", after)
	}
	if got := c.SwapFirehoseCount(); got != 0 {
		t.Fatalf("second SwapFirehoseCount = %d, want 0", got)
	}
}

// TestFirehoseCounterNoLossAcrossConsumers is the regression test for the bug
// that made english_posts_stored exceed total_firehose_posts in every
// cycle-end snapshot: the analysis cycle used to Swap(0) the shared counter,
// discarding every post counted since the last snapshot. Posts are
// incremented *between* the snapshot read and the analysis read, which is
// exactly the window the old code dropped. Both consumers must together
// account for every increment.
func TestFirehoseCounterNoLossAcrossConsumers(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")
	ctx := context.Background()

	var total, snapshotSum, analysisSum int64
	increment := func(n int) {
		for i := 0; i < n; i++ {
			c.IncrementFirehosePost()
		}
		total += int64(n)
	}

	increment(100)
	if err := c.TakeSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	snapshotSum += int64(ms.snapshots[0].TotalFirehosePosts)

	// Posts arriving in the gap the old implementation threw away.
	increment(30)
	analysisSum += c.FirehoseSinceAnalysis()

	increment(20)
	if err := c.TakeSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	snapshotSum += int64(ms.snapshots[1].TotalFirehosePosts)

	increment(7)
	analysisSum += c.FirehoseSinceAnalysis()
	if err := c.TakeSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	snapshotSum += int64(ms.snapshots[2].TotalFirehosePosts)

	if total != 157 {
		t.Fatalf("test setup: total = %d, want 157", total)
	}
	if snapshotSum != total {
		t.Errorf("snapshot deltas sum to %d, want %d (posts lost to the analysis cursor)", snapshotSum, total)
	}
	if analysisSum != total {
		t.Errorf("analysis deltas sum to %d, want %d (posts lost to the snapshot cursor)", analysisSum, total)
	}

	// Per-snapshot deltas: 100, then 30+20, then 7.
	for i, want := range []int{100, 50, 7} {
		if got := ms.snapshots[i].TotalFirehosePosts; got != want {
			t.Errorf("snapshot[%d].TotalFirehosePosts = %d, want %d", i, got, want)
		}
	}
}

func TestIncrementEnglishPost(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.IncrementEnglishPost(false) // root
	c.IncrementEnglishPost(false) // root
	c.IncrementEnglishPost(true)  // reply

	if got := c.englishPosts.Load(); got != 3 {
		t.Fatalf("englishPosts = %d, want 3", got)
	}
	if got := c.rootPosts.Load(); got != 2 {
		t.Fatalf("rootPosts = %d, want 2", got)
	}
	if got := c.replyPosts.Load(); got != 1 {
		t.Fatalf("replyPosts = %d, want 1", got)
	}
}

func TestIncrementDroppedPosts(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.IncrementDroppedPosts(5)
	c.IncrementDroppedPosts(3)

	got := c.SwapDroppedPosts()
	if got != 8 {
		t.Fatalf("SwapDroppedPosts = %d, want 8", got)
	}
	if after := c.droppedPosts.Load(); after != 0 {
		t.Fatalf("after swap, dropped = %d, want 0", after)
	}
}

func TestLastPostReceived(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	// Before any posts, should return zero time
	if got := c.LastPostReceived(); !got.IsZero() {
		t.Fatalf("initial LastPostReceived = %v, want zero", got)
	}

	c.IncrementFirehosePost()
	lpr := c.LastPostReceived()
	if lpr.IsZero() {
		t.Fatal("LastPostReceived is zero after IncrementFirehosePost")
	}

	// Should be within the last second
	if time.Since(lpr) > time.Second {
		t.Fatalf("LastPostReceived = %v, too old", lpr)
	}
}

func TestSetConsumer(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	// Initially nil — TakeSnapshot should not panic
	c.SetConsumer(nil)

	provider := &mockConsumerProvider{
		report: jetstream.StatsReport{ActiveEndpoint: "wss://test"},
	}
	c.SetConsumer(provider)

	c.mu.RLock()
	got := c.provider
	c.mu.RUnlock()

	if got != provider {
		t.Fatal("SetConsumer did not store provider")
	}

	// Set back to nil (simulates consumer restart)
	c.SetConsumer(nil)
	c.mu.RLock()
	got = c.provider
	c.mu.RUnlock()
	if got != nil {
		t.Fatal("SetConsumer(nil) did not clear provider")
	}
}

func TestRecordAnalysis(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.RecordAnalysis(1000, 950, 5, "positive", false)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.lastAnalysis.ran {
		t.Error("lastAnalysis.ran should be true")
	}
	if c.lastAnalysis.postsConsidered != 1000 {
		t.Errorf("postsConsidered = %d, want 1000", c.lastAnalysis.postsConsidered)
	}
	if c.lastAnalysis.postsHydrated != 950 {
		t.Errorf("postsHydrated = %d, want 950", c.lastAnalysis.postsHydrated)
	}
	if c.lastAnalysis.hydrationErrors != 5 {
		t.Errorf("hydrationErrors = %d, want 5", c.lastAnalysis.hydrationErrors)
	}
	if c.lastAnalysis.sentimentResult != "positive" {
		t.Errorf("sentimentResult = %q, want %q", c.lastAnalysis.sentimentResult, "positive")
	}
	if c.lastAnalysis.postingSkipped {
		t.Error("postingSkipped should be false")
	}
}

func TestTakeSnapshot_WithProvider(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")

	provider := &mockConsumerProvider{
		report: jetstream.StatsReport{
			EventsReceived:    100,
			PostsProcessed:    80,
			EventsSkipped:     20,
			Reconnects:        2,
			Errors:            1,
			EndpointRotations: 1,
			ActiveEndpoint:    "wss://jetstream2.us-west.bsky.network/subscribe",
			ConnectionUptime:  30 * time.Minute,
		},
	}
	c.SetConsumer(provider)

	// Simulate some traffic
	for i := 0; i < 60; i++ {
		c.IncrementFirehosePost()
	}
	for i := 0; i < 30; i++ {
		c.IncrementEnglishPost(false)
	}
	for i := 0; i < 10; i++ {
		c.IncrementEnglishPost(true)
	}
	c.IncrementDroppedPosts(3)
	c.RecordAnalysis(500, 480, 2, "satisfied", false)

	err := c.TakeSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TakeSnapshot error: %v", err)
	}

	if len(ms.snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(ms.snapshots))
	}

	snap := ms.snapshots[0]
	if snap.ID != 1 {
		t.Errorf("snapshot ID = %d, want 1", snap.ID)
	}
	if snap.ActiveEndpoint != "wss://jetstream2.us-west.bsky.network/subscribe" {
		t.Errorf("ActiveEndpoint = %q", snap.ActiveEndpoint)
	}
	if snap.EventsReceived != 100 {
		t.Errorf("EventsReceived = %d, want 100", snap.EventsReceived)
	}
	if snap.EnglishPostsStored != 40 {
		t.Errorf("EnglishPostsStored = %d, want 40", snap.EnglishPostsStored)
	}
	if snap.RootPosts != 30 {
		t.Errorf("RootPosts = %d, want 30", snap.RootPosts)
	}
	if snap.ReplyPosts != 10 {
		t.Errorf("ReplyPosts = %d, want 10", snap.ReplyPosts)
	}
	if snap.DroppedPosts != 3 {
		t.Errorf("DroppedPosts = %d, want 3", snap.DroppedPosts)
	}
	if snap.AnalysisRan != 1 {
		t.Errorf("AnalysisRan = %d, want 1", snap.AnalysisRan)
	}
	if snap.PostsConsidered != 500 {
		t.Errorf("PostsConsidered = %d, want 500", snap.PostsConsidered)
	}
	if snap.SentimentResult != "satisfied" {
		t.Errorf("SentimentResult = %q, want %q", snap.SentimentResult, "satisfied")
	}

	// PostsPerMinuteAvg = 40 / 30.0
	expectedPPM := 40.0 / 30.0
	if snap.PostsPerMinuteAvg != expectedPPM {
		t.Errorf("PostsPerMinuteAvg = %f, want %f", snap.PostsPerMinuteAvg, expectedPPM)
	}

	// Counters should be reset after snapshot
	if c.englishPosts.Load() != 0 {
		t.Errorf("englishPosts not reset after snapshot: %d", c.englishPosts.Load())
	}
	if c.rootPosts.Load() != 0 {
		t.Errorf("rootPosts not reset after snapshot: %d", c.rootPosts.Load())
	}

	// Analysis state should be reset
	c.mu.RLock()
	analysisRan := c.lastAnalysis.ran
	c.mu.RUnlock()
	if analysisRan {
		t.Error("lastAnalysis.ran should be reset to false after snapshot")
	}
}

func TestTakeSnapshot_NilProvider(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")

	// No provider set — should use zeros and not panic
	c.IncrementFirehosePost()
	c.IncrementEnglishPost(false)

	err := c.TakeSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TakeSnapshot with nil provider error: %v", err)
	}

	if len(ms.snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(ms.snapshots))
	}

	snap := ms.snapshots[0]
	if snap.ActiveEndpoint != "" {
		t.Errorf("ActiveEndpoint should be empty with nil provider, got %q", snap.ActiveEndpoint)
	}
	if snap.EventsReceived != 0 {
		t.Errorf("EventsReceived should be 0 with nil provider, got %d", snap.EventsReceived)
	}
	if snap.EnglishPostsStored != 1 {
		t.Errorf("EnglishPostsStored = %d, want 1", snap.EnglishPostsStored)
	}
}

func TestTakeSnapshot_DeltaComputation(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")

	provider := &mockConsumerProvider{
		report: jetstream.StatsReport{
			EventsReceived: 50,
			PostsProcessed: 40,
		},
	}
	c.SetConsumer(provider)

	// First snapshot — deltas from zero
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ms.snapshots[0].EventsReceived != 50 {
		t.Errorf("first snapshot EventsReceived = %d, want 50", ms.snapshots[0].EventsReceived)
	}

	provider.report.EventsReceived = 120
	provider.report.PostsProcessed = 100

	// Second snapshot — delta should be 120-50=70
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ms.snapshots[1].EventsReceived != 70 {
		t.Errorf("second snapshot EventsReceived = %d, want 70", ms.snapshots[1].EventsReceived)
	}
	if ms.snapshots[1].PostsProcessed != 60 {
		t.Errorf("second snapshot PostsProcessed = %d, want 60", ms.snapshots[1].PostsProcessed)
	}
}

// TestTakeSnapshot_FirehoseCounterReset verifies the snapshot delta is
// unaffected by the analysis cycle reading the counter out-of-band.
func TestTakeSnapshot_FirehoseCounterReset(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")

	for i := 0; i < 100; i++ {
		c.IncrementFirehosePost()
	}
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ms.snapshots[0].TotalFirehosePosts; got != 100 {
		t.Fatalf("first snapshot TotalFirehosePosts = %d, want 100", got)
	}

	// Analysis cycle resets the counter out-of-band.
	c.SwapFirehoseCount()

	for i := 0; i < 30; i++ {
		c.IncrementFirehosePost()
	}
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ms.snapshots[1].TotalFirehosePosts; got != 30 {
		t.Fatalf("second snapshot TotalFirehosePosts = %d, want 30 (got negative means regression)", got)
	}
}

// TestTakeSnapshot_EarlyRejectedNonEnglish covers the counter that makes the
// true Jetstream post volume reconstructible: total_firehose_posts only counts
// posts that reach the language filter, so without this the pre-filter
// rejections were invisible.
func TestTakeSnapshot_EarlyRejectedNonEnglish(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")
	provider := &mockConsumerProvider{
		report: jetstream.StatsReport{EarlyRejectedNonEnglish: 4000},
	}
	c.SetConsumer(provider)

	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ms.snapshots[0].EarlyRejectedNonEnglish; got != 4000 {
		t.Errorf("first snapshot EarlyRejectedNonEnglish = %d, want 4000", got)
	}

	provider.report.EarlyRejectedNonEnglish = 6500
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ms.snapshots[1].EarlyRejectedNonEnglish; got != 2500 {
		t.Errorf("second snapshot EarlyRejectedNonEnglish = %d, want 2500 (delta)", got)
	}
}

// TestTakeSnapshot_PostsPerMinuteUsesElapsedTime pins the rate to the measured
// interval. The previous code always divided by 30, so an off-schedule
// snapshot (the analysis cycle takes one right after the ticker's) reported a
// rate that was wrong by the ratio of the real gap to 30 minutes.
func TestTakeSnapshot_PostsPerMinuteUsesElapsedTime(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")
	ctx := context.Background()

	// First snapshot has no predecessor and falls back to the nominal window.
	for i := 0; i < 60; i++ {
		c.IncrementEnglishPost(false)
	}
	if err := c.TakeSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := ms.snapshots[0].PostsPerMinuteAvg, 60.0/30.0; got != want {
		t.Errorf("first snapshot PostsPerMinuteAvg = %f, want %f", got, want)
	}

	// Backdate the cursor so the next snapshot measures a 10-minute gap.
	c.lastSeen.snapshotAt = time.Now().UTC().Add(-10 * time.Minute)
	for i := 0; i < 500; i++ {
		c.IncrementEnglishPost(false)
	}
	if err := c.TakeSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	// 500 posts over ~10 minutes is ~50/min, not the 500/30 = 16.7 the fixed
	// divisor produced.
	got := ms.snapshots[1].PostsPerMinuteAvg
	if got < 49.0 || got > 51.0 {
		t.Errorf("second snapshot PostsPerMinuteAvg = %f, want ~50 (500 posts / 10 min)", got)
	}
}

// TestTakeSnapshot_ConsumerRestartGeneration covers runJetstream rebuilding
// the Consumer after a fatal error: the new consumer's atomics start at zero,
// which used to produce large negative deltas for every consumer stat.
func TestTakeSnapshot_ConsumerRestartGeneration(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")
	provider := &mockConsumerProvider{
		report: jetstream.StatsReport{
			EventsReceived:          10000,
			PostsProcessed:          8000,
			EventsSkipped:           2000,
			Errors:                  7,
			Reconnects:              3,
			EndpointRotations:       2,
			EarlyRejectedNonEnglish: 5000,
		},
	}
	c.SetConsumer(provider)
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Fresh consumer: every counter restarts from zero and climbs a little.
	c.SetConsumer(&mockConsumerProvider{
		report: jetstream.StatsReport{
			EventsReceived:          120,
			PostsProcessed:          90,
			EventsSkipped:           30,
			Errors:                  1,
			Reconnects:              1,
			EndpointRotations:       0,
			EarlyRejectedNonEnglish: 60,
		},
	})
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}

	snap := ms.snapshots[1]
	checks := []struct {
		name string
		got  int
		want int
	}{
		{"EventsReceived", snap.EventsReceived, 120},
		{"PostsProcessed", snap.PostsProcessed, 90},
		{"EventsSkipped", snap.EventsSkipped, 30},
		{"ConsumerErrors", snap.ConsumerErrors, 1},
		{"ReconnectCount", snap.ReconnectCount, 1},
		{"EndpointRotations", snap.EndpointRotations, 0},
		{"EarlyRejectedNonEnglish", snap.EarlyRejectedNonEnglish, 60},
	}
	for _, ch := range checks {
		if ch.got != ch.want {
			t.Errorf("%s after consumer restart = %d, want %d", ch.name, ch.got, ch.want)
		}
	}

	// The third snapshot must resume normal differencing against the new
	// generation, not against the pre-restart values.
	c.SetConsumer(&mockConsumerProvider{
		report: jetstream.StatsReport{EventsReceived: 200, PostsProcessed: 150},
	})
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ms.snapshots[2].EventsReceived; got != 80 {
		t.Errorf("third snapshot EventsReceived = %d, want 80", got)
	}
}

func TestCounterDelta(t *testing.T) {
	tests := []struct {
		name             string
		current, lastSee int64
		want             int64
	}{
		{"normal increase", 150, 100, 50},
		{"no change", 100, 100, 0},
		{"from zero", 42, 0, 42},
		{"counter restarted", 30, 100, 30},
		{"restarted to zero", 0, 100, 0},
	}
	for _, tt := range tests {
		if got := counterDelta(tt.current, tt.lastSee); got != tt.want {
			t.Errorf("%s: counterDelta(%d, %d) = %d, want %d", tt.name, tt.current, tt.lastSee, got, tt.want)
		}
	}
}

func TestLogEvent(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")

	err := c.LogEvent(context.Background(), "app_start", "profile=staging")
	if err != nil {
		t.Fatalf("LogEvent error: %v", err)
	}

	if len(ms.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(ms.events))
	}

	e := ms.events[0]
	if e.EventType != "app_start" {
		t.Errorf("EventType = %q, want %q", e.EventType, "app_start")
	}
	if e.Details != "profile=staging" {
		t.Errorf("Details = %q, want %q", e.Details, "profile=staging")
	}
	if e.EventTime.IsZero() {
		t.Error("EventTime should not be zero")
	}
}

func TestLogEvent_StoreError(t *testing.T) {
	ms := &mockStatsStore{eventErr: context.DeadlineExceeded}
	c := New(ms, "")

	err := c.LogEvent(context.Background(), "test", "details")
	if err == nil {
		t.Fatal("expected error from LogEvent when store fails")
	}
}

func TestIncrementSlowFlush(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.IncrementSlowFlush(500)
	c.IncrementSlowFlush(1200)
	c.IncrementSlowFlush(800)

	count := c.slowFlushCount.Load()
	if count != 3 {
		t.Errorf("slowFlushCount = %d, want 3", count)
	}
	maxMs := c.slowFlushMaxMs.Load()
	if maxMs != 1200 {
		t.Errorf("slowFlushMaxMs = %d, want 1200", maxMs)
	}
}

func TestRecordCycleDuration(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.RecordCycleDuration(45000)
	c.mu.RLock()
	got := c.lastAnalysis.cycleDurationMs
	c.mu.RUnlock()
	if got != 45000 {
		t.Errorf("cycleDurationMs = %d, want 45000", got)
	}
}

func TestRecordTrendingDuration(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.RecordTrendingDuration(12000)
	c.mu.RLock()
	got := c.lastAnalysis.trendingDurationMs
	c.mu.RUnlock()
	if got != 12000 {
		t.Errorf("trendingDurationMs = %d, want 12000", got)
	}
}

func TestSetWriteChannelFunc(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.SetWriteChannelFunc(func() int { return 42 })
	c.mu.RLock()
	depth := c.writeChDepthFn()
	c.mu.RUnlock()
	if depth != 42 {
		t.Errorf("writeChDepthFn() = %d, want 42", depth)
	}
}

func TestTakeSnapshotIncludesHealthMetrics(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")
	c.SetConsumer(&mockConsumerProvider{report: jetstream.StatsReport{
		ActiveEndpoint: "wss://test",
	}})
	c.SetWriteChannelFunc(func() int { return 100 })
	c.IncrementSlowFlush(2000)
	c.RecordCycleDuration(30000)
	c.RecordTrendingDuration(5000)

	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}

	if len(ms.snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(ms.snapshots))
	}
	snap := ms.snapshots[0]

	if snap.SysBytes == 0 {
		t.Error("SysBytes should be >0 (runtime.MemStats.Sys)")
	}
	if snap.StackInuseBytes == 0 {
		t.Error("StackInuseBytes should be >0 (runtime.MemStats.StackInuse)")
	}
	if snap.HeapReleasedBytes < 0 {
		t.Errorf("HeapReleasedBytes = %d, want >= 0", snap.HeapReleasedBytes)
	}
	// RSS comes from procfs, which only exists on Linux; 0 elsewhere.
	if snap.RSSBytes < 0 {
		t.Errorf("RSSBytes = %d, want >= 0", snap.RSSBytes)
	}
	if runtime.GOOS == "linux" && snap.RSSBytes == 0 {
		t.Error("RSSBytes = 0 on linux, want > 0")
	}
	if snap.GoroutineCount == 0 {
		t.Error("GoroutineCount should be >0")
	}
	if snap.WriteChannelDepth != 100 {
		t.Errorf("WriteChannelDepth = %d, want 100", snap.WriteChannelDepth)
	}
	if snap.SlowFlushCount != 1 {
		t.Errorf("SlowFlushCount = %d, want 1", snap.SlowFlushCount)
	}
	if snap.SlowFlushMaxMs != 2000 {
		t.Errorf("SlowFlushMaxMs = %d, want 2000", snap.SlowFlushMaxMs)
	}
	if snap.CycleDurationMs != 30000 {
		t.Errorf("CycleDurationMs = %d, want 30000", snap.CycleDurationMs)
	}
	if snap.TrendingDurationMs != 5000 {
		t.Errorf("TrendingDurationMs = %d, want 5000", snap.TrendingDurationMs)
	}
}

func TestBoolToInt(t *testing.T) {
	tests := []struct {
		in   bool
		want int
	}{
		{true, 1},
		{false, 0},
	}
	for _, tt := range tests {
		if got := boolToInt(tt.in); got != tt.want {
			t.Errorf("boolToInt(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
