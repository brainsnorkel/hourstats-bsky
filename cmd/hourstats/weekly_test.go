package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
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

// recordedReply is one call the fake poster received, with every thread
// argument, so a swapped root/parent or quote argument shows up in a test.
type recordedReply struct {
	text                 string
	rootURI, rootCID     string
	parentURI, parentCID string
	quoteURI, quoteCID   string // "" for a plain text reply
}

// fakeReplyPoster records every reply it is asked to post.
type fakeReplyPoster struct {
	mu      sync.Mutex
	replies []recordedReply
}

func (f *fakeReplyPoster) PostWithFacetsAsReply(_ context.Context, text string, _ []*bsky.RichtextFacet, rootURI, rootCID, parentURI, parentCID string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, recordedReply{text: text, rootURI: rootURI, rootCID: rootCID, parentURI: parentURI, parentCID: parentCID})
	return "at://did:plc:me/app.bsky.feed.post/reply", "replycid", nil
}

func (f *fakeReplyPoster) PostReplyWithQuote(_ context.Context, text string, rootURI, rootCID, parentURI, parentCID, quoteURI, quoteCID string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, recordedReply{text: text, rootURI: rootURI, rootCID: rootCID, parentURI: parentURI, parentCID: parentCID, quoteURI: quoteURI, quoteCID: quoteCID})
	return "at://did:plc:me/app.bsky.feed.post/reply", "replycid", nil
}

func TestPostWeeklyTopPostReplyDecisions(t *testing.T) {
	const topURI, topCID = "at://did:plc:top/app.bsky.feed.post/1", "topcid"
	const rootURI, rootCID = "at://did:plc:me/app.bsky.feed.post/root", "rootcid"
	report := weeklyReport{
		Start: mustDate("2025-08-25"), End: mustDate("2025-08-31"),
		TopPost: &store.Post{URI: topURI, CID: topCID, AuthorHandle: "stored.bsky.social", Likes: 9_800, Reposts: 1_200, Replies: 300},
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
		wantLog   []string // fragments the captured log must contain
	}{
		{
			name:      "ok and quotable quotes with live figures",
			gate:      &fakeGate{verdicts: map[string]client.Verdict{topURI: live}},
			wantCalls: 1, wantQuote: true, wantText: liveText, gateCalls: 1,
			// The posted line must show the gap between the stored and the
			// live figures, and where the printed ones came from.
			wantLog: []string{
				`"msg":"post of the week reply posted"`,
				`"counts_source":"live"`,
				`"stored_likes":9800`,
				`"live_likes":39300`,
			},
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
			// The skip must be told apart from a gate rejection in the log.
			wantLog: []string{`"level":"WARN"`, `"msg":"post of the week text over limit, omitting reply"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLogs(t)
			poster := &fakeReplyPoster{}
			postWeeklyTopPostReply(context.Background(), poster, tc.gate, report, rootURI, rootCID)

			if got := tc.gate.calls.Load(); got != tc.gateCalls {
				t.Errorf("gate calls = %d, want %d", got, tc.gateCalls)
			}
			for _, want := range tc.wantLog {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("log is missing %s:\n%s", want, buf.String())
				}
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
			// The reply sits directly under the root, so root and parent are
			// the same post; the quote is the top post itself.
			if got.rootURI != rootURI || got.rootCID != rootCID || got.parentURI != rootURI || got.parentCID != rootCID {
				t.Errorf("thread root = %s/%s, parent = %s/%s, want both %s/%s",
					got.rootURI, got.rootCID, got.parentURI, got.parentCID, rootURI, rootCID)
			}
			if tc.wantQuote && (got.quoteURI != topURI || got.quoteCID != topCID) {
				t.Errorf("quote = %s/%s, want %s/%s", got.quoteURI, got.quoteCID, topURI, topCID)
			}
			if !tc.wantQuote && (got.quoteURI != "" || got.quoteCID != "") {
				t.Errorf("quote = %s/%s, want a plain text reply", got.quoteURI, got.quoteCID)
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

	runWeeklyReport(ctx, db, "", "", true, now, 60, nil)

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
	seedWeekDays(t, ctx, db, start)
	buf.Reset()

	runWeeklyReport(ctx, db, "", "", true, later, 60, nil)

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

// seedWeekDays stores the seven daily rows of the week starting on the Monday
// start, enough for a weekly report to go ahead.
func seedWeekDays(t *testing.T, ctx context.Context, db *store.Store, start time.Time) {
	t.Helper()
	for i := 0; i < 7; i++ {
		day := start.AddDate(0, 0, i).Format(dateFormat)
		if err := db.StoreDailySentiment(ctx, store.DailySentimentDataPoint{
			Date: day, RunID: "run-" + day, AverageSentiment: 10 + float64(i), MinSentiment: 5, MaxSentiment: 15,
			Q1Sentiment: 8, MedianSentiment: 10, Q3Sentiment: 12, TotalRuns: 24, TotalPosts: 1_000_000,
		}); err != nil {
			t.Fatalf("StoreDailySentiment(%s): %v", day, err)
		}
	}
}

// TestRunWeeklyReportDryRunAppliesReplyLimit: the DRY_RUN preview is held to
// the same 300-rune rule as the live reply, so a stored handle that would make
// the live run omit the reply is previewed as that omission, not as a reply
// the live run would never post.
func TestRunWeeklyReportDryRunAppliesReplyLimit(t *testing.T) {
	// A Wednesday: the previous week ran 26 Oct to 1 Nov.
	now := time.Date(2026, time.November, 4, 0, 5, 0, 0, time.UTC)
	start, end := previousWeek(now)
	const topURI, topCID = "at://did:plc:top/app.bsky.feed.post/1", "topcid"

	cases := []struct {
		name        string
		handle      string
		wantOmitted bool
	}{
		{name: "reply within the limit is previewed", handle: "stored.bsky.social"},
		// A 300-rune handle cannot fit a 300-grapheme post however short the
		// rest is.
		{name: "reply over the limit is previewed as omitted", handle: strings.Repeat("a", blueskyPostLimit), wantOmitted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.New(filepath.Join(t.TempDir(), "weekly.db"))
			if err != nil {
				t.Fatalf("store.New: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			ctx := context.Background()

			// The guard names the week before, so this run is a normal
			// catch-up rather than a first enable.
			if err := db.SetKeyValue(ctx, weeklyReportGuardKey, start.AddDate(0, 0, -7).Format(dateFormat)); err != nil {
				t.Fatalf("SetKeyValue: %v", err)
			}
			seedWeekDays(t, ctx, db, start)
			top := store.Post{URI: topURI, CID: topCID, AuthorHandle: tc.handle, Likes: 9_800, Reposts: 1_200, Replies: 300, EngagementScore: 12_000}
			if err := db.StoreDailyTopPost(ctx, start.AddDate(0, 0, 3).Format(dateFormat), top); err != nil {
				t.Fatalf("StoreDailyTopPost: %v", err)
			}
			buf := captureLogs(t)

			runWeeklyReport(ctx, db, "", "", true, now, 60, nil)

			out := buf.String()
			if !strings.Contains(out, `"msg":"DRY_RUN: would post weekly report"`) {
				t.Fatalf("dry run did not preview the report: %s", out)
			}
			if !strings.Contains(out, `"has_top_post":true`) {
				t.Errorf("preview does not know about the top post: %s", out)
			}
			// The stored-figure reply is what the preview is judged on, the
			// same text the live path builds when nothing live arrives.
			replyText := buildPostOfWeekText(start, end, storedPostOfWeekFigures(top))
			replyLength := postLength(replyText)
			if (replyLength > blueskyPostLimit) != tc.wantOmitted {
				t.Fatalf("test setup: reply length %d, want over limit = %v", replyLength, tc.wantOmitted)
			}
			if !strings.Contains(out, `"reply_length":`+strconv.Itoa(replyLength)) {
				t.Errorf("preview does not carry reply_length=%d: %s", replyLength, out)
			}
			if !strings.Contains(out, `"reply_omitted":`+strconv.FormatBool(tc.wantOmitted)) {
				t.Errorf("preview does not carry reply_omitted=%v: %s", tc.wantOmitted, out)
			}
			if tc.wantOmitted {
				if !strings.Contains(out, `"reply":""`) || strings.Contains(out, "Most engaged post by") {
					t.Errorf("preview shows a reply the live run would omit: %s", out)
				}
				return
			}
			const wantReply = "Post of the week · 26 Oct – 1 Nov\n\nMost engaged post by @stored.bsky.social: 9.8k likes, 1.2k reposts, 300 replies"
			// The JSON handler writes each newline as the two characters \n.
			if !strings.Contains(out, `"reply":"`+strings.ReplaceAll(wantReply, "\n", `\n`)+`"`) {
				t.Errorf("preview reply is not %q: %s", wantReply, out)
			}
		})
	}
}

// TestArmWeeklyReportGuardSeedsFreshStore: on a database that has never had
// reports enabled, startup arms the guard to the most recent complete week,
// which is the week deliberately never posted.
func TestArmWeeklyReportGuardSeedsFreshStore(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	buf := captureLogs(t)

	// A Wednesday: the most recent complete week ran 26 Oct to 1 Nov.
	now := time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)
	armWeeklyReportGuard(ctx, db, now)

	got, err := db.GetKeyValue(ctx, weeklyReportGuardKey)
	if err != nil {
		t.Fatalf("guard key not armed: %v", err)
	}
	if got != "2026-10-26" {
		t.Errorf("guard key = %q, want 2026-10-26 (the Monday of the most recent complete week)", got)
	}
	out := buf.String()
	if !strings.Contains(out, `"msg":"weekly report: guard armed at startup, first report is the next complete week"`) ||
		!strings.Contains(out, `"week_start":"2026-10-26"`) {
		t.Errorf("arming line missing or wrong: %s", out)
	}
}

// TestArmWeeklyReportGuardKeepsExistingKey: a database that already carries a
// guard, from an earlier run or an earlier deploy, is left exactly as it is;
// re-arming would turn every restart into a skipped week.
func TestArmWeeklyReportGuardKeepsExistingKey(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	if err := db.SetKeyValue(ctx, weeklyReportGuardKey, "2026-10-19"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}
	buf := captureLogs(t)

	armWeeklyReportGuard(ctx, db, time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC))

	if got, _ := db.GetKeyValue(ctx, weeklyReportGuardKey); got != "2026-10-19" {
		t.Errorf("guard key = %q, want it untouched at 2026-10-19", got)
	}
	out := buf.String()
	if !strings.Contains(out, `"msg":"weekly report: guard already set"`) || !strings.Contains(out, `"week_start":"2026-10-19"`) {
		t.Errorf("already-set line missing or without the value: %s", out)
	}
	if strings.Contains(out, "guard armed at startup") {
		t.Errorf("an existing guard was re-armed: %s", out)
	}
}

// TestArmWeeklyReportGuardSundayDeploy is the scenario the startup seed
// exists for. Reports are enabled on a Sunday evening; the week ending that
// night must be the first one reported, at Monday's daily job. Seeding inside
// the daily job would instead arm the guard to that week at Monday 00:00 and
// never post it.
func TestArmWeeklyReportGuardSundayDeploy(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	buf := captureLogs(t)

	deploy := time.Date(2026, time.November, 8, 20, 0, 0, 0, time.UTC) // a Sunday
	armWeeklyReportGuard(ctx, db, deploy)
	if got, _ := db.GetKeyValue(ctx, weeklyReportGuardKey); got != "2026-10-26" {
		t.Fatalf("guard after a Sunday arm = %q, want 2026-10-26 (the week before the one still in progress)", got)
	}

	weekStart := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	seedWeekDays(t, ctx, db, weekStart) // 2 to 8 Nov
	buf.Reset()

	monday := time.Date(2026, time.November, 9, 0, 5, 0, 0, time.UTC)
	runWeeklyReport(ctx, db, "", "", true, monday, 60, nil)

	out := buf.String()
	if !strings.Contains(out, `"msg":"DRY_RUN: would post weekly report"`) {
		t.Fatalf("Monday's run did not preview the week that ended on deploy night: %s", out)
	}
	if !strings.Contains(out, `"week_start":"2026-11-02"`) {
		t.Errorf("preview is not for the week of 2026-11-02: %s", out)
	}
	if strings.Contains(out, "first enable") {
		t.Errorf("Monday's run re-seeded the guard instead of posting: %s", out)
	}
	// A dry run posts nothing, so the guard still names the armed week.
	if got, _ := db.GetKeyValue(ctx, weeklyReportGuardKey); got != "2026-10-26" {
		t.Errorf("guard after the dry run = %q, want it unchanged at 2026-10-26", got)
	}
}

// TestArmWeeklyReportGuardMondayDeploy: enabling reports on a Monday, after
// the 00:00 job has passed, arms the guard to the week that just ended. That
// week is the one deliberately skipped, so Tuesday's job posts nothing even
// though its data is all there.
func TestArmWeeklyReportGuardMondayDeploy(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()

	deploy := time.Date(2026, time.November, 9, 5, 0, 0, 0, time.UTC) // a Monday
	armWeeklyReportGuard(ctx, db, deploy)
	if got, _ := db.GetKeyValue(ctx, weeklyReportGuardKey); got != "2026-11-02" {
		t.Fatalf("guard after a Monday arm = %q, want 2026-11-02 (the week that just ended)", got)
	}
	seedWeekDays(t, ctx, db, time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC))
	buf := captureLogs(t)

	tuesday := time.Date(2026, time.November, 10, 0, 5, 0, 0, time.UTC)
	runWeeklyReport(ctx, db, "", "", true, tuesday, 60, nil)

	out := buf.String()
	if !strings.Contains(out, `"msg":"weekly report already posted"`) || !strings.Contains(out, `"week_start":"2026-11-02"`) {
		t.Errorf("Tuesday's run did not treat the armed week as done: %s", out)
	}
	if strings.Contains(out, "DRY_RUN: would post weekly report") {
		t.Errorf("Tuesday's run previewed the week the guard was armed to: %s", out)
	}
}

// TestArmWeeklyReportGuardSkipsWhenUnreadable: a read error at startup is not
// an absent key. Nothing is written, and the daily run's own seed is left to
// handle a key that really is absent.
func TestArmWeeklyReportGuardSkipsWhenUnreadable(t *testing.T) {
	db := newTestStore(t)
	buf := captureLogs(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	armWeeklyReportGuard(cancelled, db, time.Date(2026, time.November, 8, 20, 0, 0, 0, time.UTC))

	out := buf.String()
	if !strings.Contains(out, `"level":"WARN"`) || !strings.Contains(out, `"msg":"weekly report: guard key unreadable at startup, leaving it to the daily run"`) {
		t.Errorf("unreadable guard was not warned about: %s", out)
	}
	if strings.Contains(out, "guard armed at startup") {
		t.Errorf("a read error was treated as an absent key: %s", out)
	}
	if got, err := db.GetKeyValue(context.Background(), weeklyReportGuardKey); err == nil {
		t.Errorf("guard key was written (%q) after a failed read, want it still unset", got)
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetKeyValue after the failed arm: %v, want a not-found error", err)
	}
}

// TestRunWeeklyReportSkipsWhenGuardUnreadable: a read error that is not
// "no such key" must not be mistaken for a first enable. Arming the guard on a
// database that just failed a read, or posting without knowing what was
// already posted, are both worse than losing one night's run.
func TestRunWeeklyReportSkipsWhenGuardUnreadable(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "weekly.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	buf := captureLogs(t)

	// A cancelled context makes GetKeyValue fail before it reaches the
	// database, with a wrapped context.Canceled rather than sql.ErrNoRows.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	now := time.Date(2026, time.November, 4, 0, 5, 0, 0, time.UTC)

	runWeeklyReport(cancelled, db, "", "", true, now, 60, nil)

	out := buf.String()
	if !strings.Contains(out, `"level":"WARN"`) || !strings.Contains(out, `"msg":"weekly report: guard key unreadable, skipping this run"`) {
		t.Errorf("unreadable guard was not warned about: %s", out)
	}
	if strings.Contains(out, "first enable") {
		t.Errorf("a read error was treated as a first enable: %s", out)
	}
	if strings.Contains(out, "DRY_RUN: would post weekly report") {
		t.Errorf("a read error let the report go ahead: %s", out)
	}
	if got, err := db.GetKeyValue(context.Background(), weeklyReportGuardKey); err == nil {
		t.Errorf("guard key was written (%q) on a run whose read failed, want it still unset", got)
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetKeyValue after the failed run: %v, want a not-found error", err)
	}
}

// TestBuildWeeklyReportTextDropsWrongYearTopic: the grouper is date-blind,
// so a label naming a year before the week costs the post its topic line;
// the week's own years, later years, a cross-year week, January's look back
// at the year just ended, a currency amount and other four-digit numbers do
// not. A bare count that looks like a year is the accepted cost, as is a
// range that ended before the week. The currency and over-60-rune cases
// cannot come out of the grouper's label allowlist today; they pin the
// defence in depth.
func TestBuildWeeklyReportTextDropsWrongYearTopic(t *testing.T) {
	crossYear := time.Date(2025, time.December, 29, 0, 0, 0, 0, time.UTC) // Mon 29 Dec 2025 – Sun 4 Jan 2026
	january := time.Date(2027, time.January, 4, 0, 0, 0, 0, time.UTC)     // Mon 4 – Sun 10 Jan 2027
	february := time.Date(2027, time.February, 1, 0, 0, 0, 0, time.UTC)   // Mon 1 – Sun 7 Feb 2027
	cases := []struct {
		name      string
		label     string
		start     time.Time // zero: the 2–8 Nov 2026 week
		wantYears []string
		wantLine  bool
	}{
		{name: "a past year", label: "2024 US Presidential Election", wantYears: []string{"2024"}},
		{name: "two past years, each reported once", label: "2020 vs 2024: 2024 Turnout", wantYears: []string{"2020", "2024"}},
		{name: "the week's year", label: "2026 Midterm Elections", wantLine: true},
		{name: "a later year is kept", label: "2028 Presidential Race", wantLine: true},
		{name: "a far later year is kept", label: "Agenda 2030", wantLine: true},
		{name: "a past year beyond the 60-rune cut still counts", label: strings.Repeat("Midterms ", 7) + "2024", wantYears: []string{"2024"}},
		{name: "a currency amount is not a year", label: "$2000 Stimulus Checks", wantLine: true},
		{name: "a bare count reads as a year, the accepted cost", label: "Over 2000 Flights Cancelled", wantYears: []string{"2000"}},
		{name: "a year glued to a letter is not a year", label: "Election2024 Recap", wantLine: true},
		{name: "a longer number is not a year", label: "Flight 20245", wantLine: true},
		{name: "a two-digit number is not a year", label: "COVID-19 Inquiry", wantLine: true},
		{name: "a season range ending in the week's year", label: "2025-26 NBA Season", wantLine: true},
		{name: "a fiscal range ending before the week", label: "2024/25 Budget", wantYears: []string{"2024/25"}},
		{name: "a range starting in the week's year", label: "2026/27 Budget", wantLine: true},
		{name: "a range written in full is judged by its end", label: "2024-2026 Term", wantLine: true},
		{name: "a range written in full that ended before the week", label: "2023-2025 Term", wantYears: []string{"2023-2025"}},
		{name: "a range running backwards is judged by its first year", label: "2026-1999 Countdown", wantLine: true},
		{name: "a century roll is a range end", label: "1999-00 Y2K Scare", wantYears: []string{"1999-00"}},
		{name: "a decade range is a range", label: "2016-26 Decade", wantLine: true},
		{name: "a thirty-year range is a range", label: "2020-50 Plan", wantLine: true},
		{name: "a tail past thirty years is not a range", label: "2020-51 Plan", wantYears: []string{"2020"}},
		{name: "a date is judged by its year, not read as a range", label: "2024-11-05 Vote", wantYears: []string{"2024"}},
		{name: "a year-month is judged by its year", label: "2024-12 Recap", wantYears: []string{"2024"}},
		{name: "no label", label: "", wantLine: false},
		{name: "a cross-year week keeps its first year", label: "2025 Review and 2026 Preview", start: crossYear, wantLine: true},
		{name: "a cross-year week still drops an earlier year", label: "2024 Review", start: crossYear, wantYears: []string{"2024"}},
		{name: "a season ending in the week's year is kept in January too", label: "2026-27 NBA Season", start: january, wantLine: true},
		{name: "a January week keeps the year just ended", label: "2026 Year in Review", start: january, wantLine: true},
		{name: "a January week keeps the season that ended last year", label: "2025-26 NBA Season", start: january, wantLine: true},
		{name: "a January week still drops two years back", label: "2025 Recap", start: january, wantYears: []string{"2025"}},
		{name: "a February week drops the year just ended", label: "2026 Year in Review", start: february, wantYears: []string{"2026"}},
		{name: "a February week drops the season that ended last year", label: "2025-26 NBA Season", start: february, wantYears: []string{"2025-26"}},
		{name: "a February week drops a full range that ended last year", label: "2025-2026 Season", start: february, wantYears: []string{"2025-2026"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := previewReport(7, tc.label)
			if !tc.start.IsZero() {
				r.Start, r.End = tc.start, tc.start.AddDate(0, 0, 6)
				r.Days = weekDays(tc.start.Format(dateFormat), []float64{1, 2, 3, 4, 5, 6, 7}, 1_000_000)
			}
			if got := r.droppedTopicYears(); !slices.Equal(got, tc.wantYears) {
				t.Errorf("droppedTopicYears = %v, want %v", got, tc.wantYears)
			}
			text := buildWeeklyReportText(r)
			if got := strings.Contains(text, "Top topic:"); got != tc.wantLine {
				t.Errorf("topic line present = %v, want %v:\n%s", got, tc.wantLine, text)
			}
			if !strings.Contains(text, "English posts analysed") || postLength(text) > blueskyPostLimit {
				t.Errorf("the rest of the post is wrong (%d graphemes):\n%s", postLength(text), text)
			}
		})
	}
}

// TestWeeklyTopicDroppedDetails: the event's details line, as the alert
// message will carry it.
func TestWeeklyTopicDroppedDetails(t *testing.T) {
	got := weeklyTopicDroppedDetails("2026-11-02", []string{"2024", "2020-21"}, `Bush v. Gore "Recount"`)
	want := `week_start=2026-11-02 years=2024,2020-21 label="Bush v. Gore \"Recount\""`
	if got != want {
		t.Errorf("details = %s, want %s", got, want)
	}
}

// TestRunWeeklyReportWarnsWhenTopicDropped: the Monday run of a week whose
// label names an earlier year logs the drop and, in a dry run, previews the
// text without the topic line and writes no event, since nothing was posted.
func TestRunWeeklyReportWarnsWhenTopicDropped(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	seedPreviewWeek(t, ctx, db, "2024 US Presidential Election")
	// The guard names the week before, so this is a normal Monday run.
	if err := db.SetKeyValue(ctx, weeklyReportGuardKey, "2026-10-26"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}
	buf := captureLogs(t)
	events := &fakeEventLogger{}

	runWeeklyReport(ctx, db, "", "", true, time.Date(2026, time.November, 9, 0, 5, 0, 0, time.UTC), 60, events)

	out := buf.String()
	for _, want := range []string{
		`"level":"WARN"`, `"msg":"weekly report: topic label names a year before the week, topic line dropped"`,
		`"week_start":"2026-11-02"`, `"topic":"2024 US Presidential Election"`, `"years":["2024"]`,
		`"msg":"DRY_RUN: would post weekly report"`, `"topic_dropped_years":["2024"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s: %s", want, out)
		}
	}
	if strings.Contains(out, "Top topic") {
		t.Errorf("the dry run previews the topic line the live post drops: %s", out)
	}
	if len(events.events) != 0 {
		t.Errorf("a dry run wrote %v; the event is for a post that went out", events.events)
	}
}

// TestWeeklyReportTopTopicEndToEnd: the topic line reads sentiment_history
// rank-1 labels through topics.SubjectKey, counts distinct clock hours among
// cycles at or above the posting floor, and prints the modal spelling.
func TestWeeklyReportTopTopicEndToEnd(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.November, 9, 0, 30, 0, 0, time.UTC)
	start, _ := previousWeek(now)
	cycle := func(t *testing.T, db *store.Store, at time.Time, label string, posts int) {
		t.Helper()
		dp := store.SentimentDataPoint{RunID: "run-" + at.Format(time.RFC3339), Timestamp: at, TopTopic: label, TotalPosts: posts}
		if err := db.StoreSentimentDataPoint(ctx, dp); err != nil {
			t.Fatalf("StoreSentimentDataPoint: %v", err)
		}
	}
	at := func(day, h, m int) time.Time {
		return start.AddDate(0, 0, day).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}
	topicLine := func(t *testing.T, db *store.Store, cycleMinutes int, want string) {
		t.Helper()
		r, ok := loadWeeklyReport(ctx, db, now, cycleMinutes)
		if !ok {
			t.Fatal("loadWeeklyReport not ok")
		}
		if got := buildWeeklyReportText(r); !slices.Contains(strings.Split(got, "\n"), want) {
			t.Errorf("text lacks line %q:\n%s", want, got)
		}
	}

	t.Run("hourly cycles, two spellings of one subject", func(t *testing.T) {
		db := newTestStore(t)
		seedWeekDays(t, ctx, db, start)
		cycle(t, db, at(0, 10, 55), "2026 US Midterm Elections", minPostsRequired)
		cycle(t, db, at(1, 14, 55), "2026 US Midterm Elections", minPostsRequired)
		cycle(t, db, at(2, 9, 55), "2026 Midterm Elections", minPostsRequired)
		cycle(t, db, at(3, 9, 55), "Halloween Costumes", minPostsRequired)
		// Under the floor: nothing was published, so these are not counted.
		cycle(t, db, at(4, 9, 55), "Halloween Costumes", minPostsRequired-1)
		cycle(t, db, at(4, 10, 55), "Halloween Costumes", 0)
		cycle(t, db, at(4, 11, 55), "Halloween Costumes", 0)
		topicLine(t, db, 60, "Top topic: 2026 US Midterm Elections, #1 for 3 of 168 hours")
	})

	t.Run("30-minute cycles count a clock hour once", func(t *testing.T) {
		db := newTestStore(t)
		seedWeekDays(t, ctx, db, start)
		// Midterms leads both cycles of 10:00 and of 11:00 (4 cycles, 2
		// hours); Halloween leads 3 cycles in 3 different hours.
		for _, ts := range []time.Time{at(0, 10, 25), at(0, 10, 55), at(0, 11, 25), at(0, 11, 55)} {
			cycle(t, db, ts, "2026 US Midterm Elections", minPostsRequired)
		}
		for _, h := range []int{13, 15, 17} {
			cycle(t, db, at(0, h, 25), "Halloween Costumes", minPostsRequired)
		}
		topicLine(t, db, 30, "Top topic: Halloween Costumes, #1 for 3 of 168 hours")

		// A second spelling inside an hour Midterms already led adds nothing;
		// two cycles in a new hour add one, so Midterms leads 10, 11 and 18
		// (3 hours from 7 cycles) and wins the tie with Halloween's 3 on its
		// later latest cycle.
		cycle(t, db, at(0, 11, 40), "2026 Midterm Elections", minPostsRequired)
		cycle(t, db, at(0, 18, 25), "2026 Midterm Elections", minPostsRequired)
		cycle(t, db, at(0, 18, 55), "2026 US Midterm Elections", minPostsRequired)
		topicLine(t, db, 30, "Top topic: 2026 US Midterm Elections, #1 for 3 of 168 hours")
	})
}
