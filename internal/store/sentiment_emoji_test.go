package store

import (
	"context"
	"testing"
	"time"
)

func TestSentimentHistory_EmojiPctRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	emojiPct := 17.25
	withValue := SentimentDataPoint{
		RunID:                "run-emoji",
		Timestamp:            now,
		NetSentimentPercent:  12.5,
		TotalPosts:           500,
		NetSentimentPctEmoji: &emojiPct,
	}
	withNil := SentimentDataPoint{
		RunID:               "run-nil",
		Timestamp:           now.Add(time.Minute),
		NetSentimentPercent: 9.5,
		TotalPosts:          400,
	}
	for _, dp := range []SentimentDataPoint{withValue, withNil} {
		if err := s.StoreSentimentDataPoint(ctx, dp); err != nil {
			t.Fatalf("StoreSentimentDataPoint(%s): %v", dp.RunID, err)
		}
	}

	byRun := readHistoryByRun(t, ctx, s, time.Hour)

	got, ok := byRun["run-emoji"]
	if !ok {
		t.Fatalf("run-emoji missing from history")
	}
	if got.NetSentimentPctEmoji == nil {
		t.Fatalf("run-emoji NetSentimentPctEmoji = nil, want %v", emojiPct)
	}
	if *got.NetSentimentPctEmoji != emojiPct {
		t.Errorf("run-emoji NetSentimentPctEmoji = %v, want %v", *got.NetSentimentPctEmoji, emojiPct)
	}

	got, ok = byRun["run-nil"]
	if !ok {
		t.Fatalf("run-nil missing from history")
	}
	if got.NetSentimentPctEmoji != nil {
		t.Errorf("run-nil NetSentimentPctEmoji = %v, want nil", *got.NetSentimentPctEmoji)
	}
}

// TestSentimentHistory_EmojiPctPreMigrationRow covers rows written before the
// column existed: the value must read back as nil, not as a real 0%.
func TestSentimentHistory_EmojiPctPreMigrationRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO sentiment_history (run_id, timestamp, average_compound_score, net_sentiment_percent, sentiment_category, total_posts, created_at, ttl)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"run-legacy", timeToStr(now), 0.11, 11.0, "positive", 600, nowUTC(),
		time.Now().UTC().Add(8*24*time.Hour).Unix(),
	)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	got, ok := readHistoryByRun(t, ctx, s, time.Hour)["run-legacy"]
	if !ok {
		t.Fatalf("run-legacy missing from history")
	}
	if got.NetSentimentPctEmoji != nil {
		t.Errorf("legacy NetSentimentPctEmoji = %v, want nil", *got.NetSentimentPctEmoji)
	}
	if got.NetSentimentPercent != 11.0 {
		t.Errorf("legacy NetSentimentPercent = %v, want 11", got.NetSentimentPercent)
	}
}

func readHistoryByRun(t *testing.T, ctx context.Context, s *Store, d time.Duration) map[string]SentimentDataPoint {
	t.Helper()
	hist, err := s.GetSentimentHistory(ctx, d)
	if err != nil {
		t.Fatalf("GetSentimentHistory: %v", err)
	}
	byRun := make(map[string]SentimentDataPoint, len(hist))
	for _, dp := range hist {
		byRun[dp.RunID] = dp
	}
	return byRun
}
