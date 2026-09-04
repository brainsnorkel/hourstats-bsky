package store

import (
	"context"
	"testing"
	"time"
)

func TestDailySentiment_FirehoseColumnAndRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i, d := range []string{"2026-08-30", "2026-08-31", "2026-09-01", "2026-09-02"} {
		dp := DailySentimentDataPoint{Date: d, RunID: "daily-" + d, AverageSentiment: float64(i), TotalPosts: 100 * (i + 1), TotalFirehosePosts: 250 * (i + 1)}
		if err := s.StoreDailySentiment(ctx, dp); err != nil {
			t.Fatalf("store %s: %v", d, err)
		}
	}
	got, err := s.GetDailySentimentRange(ctx, "2026-08-31", "2026-09-01")
	if err != nil || len(got) != 2 {
		t.Fatalf("range = %d rows, %v; want 2", len(got), err)
	}
	if got[0].Date != "2026-08-31" || got[1].Date != "2026-09-01" {
		t.Errorf("range order = %s, %s", got[0].Date, got[1].Date)
	}
	if got[0].TotalFirehosePosts != 500 || got[1].TotalFirehosePosts != 750 {
		t.Errorf("firehose totals = %d, %d; want 500, 750", got[0].TotalFirehosePosts, got[1].TotalFirehosePosts)
	}

	one, err := s.GetDailySentimentForDate(ctx, "2026-09-02")
	if err != nil || one.TotalFirehosePosts != 1000 {
		t.Fatalf("for date: %+v, %v", one, err)
	}
	hist, err := s.GetDailySentimentHistory(ctx, 3650)
	if err != nil || len(hist) != 4 || hist[3].TotalFirehosePosts != 1000 {
		t.Fatalf("history = %d rows, %v", len(hist), err)
	}

	updated, err := s.UpdateDailyFirehoseTotal(ctx, "2026-08-30", 999)
	if err != nil || !updated {
		t.Fatalf("update firehose: updated=%v err=%v", updated, err)
	}
	if one, _ := s.GetDailySentimentForDate(ctx, "2026-08-30"); one.TotalFirehosePosts != 999 {
		t.Errorf("after update = %d, want 999", one.TotalFirehosePosts)
	}
	if updated, err := s.UpdateDailyFirehoseTotal(ctx, "1999-01-01", 1); err != nil || updated {
		t.Errorf("update missing date: updated=%v err=%v", updated, err)
	}
}

func snap(t *testing.T, s *Store, at string, rank int, topicID, label string, authors int) {
	t.Helper()
	if err := s.InsertTopicSnapshot(context.Background(), at, rank, topicID, label, "", authors, "[]", "[]", "", "", false, ""); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
}

func TestRollupTopicDaily_MergesAndRanks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Three hourly snapshots on the day, one just outside it.
	snap(t, s, "2026-09-01T00:25:00Z", 1, "swift", "Taylor Swift", 900)
	snap(t, s, "2026-09-01T00:25:00Z", 2, "nasa", "NASA launch", 400)
	snap(t, s, "2026-09-01T01:25:00Z", 3, "swift", "Taylor Swift engagement", 1200)
	snap(t, s, "2026-09-01T02:25:00Z", 1, "nasa", "NASA Artemis launch", 500)
	snap(t, s, "2026-09-02T00:25:00Z", 1, "swift", "Taylor Swift wedding", 100)

	n, err := s.RollupTopicDaily(ctx, "2026-09-01")
	if err != nil || n != 2 {
		t.Fatalf("rollup = %d, %v; want 2", n, err)
	}
	rows, err := s.GetTopicDaily(ctx, "2026-09-01")
	if err != nil || len(rows) != 2 {
		t.Fatalf("topic_daily = %d rows, %v", len(rows), err)
	}
	byID := map[string]TopicDailyRow{}
	for _, r := range rows {
		byID[r.TopicID] = r
	}
	if sw := byID["swift"]; sw.Appearances != 2 || sw.BestRank != 1 || sw.MaxAuthors != 1200 || sw.Label != "Taylor Swift engagement" {
		t.Errorf("swift = %+v", sw)
	}
	if na := byID["nasa"]; na.Appearances != 2 || na.BestRank != 1 || na.MaxAuthors != 500 || na.Label != "NASA Artemis launch" {
		t.Errorf("nasa = %+v", na)
	}

	// Simulate the 48h purge removing the day's first two hours, then a
	// re-run: the merged row must keep the earlier, fuller figures.
	if _, err := s.PurgeTopicSnapshots(ctx, "2026-09-01T02:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RollupTopicDaily(ctx, "2026-09-01"); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.GetTopicDaily(ctx, "2026-09-01")
	for _, r := range rows {
		byID[r.TopicID] = r
	}
	if sw := byID["swift"]; sw.Appearances != 2 || sw.MaxAuthors != 1200 {
		t.Errorf("swift after partial re-run = %+v, want figures preserved", sw)
	}

	// Day with no snapshots writes nothing and is not an error.
	if n, err := s.RollupTopicDaily(ctx, "2026-09-05"); err != nil || n != 0 {
		t.Errorf("empty day rollup = %d, %v", n, err)
	}
	if _, err := s.RollupTopicDaily(ctx, "not-a-date"); err == nil {
		t.Error("bad date should error")
	}
}

func TestGetTopTopicForRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if label, n, err := s.GetTopTopicForRange(ctx, "2026-09-01", "2026-09-07"); err != nil || label != "" || n != 0 {
		t.Fatalf("empty range = %q, %d, %v", label, n, err)
	}

	// swift: 3 + 4 = 7 appearances over two days; nasa: 6 on one day.
	snap(t, s, "2026-09-01T00:25:00Z", 2, "swift", "Taylor Swift", 1)
	snap(t, s, "2026-09-01T01:25:00Z", 2, "swift", "Taylor Swift", 1)
	snap(t, s, "2026-09-01T02:25:00Z", 2, "swift", "Taylor Swift", 1)
	for h := 0; h < 6; h++ {
		snap(t, s, "2026-09-01T0"+string(rune('0'+h))+":25:00Z", 1, "nasa", "NASA", 1)
	}
	for h := 0; h < 4; h++ {
		snap(t, s, "2026-09-02T0"+string(rune('0'+h))+":25:00Z", 3, "swift", "Taylor Swift engagement", 1)
	}
	for _, d := range []string{"2026-09-01", "2026-09-02"} {
		if _, err := s.RollupTopicDaily(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	label, n, err := s.GetTopTopicForRange(ctx, "2026-09-01", "2026-09-07")
	if err != nil || label != "Taylor Swift engagement" || n != 7 {
		t.Errorf("week top = %q, %d, %v; want Taylor Swift engagement, 7", label, n, err)
	}
	label, n, _ = s.GetTopTopicForRange(ctx, "2026-09-01", "2026-09-01")
	if label != "NASA" || n != 6 {
		t.Errorf("day top = %q, %d; want NASA, 6", label, n)
	}
}

func TestDailyTopPost_KeepsBestAndRanges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if p, err := s.GetTopPostForRange(ctx, "2026-09-01", "2026-09-07"); err != nil || p != nil {
		t.Fatalf("empty = %+v, %v", p, err)
	}
	if err := s.StoreDailyTopPost(ctx, "2026-09-01", Post{URI: "", CID: "c"}); err == nil {
		t.Error("post without URI should be rejected")
	}
	a := Post{URI: "at://a", CID: "ca", AuthorHandle: "a.bsky", Likes: 10, Reposts: 2, Replies: 1, EngagementScore: 100}
	b := Post{URI: "at://b", CID: "cb", AuthorHandle: "b.bsky", Likes: 50, Reposts: 5, Replies: 3, EngagementScore: 500}
	lower := Post{URI: "at://c", CID: "cc", AuthorHandle: "c.bsky", EngagementScore: 5}
	if err := s.StoreDailyTopPost(ctx, "2026-09-01", a); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreDailyTopPost(ctx, "2026-09-02", b); err != nil {
		t.Fatal(err)
	}
	// A weaker re-run for the 2nd must not demote the stored post.
	if err := s.StoreDailyTopPost(ctx, "2026-09-02", lower); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTopPostForRange(ctx, "2026-09-01", "2026-09-07")
	if err != nil || got == nil || got.URI != "at://b" || got.Likes != 50 || got.AuthorHandle != "b.bsky" {
		t.Fatalf("range top = %+v, %v", got, err)
	}
	got, _ = s.GetTopPostForRange(ctx, "2026-09-01", "2026-09-01")
	if got == nil || got.URI != "at://a" {
		t.Errorf("single day top = %+v", got)
	}
	if has, err := s.HasDailyTopPost(ctx, "2026-09-02"); err != nil || !has {
		t.Errorf("has 09-02 = %v, %v", has, err)
	}
	if has, _ := s.HasDailyTopPost(ctx, "2026-09-03"); has {
		t.Error("has 09-03 should be false")
	}
}

func TestPurgeReportRollups(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -401).Format("2006-01-02")
	recent := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	for _, d := range []string{old, recent} {
		snap(t, s, d+"T01:25:00Z", 1, "x", "X", 1)
		if _, err := s.RollupTopicDaily(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreDailyTopPost(ctx, d, Post{URI: "at://" + d, CID: "c", EngagementScore: 1}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.PurgeReportRollups(ctx, 400*24*time.Hour)
	if err != nil || n != 2 {
		t.Fatalf("purged = %d, %v; want 2", n, err)
	}
	if rows, _ := s.GetTopicDaily(ctx, old); len(rows) != 0 {
		t.Error("old topic_daily row survived")
	}
	if rows, _ := s.GetTopicDaily(ctx, recent); len(rows) != 1 {
		t.Error("recent topic_daily row purged")
	}
	if has, _ := s.HasDailyTopPost(ctx, recent); !has {
		t.Error("recent daily_top_post purged")
	}
}
