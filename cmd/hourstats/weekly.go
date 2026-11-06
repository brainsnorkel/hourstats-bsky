package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// Weekly week-in-review thread (Monday, after the daily cycle)
// ---------------------------------------------------------------------------

const (
	// weeklyReportGuardKey holds the Monday date of the last week reported,
	// or, when no week has been reported yet, the Monday of the most recent
	// complete week at the moment the process first started with
	// REPORTS_ENABLED (armWeeklyReportGuard): that week is deliberately never
	// posted, so switching reports on mid-week does not publish a review that
	// is already days stale. Arming at startup rather than in the daily job is
	// what lets a Sunday deploy report the week that ends that night: the seed
	// names the week before it, and Monday's job finds a newer week. Unset
	// means reports have never been enabled on this database.
	weeklyReportGuardKey = "weekly_report_last_week"
	// weeklyReportMinDays is how many of the seven daily rows must exist.
	weeklyReportMinDays = 5
	// weekHours is the number of hourly trending snapshots in a full week.
	weekHours = 7 * 24
)

// weeklyReport is everything the two weekly posts are built from.
type weeklyReport struct {
	Start, End time.Time
	Days       []store.DailySentimentDataPoint // the week, ordered
	PrevDays   []store.DailySentimentDataPoint // the week before, for the delta
	TopicLabel string                          // "" when no topic data exists
	TopicHours int                             // hours the topic trended, from cycle appearances
	TopPost    *store.Post                     // nil when no daily top post exists
}

// topicHours converts trending-cycle appearances to hours for the
// configured analysis interval, capped at the hours in a week.
func topicHours(appearances, cycleMinutes int) int {
	if cycleMinutes <= 0 {
		cycleMinutes = 60
	}
	h := int(math.Round(float64(appearances*cycleMinutes) / 60))
	return min(h, weekHours)
}

// buildWeeklyReportText renders the root post.
func buildWeeklyReportText(r weeklyReport) string {
	avg := meanDailyAverage(r.Days)
	hi, lo := happiestUnhappiest(r.Days)

	mood := fmt.Sprintf("Mood: %s %s", signedPct(avg), moodPhrase(avg))
	if len(r.PrevDays) >= weeklyReportMinDays {
		mood += fmt.Sprintf(", %s vs the week before", signedPoints(avg-meanDailyAverage(r.PrevDays)))
	}

	head := []string{
		"Week in review · " + rangeLabel(r.Start, r.End),
		"",
		mood,
		fmt.Sprintf("Happiest day: %s, %s", dayLabel(r.Days[hi].Date), signedPct(r.Days[hi].AverageSentiment)),
		fmt.Sprintf("Unhappiest day: %s, %s", dayLabel(r.Days[lo].Date), signedPct(r.Days[lo].AverageSentiment)),
	}
	tail := []string{"", fmt.Sprintf("%s English posts analysed", compactCount(sumPosts(r.Days)))}

	// The topic label is the only free text: cap it, drop the line when the
	// label names a year before the week (droppedTopicYears), and drop it
	// rather than let the post exceed the limit.
	label := truncateRunes(strings.Join(strings.Fields(r.TopicLabel), " "), maxTopicLabelRunes)
	if label != "" && len(r.droppedTopicYears()) == 0 {
		topic := fmt.Sprintf("Stickiest topic: %s, trending %d of %d hours", label, r.TopicHours, weekHours)
		withTopic := strings.Join(append(append(head, topic), tail...), "\n")
		if postLength(withTopic) <= blueskyPostLimit {
			return withTopic
		}
	}
	return strings.Join(append(head, tail...), "\n")
}

// maxTopicLabelRunes bounds the stickiest-topic label in the weekly post.
const maxTopicLabelRunes = 60

// labelYear finds four-digit years (19xx or 20xx) in a topic label, with an
// optional range tail of two or four digits ("2026-27", "2024/25",
// "2025-2026"). The character before the year is captured, RE2 having no
// lookbehind, so a currency amount ("$2000") and a year glued to a letter
// ("Election2024") are not years; a bare count ("Over 2000 Flights") still
// is. The grouper's label allowlist (validLabelPattern in internal/topics)
// admits no currency sign and folds dashes to "-", so those two exclusions
// are defence in depth for a label stored under other rules.
var labelYear = regexp.MustCompile(`(^|[^$£€¥\w])((?:19|20)\d{2})(?:[-–/]((?:19|20)\d{2}|\d{2}))?\b`)

// weeklyTopicDroppedEvent is the stats event written when the posted weekly
// report went out without its topic line; internal/alerts raises it as a
// warning at the next snapshot, so a drop that the Sunday preview could not
// foresee (the topic can change once Sunday's rows land) still reaches the
// operator.
const weeklyTopicDroppedEvent = "weekly_report_topic_dropped"

// droppedTopicYears returns the years in the topic label that end before the
// week's first year, as written ("2024", "2024/25"), distinct and in label
// order. A non-empty result costs the post its topic line
// (buildWeeklyReportText) and the Sunday preview quotes the dropped label.
// The grouper is date-blind: it has labelled the 2026 midterms a 2024
// election, and a label naming an earlier year is wrong far more often than
// it is about that year. Later years are kept, since talk of 2028 the week
// after the midterms is a real topic and the grouper's slips run backwards.
// In January the year just ended is kept too: "2026 Year in Review" in the
// first week of 2027 is right, not a slip. A label genuinely about an
// earlier year ("the 2020 election") is the cost: it loses the line too.
func (r weeklyReport) droppedTopicYears() []string {
	floor := r.Start.Year()
	if r.Start.Month() == time.January {
		floor--
	}
	var years []string
	for _, m := range labelYear.FindAllStringSubmatch(r.TopicLabel, -1) {
		year, _ := strconv.Atoi(m[2])
		shown := m[2]
		if end, _ := strconv.Atoi(m[3]); len(m[3]) == 4 {
			// A range running backwards ("2026-1999") is not a range.
			if end >= year {
				year, shown = end, m[0][len(m[1]):]
			}
		} else if m[3] != "" {
			// A two-digit tail is a range end only when it lands within
			// thirty years after the start, in the start's century or the
			// next ("1999-00"). A month ("2024-11-05", "2024-12") never does
			// for a start year past 2012, and the label is judged by its year
			// alone.
			if end += year / 100 * 100; end <= year {
				end += 100
			}
			if end <= year+30 {
				year, shown = end, m[0][len(m[1]):]
			}
		}
		if year < floor && !slices.Contains(years, shown) {
			years = append(years, shown)
		}
	}
	return years
}

// weeklyTopicDroppedDetails is the weekly_report_topic_dropped event's
// details, which internal/alerts carries into the Discord message. The label
// comes from the grouper's allowlist, so it has no newline or mention in it.
func weeklyTopicDroppedDetails(weekKey string, dropped []string, label string) string {
	return fmt.Sprintf("week_start=%s years=%s label=%q", weekKey, strings.Join(dropped, ","), label)
}

// truncateRunes shortens s to at most n runes, ending with an ellipsis when
// it was cut.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := r[:n-1]
	// Do not end on a joiner or modifier that belonged to the next glyph.
	for len(cut) > 0 && (unicode.Is(unicode.Mn, cut[len(cut)-1]) || cut[len(cut)-1] == 0x200D || cut[len(cut)-1] == 0xFE0F || cut[len(cut)-1] == ' ') {
		cut = cut[:len(cut)-1]
	}
	return string(cut) + "…"
}

// postOfWeekFigures is what the post-of-the-week reply prints about the post.
type postOfWeekFigures struct {
	Handle                  string
	Likes, Reposts, Replies int
	// Source records where the figures came from: "live" when every one was
	// taken from the feature gate's verdict, "stored" when every one is the
	// daily_top_post value, "partial" otherwise.
	Source string
}

// Values of postOfWeekFigures.Source.
const (
	figuresLive    = "live"
	figuresStored  = "stored"
	figuresPartial = "partial"
)

// invalidHandle is the AppView's marker for an author whose handle could not
// be resolved. Printing it would name nobody.
const invalidHandle = "handle.invalid"

// storedPostOfWeekFigures reads the figures as daily_top_post recorded them:
// the engagement at the hour the post was first seen.
func storedPostOfWeekFigures(p store.Post) postOfWeekFigures {
	return postOfWeekFigures{Handle: p.AuthorHandle, Likes: p.Likes, Reposts: p.Reposts, Replies: p.Replies, Source: figuresStored}
}

// resolvePostOfWeekFigures prefers the feature gate's verdict, which the
// AppView answered moments ago, over the stored row, which holds the counts
// from the hour the post was first seen, typically a quarter of what it has a
// week later. The live handle is used when it is non-empty and resolved; the
// live counts only when the verdict carries all three. Each field that the
// verdict cannot supply keeps its stored value.
func resolvePostOfWeekFigures(stored store.Post, v client.Verdict) postOfWeekFigures {
	f := storedPostOfWeekFigures(stored)
	liveHandle := v.AuthorHandle != "" && v.AuthorHandle != invalidHandle
	if liveHandle {
		f.Handle = v.AuthorHandle
	}
	if v.HasCounts {
		f.Likes, f.Reposts, f.Replies = v.Likes, v.Reposts, v.Replies
	}
	switch {
	case liveHandle && v.HasCounts:
		f.Source = figuresLive
	case !liveHandle && !v.HasCounts:
		f.Source = figuresStored
	default:
		f.Source = figuresPartial
	}
	return f
}

// buildPostOfWeekText renders the reply that quotes the week's top post.
func buildPostOfWeekText(start, end time.Time, f postOfWeekFigures) string {
	return fmt.Sprintf("Post of the week · %s\n\nMost engaged post by @%s: %s likes, %s reposts, %s replies",
		rangeLabel(start, end), f.Handle,
		compactCount(f.Likes), compactCount(f.Reposts), compactCount(f.Replies))
}

// loadWeeklyReport gathers the previous week's data. ok is false, with the
// reason logged, when there is not enough to report on or the daily rows
// could not be read; the two are told apart by r.Days, which is nil only on
// a failed read.
func loadWeeklyReport(ctx context.Context, db *store.Store, now time.Time, cycleMinutes int) (weeklyReport, bool) {
	start, end := previousWeek(now)
	r := weeklyReport{Start: start, End: end}

	days, err := db.GetDailySentimentRange(ctx, start.Format(dateFormat), end.Format(dateFormat))
	if err != nil {
		slog.Error("weekly report: get daily range failed", "error", err)
		return r, false
	}
	// The rows are kept even when too few, so a caller reporting the skip
	// (the Sunday preview) can say how many there were. They are non-nil from
	// here, even for an empty week, so that a nil Days means the read above
	// failed and the preview does not pass that off as a week Monday will skip.
	r.Days = days
	if r.Days == nil {
		r.Days = []store.DailySentimentDataPoint{}
	}
	if len(days) < weeklyReportMinDays {
		slog.Info("weekly report: not enough daily rows, skipping",
			"week_start", start.Format(dateFormat), "days", len(days), "min", weeklyReportMinDays)
		return r, false
	}

	prevStart, prevEnd := start.AddDate(0, 0, -7), start.AddDate(0, 0, -1)
	if prev, err := db.GetDailySentimentRange(ctx, prevStart.Format(dateFormat), prevEnd.Format(dateFormat)); err != nil {
		slog.Warn("weekly report: get previous week failed, omitting delta", "error", err)
	} else {
		r.PrevDays = prev
	}

	if label, hours, err := db.GetTopTopicForRange(ctx, start.Format(dateFormat), end.Format(dateFormat)); err != nil {
		slog.Warn("weekly report: top topic lookup failed, omitting topic line", "error", err)
	} else {
		r.TopicLabel, r.TopicHours = label, topicHours(hours, cycleMinutes)
	}

	if top, err := db.GetTopPostForRange(ctx, start.Format(dateFormat), end.Format(dateFormat)); err != nil {
		slog.Warn("weekly report: top post lookup failed, omitting reply", "error", err)
	} else {
		r.TopPost = top
	}
	return r, true
}

// armWeeklyReportGuard seeds the guard key at process start when it is
// absent, naming the most recent complete week so the first report covers the
// week that completes next. It runs at startup rather than inside the daily
// job because the job only runs at 00:00: a deploy on a Sunday would seed on
// Monday 00:00 to the week that had just ended, and that week would never be
// posted. An existing key is left alone. A read error other than
// sql.ErrNoRows is warned about and nothing is written; a failed write is
// warned about too, and runWeeklyReport's own seed covers a key still absent
// that night.
func armWeeklyReportGuard(ctx context.Context, db *store.Store, now time.Time) {
	last, err := db.GetKeyValue(ctx, weeklyReportGuardKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("weekly report: guard key unreadable at startup, leaving it to the daily run", "error", err)
		return
	}
	if last != "" {
		slog.Info("weekly report: guard already set", "week_start", last)
		return
	}
	start, _ := previousWeek(now)
	weekKey := start.Format(dateFormat)
	if err := db.SetKeyValue(ctx, weeklyReportGuardKey, weekKey); err != nil {
		slog.Warn("weekly report: arming guard at startup failed, the daily run will seed it", "error", err, "week_start", weekKey)
		return
	}
	slog.Info("weekly report: guard armed at startup, first report is the next complete week", "week_start", weekKey)
}

// runWeeklyReport posts the week-in-review root and, when a top post is
// known, the post-of-the-week quote reply. The guard key is set once the
// root is posted, so a failed reply is not retried by a later re-run.
//
// The guard is normally armed at process start (armWeeklyReportGuard). When
// it is still unset here, because the startup arm could not read or write
// it, this run posts nothing: the key is set to the most recent complete week
// and the function returns, and the first review goes out for the week that
// completes next. A later run posts whenever the guard names an older week,
// so a skipped Monday is still caught up the next night.
//
// When the guard key cannot be read for any reason other than sql.ErrNoRows
// (its absence), this run is skipped with a Warn and nothing is written: the
// error is not a first enable, and without the guard a post could duplicate
// one already out.
func runWeeklyReport(ctx context.Context, db *store.Store, handle, password string, dryRun bool, now time.Time, cycleMinutes int, events eventLogger) {
	start, _ := previousWeek(now)
	weekKey := start.Format(dateFormat)
	last, err := db.GetKeyValue(ctx, weeklyReportGuardKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// Without the guard a post here could duplicate one already out, and a
		// first-enable write would land on a database that just failed a read.
		slog.Warn("weekly report: guard key unreadable, skipping this run", "error", err, "week_start", weekKey)
		return
	}
	if last == "" {
		if err := db.SetKeyValue(ctx, weeklyReportGuardKey, weekKey); err != nil {
			slog.Error("persist weekly report guard failed", "error", err, "week_start", weekKey)
			return
		}
		slog.Info("weekly report: first enable, starting from the next complete week", "week_start", weekKey)
		return
	}
	if last == weekKey {
		slog.Info("weekly report already posted", "week_start", weekKey)
		return
	}

	r, ok := loadWeeklyReport(ctx, db, now, cycleMinutes)
	if !ok {
		return
	}
	dropped := r.droppedTopicYears()
	if len(dropped) > 0 {
		slog.Warn("weekly report: topic label names a year before the week, topic line dropped",
			"week_start", weekKey, "topic", r.TopicLabel, "years", dropped)
	}
	text := buildWeeklyReportText(r)
	if n := postLength(text); n > blueskyPostLimit {
		slog.Error("weekly report text over limit, skipping", "length", n)
		return
	}

	if dryRun {
		// No client is built in a dry run, so there is no verdict to take live
		// figures from: the preview shows the stored ones, held to the same
		// length rule as the live reply. A stored handle that puts the reply
		// over the limit is previewed as the omission the live run would log,
		// not as a reply; the live handle can differ, so this previews the
		// rule rather than the live outcome.
		replyText, replyLength, replyOmitted := "", 0, false
		if r.TopPost != nil {
			replyText = buildPostOfWeekText(r.Start, r.End, storedPostOfWeekFigures(*r.TopPost))
			if replyLength = postLength(replyText); replyLength > blueskyPostLimit {
				replyOmitted, replyText = true, ""
			}
		}
		slog.Info("DRY_RUN: would post weekly report",
			"week_start", weekKey, "days", len(r.Days), "prev_days", len(r.PrevDays),
			"topic", r.TopicLabel, "topic_hours", r.TopicHours, "topic_dropped_years", dropped,
			"has_top_post", r.TopPost != nil,
			"text", text, "text_length", postLength(text),
			"reply", replyText, "reply_length", replyLength, "reply_omitted", replyOmitted,
			"counts_source", figuresStored)
		return
	}

	apiCtx, apiCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer apiCancel()

	bskyClient := client.New(handle, password)
	bskyClient.SetDryRun(dryRun)
	if err := bskyClient.Authenticate(); err != nil {
		slog.Error("bluesky auth for weekly report failed", "error", err)
		return
	}

	rootURI, rootCID, err := bskyClient.PostWithFacetsRef(apiCtx, text, nil)
	if err != nil {
		slog.Error("post weekly report failed", "error", err)
		return
	}
	slog.Info("weekly report posted", "week_start", weekKey, "uri", rootURI, "topic_dropped_years", dropped)
	if err := recordReportGuard(ctx, db, weeklyReportGuardKey, weekKey); err != nil {
		// The guard is the only duplicate suppression; a restart with
		// REPORTS_RUN_AT_STARTUP would re-post.
		slog.Error("persist weekly report guard failed", "error", err, "week_start", weekKey)
	}
	if len(dropped) > 0 && events != nil {
		// Only for a post that went out, and detached from ctx so a shutdown
		// right after it still leaves the event for the alert path.
		_ = events.LogEvent(context.WithoutCancel(ctx), weeklyTopicDroppedEvent, weeklyTopicDroppedDetails(weekKey, dropped, r.TopicLabel))
	}

	if r.TopPost == nil {
		return
	}
	postWeeklyTopPostReply(apiCtx, bskyClient, newFeatureGate(bskyClient), r, rootURI, rootCID)
}

// weeklyReplyPoster is the slice of *client.BlueskyClient the post-of-the-week
// reply needs, so the decision can be driven by a recording fake in tests.
type weeklyReplyPoster interface {
	PostWithFacetsAsReply(ctx context.Context, text string, facets []*bsky.RichtextFacet, rootURI, rootCID, parentURI, parentCID string) (string, string, error)
	PostReplyWithQuote(ctx context.Context, text string, rootURI, rootCID, parentURI, parentCID, quoteURI, quoteCID string) (string, string, error)
}

// postWeeklyTopPostReply quotes the week's top post under the root, falling
// back to a plain text reply when the author has only disabled quoting, and
// skipping the reply entirely when the post must not be featured at all. The
// week between the post and this reply is long enough for a block, a label, a
// deletion or a visibility declaration to land, so the gate runs again here.
//
// The reply's text is built only after the gate has answered: the verdict's
// getPosts view carries the post's current handle and engagement, which is
// what the reply prints, falling back per field to the daily_top_post row.
// The gate runs after the root is posted so its latency never delays the
// root. r.TopPost must be non-nil.
func postWeeklyTopPostReply(ctx context.Context, poster weeklyReplyPoster, gate featureGate, r weeklyReport, rootURI, rootCID string) {
	verdicts, gateErr := checkFeatureGate(ctx, gate, "weekly", []string{r.TopPost.URI})
	verdict := verdicts[r.TopPost.URI]

	switch {
	case gateErr != nil:
		// The root is already out and its guard key already set, so the week
		// is reported either way; only the reply is lost.
		slog.Info("weekly report: post of the week reply skipped, feature gate unavailable", "uri", r.TopPost.URI, "error", gateErr)
		return
	case !verdict.OK:
		slog.Info("weekly report: post of the week reply skipped, it cannot be featured", "uri", r.TopPost.URI, "reason", verdict.Reason)
		return
	}

	figures := resolvePostOfWeekFigures(*r.TopPost, verdict)
	replyText := buildPostOfWeekText(r.Start, r.End, figures)
	if n := postLength(replyText); n > blueskyPostLimit {
		slog.Warn("post of the week text over limit, omitting reply", "length", n, "counts_source", figures.Source)
		return
	}

	var err error
	if verdict.Quotable {
		_, _, err = poster.PostReplyWithQuote(ctx, replyText, rootURI, rootCID, rootURI, rootCID, r.TopPost.URI, r.TopPost.CID)
	} else {
		slog.Info("weekly report: top post is quote-controlled, replying without embed", "uri", r.TopPost.URI)
		_, _, err = poster.PostWithFacetsAsReply(ctx, replyText, nil, rootURI, rootCID, rootURI, rootCID)
	}
	if err != nil {
		slog.Warn("post of the week reply failed", "error", err)
		return
	}
	slog.Info("post of the week reply posted", "top_post", r.TopPost.URI, "quotable", verdict.Quotable,
		"counts_source", figures.Source, "stored_likes", r.TopPost.Likes, "live_likes", verdict.Likes, "live_counts", verdict.HasCounts)
}
