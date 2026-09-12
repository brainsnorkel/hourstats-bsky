package store

import (
	"context"
	"testing"
	"time"
)

// TestSnapshotCarriesIntakeClamps: the four firehose clamp counters must
// survive a round trip through every reader, each of which selects its own
// column list.
func TestSnapshotCarriesIntakeClamps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	snap := &StatsSnapshot{
		SnapshotTime:   now,
		ActiveEndpoint: "wss://jetstream.us-west.bsky.network",
		OversizedPosts: 3,
		CappedPosts:    11,
		FuturePosts:    5,
		DeniedPosts:    7,
	}
	if err := s.InsertStatsSnapshot(ctx, snap); err != nil {
		t.Fatalf("InsertStatsSnapshot: %v", err)
	}

	check := func(what string, got StatsSnapshot) {
		t.Helper()
		if got.OversizedPosts != 3 || got.CappedPosts != 11 || got.FuturePosts != 5 || got.DeniedPosts != 7 {
			t.Errorf("%s clamps = {oversized %d, capped %d, future %d, denied %d}, want {3 11 5 7}",
				what, got.OversizedPosts, got.CappedPosts, got.FuturePosts, got.DeniedPosts)
		}
	}

	latest, err := s.GetLatestSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetLatestSnapshot: %v", err)
	}
	if latest == nil {
		t.Fatal("GetLatestSnapshot = nil, want the row just inserted")
	}
	check("GetLatestSnapshot", *latest)

	since := now.Add(-time.Hour)
	history, err := s.GetSnapshotHistory(ctx, since, 10)
	if err != nil {
		t.Fatalf("GetSnapshotHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("GetSnapshotHistory returned %d rows, want 1", len(history))
	}
	check("GetSnapshotHistory", history[0])

	health, err := s.GetHealthHistory(ctx, since, 10)
	if err != nil {
		t.Fatalf("GetHealthHistory: %v", err)
	}
	if len(health) != 1 {
		t.Fatalf("GetHealthHistory returned %d rows, want 1", len(health))
	}
	check("GetHealthHistory", health[0])
}

// TestIntakeClampColumnsDefaultToZero: the columns arrive by ALTER TABLE, so
// every row written before them reads as zero rather than NULL.
func TestIntakeClampColumnsDefaultToZero(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO stats_snapshots (snapshot_time, active_endpoint) VALUES (?, ?)`,
		timeToStr(time.Now().UTC()), "wss://legacy",
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	latest, err := s.GetLatestSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetLatestSnapshot: %v", err)
	}
	if latest.OversizedPosts != 0 || latest.CappedPosts != 0 || latest.FuturePosts != 0 || latest.DeniedPosts != 0 {
		t.Errorf("legacy row clamps = {%d %d %d %d}, want all zero",
			latest.OversizedPosts, latest.CappedPosts, latest.FuturePosts, latest.DeniedPosts)
	}
}
