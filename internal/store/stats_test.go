package store

import (
	"context"
	"testing"
	"time"
)

func TestInsertAndGetLatestSnapshot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	snap := &StatsSnapshot{
		SnapshotTime:            now,
		ActiveEndpoint:          "wss://jetstream1.us-west.bsky.network",
		EndpointRotations:       2,
		ReconnectCount:          5,
		ConnectionUptimeSeconds: 3600,
		EventsReceived:          100000,
		PostsProcessed:          50000,
		EventsSkipped:           25000,
		ConsumerErrors:          3,
		TotalFirehosePosts:      200000,
		EnglishPostsStored:      30000,
		RootPosts:               20000,
		ReplyPosts:              10000,
		PostsPerMinuteAvg:       120.5,
		AnalysisRan:             1,
		PostsConsidered:         500,
		PostsHydrated:           450,
		HydrationErrors:         2,
		SentimentResult:         "positive",
		PostingSkipped:          0,
		DroppedPosts:            10,
	}

	if err := s.InsertStatsSnapshot(ctx, snap); err != nil {
		t.Fatalf("InsertStatsSnapshot: %v", err)
	}
	if snap.ID == 0 {
		t.Error("expected non-zero ID after insert")
	}

	latest, err := s.GetLatestSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetLatestSnapshot: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if latest.ActiveEndpoint != snap.ActiveEndpoint {
		t.Errorf("ActiveEndpoint = %q, want %q", latest.ActiveEndpoint, snap.ActiveEndpoint)
	}
	if latest.EventsReceived != 100000 {
		t.Errorf("EventsReceived = %d, want 100000", latest.EventsReceived)
	}
	if latest.SentimentResult != "positive" {
		t.Errorf("SentimentResult = %q, want %q", latest.SentimentResult, "positive")
	}
}

func TestGetLatestSnapshot_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	snap, err := s.GetLatestSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetLatestSnapshot: %v", err)
	}
	if snap != nil {
		t.Errorf("expected nil snapshot on empty DB, got %+v", snap)
	}
}

func TestGetSnapshotHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		snap := &StatsSnapshot{
			SnapshotTime:    now.Add(time.Duration(i) * time.Minute),
			ActiveEndpoint:  "endpoint",
			EventsReceived:  i * 1000,
			SentimentResult: "neutral",
		}
		if err := s.InsertStatsSnapshot(ctx, snap); err != nil {
			t.Fatalf("InsertStatsSnapshot[%d]: %v", i, err)
		}
	}

	history, err := s.GetSnapshotHistory(ctx, now.Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("GetSnapshotHistory: %v", err)
	}
	if len(history) != 5 {
		t.Fatalf("expected 5 snapshots, got %d", len(history))
	}
	if history[0].EventsReceived != 4000 {
		t.Errorf("first (most recent) EventsReceived = %d, want 4000", history[0].EventsReceived)
	}

	limited, err := s.GetSnapshotHistory(ctx, now.Add(-time.Minute), 2)
	if err != nil {
		t.Fatalf("GetSnapshotHistory (limited): %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("expected 2 snapshots with limit, got %d", len(limited))
	}
}

func TestInsertAndGetEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	events := []*StatsEvent{
		{EventTime: now, EventType: "analysis_complete", Details: "ran in 5s"},
		{EventTime: now.Add(time.Minute), EventType: "backup_complete", Details: "backup ok"},
		{EventTime: now.Add(2 * time.Minute), EventType: "analysis_complete", Details: "ran in 3s"},
	}
	for i, e := range events {
		if err := s.InsertStatsEvent(ctx, e); err != nil {
			t.Fatalf("InsertStatsEvent[%d]: %v", i, err)
		}
		if e.ID == 0 {
			t.Errorf("event[%d] expected non-zero ID", i)
		}
	}

	all, err := s.GetEvents(ctx, now.Add(-time.Minute), "", 100)
	if err != nil {
		t.Fatalf("GetEvents (all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}

	filtered, err := s.GetEvents(ctx, now.Add(-time.Minute), "analysis_complete", 100)
	if err != nil {
		t.Fatalf("GetEvents (filtered): %v", err)
	}
	if len(filtered) != 2 {
		t.Errorf("expected 2 analysis_complete events, got %d", len(filtered))
	}

	limited, err := s.GetEvents(ctx, now.Add(-time.Minute), "", 1)
	if err != nil {
		t.Fatalf("GetEvents (limited): %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("expected 1 event with limit, got %d", len(limited))
	}
}

func TestPurgeExpiredStats(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	oldSnap := &StatsSnapshot{SnapshotTime: old, ActiveEndpoint: "old", SentimentResult: "neutral"}
	recentSnap := &StatsSnapshot{SnapshotTime: recent, ActiveEndpoint: "recent", SentimentResult: "neutral"}
	if err := s.InsertStatsSnapshot(ctx, oldSnap); err != nil {
		t.Fatalf("InsertStatsSnapshot (old): %v", err)
	}
	if err := s.InsertStatsSnapshot(ctx, recentSnap); err != nil {
		t.Fatalf("InsertStatsSnapshot (recent): %v", err)
	}

	oldEvent := &StatsEvent{EventTime: old, EventType: "old", Details: "old"}
	recentEvent := &StatsEvent{EventTime: recent, EventType: "recent", Details: "recent"}
	if err := s.InsertStatsEvent(ctx, oldEvent); err != nil {
		t.Fatalf("InsertStatsEvent (old): %v", err)
	}
	if err := s.InsertStatsEvent(ctx, recentEvent); err != nil {
		t.Fatalf("InsertStatsEvent (recent): %v", err)
	}

	deleted, err := s.PurgeExpiredStats(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("PurgeExpiredStats: %v", err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 deleted (1 snapshot + 1 event), got %d", deleted)
	}

	remaining, err := s.GetLatestSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetLatestSnapshot after purge: %v", err)
	}
	if remaining == nil {
		t.Fatal("expected recent snapshot to survive purge")
	}
	if remaining.ActiveEndpoint != "recent" {
		t.Errorf("expected recent snapshot to survive, got %q", remaining.ActiveEndpoint)
	}
}
