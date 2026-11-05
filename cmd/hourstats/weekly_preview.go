package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/alerts"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// Sunday preview of the coming weekly report, posted to the operator's
// Discord channel so a wrong-year topic label can be caught before Monday.
// ---------------------------------------------------------------------------

// weeklyPreviewPoster is the slice of *alerts.Notifier the preview needs, so
// tests can drive it with a recording fake.
type weeklyPreviewPoster interface {
	PostMessage(ctx context.Context, content string) error
}

// eventLogger is the slice of *stats.Collector the preview writes its failure
// event through.
type eventLogger interface {
	LogEvent(ctx context.Context, eventType, details string) error
}

// weeklyPreviewTarget names the app the preview describes, so the skip
// instruction it prints seeds that app's guard and not another's.
type weeklyPreviewTarget struct {
	Profile string // HOURSTATS_PROFILE, as in the Fly app name hourstats-<profile>
	DBPath  string // the SQLite file on the machine
}

// weeklyPreviewFailedEvent is the stats event written when the Discord post
// fails; internal/alerts raises it at the next snapshot, which is the retry.
const weeklyPreviewFailedEvent = "weekly_report_preview_failed"

// weeklyPreviewWeek is the week the next weekly report will cover: the week
// ending on the Sunday that is today or next, i.e. previousWeek of the Monday
// on or after today. On a Sunday that is the week ending tonight; on a Monday
// the week that just ended, so a manual run on a Monday previews what is
// about to post (or just did) rather than an empty week.
func weeklyPreviewWeek(now time.Time) (start, end time.Time) {
	today := utcDate(now)
	return previousWeek(today.AddDate(0, 0, (8-int(today.Weekday()))%7))
}

// labelYear finds four-digit years (19xx or 20xx) in a topic label.
var labelYear = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)

// buildWeeklyReportPreview renders the Discord message and the list of
// things worth a look before Monday. loadFailed means the week could not be
// read at all (a database error, or a context cancelled during the Sunday
// job): nothing is known about it, so the preview says it is unreviewed and
// never claims Monday will skip it, since Monday's own read will likely
// succeed and post it. days is how many daily rows the week has; fewer than
// seven is stated but is not a flag, since the Sunday run sees rows through
// Saturday.
func buildWeeklyReportPreview(r weeklyReport, loadFailed, ok bool, days int, target weeklyPreviewTarget) (content string, flags []string) {
	weekKey := r.Start.Format(dateFormat)
	label := strings.Join(strings.Fields(r.TopicLabel), " ")
	text := ""
	switch {
	case loadFailed:
		flags = append(flags, "preview unavailable: the week could not be read (see the process log), so Monday's run will post it unreviewed unless skipped")
	case !ok:
		flags = append(flags, fmt.Sprintf("Monday's run will skip: %d daily rows (minimum %d)", days, weeklyReportMinDays))
	default:
		text = buildWeeklyReportText(r)
		if label == "" {
			flags = append(flags, "no topic label (no topic_daily rows for the week)")
		}
		for _, y := range labelYear.FindAllString(label, -1) {
			if y != strconv.Itoa(r.Start.Year()) && y != strconv.Itoa(r.End.Year()) {
				flags = append(flags, "SUSPECT: the grouper is date-blind, the label names "+y)
			}
		}
		switch {
		case label != "" && !strings.Contains(text, "Stickiest topic:"):
			flags = append(flags, "topic line omitted: the post would exceed 300 graphemes with it")
		case truncateRunes(label, maxTopicLabelRunes) != label:
			flags = append(flags, fmt.Sprintf("topic label cut to %d runes", maxTopicLabelRunes))
		}
	}

	lines := []string{fmt.Sprintf("HourStats %s weekly report preview · week %s · posts Monday 00:00 UTC", target.Profile, rangeLabel(r.Start, r.End))}
	if len(flags) == 0 {
		lines = append(lines, "✅ nothing suspect")
	} else {
		lines = append(lines, "⚠️ "+strings.Join(flags, " · "))
	}
	switch {
	case loadFailed:
		// The row count is unknown, so nothing is said about it.
	case days < 7:
		lines = append(lines, fmt.Sprintf("built from %d of 7 days; Sunday's rows land at 00:00 Monday, so the topic and post of the week can change", days))
	default:
		lines = append(lines, "built from all 7 days")
	}
	if len(flags) > 0 {
		lines = append(lines, fmt.Sprintf("to skip this week: `fly ssh console -a hourstats-%s -C \"su-exec hourstats sqlite3 -cmd '.timeout 5000' %s \\\"INSERT OR REPLACE INTO key_value(key,value) VALUES('%s','%s')\\\"\"`",
			target.Profile, target.DBPath, weeklyReportGuardKey, weekKey))
	}
	if text != "" {
		// A backtick in the Gemini label would end the code fence early.
		lines = append(lines, "```", strings.ReplaceAll(text, "`", "'"), "```")
	} else {
		lines = append(lines, "root: not built")
	}
	switch {
	case loadFailed || !ok:
		// The top post is only looked up once the week has enough rows.
		lines = append(lines, "reply: none (root not built)")
	case r.TopPost == nil:
		lines = append(lines, "reply: none (no daily top post for the week)")
	default:
		reply := buildPostOfWeekText(r.Start, r.End, storedPostOfWeekFigures(*r.TopPost))
		if postLength(reply) > blueskyPostLimit {
			reply = "omitted, over 300 graphemes with the stored handle"
		}
		lines = append(lines, "reply (stored counts; the live run reads the gate view): "+strings.ReplaceAll(reply, "\n", " "))
	}
	return strings.Join(lines, "\n"), flags
}

// runWeeklyReportPreview loads the week the next weekly report will cover,
// renders the preview and posts it to Discord. It never touches Bluesky and
// DRY_RUN does not apply to it. A nil poster, or one with no webhook, means
// the preview is only logged; a failed post is warned about and recorded as
// a weekly_report_preview_failed stats event, which the alert path re-raises
// to the same channel at the next snapshot. events may be nil. A week that
// could not be read (the daily read failed, or ctx was cancelled part-way,
// as a SIGTERM during the Sunday job does) is posted as unavailable and
// unreviewed rather than as one Monday will skip.
func runWeeklyReportPreview(ctx context.Context, db *store.Store, poster weeklyPreviewPoster, events eventLogger, target weeklyPreviewTarget, now time.Time, cycleMinutes int) {
	start, _ := weeklyPreviewWeek(now)
	weekKey := start.Format(dateFormat)
	// loadWeeklyReport takes the run time and reads the week before it, so it
	// is asked as of the Monday that follows the previewed week.
	r, ok := loadWeeklyReport(ctx, db, start.AddDate(0, 0, 7), cycleMinutes)
	// loadWeeklyReport leaves Days nil only when the daily read failed. A
	// context cancelled after that read makes the topic and top-post lookups
	// fail and be omitted silently, so that week is unread too.
	loadFailed := r.Days == nil || ctx.Err() != nil
	if loadFailed {
		ok = false
	}
	content, flags := buildWeeklyReportPreview(r, loadFailed, ok, len(r.Days), target)

	var postErr error
	if poster != nil {
		postErr = poster.PostMessage(ctx, content)
	}
	slog.Info("weekly report preview",
		"week_start", weekKey, "loaded", !loadFailed, "ok", ok, "days", len(r.Days), "topic", r.TopicLabel,
		"flags", flags, "discord", poster != nil && postErr == nil, "text", content)
	switch {
	case postErr == nil:
	case errors.Is(postErr, alerts.ErrNoWebhook):
		slog.Info("weekly report preview: no discord webhook, log only", "week_start", weekKey)
	default:
		slog.Warn("weekly report preview: discord post failed", "week_start", weekKey, "error", postErr)
		if events != nil {
			_ = events.LogEvent(ctx, weeklyPreviewFailedEvent, fmt.Sprintf("week_start=%s flags=%d error=%q", weekKey, len(flags), postErr))
		}
	}
}
