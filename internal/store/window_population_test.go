package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestWindowPopulationColumnsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateRun(ctx, RunState{
		RunID:               "run-pop",
		Status:              "complete",
		CutoffTime:          time.Now().UTC(),
		TotalPostsRetrieved: 192928,
		WindowPosts:         193985,
		HydratedPosts:       145543,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	run, err := s.GetRun(ctx, "run-pop")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.TotalPostsRetrieved != 192928 || run.WindowPosts != 193985 || run.HydratedPosts != 145543 {
		t.Errorf("run counts = total %d window %d hydrated %d, want 192928 193985 145543",
			run.TotalPostsRetrieved, run.WindowPosts, run.HydratedPosts)
	}

	ts := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	if err := s.StoreSentimentDataPoint(ctx, SentimentDataPoint{
		RunID:         "run-pop",
		Timestamp:     ts,
		TotalPosts:    192928,
		WindowPosts:   193985,
		HydratedPosts: 145543,
	}); err != nil {
		t.Fatalf("StoreSentimentDataPoint: %v", err)
	}
	points, err := s.GetSentimentHistory(ctx, time.Hour)
	if err != nil {
		t.Fatalf("GetSentimentHistory: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("len(points) = %d, want 1", len(points))
	}
	p := points[0]
	if p.TotalPosts != 192928 || p.WindowPosts != 193985 || p.HydratedPosts != 145543 {
		t.Errorf("sentiment counts = total %d window %d hydrated %d, want 192928 193985 145543",
			p.TotalPosts, p.WindowPosts, p.HydratedPosts)
	}

	// The essential-tables backup copies whole tables, so the columns ride along.
	backupPath, err := s.Backup(ctx, t.TempDir(), "test", 7)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	dest, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer dest.Close()
	for _, table := range []string{"runs", "sentiment_history"} {
		var window, hydrated int
		if err := dest.QueryRowContext(ctx,
			`SELECT window_posts, hydrated_posts FROM `+table+` WHERE run_id = 'run-pop'`).Scan(&window, &hydrated); err != nil {
			t.Fatalf("read %s from backup: %v", table, err)
		}
		if window != 193985 || hydrated != 145543 {
			t.Errorf("%s backup = window %d hydrated %d, want 193985 145543", table, window, hydrated)
		}
	}
}
