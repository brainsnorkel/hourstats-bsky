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
		loadFailed  bool
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
				"Top topic: 2026 Midterm Elections, #1 for 40 of 168 hours",
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
			// A failed read is not an empty week: Monday's own read will likely
			// succeed and post it, so the preview must not say it will skip.
			name: "read failed", r: weeklyReport{Start: time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC), End: time.Date(2026, time.November, 8, 0, 0, 0, 0, time.UTC)}, loadFailed: true, ok: false, days: 0,
			wantFlags:   []string{"preview unavailable: the week could not be read"},
			wantContent: []string{"⚠️ preview unavailable", "Monday's run will post it unreviewed unless skipped", wantSkipInstruction, "root: not built", "reply: none (root not built)"},
			notContent:  []string{"will skip", "daily rows", "built from", "Week in review", "no daily top post"},
		},
		{
			name: "empty topic label", r: previewReport(6, ""), ok: true, days: 6,
			wantFlags:   []string{"no topic label"},
			wantContent: []string{wantSkipInstruction},
			notContent:  []string{"Top topic"},
		},
		{
			// The posting path drops the line (buildWeeklyReportText), so the
			// preview shows the text without it and quotes the label it lost.
			name: "wrong-year label", r: previewReport(6, "2024 US Presidential Election"), ok: true, days: 6,
			wantFlags:   []string{"topic line dropped: the grouper is date-blind and the label `2024 US Presidential Election` names 2024"},
			wantContent: []string{"⚠️ topic line dropped", wantSkipInstruction, "```\nWeek in review · 2–8 Nov\n", "English posts analysed"},
			notContent:  []string{"Top topic", "topic line omitted", "SUSPECT"},
		},
		{
			name: "a cross-year week keeps both of its years", ok: true, days: 6,
			r: func() weeklyReport {
				r := previewReport(6, "2025 Review and 2026 Preview")
				r.Start = time.Date(2025, time.December, 29, 0, 0, 0, 0, time.UTC)
				r.End = time.Date(2026, time.January, 4, 0, 0, 0, 0, time.UTC)
				return r
			}(),
			wantContent: []string{"✅ nothing suspect"},
		},
		{
			name: "a currency amount is not a year", r: previewReport(6, "$2000 Stimulus"), ok: true, days: 6,
			wantContent: []string{"✅ nothing suspect"},
		},
		{
			// A long label is cut to 60 runes by buildWeeklyReportText rather
			// than taking the post over the limit; the preview says so.
			name: "long label cut", r: previewReport(6, strings.Repeat("Election ", 10)), ok: true, days: 6,
			wantFlags:   []string{"topic label cut to 60 runes"},
			wantContent: []string{"…, #1 for 40 of 168 hours", wantSkipInstruction},
		},
		{
			// A backtick in the label would close the code fence early.
			name: "backtick in label", r: previewReport(6, "The `rm -rf` Incident"), ok: true, days: 6,
			wantContent: []string{"Top topic: The 'rm -rf' Incident"},
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
			content, flags := buildWeeklyReportPreview(tc.r, tc.loadFailed, tc.ok, tc.days, testPreviewTarget)
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
// a top post on the Wednesday, and one cycle that day whose rank-1 topic
// was label, as the analysis cycle records it on sentiment_history.
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
	cycle := wed.Add(14 * time.Hour)
	if err := db.StoreSentimentDataPoint(ctx, store.SentimentDataPoint{RunID: "run-cycle", Timestamp: cycle, TopTopic: label, TotalPosts: minPostsRequired}); err != nil {
		t.Fatalf("StoreSentimentDataPoint: %v", err)
	}
}

// TestRunWeeklyReportPreviewPostsToDiscord: on Sunday 8 Nov the preview is
// built for the week ending that night (2–8 Nov), reaches the poster with the
// wrong-year label reported as dropped, and is logged with discord=true.
func TestRunWeeklyReportPreviewPostsToDiscord(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	seedPreviewWeek(t, ctx, db, "2024 US Presidential Election")
	buf := captureLogs(t)
	poster := &fakePreviewPoster{}
	events := &fakeEventLogger{}
	now := time.Date(2026, time.November, 8, 0, 20, 0, 0, time.UTC)

	runWeeklyReportPreview(ctx, db, poster, events, testPreviewTarget, now)

	if len(poster.contents) != 1 {
		t.Fatalf("posted %d messages, want 1", len(poster.contents))
	}
	content := poster.contents[0]
	t.Logf("rendered preview:\n%s", content)
	for _, want := range []string{
		"HourStats test weekly report preview · week 2–8 Nov",
		"⚠️ topic line dropped: the grouper is date-blind and the label `2024 US Presidential Election` names 2024",
		"built from 6 of 7 days",
		wantSkipInstruction,
		"6M English posts analysed",
		"Most engaged post by @top.bsky.social: 9.8k likes, 1.2k reposts, 300 replies",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("posted content lacks %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "Top topic") {
		t.Errorf("posted content carries the topic line the posting path drops:\n%s", content)
	}
	out := buf.String()
	for _, want := range []string{
		`"msg":"weekly report preview"`, `"week_start":"2026-11-02"`, `"ok":true`, `"days":6`,
		`"topic":"2024 US Presidential Election"`, `"discord":true`, `"flags":["topic line dropped`,
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

			runWeeklyReportPreview(ctx, db, tc.poster, events, testPreviewTarget, now)

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

	runWeeklyReportPreview(ctx, db, poster, events, testPreviewTarget, now)

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
	runWeeklyReportPreview(ctx, db, poster, nil, testPreviewTarget, now)
}

// TestRunWeeklyReportPreviewEmptyWeek: a week with no rows is previewed as the
// skip Monday will make, with the row count, rather than a crash.
func TestRunWeeklyReportPreviewEmptyWeek(t *testing.T) {
	db := newTestStore(t)
	buf := captureLogs(t)
	poster := &fakePreviewPoster{}
	now := time.Date(2026, time.November, 8, 0, 20, 0, 0, time.UTC)

	runWeeklyReportPreview(context.Background(), db, poster, nil, testPreviewTarget, now)

	if len(poster.contents) != 1 || !strings.Contains(poster.contents[0], "Monday's run will skip: 0 daily rows (minimum 5)") {
		t.Errorf("posted = %q", poster.contents)
	}
	if out := buf.String(); !strings.Contains(out, `"loaded":true`) || !strings.Contains(out, `"ok":false`) || !strings.Contains(out, `"days":0`) {
		t.Errorf("log lacks loaded=true ok=false days=0: %s", out)
	}
}

// TestRunWeeklyReportPreviewUnreadWeekIsNotASkip: a week that could not be
// read, because the daily read failed or the context was cancelled (a
// SIGTERM landing during the Sunday job), is posted as unavailable and
// unreviewed. Telling the operator Monday will skip it would be false:
// Monday's own read will succeed and post the week.
func TestRunWeeklyReportPreviewUnreadWeekIsNotASkip(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, db *store.Store) context.Context
	}{
		{
			name: "context cancelled before the read",
			setup: func(t *testing.T, db *store.Store) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
		},
		{
			name: "database read fails",
			setup: func(t *testing.T, db *store.Store) context.Context {
				if err := db.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
				return context.Background()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestStore(t)
			seedPreviewWeek(t, context.Background(), db, "Budget")
			ctx := tc.setup(t, db)
			buf := captureLogs(t)
			poster := &fakePreviewPoster{}
			events := &fakeEventLogger{}
			now := time.Date(2026, time.November, 8, 0, 20, 0, 0, time.UTC)

			runWeeklyReportPreview(ctx, db, poster, events, testPreviewTarget, now)

			if len(poster.contents) != 1 {
				t.Fatalf("posted %d messages, want 1", len(poster.contents))
			}
			content := poster.contents[0]
			for _, want := range []string{"⚠️ preview unavailable: the week could not be read", "unreviewed", wantSkipInstruction, "root: not built"} {
				if !strings.Contains(content, want) {
					t.Errorf("posted content lacks %q:\n%s", want, content)
				}
			}
			for _, not := range []string{"will skip", "daily rows", "built from", "Week in review", "Budget"} {
				if strings.Contains(content, not) {
					t.Errorf("posted content has %q, want it absent:\n%s", not, content)
				}
			}
			out := buf.String()
			for _, want := range []string{`"msg":"weekly report preview"`, `"week_start":"2026-11-02"`, `"loaded":false`, `"ok":false`, `"discord":true`} {
				if !strings.Contains(out, want) {
					t.Errorf("log lacks %s: %s", want, out)
				}
			}
			if len(events.events) != 0 {
				t.Errorf("the post went out, yet an event was written: %v", events.events)
			}
		})
	}
}
