package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type fakeBackfillStore struct {
	missing []store.DailySentimentDataPoint
	totals  map[string]store.DayCycleTotals
	errFor  string
	updated map[string]int
}

func (f *fakeBackfillStore) GetDailySentimentMissingFirehose(_ context.Context, since string) ([]store.DailySentimentDataPoint, error) {
	var out []store.DailySentimentDataPoint
	for _, d := range f.missing {
		if d.Date >= since {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeBackfillStore) GetDayCycleTotals(_ context.Context, date string, minPosts int) (store.DayCycleTotals, error) {
	if minPosts != minPostsRequired {
		return store.DayCycleTotals{}, errors.New("wrong minPosts")
	}
	if date == f.errFor {
		return store.DayCycleTotals{}, errors.New("db down")
	}
	return f.totals[date], nil
}

func (f *fakeBackfillStore) UpdateDailyFirehoseTotal(_ context.Context, date string, total int) (bool, error) {
	if f.updated == nil {
		f.updated = map[string]int{}
	}
	f.updated[date] = total
	return true, nil
}

func TestBackfillDailyFirehoseTotals_OnlyCompleteDays(t *testing.T) {
	now := time.Date(2026, 9, 4, 0, 5, 0, 0, time.UTC)
	f := &fakeBackfillStore{
		missing: []store.DailySentimentDataPoint{
			{Date: "2024-01-01", TotalRuns: 24, TotalPosts: 24000}, // outside retention window
			{Date: "2026-08-01", TotalRuns: 24, TotalPosts: 24000}, // complete: filled
			{Date: "2026-08-02", TotalRuns: 24, TotalPosts: 24000}, // history truncated: skipped
			{Date: "2026-08-03", TotalRuns: 24, TotalPosts: 24000}, // english mismatch: skipped
			{Date: "2026-08-04", TotalRuns: 24, TotalPosts: 24000}, // firehose untracked: skipped
			{Date: "2026-08-05", TotalRuns: 24, TotalPosts: 24000}, // lookup error: skipped
		},
		totals: map[string]store.DayCycleTotals{
			"2024-01-01": {Cycles: 24, EnglishPosts: 24000, FirehosePosts: 60000},
			"2026-08-01": {Cycles: 24, EnglishPosts: 24000, FirehosePosts: 60000},
			"2026-08-02": {Cycles: 10, EnglishPosts: 10000, FirehosePosts: 25000},
			"2026-08-03": {Cycles: 24, EnglishPosts: 23000, FirehosePosts: 60000},
			"2026-08-04": {Cycles: 24, EnglishPosts: 24000, FirehosePosts: 0},
		},
		errFor: "2026-08-05",
	}
	backfillDailyFirehoseTotals(context.Background(), f, now)
	if len(f.updated) != 1 || f.updated["2026-08-01"] != 60000 {
		t.Errorf("updated = %v, want only 2026-08-01=60000", f.updated)
	}
}

type fakeTopPostStore struct {
	earliest string
	missing  []string
	posts    map[string]*store.Post
	stored   map[string]string
	gotSince string
}

func (f *fakeTopPostStore) GetEarliestRunDate(context.Context) (string, error) {
	return f.earliest, nil
}
func (f *fakeTopPostStore) GetDatesMissingTopPost(_ context.Context, since string) ([]string, error) {
	f.gotSince = since
	var out []string
	for _, d := range f.missing {
		if d >= since {
			out = append(out, d)
		}
	}
	return out, nil
}
func (f *fakeTopPostStore) GetTopPostForDate(_ context.Context, date string) (*store.Post, error) {
	return f.posts[date], nil
}
func (f *fakeTopPostStore) StoreDailyTopPost(_ context.Context, date string, p store.Post) error {
	if f.stored == nil {
		f.stored = map[string]string{}
	}
	f.stored[date] = p.URI
	return nil
}

func TestBackfillDailyTopPosts(t *testing.T) {
	now := time.Date(2026, 9, 4, 0, 5, 0, 0, time.UTC)
	f := &fakeTopPostStore{
		earliest: "2026-02-13",
		missing:  []string{"2026-01-01", "2026-02-13", "2026-08-24", "2026-08-25"},
		posts: map[string]*store.Post{
			"2026-01-01": {URI: "at://too-early", CID: "c"},
			"2026-02-13": {URI: "at://first", CID: "c"},
			"2026-08-24": {URI: "at://mon", CID: "c"},
			// 2026-08-25 has runs rows but no usable top post: skipped, not an error.
		},
	}
	backfillDailyTopPosts(context.Background(), f, now)
	if f.gotSince != "2026-02-13" {
		t.Errorf("since = %s, want the earliest run date", f.gotSince)
	}
	if len(f.stored) != 2 || f.stored["2026-02-13"] != "at://first" || f.stored["2026-08-24"] != "at://mon" {
		t.Errorf("stored = %v", f.stored)
	}

	// No runs at all: nothing is queried or written.
	g := &fakeTopPostStore{missing: []string{"2026-08-24"}}
	backfillDailyTopPosts(context.Background(), g, now)
	if g.gotSince != "" || len(g.stored) != 0 {
		t.Errorf("empty runs: since=%q stored=%v", g.gotSince, g.stored)
	}
}
