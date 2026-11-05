package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/alerts"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

var testPreviewTarget = weeklyPreviewTarget{Profile: "test", DBPath: "/data/hourstats-test.db"}

// the skip instruction the preview must print for the 2–8 Nov 2026 week on
// the test app, exactly as it should be pasted into a shell.
const wantSkipInstruction = "`fly ssh console -a hourstats-test -C \"su-exec hourstats sqlite3 -cmd '.timeout 5000' /data/hourstats-test.db \\\"INSERT OR REPLACE INTO key_value(key,value) VALUES('weekly_report_last_week','2026-11-02')\\\"\"`"

// TestWeeklyPreviewWeek: the preview covers the week the next weekly report
// will, whatever time of the weekend or Monday it is asked.
func TestWeeklyPreviewWeek(t *testing.T) {
	cases := []struct {
		now       time.Time
		wantStart string
	}{
		{time.Date(2026, time.November, 8, 0, 20, 0, 0, time.UTC), "2026-11-02"},  // Sunday, the scheduled run
		{time.Date(2026, time.November, 8, 23, 59, 0, 0, time.UTC), "2026-11-02"}, // Sunday, late manual run
		{time.Date(2026, time.November, 9, 0, 5, 0, 0, time.UTC), "2026-11-02"},   // Monday, the report is about to post
		{time.Date(2026, time.November, 9, 15, 0, 0, 0, time.UTC), "2026-11-02"},  // Monday afternoon, it already posted
		{time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC), "2026-11-09"}, // Tuesday: the week in progress
	}
	for _, tc := range cases {
		start, end := weeklyPreviewWeek(tc.now)
		if start.Format(dateFormat) != tc.wantStart {
			t.Errorf("weeklyPreviewWeek(%s) start = %s, want %s", tc.now.Format(time.RFC3339), start.Format(dateFormat), tc.wantStart)
		}
		if end.Sub(start) != 6*24*time.Hour || end.Weekday() != time.Sunday {
			t.Errorf("weeklyPreviewWeek(%s) end = %s, want the Sunday six days after %s", tc.now.Format(time.RFC3339), end.Format(dateFormat), tc.wantStart)
		}
	}
}

// previewReport builds a weekly report for 2–8 Nov 2026 with the given
// number of days and topic label.
func previewReport(days int, label string) weeklyReport {
	start := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	avgs := make([]float64, days)
	for i := range avgs {
		avgs[i] = 10 + float64(i)
	}
	return weeklyReport{
		Start: start, End: start.AddDate(0, 0, 6),
		Days:       weekDays("2026-11-02", avgs, 1_000_000),
		TopicLabel: label, TopicHours: 40,
		TopPost: &store.Post{URI: "at://did:plc:top/app.bsky.feed.post/1", AuthorHandle: "top.bsky.social", Likes: 9_800, Reposts: 1_200, Replies: 300},
	}
}

func TestBuildWeeklyReportPreview(t *testing.T) {
	cases := []struct {
		name        string
		r           weeklyReport
		ok          bool
		days        int
		wantFlags   []string // substrings, one per flag, in order
		wantContent []string // substrings
		notContent  []string
	}{
		{
			name: "nothing suspect", r: previewReport(6, "2026 Midterm Elections"), ok: true, days: 6,
			wantContent: []string{
				"HourStats test weekly report preview · week 2–8 Nov · posts Monday 00:00 UTC",
				"✅ nothing suspect",
				"built from 6 of 7 days; Sunday's rows land at 00:00 Monday",
				"```\nWeek in review · 2–8 Nov\n",
				"Stickiest topic: 2026 Midterm Elections, trending 40 of 168 hours",
				"reply (stored counts; the live run reads the gate view): Post of the week · 2–8 Nov  Most engaged post by @top.bsky.social: 9.8k likes, 1.2k reposts, 300 replies",
			},
			notContent: []string{"⚠️", "fly ssh console", "@here"},
		},
		{
			name: "all seven days", r: previewReport(7, "Budget"), ok: true, days: 7,
			wantContent: []string{"built from all 7 days"},
			notContent:  []string{"of 7 days;"},
		},
		{
			name: "not enough rows", r: weeklyReport{Start: time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC), End: time.Date(2026, time.November, 8, 0, 0, 0, 0, time.UTC), Days: weekDays("2026-11-02", []float64{1, 2, 3}, 10)}, ok: false, days: 3,
			wantFlags:   []string{"Monday's run will skip: 3 daily rows (minimum 5)"},
			wantContent: []string{"⚠️ Monday's run will skip: 3 daily rows (minimum 5)", "root: not built", "reply: none", wantSkipInstruction},
			notContent:  []string{"Week in review"},
		},
		{
			name: "empty topic label", r: previewReport(6, ""), ok: true, days: 6,
			wantFlags:   []string{"no topic label"},
			wantContent: []string{wantSkipInstruction},
			notContent:  []string{"Stickiest topic"},
		},
		{
			name: "wrong-year label", r: previewReport(6, "2024 US Presidential Election"), ok: true, days: 6,
			wantFlags:   []string{"SUSPECT: the grouper is date-blind, the label names 2024"},
			wantContent: []string{"⚠️ SUSPECT", wantSkipInstruction, "Stickiest topic: 2024 US Presidential Election"},
		},
		{
			name: "a year is only suspect when it is neither of the week's", ok: true, days: 6,
			r: func() weeklyReport {
				r := previewReport(6, "2025 Review and 2026 Preview")
				r.Start = time.Date(2025, time.December, 29, 0, 0, 0, 0, time.UTC)
				r.End = time.Date(2026, time.January, 4, 0, 0, 0, 0, time.UTC)
				return r
			}(),
			wantContent: []string{"✅ nothing suspect"},
		},
		{
			name: "a dollar figure is not a year", r: previewReport(6, "$5000 Stimulus"), ok: true, days: 6,
			wantContent: []string{"✅ nothing suspect"},
		},
		{
			// A long label is cut to 60 runes by buildWeeklyReportText rather
			// than taking the post over the limit; the preview says so.
			name: "long label cut", r: previewReport(6, strings.Repeat("Election ", 10)), ok: true, days: 6,
			wantFlags:   []string{"topic label cut to 60 runes"},
			wantContent: []string{"…, trending 40 of 168 hours", wantSkipInstruction},
		},
		{
			// A backtick in the label would close the code fence early.
			name: "backtick in label", r: previewReport(6, "The `rm -rf` Incident"), ok: true, days: 6,
			wantContent: []string{"Stickiest topic: The 'rm -rf' Incident"},
		},
		{
			name: "no top post", ok: true, days: 6,
			r: func() weeklyReport {
				r := previewReport(6, "Budget")
				r.TopPost = nil
				return r
			}(),
			wantContent: []string{"reply: none (no daily top post for the week)"},
			notContent:  []string{"Most engaged post"},
		},
		{
			name: "reply over the limit is previewed as omitted", ok: true, days: 6,
			r: func() weeklyReport {
				r := previewReport(6, "Budget")
				r.TopPost.AuthorHandle = strings.Repeat("a", blueskyPostLimit)
				return r
			}(),
			wantContent: []string{"reply (stored counts; the live run reads the gate view): omitted, over 300 graphemes with the stored handle"},
			notContent:  []string{"Most engaged post"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, flags := buildWeeklyReportPreview(tc.r, tc.ok, tc.days, testPreviewTarget)
			if len(flags) != len(tc.wantFlags) {
				t.Errorf("flags = %q, want %d: %q", flags, len(tc.wantFlags), tc.wantFlags)
			}
			for i, want := range tc.wantFlags {
				if i < len(flags) && !strings.Contains(flags[i], want) {
					t.Errorf("flag[%d] = %q, want it to contain %q", i, flags[i], want)
				}
			}
			for _, want := range tc.wantContent {
				if !strings.Contains(content, want) {
					t.Errorf("content lacks %q:\n%s", want, content)
				}
			}
			for _, not := range tc.notContent {
				if strings.Contains(content, not) {
					t.Errorf("content has %q, want it absent:\n%s", not, content)
				}
			}
			// The skip instruction is printed exactly when something is flagged.
			if hasSkip := strings.Contains(content, "to skip this week: `fly ssh console"); hasSkip != (len(flags) > 0) {
				t.Errorf("skip instruction present = %v with flags %q", hasSkip, flags)
			}
			if strings.Contains(content, "ALERT_DISCORD") || strings.Contains(content, "did:plc:") {
				t.Errorf("content carries a DID or a secret name:\n%s", content)
			}
			if n := len([]rune(content)); n > 1950 {
				t.Errorf("content is %d runes, Discord would need it truncated", n)
			}
		})
	}
}

// fakePreviewPoster records what the runner asks it to post.
type fakePreviewPoster struct {
	contents []string
	err      error
}

func (f *fakePreviewPoster) PostMessage(_ context.Context, content string) error {
	f.contents = append(f.contents, content)
	return f.err
}

// fakeEventLogger records the stats events the runner writes.
type fakeEventLogger struct {
	events []string
}

func (f *fakeEventLogger) LogEvent(_ context.Context, eventType, details string) error {
	f.events = append(f.events, eventType+" "+details)
	return nil
}

// seedPreviewWeek stores Monday to Saturday of the week starting 2 Nov 2026,
// a top post on the Wednesday, and one trending snapshot that day carrying
// label, rolled up into topic_daily as the daily job would have.
func seedPreviewWeek(t *testing.T, ctx context.Context, db *store.Store, label string) {
	t.Helper()
	start := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		day := start.AddDate(0, 0, i).Format(dateFormat)
		if err := db.StoreDailySentiment(ctx, store.DailySentimentDataPoint{
			Date: day, RunID: "run-" + day, AverageSentiment: 10 + float64(i), MinSentiment: 5, MaxSentiment: 15,
			Q1Sentiment: 8, MedianSentiment: 10, Q3Sentiment: 12, TotalRuns: 24, TotalPosts: 1_000_000,
		}); err != nil {
			t.Fatalf("StoreDailySentiment(%s): %v", day, err)
		}
	}
	wed := start.AddDate(0, 0, 2)
	top := store.Post{URI: "at://did:plc:top/app.bsky.feed.post/1", CID: "cid", AuthorHandle: "top.bsky.social", Likes: 9_800, Reposts: 1_200, Replies: 300, EngagementScore: 12_000}
	if err := db.StoreDailyTopPost(ctx, wed.Format(dateFormat), top); err != nil {
		t.Fatalf("StoreDailyTopPost: %v", err)
	}
	snap := wed.Add(14 * time.Hour).Format(time.RFC3339)
	if err := db.InsertTopicSnapshot(ctx, snap, 1, "topic-1", label, "", 50, "[]", "[]", "", "", false, ""); err != nil {
		t.Fatalf("InsertTopicSnapshot: %v", err)
	}
	if _, err := db.RollupTopicDaily(ctx, wed.Format(dateFormat)); err != nil {
		t.Fatalf("RollupTopicDaily: %v", err)
	}
}

// TestRunWeeklyReportPreviewPostsToDiscord: on Sunday 8 Nov the preview is
// built for the week ending that night (2–8 Nov), reaches the poster with the
// wrong-year label flagged, and is logged with discord=true.
func TestRunWeeklyReportPreviewPostsToDiscord(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	seedPreviewWeek(t, ctx, db, "2024 US Presidential Election")
	buf := captureLogs(t)
	poster := &fakePreviewPoster{}
	events := &fakeEventLogger{}
	now := time.Date(2026, time.November, 8, 0, 20, 0, 0, time.UTC)

	runWeeklyReportPreview(ctx, db, poster, events, testPreviewTarget, now, 60)

	if len(poster.contents) != 1 {
		t.Fatalf("posted %d messages, want 1", len(poster.contents))
	}
	content := poster.contents[0]
	t.Logf("rendered preview:\n%s", content)
	for _, want := range []string{
		"HourStats test weekly report preview · week 2–8 Nov",
		"⚠️ SUSPECT: the grouper is date-blind, the label names 2024",
		"built from 6 of 7 days",
		wantSkipInstruction,
		"Stickiest topic: 2024 US Presidential Election, trending 1 of 168 hours",
		"6M English posts analysed",
		"Most engaged post by @top.bsky.social: 9.8k likes, 1.2k reposts, 300 replies",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("posted content lacks %q:\n%s", want, content)
		}
	}
	out := buf.String()
	for _, want := range []string{
		`"msg":"weekly report preview"`, `"week_start":"2026-11-02"`, `"ok":true`, `"days":6`,
		`"topic":"2024 US Presidential Election"`, `"discord":true`, `"flags":["SUSPECT`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s: %s", want, out)
		}
	}
	if strings.Contains(out, `"level":"WARN"`) || len(events.events) != 0 {
		t.Errorf("a successful preview warned or wrote an event: %s %v", out, events.events)
	}
}

// TestRunWeeklyReportPreviewLogOnly: a nil poster, or a notifier without a
// webhook, means the preview is only logged; neither is a failure.
func TestRunWeeklyReportPreviewLogOnly(t *testing.T) {
	cases := []struct {
		name   string
		poster weeklyPreviewPoster
	}{
		{name: "nil poster", poster: nil},
		{name: "unset webhook", poster: alerts.NewNotifier("test", "", nil)},
		{name: "wrapped ErrNoWebhook", poster: &fakePreviewPoster{err: fmt.Errorf("wrapped: %w", alerts.ErrNoWebhook)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestStore(t)
			ctx := context.Background()
			seedPreviewWeek(t, ctx, db, "Budget")
			buf := captureLogs(t)
			events := &fakeEventLogger{}
			now := time.Date(2026, time.November, 8, 0, 20, 0, 0, time.UTC)

			runWeeklyReportPreview(ctx, db, tc.poster, events, testPreviewTarget, now, 60)

			out := buf.String()
			if !strings.Contains(out, `"msg":"weekly report preview"`) || !strings.Contains(out, `"discord":false`) {
				t.Errorf("log lacks the preview line with discord=false: %s", out)
			}
			if !strings.Contains(out, `"text":"HourStats test weekly report preview`) {
				t.Errorf("log-only preview does not carry the text: %s", out)
			}
			if strings.Contains(out, `"level":"WARN"`) || len(events.events) != 0 {
				t.Errorf("log-only preview warned or wrote an event: %s %v", out, events.events)
			}
		})
	}
}

// TestRunWeeklyReportPreviewFailureIsAnEvent: a failed Discord post is warned
// about and recorded as weekly_report_preview_failed, which the alert path
// raises to the channel at the next snapshot.
func TestRunWeeklyReportPreviewFailureIsAnEvent(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	seedPreviewWeek(t, ctx, db, "Budget")
	buf := captureLogs(t)
	poster := &fakePreviewPoster{err: errors.New("post discord message: discord returned status 500")}
	events := &fakeEventLogger{}
	now := time.Date(2026, time.November, 8, 0, 20, 0, 0, time.UTC)

	runWeeklyReportPreview(ctx, db, poster, events, testPreviewTarget, now, 60)

	out := buf.String()
	if !strings.Contains(out, `"msg":"weekly report preview: discord post failed"`) || !strings.Contains(out, "status 500") {
		t.Errorf("failure not warned about: %s", out)
	}
	if !strings.Contains(out, `"discord":false`) {
		t.Errorf("preview line claims the post went out: %s", out)
	}
	if len(events.events) != 1 || !strings.HasPrefix(events.events[0], weeklyPreviewFailedEvent+" week_start=2026-11-02 ") {
		t.Errorf("events = %q, want one %s for 2026-11-02", events.events, weeklyPreviewFailedEvent)
	}
	// With no collector the failure is still logged and nothing panics.
	runWeeklyReportPreview(ctx, db, poster, nil, testPreviewTarget, now, 60)
}

// TestRunWeeklyReportPreviewEmptyWeek: a week with no rows is previewed as the
// skip Monday will make, with the row count, rather than a crash.
func TestRunWeeklyReportPreviewEmptyWeek(t *testing.T) {
	db := newTestStore(t)
	buf := captureLogs(t)
	poster := &fakePreviewPoster{}
	now := time.Date(2026, time.November, 8, 0, 20, 0, 0, time.UTC)

	runWeeklyReportPreview(context.Background(), db, poster, nil, testPreviewTarget, now, 60)

	if len(poster.contents) != 1 || !strings.Contains(poster.contents[0], "Monday's run will skip: 0 daily rows (minimum 5)") {
		t.Errorf("posted = %q", poster.contents)
	}
	if out := buf.String(); !strings.Contains(out, `"ok":false`) || !strings.Contains(out, `"days":0`) {
		t.Errorf("log lacks ok=false days=0: %s", out)
	}
}
