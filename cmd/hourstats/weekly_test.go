package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func TestResolvePostOfWeekFigures(t *testing.T) {
	stored := store.Post{AuthorHandle: "stored.bsky.social", Likes: 9_800, Reposts: 1_200, Replies: 300}

	cases := []struct {
		name    string
		verdict client.Verdict
		want    postOfWeekFigures
	}{
		{
			name:    "all live",
			verdict: client.Verdict{OK: true, Quotable: true, AuthorHandle: "live.bsky.social", Likes: 39_300, Reposts: 4_100, Replies: 870, HasCounts: true},
			want:    postOfWeekFigures{Handle: "live.bsky.social", Likes: 39_300, Reposts: 4_100, Replies: 870, Source: figuresLive},
		},
		{
			// A view without counts is not a post with zero engagement.
			name:    "no live counts keeps the stored counts, live handle",
			verdict: client.Verdict{OK: true, Quotable: true, AuthorHandle: "live.bsky.social"},
			want:    postOfWeekFigures{Handle: "live.bsky.social", Likes: 9_800, Reposts: 1_200, Replies: 300, Source: figuresPartial},
		},
		{
			name:    "handle.invalid keeps the stored handle, live counts",
			verdict: client.Verdict{OK: true, Quotable: true, AuthorHandle: "handle.invalid", Likes: 39_300, Reposts: 4_100, Replies: 870, HasCounts: true},
			want:    postOfWeekFigures{Handle: "stored.bsky.social", Likes: 39_300, Reposts: 4_100, Replies: 870, Source: figuresPartial},
		},
		{
			name:    "empty handle keeps the stored handle, live counts",
			verdict: client.Verdict{OK: true, Quotable: true, Likes: 39_300, Reposts: 4_100, Replies: 870, HasCounts: true},
			want:    postOfWeekFigures{Handle: "stored.bsky.social", Likes: 39_300, Reposts: 4_100, Replies: 870, Source: figuresPartial},
		},
		{
			name:    "nothing live",
			verdict: client.Verdict{OK: true, Quotable: true},
			want:    postOfWeekFigures{Handle: "stored.bsky.social", Likes: 9_800, Reposts: 1_200, Replies: 300, Source: figuresStored},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvePostOfWeekFigures(stored, tc.verdict); got != tc.want {
				t.Errorf("resolvePostOfWeekFigures() = %+v, want %+v", got, tc.want)
			}
		})
	}

	if got := storedPostOfWeekFigures(stored); got != (postOfWeekFigures{Handle: "stored.bsky.social", Likes: 9_800, Reposts: 1_200, Replies: 300, Source: figuresStored}) {
		t.Errorf("storedPostOfWeekFigures() = %+v", got)
	}
}

// recordedReply is one call the fake poster received.
type recordedReply struct {
	text     string
	quoteURI string // "" for a plain text reply
}

// fakeReplyPoster records every reply it is asked to post.
type fakeReplyPoster struct {
	mu      sync.Mutex
	replies []recordedReply
}

func (f *fakeReplyPoster) PostWithFacetsAsReply(_ context.Context, text string, _ []*bsky.RichtextFacet, _, _, _, _ string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, recordedReply{text: text})
	return "at://did:plc:me/app.bsky.feed.post/reply", "replycid", nil
}

func (f *fakeReplyPoster) PostReplyWithQuote(_ context.Context, text string, _, _, _, _, quoteURI, _ string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, recordedReply{text: text, quoteURI: quoteURI})
	return "at://did:plc:me/app.bsky.feed.post/reply", "replycid", nil
}

func TestPostWeeklyTopPostReplyDecisions(t *testing.T) {
	const topURI = "at://did:plc:top/app.bsky.feed.post/1"
	report := weeklyReport{
		Start: mustDate("2025-08-25"), End: mustDate("2025-08-31"),
		TopPost: &store.Post{URI: topURI, CID: "topcid", AuthorHandle: "stored.bsky.social", Likes: 9_800, Reposts: 1_200, Replies: 300},
	}
	live := client.Verdict{OK: true, Quotable: true, AuthorHandle: "live.bsky.social", Likes: 39_300, Reposts: 4_100, Replies: 870, HasCounts: true}
	const liveText = "Post of the week · 25–31 Aug\n\nMost engaged post by @live.bsky.social: 39.3k likes, 4.1k reposts, 870 replies"

	quoteControlled := live
	quoteControlled.Quotable, quoteControlled.Reason = false, client.ReasonQuoteControl
	blocked := live
	blocked.OK, blocked.Quotable, blocked.Reason = false, false, client.ReasonBlocked
	// Over the limit by the handle alone: a 300-rune handle cannot fit a
	// 300-grapheme post however short the rest is.
	longHandle := live
	longHandle.AuthorHandle = strings.Repeat("a", blueskyPostLimit)

	cases := []struct {
		name      string
		gate      *fakeGate
		wantCalls int
		wantQuote bool
		wantText  string
		gateCalls int64
	}{
		{
			name:      "ok and quotable quotes with live figures",
			gate:      &fakeGate{verdicts: map[string]client.Verdict{topURI: live}},
			wantCalls: 1, wantQuote: true, wantText: liveText, gateCalls: 1,
		},
		{
			name:      "quote-controlled replies as text",
			gate:      &fakeGate{verdicts: map[string]client.Verdict{topURI: quoteControlled}},
			wantCalls: 1, wantQuote: false, wantText: liveText, gateCalls: 1,
		},
		{
			name:      "not ok posts nothing",
			gate:      &fakeGate{verdicts: map[string]client.Verdict{topURI: blocked}},
			wantCalls: 0, gateCalls: 1,
		},
		{
			name:      "gate down on both attempts posts nothing",
			gate:      &fakeGate{err: errors.New("appview down")},
			wantCalls: 0, gateCalls: 2,
		},
		{
			name:      "reply over the post limit posts nothing",
			gate:      &fakeGate{verdicts: map[string]client.Verdict{topURI: longHandle}},
			wantCalls: 0, gateCalls: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			poster := &fakeReplyPoster{}
			postWeeklyTopPostReply(context.Background(), poster, tc.gate, report, "at://did:plc:me/app.bsky.feed.post/root", "rootcid")

			if got := tc.gate.calls.Load(); got != tc.gateCalls {
				t.Errorf("gate calls = %d, want %d", got, tc.gateCalls)
			}
			if len(poster.replies) != tc.wantCalls {
				t.Fatalf("replies posted = %d, want %d: %+v", len(poster.replies), tc.wantCalls, poster.replies)
			}
			if tc.wantCalls == 0 {
				return
			}
			got := poster.replies[0]
			if got.text != tc.wantText {
				t.Errorf("reply text = %q, want %q", got.text, tc.wantText)
			}
			if tc.wantQuote && got.quoteURI != topURI {
				t.Errorf("quoteURI = %q, want %q", got.quoteURI, topURI)
			}
			if !tc.wantQuote && got.quoteURI != "" {
				t.Errorf("quoteURI = %q, want a plain text reply", got.quoteURI)
			}
		})
	}
}

// captureLogs routes slog's default logger into a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestRunWeeklyReportFirstEnableWaitsForNextWeek: switching reports on
// mid-week must not publish a review of a week that ended days ago. The first
// run only arms the guard; the next complete week is the first one posted.
func TestRunWeeklyReportFirstEnableWaitsForNextWeek(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "weekly.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	buf := captureLogs(t)

	// A Wednesday: the most recent complete week ended two days ago.
	now := time.Date(2026, time.November, 4, 0, 5, 0, 0, time.UTC)
	wantKey, _ := previousWeek(now)

	runWeeklyReport(ctx, db, "", "", true, now, 60)

	got, err := db.GetKeyValue(ctx, weeklyReportGuardKey)
	if err != nil {
		t.Fatalf("guard key not set on first enable: %v", err)
	}
	if got != wantKey.Format(dateFormat) {
		t.Errorf("guard key = %q, want %q (the Monday of the most recent complete week)", got, wantKey.Format(dateFormat))
	}
	out := buf.String()
	if !strings.Contains(out, `"msg":"weekly report: first enable, starting from the next complete week"`) {
		t.Errorf("first-enable line missing from log: %s", out)
	}
	if strings.Contains(out, "DRY_RUN: would post weekly report") {
		t.Errorf("first enable previewed a stale week: %s", out)
	}

	// One week on, the week that completed since is the first one reported.
	later := now.AddDate(0, 0, 7)
	start, _ := previousWeek(later)
	for i := 0; i < 7; i++ {
		day := start.AddDate(0, 0, i).Format(dateFormat)
		if err := db.StoreDailySentiment(ctx, store.DailySentimentDataPoint{
			Date: day, RunID: "run-" + day, AverageSentiment: 10 + float64(i), MinSentiment: 5, MaxSentiment: 15,
			Q1Sentiment: 8, MedianSentiment: 10, Q3Sentiment: 12, TotalRuns: 24, TotalPosts: 1_000_000,
		}); err != nil {
			t.Fatalf("StoreDailySentiment(%s): %v", day, err)
		}
	}
	buf.Reset()

	runWeeklyReport(ctx, db, "", "", true, later, 60)

	out = buf.String()
	if !strings.Contains(out, "DRY_RUN: would post weekly report") {
		t.Errorf("second run did not preview the newly complete week: %s", out)
	}
	if !strings.Contains(out, `"week_start":"`+start.Format(dateFormat)+`"`) {
		t.Errorf("preview is not for week %s: %s", start.Format(dateFormat), out)
	}
	// A dry run posts nothing, so the guard still names the first-enable week.
	if got, _ := db.GetKeyValue(ctx, weeklyReportGuardKey); got != wantKey.Format(dateFormat) {
		t.Errorf("guard key after dry run = %q, want it unchanged at %q", got, wantKey.Format(dateFormat))
	}
}
