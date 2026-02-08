package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPostBuffer_InsertAndGetSince(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	post := Post{
		URI:       "at://did:plc:abc/app.bsky.feed.post/123",
		CID:       "cid123",
		Text:      "hello world",
		AuthorDID: "did:plc:abc",
		CreatedAt: now.Format(time.RFC3339),
	}

	if err := s.InsertPost(ctx, post); err != nil {
		t.Fatalf("InsertPost: %v", err)
	}

	posts, err := s.GetPostsSince(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostsSince: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}
	if posts[0].URI != post.URI {
		t.Errorf("URI = %q, want %q", posts[0].URI, post.URI)
	}
	if posts[0].Text != "hello world" {
		t.Errorf("Text = %q, want %q", posts[0].Text, "hello world")
	}
}

func TestPostBuffer_InsertBatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	var posts []Post
	for i := 0; i < 100; i++ {
		posts = append(posts, Post{
			URI:       fmt.Sprintf("at://did:plc:abc/app.bsky.feed.post/batch_%04d", i),
			CID:       "cid",
			Text:      "batch post",
			AuthorDID: "did:plc:abc",
			CreatedAt: now.Format(time.RFC3339),
		})
	}

	if err := s.InsertPostsBatch(ctx, posts); err != nil {
		t.Fatalf("InsertPostsBatch: %v", err)
	}

	count, err := s.GetPostCount(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostCount: %v", err)
	}
	if count != 100 {
		t.Fatalf("expected 100 posts, got %d", count)
	}
}

func TestPostBuffer_PurgeExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert a post with an old inserted_at by directly using SQL
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO post_buffer (uri, cid, text, author_did, created_at, inserted_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"at://did:plc:old/app.bsky.feed.post/old", "cid", "old post", "did:plc:old",
		time.Now().UTC().Add(-3*time.Hour).Format(time.RFC3339),
		time.Now().UTC().Add(-3*time.Hour).Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert old post: %v", err)
	}

	// Insert a recent post
	if err := s.InsertPost(ctx, Post{
		URI:       "at://did:plc:new/app.bsky.feed.post/new",
		CID:       "cid",
		Text:      "new post",
		AuthorDID: "did:plc:new",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("InsertPost: %v", err)
	}

	deleted, err := s.PurgeExpiredPosts(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("PurgeExpiredPosts: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Verify only new post remains
	count, err := s.GetPostCount(ctx, time.Time{})
	if err != nil {
		t.Fatalf("GetPostCount: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 remaining, got %d", count)
	}
}

func TestCursor_EmptyReturnsZero(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cursor, err := s.GetCursor(ctx)
	if err != nil {
		t.Fatalf("GetCursor: %v", err)
	}
	if cursor != 0 {
		t.Errorf("expected 0, got %d", cursor)
	}
}

func TestCursor_SaveAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCursor(ctx, 12345678); err != nil {
		t.Fatalf("SaveCursor: %v", err)
	}

	cursor, err := s.GetCursor(ctx)
	if err != nil {
		t.Fatalf("GetCursor: %v", err)
	}
	if cursor != 12345678 {
		t.Errorf("expected 12345678, got %d", cursor)
	}

	// Update cursor
	if err := s.SaveCursor(ctx, 99999999); err != nil {
		t.Fatalf("SaveCursor update: %v", err)
	}
	cursor, err = s.GetCursor(ctx)
	if err != nil {
		t.Fatalf("GetCursor after update: %v", err)
	}
	if cursor != 99999999 {
		t.Errorf("expected 99999999, got %d", cursor)
	}
}

func TestRuns_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	run := RunState{
		RunID:                   "run-123",
		Status:                  "initializing",
		AnalysisIntervalMinutes: 30,
		CutoffTime:              now.Add(-30 * time.Minute),
		TopPosts: []Post{
			{URI: "at://did:plc:abc/app.bsky.feed.post/1", CID: "cid1", Text: "top post", AuthorDID: "did:plc:abc"},
			{URI: "at://did:plc:def/app.bsky.feed.post/2", CID: "cid2", Text: "second post", AuthorDID: "did:plc:def"},
		},
	}

	if err := s.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	got, err := s.GetRun(ctx, "run-123")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.RunID != "run-123" {
		t.Errorf("RunID = %q, want %q", got.RunID, "run-123")
	}
	if got.Status != "initializing" {
		t.Errorf("Status = %q, want %q", got.Status, "initializing")
	}
	if len(got.TopPosts) != 2 {
		t.Fatalf("expected 2 top posts, got %d", len(got.TopPosts))
	}
	if got.TopPosts[0].Text != "top post" {
		t.Errorf("TopPosts[0].Text = %q, want %q", got.TopPosts[0].Text, "top post")
	}
}

func TestRuns_Update(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	run := RunState{
		RunID:                   "run-456",
		Status:                  "initializing",
		AnalysisIntervalMinutes: 30,
		CutoffTime:              now.Add(-30 * time.Minute),
	}
	if err := s.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	run.Status = "completed"
	run.TotalPostsRetrieved = 5000
	run.OverallSentiment = "positive"
	run.NetSentimentPercentage = 12.5
	if err := s.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	got, err := s.GetRun(ctx, "run-456")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q", got.Status, "completed")
	}
	if got.TotalPostsRetrieved != 5000 {
		t.Errorf("TotalPostsRetrieved = %d, want 5000", got.TotalPostsRetrieved)
	}
}

func TestSentimentHistory_StoreAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()

	// Store 3 data points over the last 2 hours
	for i := 0; i < 3; i++ {
		dp := SentimentDataPoint{
			RunID:                "run-" + string(rune('a'+i)),
			Timestamp:            now.Add(-time.Duration(i) * time.Hour),
			AverageCompoundScore: 0.1 * float64(i+1),
			NetSentimentPercent:  10.0 * float64(i+1),
			SentimentCategory:    "positive",
			TotalPosts:           1000 * (i + 1),
		}
		if err := s.StoreSentimentDataPoint(ctx, dp); err != nil {
			t.Fatalf("StoreSentimentDataPoint[%d]: %v", i, err)
		}
	}

	// Get last 3 hours
	results, err := s.GetSentimentHistory(ctx, 3*time.Hour)
	if err != nil {
		t.Fatalf("GetSentimentHistory: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Should be ordered by timestamp ascending (oldest first)
	if !results[0].Timestamp.Before(results[2].Timestamp) {
		t.Errorf("expected ascending order by timestamp, got %v then %v",
			results[0].Timestamp, results[2].Timestamp)
	}

	// Get last 30 minutes should return only the most recent point (i=0, now)
	results, err = s.GetSentimentHistory(ctx, 30*time.Minute)
	if err != nil {
		t.Fatalf("GetSentimentHistory (30m): %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 30m window, got %d", len(results))
	}
}

func TestDailySentiment_StoreAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	dp := DailySentimentDataPoint{
		Date:             "2026-02-07",
		RunID:            "daily-2026-02-07",
		AverageSentiment: 11.5,
		MinSentiment:     5.2,
		MaxSentiment:     18.3,
		Q1Sentiment:      8.1,
		MedianSentiment:  11.0,
		Q3Sentiment:      14.2,
		TotalRuns:        48,
		TotalPosts:       100000,
	}

	if err := s.StoreDailySentiment(ctx, dp); err != nil {
		t.Fatalf("StoreDailySentiment: %v", err)
	}

	got, err := s.GetDailySentimentForDate(ctx, "2026-02-07")
	if err != nil {
		t.Fatalf("GetDailySentimentForDate: %v", err)
	}
	if got.AverageSentiment != 11.5 {
		t.Errorf("AverageSentiment = %f, want 11.5", got.AverageSentiment)
	}
	if got.TotalRuns != 48 {
		t.Errorf("TotalRuns = %d, want 48", got.TotalRuns)
	}
}

func TestDailySentiment_GetHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert 3 days of data
	for i := 0; i < 3; i++ {
		date := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		dp := DailySentimentDataPoint{
			Date:             date,
			RunID:            "daily-" + date,
			AverageSentiment: 10.0 + float64(i),
			TotalRuns:        48,
			TotalPosts:       100000,
		}
		if err := s.StoreDailySentiment(ctx, dp); err != nil {
			t.Fatalf("StoreDailySentiment[%d]: %v", i, err)
		}
	}

	results, err := s.GetDailySentimentHistory(ctx, 7)
	if err != nil {
		t.Fatalf("GetDailySentimentHistory: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

func TestDailySentiment_GetYearly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert 5 days of data
	for i := 0; i < 5; i++ {
		date := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		dp := DailySentimentDataPoint{
			Date:             date,
			RunID:            "daily-" + date,
			AverageSentiment: 10.0 + float64(i),
			MinSentiment:     5.0,
			MaxSentiment:     15.0,
			Q1Sentiment:      8.0,
			MedianSentiment:  10.0,
			Q3Sentiment:      12.0,
			TotalRuns:        48,
			TotalPosts:       100000,
		}
		if err := s.StoreDailySentiment(ctx, dp); err != nil {
			t.Fatalf("StoreDailySentiment[%d]: %v", i, err)
		}
	}

	results, err := s.GetYearlySentimentData(ctx)
	if err != nil {
		t.Fatalf("GetYearlySentimentData: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	// Verify YearlySparklineDataPoint conversion
	if results[0].NetSentimentPercent != results[0].AverageSentiment {
		t.Errorf("NetSentimentPercent should equal AverageSentiment")
	}
	if results[0].Timestamp.IsZero() {
		t.Errorf("Timestamp should not be zero")
	}
}

func TestBackup_CreatesAndPrunes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	if err := s.InsertPost(ctx, Post{
		URI:       "at://did:plc:abc/app.bsky.feed.post/1",
		CID:       "cid1",
		Text:      "backup test",
		AuthorDID: "did:plc:abc",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("InsertPost: %v", err)
	}

	backupPath, err := s.Backup(ctx, tmpDir, "test", 7)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("Stat backup: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("backup file is empty")
	}

	verifyStore, err := New(backupPath)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer verifyStore.Close()

	posts, err := verifyStore.GetPostsSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("GetPostsSince from backup: %v", err)
	}
	if len(posts) != 1 {
		t.Errorf("expected 1 post in backup, got %d", len(posts))
	}
}

func TestBackup_PrunesOldFiles(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	oldTs := time.Now().UTC().AddDate(0, 0, -10).Format(backupTimeFormat)
	oldFile := filepath.Join(backupDir, fmt.Sprintf("hourstats-test-%s.db", oldTs))
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatalf("write old backup: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	_, err = s.Backup(ctx, tmpDir, "test", 7)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("old backup should have been pruned")
	}
}
