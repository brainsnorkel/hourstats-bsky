package stats

import (
	"context"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/jetstream"
)

// TestOversizedPostCounter: the oversized count is the callback's own, since
// the consumer has no opinion on a record's text.
func TestOversizedPostCounter(t *testing.T) {
	c := New(&mockStatsStore{}, "")

	c.IncrementOversizedPosts()
	c.IncrementOversizedPosts()

	if got := c.SwapOversizedPosts(); got != 2 {
		t.Errorf("SwapOversizedPosts = %d, want 2", got)
	}
	if got := c.SwapOversizedPosts(); got != 0 {
		t.Errorf("SwapOversizedPosts after reset = %d, want 0", got)
	}
}

// TestTakeSnapshotIncludesIntakeClamps: all four clamps land on the snapshot,
// and each is per interval — the three consumer counters as wire deltas, the
// oversized one as a swap.
func TestTakeSnapshotIncludesIntakeClamps(t *testing.T) {
	ms := &mockStatsStore{}
	c := New(ms, "")
	provider := &mockConsumerProvider{
		report: jetstream.StatsReport{
			EventsReceived: 10,
			PostsCapped:    4,
			PostsFuture:    2,
			PostsDenied:    6,
		},
	}
	c.SetConsumer(provider)
	c.IncrementOversizedPosts()

	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	first := ms.snapshots[0]
	if first.OversizedPosts != 1 || first.CappedPosts != 4 || first.FuturePosts != 2 || first.DeniedPosts != 6 {
		t.Errorf("first snapshot clamps = {oversized %d, capped %d, future %d, denied %d}, want {1 4 2 6}",
			first.OversizedPosts, first.CappedPosts, first.FuturePosts, first.DeniedPosts)
	}

	// A second snapshot with no new drops reports zero, not the running total.
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatalf("TakeSnapshot (second): %v", err)
	}
	second := ms.snapshots[1]
	if second.OversizedPosts != 0 || second.CappedPosts != 0 || second.FuturePosts != 0 || second.DeniedPosts != 0 {
		t.Errorf("second snapshot clamps = {%d %d %d %d}, want all zero",
			second.OversizedPosts, second.CappedPosts, second.FuturePosts, second.DeniedPosts)
	}

	// Only the growth since the previous snapshot counts.
	provider.report.PostsCapped = 9
	provider.report.PostsDenied = 6
	if err := c.TakeSnapshot(context.Background()); err != nil {
		t.Fatalf("TakeSnapshot (third): %v", err)
	}
	third := ms.snapshots[2]
	if third.CappedPosts != 5 {
		t.Errorf("third snapshot CappedPosts = %d, want 5", third.CappedPosts)
	}
	if third.DeniedPosts != 0 {
		t.Errorf("third snapshot DeniedPosts = %d, want 0", third.DeniedPosts)
	}
}
