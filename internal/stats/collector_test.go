package stats

import (
	"context"
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

func TestSwapFirehoseCount(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.IncrementFirehosePost()
	c.IncrementFirehosePost()

	got := c.SwapFirehoseCount()
	if got != 2 {
		t.Fatalf("SwapFirehoseCount = %d, want 2", got)
	}
	if after := c.GetFirehoseCount(); after != 0 {
		t.Fatalf("after swap, firehose count = %d, want 0", after)
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

// TestTakeSnapshot_FirehoseCounterReset verifies the collector handles
// SwapFirehoseCount() being called out-of-band by the analysis cycle.
// Without the reset-detection, the second snapshot's delta would be
// current(30) - lastSeen(100) = -70, matching the negative values seen
// in prod/staging.
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
