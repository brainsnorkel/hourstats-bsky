package store

import (
	"context"
	"testing"
	"time"
)

func TestSentimentHistory_TopTopicRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	dp := SentimentDataPoint{RunID: "run-1", Timestamp: now, NetSentimentPercent: 12.5, TotalPosts: 500}
	if err := s.StoreSentimentDataPoint(ctx, dp); err != nil {
		t.Fatalf("store: %v", err)
	}

	snap := now.Format(time.RFC3339)
	if err := s.InsertTopicSnapshot(ctx, snap, 2, "t2", "Second topic", "", 10, "[]", "[]", "", "", false, ""); err != nil {
		t.Fatalf("insert rank 2: %v", err)
	}
	if err := s.InsertTopicSnapshot(ctx, snap, 1, "t1", "Top topic", "", 20, "[]", "[]", "", "", false, ""); err != nil {
		t.Fatalf("insert rank 1: %v", err)
	}

	label, err := s.GetTopicLabelAt(ctx, snap, 1)
	if err != nil || label != "Top topic" {
		t.Fatalf("GetTopicLabelAt = %q, %v; want %q", label, err, "Top topic")
	}
	if label, err := s.GetTopicLabelAt(ctx, "2000-01-01T00:00:00Z", 1); err != nil || label != "" {
		t.Fatalf("GetTopicLabelAt(missing) = %q, %v; want empty, nil", label, err)
	}

	if updated, err := s.SetSentimentTopTopic(ctx, "run-1", label); err != nil || !updated {
		t.Fatalf("set top topic: updated=%v, err=%v", updated, err)
	}
	hist, err := s.GetSentimentHistory(ctx, time.Hour)
	if err != nil || len(hist) != 1 {
		t.Fatalf("history = %d rows, %v", len(hist), err)
	}
	if hist[0].TopTopic != "Top topic" {
		t.Fatalf("TopTopic = %q, want %q", hist[0].TopTopic, "Top topic")
	}

	// Re-storing the same point without a topic must not erase the label.
	if err := s.StoreSentimentDataPoint(ctx, dp); err != nil {
		t.Fatalf("re-store: %v", err)
	}
	hist, _ = s.GetSentimentHistory(ctx, time.Hour)
	if hist[0].TopTopic != "Top topic" {
		t.Fatalf("TopTopic after re-store = %q, want preserved", hist[0].TopTopic)
	}

	// Setting a topic on an unknown run reports no update rather than an error.
	if updated, err := s.SetSentimentTopTopic(ctx, "run-missing", "x"); err != nil || updated {
		t.Fatalf("set on missing run: updated=%v, err=%v; want false, nil", updated, err)
	}
}
