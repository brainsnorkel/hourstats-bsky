package store

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	_, err := s.writeDB.ExecContext(ctx,
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

	if err := s.SetKeyValue(ctx, "backup_test", "hello"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}

	if err := s.InsertPost(ctx, Post{
		URI:       "at://did:plc:abc/app.bsky.feed.post/1",
		CID:       "cid1",
		Text:      "transient post",
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

	val, err := verifyStore.GetKeyValue(ctx, "backup_test")
	if err != nil {
		t.Fatalf("GetKeyValue from backup: %v", err)
	}
	if val != "hello" {
		t.Errorf("key_value = %q, want %q", val, "hello")
	}

	posts, err := verifyStore.GetPostsSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("GetPostsSince from backup: %v", err)
	}
	if len(posts) != 0 {
		t.Errorf("expected 0 posts in backup (transient data excluded), got %d", len(posts))
	}
}

func TestKeyValue_SetAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetKeyValue(ctx, "test_key", "test_value"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}

	val, err := s.GetKeyValue(ctx, "test_key")
	if err != nil {
		t.Fatalf("GetKeyValue: %v", err)
	}
	if val != "test_value" {
		t.Errorf("value = %q, want %q", val, "test_value")
	}
}

func TestKeyValue_Upsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetKeyValue(ctx, "k", "v1"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}
	if err := s.SetKeyValue(ctx, "k", "v2"); err != nil {
		t.Fatalf("SetKeyValue upsert: %v", err)
	}

	val, err := s.GetKeyValue(ctx, "k")
	if err != nil {
		t.Fatalf("GetKeyValue: %v", err)
	}
	if val != "v2" {
		t.Errorf("value = %q, want %q", val, "v2")
	}
}

func TestKeyValue_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetKeyValue(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestGetTopPostForDate_MultipleRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	run1 := RunState{
		RunID:                   "run-a",
		Status:                  "complete",
		AnalysisIntervalMinutes: 30,
		CutoffTime:              now.Add(-30 * time.Minute),
		TopPosts: []Post{
			{URI: "at://did:plc:abc/app.bsky.feed.post/1", CID: "cid1", AuthorHandle: "user1.bsky.social", EngagementScore: 100},
			{URI: "at://did:plc:abc/app.bsky.feed.post/2", CID: "cid2", AuthorHandle: "user2.bsky.social", EngagementScore: 50},
		},
	}
	run2 := RunState{
		RunID:                   "run-b",
		Status:                  "complete",
		AnalysisIntervalMinutes: 30,
		CutoffTime:              now.Add(-60 * time.Minute),
		TopPosts: []Post{
			{URI: "at://did:plc:def/app.bsky.feed.post/3", CID: "cid3", AuthorHandle: "user3.bsky.social", EngagementScore: 200},
		},
	}

	if err := s.CreateRun(ctx, run1); err != nil {
		t.Fatalf("CreateRun 1: %v", err)
	}
	if err := s.CreateRun(ctx, run2); err != nil {
		t.Fatalf("CreateRun 2: %v", err)
	}

	best, err := s.GetTopPostForDate(ctx, today)
	if err != nil {
		t.Fatalf("GetTopPostForDate: %v", err)
	}
	if best == nil {
		t.Fatal("expected a top post, got nil")
	}
	if best.EngagementScore != 200 {
		t.Errorf("EngagementScore = %f, want 200", best.EngagementScore)
	}
	if best.URI != "at://did:plc:def/app.bsky.feed.post/3" {
		t.Errorf("URI = %q, want the highest-engagement post", best.URI)
	}
}

func TestGetTopPostForDate_NoRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	best, err := s.GetTopPostForDate(ctx, "2020-01-01")
	if err != nil {
		t.Fatalf("GetTopPostForDate: %v", err)
	}
	if best != nil {
		t.Errorf("expected nil for date with no runs, got %+v", best)
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

func TestGetKeyValueWithTimestamp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetKeyValue(ctx, "ts_key", "ts_val"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}

	entry, err := s.GetKeyValueWithTimestamp(ctx, "ts_key")
	if err != nil {
		t.Fatalf("GetKeyValueWithTimestamp: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
	if entry.Key != "ts_key" {
		t.Errorf("Key = %q, want %q", entry.Key, "ts_key")
	}
	if entry.Value != "ts_val" {
		t.Errorf("Value = %q, want %q", entry.Value, "ts_val")
	}
	if entry.UpdatedAt == "" {
		t.Error("expected non-empty UpdatedAt")
	}
}

func TestGetKeyValueWithTimestamp_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	entry, err := s.GetKeyValueWithTimestamp(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetKeyValueWithTimestamp: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil for nonexistent key, got %+v", entry)
	}
}

func TestDeleteKeyValues(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, kv := range []struct{ k, v string }{
		{"del1", "v1"}, {"del2", "v2"}, {"keep", "v3"},
	} {
		if err := s.SetKeyValue(ctx, kv.k, kv.v); err != nil {
			t.Fatalf("SetKeyValue(%q): %v", kv.k, err)
		}
	}

	deleted, err := s.DeleteKeyValues(ctx, []string{"del1", "del2"})
	if err != nil {
		t.Fatalf("DeleteKeyValues: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	val, err := s.GetKeyValue(ctx, "keep")
	if err != nil {
		t.Fatalf("GetKeyValue(keep): %v", err)
	}
	if val != "v3" {
		t.Errorf("keep value = %q, want %q", val, "v3")
	}

	_, err = s.GetKeyValue(ctx, "del1")
	if err == nil {
		t.Error("expected error for deleted key")
	}
}

func TestDeleteKeyValues_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	deleted, err := s.DeleteKeyValues(ctx, nil)
	if err != nil {
		t.Fatalf("DeleteKeyValues(nil): %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}

func TestGetLatestCompletedRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()

	if err := s.CreateRun(ctx, RunState{
		RunID: "run-incomplete", Status: "running",
		AnalysisIntervalMinutes: 30, CutoffTime: now,
	}); err != nil {
		t.Fatalf("CreateRun(run-incomplete): %v", err)
	}

	if err := s.CreateRun(ctx, RunState{
		RunID: "run-old", Status: "complete",
		AnalysisIntervalMinutes: 30, CutoffTime: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateRun(run-old): %v", err)
	}

	time.Sleep(1 * time.Second)

	if err := s.CreateRun(ctx, RunState{
		RunID: "run-latest", Status: "complete",
		AnalysisIntervalMinutes: 30, CutoffTime: now.Add(-1 * time.Hour),
		OverallSentiment: "positive", NetSentimentPercentage: 42.5, TotalPostsRetrieved: 200,
	}); err != nil {
		t.Fatalf("CreateRun(run-latest): %v", err)
	}

	latest, err := s.GetLatestCompletedRun(ctx)
	if err != nil {
		t.Fatalf("GetLatestCompletedRun: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil completed run")
	}
	if latest.RunID != "run-latest" {
		t.Errorf("RunID = %q, want %q", latest.RunID, "run-latest")
	}
	if latest.OverallSentiment != "positive" {
		t.Errorf("OverallSentiment = %q, want %q", latest.OverallSentiment, "positive")
	}
}

func TestGetLatestCompletedRun_None(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	run, err := s.GetLatestCompletedRun(ctx)
	if err != nil {
		t.Fatalf("GetLatestCompletedRun: %v", err)
	}
	if run != nil {
		t.Errorf("expected nil on empty DB, got %+v", run)
	}
}

func TestPurgeExpiredRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	if err := s.CreateRun(ctx, RunState{RunID: "old-run", Status: "complete", AnalysisIntervalMinutes: 30, CutoffTime: now}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE runs SET created_at = ? WHERE run_id = ?`,
		timeToStr(now.Add(-72*time.Hour)), "old-run")
	if err != nil {
		t.Fatalf("update created_at: %v", err)
	}

	if err := s.CreateRun(ctx, RunState{RunID: "new-run", Status: "complete", AnalysisIntervalMinutes: 30, CutoffTime: now}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	deleted, err := s.PurgeExpiredRuns(ctx, 48*time.Hour)
	if err != nil {
		t.Fatalf("PurgeExpiredRuns: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	run, err := s.GetRun(ctx, "new-run")
	if err != nil {
		t.Fatalf("GetRun(new-run): %v", err)
	}
	if run == nil {
		t.Error("new-run should survive purge")
	}
}

func TestUpdatePostEngagement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	post := Post{
		URI:             "at://did:plc:abc/app.bsky.feed.post/eng1",
		CID:             "cid-eng",
		Text:            "engagement test",
		AuthorDID:       "did:plc:abc",
		Sentiment:       "positive",
		EngagementScore: 42.5,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.InsertPost(ctx, post); err != nil {
		t.Fatalf("InsertPost: %v", err)
	}

	if err := s.UpdatePostEngagement(ctx, post.URI, 10, 5, 3, "alice.bsky.social"); err != nil {
		t.Fatalf("UpdatePostEngagement: %v", err)
	}

	posts, err := s.GetPostsSince(ctx, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostsSince: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}
	if posts[0].Likes != 10 {
		t.Errorf("Likes = %d, want 10", posts[0].Likes)
	}
	if posts[0].Reposts != 5 {
		t.Errorf("Reposts = %d, want 5", posts[0].Reposts)
	}
	if posts[0].Replies != 3 {
		t.Errorf("Replies = %d, want 3", posts[0].Replies)
	}
	if posts[0].AuthorHandle != "alice.bsky.social" {
		t.Errorf("AuthorHandle = %q, want %q", posts[0].AuthorHandle, "alice.bsky.social")
	}
	// UpdatePostEngagement must leave the sentiment and engagement_score
	// columns exactly as the insert left them.
	if posts[0].Sentiment != "positive" {
		t.Errorf("Sentiment = %q, want %q (must be untouched by the update)", posts[0].Sentiment, "positive")
	}
	if posts[0].EngagementScore != 42.5 {
		t.Errorf("EngagementScore = %v, want 42.5 (must be untouched by the update)", posts[0].EngagementScore)
	}
}

func TestPurgeExpiredSentimentHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := SentimentDataPoint{
		RunID:               "old-sdp",
		Timestamp:           now.Add(-72 * time.Hour),
		NetSentimentPercent: 50,
		SentimentCategory:   "positive",
		TotalPosts:          100,
		CreatedAt:           now.Add(-72 * time.Hour),
	}
	recent := SentimentDataPoint{
		RunID:               "recent-sdp",
		Timestamp:           now.Add(-1 * time.Hour),
		NetSentimentPercent: 30,
		SentimentCategory:   "neutral",
		TotalPosts:          200,
		CreatedAt:           now.Add(-1 * time.Hour),
	}

	if err := s.StoreSentimentDataPoint(ctx, old); err != nil {
		t.Fatalf("StoreSentimentDataPoint (old): %v", err)
	}
	if err := s.StoreSentimentDataPoint(ctx, recent); err != nil {
		t.Fatalf("StoreSentimentDataPoint (recent): %v", err)
	}

	deleted, err := s.PurgeExpiredSentimentHistory(ctx, 48*time.Hour)
	if err != nil {
		t.Fatalf("PurgeExpiredSentimentHistory: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	remaining, err := s.GetSentimentHistory(ctx, 96*time.Hour)
	if err != nil {
		t.Fatalf("GetSentimentHistory: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(remaining))
	}
}

func TestBackupToWriter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetKeyValue(ctx, "bw_key", "bw_val"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}

	var buf bytes.Buffer
	if err := s.BackupToWriter(ctx, &buf); err != nil {
		t.Fatalf("BackupToWriter: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected non-empty backup output")
	}
}

func TestRunWALCheckpoint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetKeyValue(ctx, "wal_test", "data"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}

	// RunWALCheckpoint has no return value; just verify it doesn't panic.
	// Use a high threshold so the test exercises the PASSIVE path.
	s.RunWALCheckpoint(ctx, 50*1024*1024)
}

func TestRunStartupMaintenance(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.RunStartupMaintenance(ctx); err != nil {
		t.Fatalf("RunStartupMaintenance: %v", err)
	}
}

func TestRunVacuum(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.RunVacuum(ctx); err != nil {
		t.Fatalf("RunVacuum: %v", err)
	}
}

func TestDB(t *testing.T) {
	s := newTestStore(t)
	db := s.DB()
	if db == nil {
		t.Fatal("DB() returned nil")
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("DB().Ping(): %v", err)
	}
}

func TestGetDailyPostCounts_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	counts, err := s.GetDailyPostCounts(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("GetDailyPostCounts: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("expected 0 counts, got %d", len(counts))
	}
}

func TestGetDailyPostCounts_WithData(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	yesterday := time.Now().UTC().Add(-36 * time.Hour).Truncate(24 * time.Hour).Add(12 * time.Hour)
	dp := SentimentDataPoint{
		RunID:                "run-daily-1",
		Timestamp:            yesterday,
		AverageCompoundScore: 0.5,
		NetSentimentPercent:  60.0,
		SentimentCategory:    "positive",
		TotalPosts:           100,
		TotalFirehosePosts:   5000,
	}
	if err := s.StoreSentimentDataPoint(ctx, dp); err != nil {
		t.Fatalf("StoreSentimentDataPoint: %v", err)
	}

	counts, err := s.GetDailyPostCounts(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("GetDailyPostCounts: %v", err)
	}
	if len(counts) == 0 {
		t.Fatal("expected at least 1 daily count")
	}
	if counts[0].Count != 100 {
		t.Errorf("Count = %d, want 100", counts[0].Count)
	}
}

func TestGetPostingActivity_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pa, err := s.GetPostingActivity(ctx)
	if err != nil {
		t.Fatalf("GetPostingActivity: %v", err)
	}
	if pa == nil {
		t.Fatal("expected non-nil PostingActivity")
	}
	if pa.SentimentSummary != nil {
		t.Error("expected nil SentimentSummary for empty DB")
	}
}

func TestGetPostingActivity_WithRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	if err := s.CreateRun(ctx, RunState{
		RunID: "pa-run", Status: "complete",
		AnalysisIntervalMinutes: 30, CutoffTime: now,
		OverallSentiment: "happy", NetSentimentPercentage: 75.0, TotalPostsRetrieved: 300,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	pa, err := s.GetPostingActivity(ctx)
	if err != nil {
		t.Fatalf("GetPostingActivity: %v", err)
	}
	if pa.SentimentSummary == nil {
		t.Fatal("expected non-nil SentimentSummary")
	}
	if pa.SentimentSummary.Summary == "" {
		t.Error("expected non-empty Summary")
	}
}

func TestGetWeeklyPostTotals_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	totals, err := s.GetWeeklyPostTotals(ctx)
	if err != nil {
		t.Fatalf("GetWeeklyPostTotals: %v", err)
	}
	if len(totals) != 0 {
		t.Errorf("expected 0 totals, got %d", len(totals))
	}
}

func TestGetLatestTopicSnapshotTime_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	snapTime, count, err := s.GetLatestTopicSnapshotTime(ctx)
	if err != nil {
		t.Fatalf("GetLatestTopicSnapshotTime: %v", err)
	}
	if snapTime != "" {
		t.Errorf("expected empty snapshot time, got %q", snapTime)
	}
	if count != 0 {
		t.Errorf("expected 0 count, got %d", count)
	}
}

func TestGetLatestTopicSnapshotTime_WithData(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	snapTime := "2025-06-15T12:00:00Z"
	if err := s.InsertTopicSnapshot(ctx, snapTime, 1, "topic-1", "Go", "Go language", 10, `["go"]`, `[]`, "at://uri", "handle", false, ""); err != nil {
		t.Fatalf("InsertTopicSnapshot: %v", err)
	}

	got, count, err := s.GetLatestTopicSnapshotTime(ctx)
	if err != nil {
		t.Fatalf("GetLatestTopicSnapshotTime: %v", err)
	}
	if got != snapTime {
		t.Errorf("snapshot time = %q, want %q", got, snapTime)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestGetRecentTopicSnapshots(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	snapTime := "2025-06-15T12:00:00Z"
	if err := s.InsertTopicSnapshot(ctx, snapTime, 1, "topic-1", "Rust", "Rust lang", 5, `["rust"]`, `["rs"]`, "at://uri", "handle", true, "popular"); err != nil {
		t.Fatalf("InsertTopicSnapshot: %v", err)
	}

	rows, err := s.GetRecentTopicSnapshots(ctx, "2025-06-01T00:00:00Z", 10)
	if err != nil {
		t.Fatalf("GetRecentTopicSnapshots: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Label != "Rust" {
		t.Errorf("Label = %q, want %q", rows[0].Label, "Rust")
	}
	if !rows[0].IsMeme {
		t.Error("expected IsMeme=true")
	}
}

func TestGetRecentTopicSnapshots_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rows, err := s.GetRecentTopicSnapshots(ctx, "2025-06-01T00:00:00Z", 10)
	if err != nil {
		t.Fatalf("GetRecentTopicSnapshots: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

// captureWarnLog swaps the default slog logger for one that writes JSON to a
// buffer, returning the buffer and a restore func. Used to assert that
// invalid-env-value fallbacks in envInt/envString/clampInt log a warning.
func captureWarnLog() (*bytes.Buffer, func()) {
	var out bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelWarn})))
	return &out, func() { slog.SetDefault(prev) }
}

func TestEnvInt(t *testing.T) {
	t.Run("unset returns fallback", func(t *testing.T) {
		if got := envInt("HOURSTATS_TEST_ENVINT_UNSET", 42); got != 42 {
			t.Errorf("envInt = %d, want 42", got)
		}
	})

	t.Run("valid value is parsed", func(t *testing.T) {
		t.Setenv("HOURSTATS_TEST_ENVINT_VALID", "7")
		if got := envInt("HOURSTATS_TEST_ENVINT_VALID", 42); got != 7 {
			t.Errorf("envInt = %d, want 7", got)
		}
	})

	t.Run("invalid value falls back to default and warns", func(t *testing.T) {
		t.Setenv("HOURSTATS_TEST_ENVINT_INVALID", "not-a-number")
		out, restore := captureWarnLog()
		defer restore()

		if got := envInt("HOURSTATS_TEST_ENVINT_INVALID", 42); got != 42 {
			t.Errorf("envInt = %d, want fallback 42", got)
		}
		if !strings.Contains(out.String(), "HOURSTATS_TEST_ENVINT_INVALID") {
			t.Errorf("expected warning log to mention the env key, got: %s", out.String())
		}
	})
}

func TestEnvString(t *testing.T) {
	allowed := []string{"MEMORY", "FILE"}

	t.Run("unset returns fallback", func(t *testing.T) {
		if got := envString("HOURSTATS_TEST_ENVSTRING_UNSET", "MEMORY", allowed); got != "MEMORY" {
			t.Errorf("envString = %q, want %q", got, "MEMORY")
		}
	})

	t.Run("valid value matches case-insensitively", func(t *testing.T) {
		t.Setenv("HOURSTATS_TEST_ENVSTRING_VALID", "file")
		if got := envString("HOURSTATS_TEST_ENVSTRING_VALID", "MEMORY", allowed); got != "FILE" {
			t.Errorf("envString = %q, want %q", got, "FILE")
		}
	})

	t.Run("invalid value falls back to default and warns", func(t *testing.T) {
		t.Setenv("HOURSTATS_TEST_ENVSTRING_INVALID", "DISK")
		out, restore := captureWarnLog()
		defer restore()

		if got := envString("HOURSTATS_TEST_ENVSTRING_INVALID", "MEMORY", allowed); got != "MEMORY" {
			t.Errorf("envString = %q, want fallback %q", got, "MEMORY")
		}
		if !strings.Contains(out.String(), "HOURSTATS_TEST_ENVSTRING_INVALID") {
			t.Errorf("expected warning log to mention the env key, got: %s", out.String())
		}
	})
}

func TestClampInt(t *testing.T) {
	t.Run("within range is unchanged", func(t *testing.T) {
		if got := clampInt("TEST_KEY", 4, 1, 8); got != 4 {
			t.Errorf("clampInt = %d, want 4", got)
		}
	})

	t.Run("below minimum clamps up and warns", func(t *testing.T) {
		out, restore := captureWarnLog()
		defer restore()

		if got := clampInt("SQLITE_READ_CONNS", 0, 1, 8); got != 1 {
			t.Errorf("clampInt = %d, want 1", got)
		}
		if !strings.Contains(out.String(), "SQLITE_READ_CONNS") {
			t.Errorf("expected clamp warning log to mention the env key, got: %s", out.String())
		}
	})

	t.Run("above maximum clamps down and warns", func(t *testing.T) {
		out, restore := captureWarnLog()
		defer restore()

		if got := clampInt("SQLITE_READ_CONNS", 20, 1, 8); got != 8 {
			t.Errorf("clampInt = %d, want 8", got)
		}
		if !strings.Contains(out.String(), "SQLITE_READ_CONNS") {
			t.Errorf("expected clamp warning log to mention the env key, got: %s", out.String())
		}
	})
}

func TestNew_EnvConfigurable(t *testing.T) {
	t.Setenv("SQLITE_MMAP_MB", "0")
	t.Setenv("SQLITE_READ_CONNS", "2")

	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New(%q): %v", dbPath, err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.SetKeyValue(ctx, "env_test", "ok"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}
	val, err := s.GetKeyValue(ctx, "env_test")
	if err != nil {
		t.Fatalf("GetKeyValue: %v", err)
	}
	if val != "ok" {
		t.Errorf("value = %q, want %q", val, "ok")
	}
}
