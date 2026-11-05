package store

import (
	"context"
	"fmt"
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
	// The by-id axis wins here: swift is 7 under one ID but split 3 + 4
	// across two labels, so by label NASA's 6 would have come first.
	label, n, err := s.GetTopTopicForRange(ctx, "2026-09-01", "2026-09-07")
	if err != nil || label != "Taylor Swift engagement" || n != 7 {
		t.Errorf("week top = %q, %d, %v; want Taylor Swift engagement, 7", label, n, err)
	}
	label, n, _ = s.GetTopTopicForRange(ctx, "2026-09-01", "2026-09-01")
	if label != "NASA" || n != 6 {
		t.Errorf("day top = %q, %d; want NASA, 6", label, n)
	}
}

// hourly inserts one snapshot per listed hour of a UTC day at the given rank.
func hourly(t *testing.T, s *Store, date string, hours []int, rank int, topicID, label string) {
	t.Helper()
	for _, h := range hours {
		snap(t, s, fmt.Sprintf("%sT%02d:25:00Z", date, h), rank, topicID, label, 1)
	}
}

func rollup(t *testing.T, s *Store, dates ...string) {
	t.Helper()
	for _, d := range dates {
		if _, err := s.RollupTopicDaily(context.Background(), d); err != nil {
			t.Fatalf("rollup %s: %v", d, err)
		}
	}
}

// The Halloween shape: the identity tracker split one subject across four
// IDs over two days, so no single ID beats the election topic, but the label
// does.
func TestGetTopTopicForRange_LabelAcrossIDsWins(t *testing.T) {
	s := newTestStore(t)
	hourly(t, s, "2026-10-31", []int{0, 1, 2}, 1, "elect", "Election")
	hourly(t, s, "2026-10-31", []int{0, 1}, 2, "hw1", "Halloween")
	hourly(t, s, "2026-10-31", []int{2, 3}, 2, "hw2", "Halloween")
	hourly(t, s, "2026-11-01", []int{0, 1, 2}, 1, "elect", "Election")
	hourly(t, s, "2026-11-01", []int{0, 1}, 2, "hw3", "Halloween")
	hourly(t, s, "2026-11-01", []int{2, 3}, 2, "hw4", "Halloween")
	rollup(t, s, "2026-10-31", "2026-11-01")

	// By id: elect 6, each hw* 2. By label: halloween 8, election 6.
	label, n, err := s.GetTopTopicForRange(context.Background(), "2026-10-26", "2026-11-01")
	if err != nil || label != "Halloween" || n != 8 {
		t.Errorf("top = %q, %d, %v; want Halloween, 8", label, n, err)
	}
}

// The midterms shape: Gemini relabelled one ID on each of three days, so no
// single label beats Halloween's two IDs, but the ID does. The displayed
// label is the one on the ID's latest-date row. The relabels sit on separate
// days because RollupTopicDaily keeps one row per (date, topic_id) carrying
// only the day's last label, so two labels on one day would collapse into one.
func TestGetTopTopicForRange_IDAcrossLabelsWins(t *testing.T) {
	s := newTestStore(t)
	hourly(t, s, "2026-11-03", []int{0, 1}, 1, "mid", "Midterms")
	hourly(t, s, "2026-11-04", []int{0, 1}, 1, "mid", "2026 Midterm Elections")
	hourly(t, s, "2026-11-05", []int{0, 1}, 1, "mid", "Midterm results")
	hourly(t, s, "2026-11-03", []int{2, 3}, 2, "hw1", "Halloween")
	hourly(t, s, "2026-11-04", []int{2, 3}, 2, "hw2", "Halloween")
	rollup(t, s, "2026-11-03", "2026-11-04", "2026-11-05")

	// topic_daily: mid has three rows, one per label, 2 appearances each.
	// By id: mid 6, hw1 2, hw2 2. By label: halloween 4, each midterms label 2.
	label, n, err := s.GetTopTopicForRange(context.Background(), "2026-11-02", "2026-11-08")
	if err != nil || label != "Midterm results" || n != 6 {
		t.Errorf("top = %q, %d, %v; want Midterm results, 6", label, n, err)
	}
}

// Labels group on lower(trim(label)), and the spelling shown is the one on
// the group's latest-date row; two rows on that date exercise the tiebreak.
func TestGetTopTopicForRange_LabelKeyAndSpelling(t *testing.T) {
	ctx := context.Background()

	t.Run("latest date, larger appearances", func(t *testing.T) {
		s := newTestStore(t)
		hourly(t, s, "2026-10-30", []int{0, 1, 2, 3, 4}, 1, "other", "Other")
		hourly(t, s, "2026-10-30", []int{0, 1, 2}, 2, "hw1", " halloween ")
		hourly(t, s, "2026-10-31", []int{0}, 2, "hw2", "Halloween")
		hourly(t, s, "2026-10-31", []int{1, 2}, 2, "hw3", "HALLOWEEN")
		rollup(t, s, "2026-10-30", "2026-10-31")

		// By id: other 5. By label: halloween 3 + 1 + 2 = 6; on the latest
		// date hw3 has 2 appearances to hw2's 1.
		label, n, err := s.GetTopTopicForRange(ctx, "2026-10-26", "2026-11-01")
		if err != nil || label != "HALLOWEEN" || n != 6 {
			t.Errorf("top = %q, %d, %v; want HALLOWEEN, 6", label, n, err)
		}
	})

	t.Run("latest date, equal appearances, smaller topic_id", func(t *testing.T) {
		s := newTestStore(t)
		hourly(t, s, "2026-10-30", []int{0, 1, 2, 3}, 1, "other", "Other")
		hourly(t, s, "2026-10-30", []int{0, 1, 2}, 2, "hw1", " halloween ")
		hourly(t, s, "2026-10-31", []int{0}, 2, "hwB", "HALLOWEEN")
		hourly(t, s, "2026-10-31", []int{1}, 2, "hwA", "Halloween")
		rollup(t, s, "2026-10-30", "2026-10-31")

		// Both latest-date rows have 1 appearance; "hwA" sorts before "hwB".
		label, n, err := s.GetTopTopicForRange(ctx, "2026-10-26", "2026-11-01")
		if err != nil || label != "Halloween" || n != 5 {
			t.Errorf("top = %q, %d, %v; want Halloween, 5", label, n, err)
		}
	})
}

// cycles seeds n sentiment_history rows on a UTC date, one per hour from
// 00:55, standing in for the day's analysis cycles.
func cycles(t *testing.T, s *Store, date string, n int) {
	t.Helper()
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse %s: %v", date, err)
	}
	if n > 24 {
		t.Fatalf("cycles(%s, %d): one row per hour; more than 24 would spill onto the next date", date, n)
	}
	for h := 0; h < n; h++ {
		dp := SentimentDataPoint{
			RunID:     fmt.Sprintf("run-%s-%02d", date, h),
			Timestamp: day.Add(time.Duration(h)*time.Hour + 55*time.Minute),
		}
		if err := s.StoreSentimentDataPoint(context.Background(), dp); err != nil {
			t.Fatalf("store cycle %s %d: %v", date, h, err)
		}
	}
}

// seedSharedFinalLabel builds the shape the cap is for: on 2026-10-31 two IDs
// end the day under the same label with 3 appearances each (a by-label day
// sum of 6), a third ID carries the label for 2 cycles on 2026-11-01, and a
// competitor ID trends 5 cycles across both days at rank 2.
func seedSharedFinalLabel(t *testing.T, s *Store) {
	t.Helper()
	hourly(t, s, "2026-10-31", []int{0, 1, 2}, 1, "hw1", "Halloween")
	hourly(t, s, "2026-10-31", []int{0, 1, 2}, 2, "hw2", "Halloween")
	hourly(t, s, "2026-11-01", []int{0, 1}, 2, "hw3", "Halloween")
	hourly(t, s, "2026-10-31", []int{0, 1, 2}, 2, "elect", "Election")
	hourly(t, s, "2026-11-01", []int{0, 1}, 2, "elect", "Election")
	rollup(t, s, "2026-10-31", "2026-11-01")
}

// The by-label day sum is capped at the day's cycle count from
// sentiment_history, per day, before the days are summed; the group's peak
// rank survives the cap.
func TestGetTopTopicForRange_LabelDaySumCappedAtCycles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedSharedFinalLabel(t, s)
	cycles(t, s, "2026-10-31", 3)
	cycles(t, s, "2026-11-01", 5)

	// 2026-10-31 alone: by label halloween 6 capped to the day's 3 cycles,
	// at rank 1; by id hw1 3 at rank 1. Same sum, same rank, so the by-id
	// result is returned, and either way the figure is 3, not 6.
	label, n, err := s.GetTopTopicForRange(ctx, "2026-10-31", "2026-10-31")
	if err != nil || label != "Halloween" || n != 3 {
		t.Errorf("capped day = %q, %d, %v; want Halloween, 3", label, n, err)
	}

	// The week: halloween 3 (capped) + 2 (under 2026-11-01's 5 cycles, so
	// uncapped) = 5 at rank 1; elect 5 by id at rank 2. Equal sums go to the
	// better peak rank, which the cap left alone: by label wins with 5. An
	// uncapped by-label sum would have reported 8.
	label, n, err = s.GetTopTopicForRange(ctx, "2026-10-26", "2026-11-01")
	if err != nil || label != "Halloween" || n != 5 {
		t.Errorf("week = %q, %d, %v; want Halloween, 5", label, n, err)
	}
}

// A date with no sentiment_history rows is left uncapped.
func TestGetTopTopicForRange_LabelDaySumUncappedWithoutCycles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedSharedFinalLabel(t, s)

	label, n, err := s.GetTopTopicForRange(ctx, "2026-10-31", "2026-10-31")
	if err != nil || label != "Halloween" || n != 6 {
		t.Errorf("uncapped day = %q, %d, %v; want Halloween, 6", label, n, err)
	}
	label, n, err = s.GetTopTopicForRange(ctx, "2026-10-26", "2026-11-01")
	if err != nil || label != "Halloween" || n != 8 {
		t.Errorf("uncapped week = %q, %d, %v; want Halloween, 8", label, n, err)
	}

	// Cycles on a date outside the range, or on a date with no topics, change
	// nothing and do not break the empty-range case.
	cycles(t, s, "2026-10-30", 1)
	cycles(t, s, "2026-11-02", 24)
	if label, n, err = s.GetTopTopicForRange(ctx, "2026-10-26", "2026-11-01"); err != nil || label != "Halloween" || n != 8 {
		t.Errorf("week with cycles outside = %q, %d, %v; want Halloween, 8", label, n, err)
	}
	if label, n, err = s.GetTopTopicForRange(ctx, "2026-11-02", "2026-11-08"); err != nil || label != "" || n != 0 {
		t.Errorf("cycles but no topics = %q, %d, %v; want empty", label, n, err)
	}
}

func TestGetTopTopicForRange_Ties(t *testing.T) {
	ctx := context.Background()

	t.Run("equal sums go to the better peak rank", func(t *testing.T) {
		s := newTestStore(t)
		hourly(t, s, "2026-09-01", []int{0, 1, 2, 3}, 2, "aaa", "Alpha")
		hourly(t, s, "2026-09-01", []int{0, 1, 2}, 3, "bbb", "Beta")
		hourly(t, s, "2026-09-01", []int{4}, 1, "bbb", "Beta")
		rollup(t, s, "2026-09-01")

		// Both 4 appearances; Beta peaked at rank 1, Alpha at 2. The two axes
		// agree here since every ID has one label.
		label, n, err := s.GetTopTopicForRange(ctx, "2026-09-01", "2026-09-07")
		if err != nil || label != "Beta" || n != 4 {
			t.Errorf("top = %q, %d, %v; want Beta, 4", label, n, err)
		}
	})

	t.Run("equal sums across the axes go to the better peak rank", func(t *testing.T) {
		s := newTestStore(t)
		hourly(t, s, "2026-11-03", []int{0, 1}, 2, "mid", "A")
		hourly(t, s, "2026-11-04", []int{0, 1}, 2, "mid", "B")
		hourly(t, s, "2026-11-03", []int{2, 3}, 1, "hw1", "Halloween")
		hourly(t, s, "2026-11-04", []int{2, 3}, 3, "hw2", "Halloween")
		rollup(t, s, "2026-11-03", "2026-11-04")

		// By id: mid 4 at rank 2 (hw1 2, hw2 2). By label: halloween 4 at
		// rank 1 (a 2, b 2). Same sum, by label peaked higher: it wins, so
		// the rank clause between the axes is what this pins. Without it the
		// by-id result "B" would come back.
		label, n, err := s.GetTopTopicForRange(ctx, "2026-11-02", "2026-11-08")
		if err != nil || label != "Halloween" || n != 4 {
			t.Errorf("top = %q, %d, %v; want Halloween, 4", label, n, err)
		}
	})

	t.Run("exact tie between the axes goes to the by-id result", func(t *testing.T) {
		s := newTestStore(t)
		hourly(t, s, "2026-11-03", []int{0, 1}, 1, "mid", "Midterms")
		hourly(t, s, "2026-11-04", []int{0, 1}, 1, "mid", "Midterm results")
		hourly(t, s, "2026-11-03", []int{2, 3}, 1, "hw1", "Halloween")
		hourly(t, s, "2026-11-04", []int{2, 3}, 1, "hw2", "Halloween")
		rollup(t, s, "2026-11-03", "2026-11-04")

		// By id: mid 4 at rank 1 (hw1 2, hw2 2). By label: halloween 4 at
		// rank 1 (each midterms label 2). Same sum, same rank: by id wins.
		label, n, err := s.GetTopTopicForRange(ctx, "2026-11-02", "2026-11-08")
		if err != nil || label != "Midterm results" || n != 4 {
			t.Errorf("top = %q, %d, %v; want Midterm results, 4", label, n, err)
		}
	})
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

func TestDatesMissingTopPostAndEarliestRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if d, err := s.GetEarliestRunDate(ctx); err != nil || d != "" {
		t.Fatalf("earliest with no runs = %q, %v", d, err)
	}
	for _, d := range []string{"2026-08-30", "2026-08-31", "2026-09-01"} {
		if err := s.StoreDailySentiment(ctx, DailySentimentDataPoint{Date: d, RunID: "daily-" + d}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.StoreDailyTopPost(ctx, "2026-08-31", Post{URI: "at://x", CID: "c", EngagementScore: 1}); err != nil {
		t.Fatal(err)
	}
	dates, err := s.GetDatesMissingTopPost(ctx, "2026-08-30")
	if err != nil || len(dates) != 2 || dates[0] != "2026-08-30" || dates[1] != "2026-09-01" {
		t.Fatalf("missing = %v, %v", dates, err)
	}
	if err := s.CreateRun(ctx, RunState{RunID: "run-1", CreatedAt: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	// CreateRun stamps created_at itself, so the earliest date is today.
	if d, err := s.GetEarliestRunDate(ctx); err != nil || d != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("earliest = %q, %v", d, err)
	}
}
