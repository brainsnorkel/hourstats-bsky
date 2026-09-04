package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// Weekly week-in-review thread (Monday, after the daily cycle)
// ---------------------------------------------------------------------------

const (
	// weeklyReportGuardKey holds the Monday date of the last week reported.
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

	// The topic label is the only free text; cap it, and drop the line
	// altogether rather than let the post exceed the limit.
	if label := truncateRunes(strings.Join(strings.Fields(r.TopicLabel), " "), maxTopicLabelRunes); label != "" {
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

// buildPostOfWeekText renders the reply that quotes the week's top post.
func buildPostOfWeekText(r weeklyReport) string {
	p := r.TopPost
	return fmt.Sprintf("Post of the week · %s\n\nMost engaged post by @%s: %s likes, %s reposts, %s replies",
		rangeLabel(r.Start, r.End), p.AuthorHandle,
		compactCount(p.Likes), compactCount(p.Reposts), compactCount(p.Replies))
}

// loadWeeklyReport gathers the previous week's data. ok is false, with the
// reason logged, when there is not enough to report on.
func loadWeeklyReport(ctx context.Context, db *store.Store, now time.Time, cycleMinutes int) (weeklyReport, bool) {
	start, end := previousWeek(now)
	r := weeklyReport{Start: start, End: end}

	days, err := db.GetDailySentimentRange(ctx, start.Format(dateFormat), end.Format(dateFormat))
	if err != nil {
		slog.Error("weekly report: get daily range failed", "error", err)
		return r, false
	}
	if len(days) < weeklyReportMinDays {
		slog.Info("weekly report: not enough daily rows, skipping",
			"week_start", start.Format(dateFormat), "days", len(days), "min", weeklyReportMinDays)
		return r, false
	}
	r.Days = days

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

// runWeeklyReport posts the week-in-review root and, when a top post is
// known, the post-of-the-week quote reply. The guard key is set once the
// root is posted, so a failed reply is not retried by a later re-run.
func runWeeklyReport(ctx context.Context, db *store.Store, handle, password string, dryRun bool, now time.Time, cycleMinutes int) {
	start, _ := previousWeek(now)
	weekKey := start.Format(dateFormat)
	if last, _ := db.GetKeyValue(ctx, weeklyReportGuardKey); last == weekKey {
		slog.Info("weekly report already posted", "week_start", weekKey)
		return
	}

	r, ok := loadWeeklyReport(ctx, db, now, cycleMinutes)
	if !ok {
		return
	}
	text := buildWeeklyReportText(r)
	if n := postLength(text); n > blueskyPostLimit {
		slog.Error("weekly report text over limit, skipping", "length", n)
		return
	}
	replyText := ""
	if r.TopPost != nil {
		replyText = buildPostOfWeekText(r)
		if n := postLength(replyText); n > blueskyPostLimit {
			slog.Warn("post of the week text over limit, omitting reply", "length", n)
			replyText = ""
		}
	}

	if dryRun {
		slog.Info("DRY_RUN: would post weekly report",
			"week_start", weekKey, "days", len(r.Days), "prev_days", len(r.PrevDays),
			"topic", r.TopicLabel, "topic_hours", r.TopicHours,
			"has_top_post", r.TopPost != nil,
			"text", text, "text_length", postLength(text),
			"reply", replyText, "reply_length", postLength(replyText))
		return
	}

	apiCtx, apiCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer apiCancel()

	bskyClient := client.New(handle, password)
	if err := bskyClient.Authenticate(); err != nil {
		slog.Error("bluesky auth for weekly report failed", "error", err)
		return
	}

	rootURI, rootCID, err := bskyClient.PostWithFacetsRef(apiCtx, text, nil)
	if err != nil {
		slog.Error("post weekly report failed", "error", err)
		return
	}
	slog.Info("weekly report posted", "week_start", weekKey, "uri", rootURI)
	if err := db.SetKeyValue(ctx, weeklyReportGuardKey, weekKey); err != nil {
		// The guard is the only duplicate suppression; a restart with
		// REPORTS_RUN_AT_STARTUP would re-post.
		slog.Error("persist weekly report guard failed", "error", err, "week_start", weekKey)
	}

	if replyText == "" {
		return
	}
	postWeeklyTopPostReply(apiCtx, bskyClient, r, replyText, rootURI, rootCID)
}

// postWeeklyTopPostReply quotes the week's top post under the root, falling
// back to a plain text reply when the author has disabled quoting. The check
// fails open, matching the hourly summary.
func postWeeklyTopPostReply(ctx context.Context, bskyClient *client.BlueskyClient, r weeklyReport, replyText, rootURI, rootCID string) {
	quoteControlled := false
	if disabled, err := bskyClient.EmbeddingDisabled(ctx, []string{r.TopPost.URI}); err != nil {
		slog.Warn("weekly report: quote-control check failed, embedding as usual", "error", err, "uri", r.TopPost.URI)
	} else if disabled[r.TopPost.URI] {
		quoteControlled = true
	}

	var err error
	if quoteControlled {
		slog.Info("weekly report: top post is quote-controlled, replying without embed", "uri", r.TopPost.URI)
		_, _, err = bskyClient.PostWithFacetsAsReply(ctx, replyText, nil, rootURI, rootCID, rootURI, rootCID)
	} else {
		_, _, err = bskyClient.PostReplyWithQuote(ctx, replyText, rootURI, rootCID, rootURI, rootCID, r.TopPost.URI, r.TopPost.CID)
	}
	if err != nil {
		slog.Warn("post of the week reply failed", "error", err)
		return
	}
	slog.Info("post of the week reply posted", "top_post", r.TopPost.URI, "quote_controlled", quoteControlled)
}
