package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

// The grouper's spellings of one subject across hours, with and without the
// year and the country, count as one subject under topics.SubjectKey, which
// the internal tests cannot import (internal/topics imports this package).
func TestGetTopRankOneSubjectForRange_SubjectKey(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	at := time.Date(2026, 11, 2, 0, 55, 0, 0, time.UTC)
	add := func(label string, n int) {
		t.Helper()
		for range n {
			at = at.Add(time.Hour)
			dp := store.SentimentDataPoint{RunID: "run-" + at.Format(time.RFC3339), Timestamp: at, TopTopic: label, TotalPosts: 500}
			if err := s.StoreSentimentDataPoint(ctx, dp); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("2024 US Presidential Election", 3)
	add("US Presidential Elections", 2)
	add("Brazilian Presidential Election", 4)

	label, n, err := s.GetTopRankOneSubjectForRange(ctx, "2026-11-02", "2026-11-08", 500, topics.SubjectKey)
	if err != nil || label != "2024 US Presidential Election" || n != 5 {
		t.Errorf("top = %q, %d, %v; want 2024 US Presidential Election, 5", label, n, err)
	}
}
