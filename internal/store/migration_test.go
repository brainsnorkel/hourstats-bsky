package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// TestMigrate_PreservesExistingDailySentiment opens a database created on the
// schema that predates the report rollups and checks that every existing
// daily_sentiment row survives the ALTER untouched, with the new column at
// its default.
func TestMigrate_PreservesExistingDailySentiment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE daily_sentiment (
			date TEXT PRIMARY KEY, run_id TEXT NOT NULL,
			average_sentiment REAL NOT NULL DEFAULT 0, min_sentiment REAL NOT NULL DEFAULT 0,
			max_sentiment REAL NOT NULL DEFAULT 0, q1_sentiment REAL NOT NULL DEFAULT 0,
			median_sentiment REAL NOT NULL DEFAULT 0, q3_sentiment REAL NOT NULL DEFAULT 0,
			total_runs INTEGER NOT NULL DEFAULT 0, total_posts INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			ttl INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO daily_sentiment (date, run_id, average_sentiment, min_sentiment, max_sentiment, q1_sentiment, median_sentiment, q3_sentiment, total_runs, total_posts, created_at, ttl)
		 VALUES ('2026-08-01', 'daily-2026-08-01', 10.5, 8.1, 12.9, 9.5, 10.4, 11.2, 24, 1900000, '2026-08-02T00:00:05Z', 1)`,
		`INSERT INTO daily_sentiment (date, run_id, average_sentiment, min_sentiment, max_sentiment, q1_sentiment, median_sentiment, q3_sentiment, total_runs, total_posts, created_at, ttl)
		 VALUES ('2026-08-02', 'daily-2026-08-02', -1.25, -4, 2, -2, -1, 0, 23, 1850000, '2026-08-03T00:00:05Z', 1)`,
	}
	for _, q := range stmts {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q[:40], err)
		}
	}
	raw.Close()

	s, err := New(path)
	if err != nil {
		t.Fatalf("New on old schema: %v", err)
	}
	defer s.Close()
	rows, err := s.GetDailySentimentRange(context.Background(), "2026-08-01", "2026-08-02")
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d, %v", len(rows), err)
	}
	a := rows[0]
	if a.RunID != "daily-2026-08-01" || a.AverageSentiment != 10.5 || a.MinSentiment != 8.1 || a.MaxSentiment != 12.9 ||
		a.Q1Sentiment != 9.5 || a.MedianSentiment != 10.4 || a.Q3Sentiment != 11.2 || a.TotalRuns != 24 || a.TotalPosts != 1900000 ||
		a.TotalFirehosePosts != 0 || a.CreatedAt.Format(time.RFC3339) != "2026-08-02T00:00:05Z" {
		t.Errorf("row 1 altered by migration: %+v", a)
	}
	if b := rows[1]; b.AverageSentiment != -1.25 || b.TotalRuns != 23 || b.TotalFirehosePosts != 0 {
		t.Errorf("row 2 altered by migration: %+v", b)
	}

	// Opening a second time re-runs the ALTER and must be a no-op.
	s.Close()
	s2, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if rows, _ := s2.GetDailySentimentRange(context.Background(), "2026-08-01", "2026-08-02"); len(rows) != 2 {
		t.Errorf("rows after reopen = %d", len(rows))
	}
}

// TestBackupCoversLongRetentionTables pins every table whose rows cannot be
// rebuilt from the firehose into the essential-tables backup list.
func TestBackupCoversLongRetentionTables(t *testing.T) {
	have := map[string]bool{}
	for _, tbl := range essentialTables {
		have[tbl] = true
	}
	for _, want := range []string{"sentiment_history", "daily_sentiment", "topic_daily", "daily_top_post", "key_value"} {
		if !have[want] {
			t.Errorf("essentialTables missing %s", want)
		}
	}
}

func TestDayCycleTotalsAndMissingFirehose(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for h := 0; h < 24; h++ {
		posts, fh := 1000, 2500
		if h == 3 {
			posts = 10 // low-confidence cycle, excluded from the daily aggregate
		}
		dp := SentimentDataPoint{RunID: "r" + day.Add(time.Duration(h)*time.Hour).Format("15"), Timestamp: day.Add(time.Duration(h) * time.Hour), TotalPosts: posts, TotalFirehosePosts: fh}
		if err := s.StoreSentimentDataPoint(ctx, dp); err != nil {
			t.Fatal(err)
		}
	}
	// Next day's first cycle must not leak into the 1st.
	if err := s.StoreSentimentDataPoint(ctx, SentimentDataPoint{RunID: "next", Timestamp: day.Add(24 * time.Hour), TotalPosts: 1000, TotalFirehosePosts: 9999}); err != nil {
		t.Fatal(err)
	}
	// Cycle count and English total exclude the low-confidence cycle; the
	// firehose total counts every cycle of the day.
	tot, err := s.GetDayCycleTotals(ctx, "2026-09-01", 500)
	if err != nil || tot.Cycles != 23 || tot.EnglishPosts != 23000 || tot.FirehosePosts != 24*2500 {
		t.Fatalf("totals = %+v, %v", tot, err)
	}

	for _, d := range []DailySentimentDataPoint{
		{Date: "2026-09-01", RunID: "a", TotalRuns: 23, TotalPosts: 23000},
		{Date: "2026-09-02", RunID: "b", TotalRuns: 24, TotalPosts: 24000, TotalFirehosePosts: 5},
		{Date: "2026-09-03", RunID: "c", TotalRuns: 24, TotalPosts: 24000},
	} {
		if err := s.StoreDailySentiment(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := s.GetDailySentimentMissingFirehose(ctx, "2026-09-01")
	if err != nil || len(missing) != 2 || missing[0].Date != "2026-09-01" || missing[1].Date != "2026-09-03" {
		t.Fatalf("missing = %+v, %v", missing, err)
	}
	if missing, _ := s.GetDailySentimentMissingFirehose(ctx, "2026-09-03"); len(missing) != 1 {
		t.Errorf("missing since 09-03 = %d", len(missing))
	}
}
