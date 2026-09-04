package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// Daily aggregation
// ---------------------------------------------------------------------------

func runDailyAggregation(ctx context.Context, db *store.Store) {
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	existing, err := db.GetDailySentimentForDate(ctx, yesterday)
	if err == nil && existing != nil {
		slog.Info("daily sentiment already exists", "date", yesterday)
		return
	}

	history, err := db.GetSentimentHistory(ctx, 48*time.Hour)
	if err != nil {
		slog.Error("get sentiment history for daily agg failed", "error", err)
		return
	}

	history = filterHighConfidence(history)

	var dayPoints []store.SentimentDataPoint
	for _, h := range history {
		if h.Timestamp.Format("2006-01-02") == yesterday {
			dayPoints = append(dayPoints, h)
		}
	}

	if len(dayPoints) == 0 {
		slog.Warn("no sentiment data for yesterday", "date", yesterday)
		return
	}

	var sentiments []float64
	var totalPosts, totalFirehose int
	for _, dp := range dayPoints {
		sentiments = append(sentiments, dp.NetSentimentPercent)
		totalPosts += dp.TotalPosts
		totalFirehose += dp.TotalFirehosePosts
	}
	sort.Float64s(sentiments)

	avg := mean(sentiments)
	daily := store.DailySentimentDataPoint{
		Date:               yesterday,
		RunID:              fmt.Sprintf("daily-%s", yesterday),
		AverageSentiment:   avg,
		MinSentiment:       sentiments[0],
		MaxSentiment:       sentiments[len(sentiments)-1],
		Q1Sentiment:        percentile(sentiments, 0.25),
		MedianSentiment:    percentile(sentiments, 0.50),
		Q3Sentiment:        percentile(sentiments, 0.75),
		TotalRuns:          len(dayPoints),
		TotalPosts:         totalPosts,
		TotalFirehosePosts: totalFirehose,
		CreatedAt:          time.Now().UTC(),
		TTL:                time.Now().Add(365 * 24 * time.Hour).Unix(),
	}

	if err := db.StoreDailySentiment(ctx, daily); err != nil {
		slog.Error("store daily sentiment failed", "error", err)
		return
	}
	slog.Info("daily aggregation complete", "date", yesterday, "runs", daily.TotalRuns, "avg", fmt.Sprintf("%.1f%%", avg))
}

// ---------------------------------------------------------------------------
// Report rollups (weekly and monthly report inputs)
// ---------------------------------------------------------------------------

// reportRollupRetention keeps topic_daily and daily_top_post long enough for
// an annual report.
const reportRollupRetention = 400 * 24 * time.Hour

// runReportRollups condenses the last three finished days of topic_snapshots
// (purged after 48h) and runs into their long-lived rollups, backfills
// firehose totals on daily rows that still lack them, then purges rollups
// past retention. Every step is idempotent and merge-safe, so covering three
// days on every run means a missed midnight loses nothing and a 48h outage
// loses only the hours the purge already removed.
func runReportRollups(ctx context.Context, db *store.Store, now time.Time) {
	backfillDailyFirehoseTotals(ctx, db, now)

	for _, daysAgo := range []int{1, 2, 3} {
		date := now.UTC().AddDate(0, 0, -daysAgo).Format("2006-01-02")

		topics, err := db.RollupTopicDaily(ctx, date)
		if err != nil {
			slog.Error("topic daily rollup failed", "date", date, "error", err)
		} else {
			slog.Info("topic daily rollup complete", "date", date, "topics", topics)
		}

		top, err := db.GetTopPostForDate(ctx, date)
		if err != nil {
			slog.Warn("top post lookup for rollup failed", "date", date, "error", err)
		} else if top == nil {
			slog.Info("no top post to roll up", "date", date)
		} else if err := db.StoreDailyTopPost(ctx, date, *top); err != nil {
			slog.Error("store daily top post failed", "date", date, "error", err)
		} else {
			slog.Info("daily top post rolled up", "date", date, "uri", top.URI, "engagement", top.EngagementScore)
		}
	}

	backfillDailyTopPosts(ctx, db, now)

	purged, err := db.PurgeReportRollups(ctx, reportRollupRetention)
	if err != nil {
		slog.Warn("purge report rollups failed", "error", err)
	} else if purged > 0 {
		slog.Info("purged report rollups", "rows", purged)
	}
}

// backfillDailyFirehoseTotals fills total_firehose_posts on daily_sentiment
// rows written before the column existed. sentiment_history is never purged
// in practice, so this reaches the whole rollup retention window. A day is
// only written when its high-confidence cycles match the daily row exactly
// (same cycle count and English total), which proves the history still holds
// the complete day rather than a truncated edge of it.
func backfillDailyFirehoseTotals(ctx context.Context, db firehoseBackfillStore, now time.Time) {
	since := now.UTC().Add(-reportRollupRetention).Format(dateFormat)
	missing, err := db.GetDailySentimentMissingFirehose(ctx, since)
	if err != nil {
		slog.Warn("firehose backfill: list daily rows failed", "error", err)
		return
	}
	var filled, incomplete, untracked int
	for _, d := range missing {
		t, err := db.GetDayCycleTotals(ctx, d.Date, minPostsRequired)
		if err != nil {
			slog.Warn("firehose backfill: day totals failed", "date", d.Date, "error", err)
			continue
		}
		switch {
		case t.Cycles != d.TotalRuns || t.EnglishPosts != d.TotalPosts:
			incomplete++
			continue
		case t.FirehosePosts < t.EnglishPosts:
			// Cycles before firehose counting existed report zero.
			untracked++
			continue
		}
		if updated, err := db.UpdateDailyFirehoseTotal(ctx, d.Date, t.FirehosePosts); err != nil {
			slog.Warn("firehose backfill: update failed", "date", d.Date, "error", err)
		} else if updated {
			filled++
		}
	}
	if len(missing) > 0 {
		slog.Info("daily firehose backfill", "candidates", len(missing), "filled", filled,
			"incomplete_history", incomplete, "untracked", untracked)
	}
}

// backfillDailyTopPosts fills daily_top_post for every daily row in the
// retention window that lacks one and that runs still covers. runs is never
// purged in practice, so on first deploy this recovers the post of the day
// for the whole history rather than only the last three days.
func backfillDailyTopPosts(ctx context.Context, db topPostBackfillStore, now time.Time) {
	earliest, err := db.GetEarliestRunDate(ctx)
	if err != nil {
		slog.Warn("top post backfill: earliest run lookup failed", "error", err)
		return
	}
	if earliest == "" {
		return
	}
	since := now.UTC().Add(-reportRollupRetention).Format(dateFormat)
	if earliest > since {
		since = earliest
	}
	dates, err := db.GetDatesMissingTopPost(ctx, since)
	if err != nil {
		slog.Warn("top post backfill: list dates failed", "error", err)
		return
	}
	var filled, empty int
	for _, date := range dates {
		top, err := db.GetTopPostForDate(ctx, date)
		if err != nil {
			slog.Warn("top post backfill: lookup failed", "date", date, "error", err)
			continue
		}
		if top == nil {
			empty++
			continue
		}
		if err := db.StoreDailyTopPost(ctx, date, *top); err != nil {
			slog.Warn("top post backfill: store failed", "date", date, "error", err)
			continue
		}
		filled++
	}
	if len(dates) > 0 {
		slog.Info("daily top post backfill", "candidates", len(dates), "filled", filled, "no_runs", empty)
	}
}

// topPostBackfillStore is the slice of *store.Store the top post backfill needs.
type topPostBackfillStore interface {
	GetEarliestRunDate(ctx context.Context) (string, error)
	GetDatesMissingTopPost(ctx context.Context, startDate string) ([]string, error)
	GetTopPostForDate(ctx context.Context, date string) (*store.Post, error)
	StoreDailyTopPost(ctx context.Context, date string, p store.Post) error
}

// firehoseBackfillStore is the slice of *store.Store the backfill needs.
type firehoseBackfillStore interface {
	GetDailySentimentMissingFirehose(ctx context.Context, startDate string) ([]store.DailySentimentDataPoint, error)
	GetDayCycleTotals(ctx context.Context, date string, minPosts int) (store.DayCycleTotals, error)
	UpdateDailyFirehoseTotal(ctx context.Context, date string, total int) (bool, error)
}

// ---------------------------------------------------------------------------
// Daily top-post quote reply
// ---------------------------------------------------------------------------

func runDailyTopPostQuote(ctx context.Context, db *store.Store, handle, password string, dryRun bool) {
	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	lastDate, _ := db.GetKeyValue(ctx, "daily_quote_last_date")
	if lastDate == today {
		slog.Info("daily quote already posted today", "date", today)
		return
	}

	yearlyURI, _ := db.GetKeyValue(ctx, "yearly_post_uri")
	yearlyCID, _ := db.GetKeyValue(ctx, "yearly_post_cid")
	if yearlyURI == "" || yearlyCID == "" {
		slog.Info("no yearly post URI/CID stored, skipping daily quote")
		return
	}

	topPost, err := db.GetTopPostForDate(ctx, yesterday)
	if err != nil {
		slog.Warn("get top post for date failed", "date", yesterday, "error", err)
		return
	}
	if topPost == nil {
		slog.Info("no top post found for yesterday", "date", yesterday)
		return
	}

	yesterdayTime, _ := time.Parse("2006-01-02", yesterday)
	text := fmt.Sprintf("Most engaged post %s by @%s",
		yesterdayTime.Format("Mon Jan 2"),
		topPost.AuthorHandle,
	)

	if dryRun {
		slog.Info("DRY_RUN: would post daily quote reply",
			"date", yesterday,
			"top_post_uri", topPost.URI,
			"author", topPost.AuthorHandle,
			"engagement", topPost.EngagementScore,
		)
		return
	}

	apiCtx, apiCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer apiCancel()

	bskyClient := client.New(handle, password)
	if err := bskyClient.Authenticate(); err != nil {
		slog.Error("bluesky auth for daily quote failed", "error", err)
		return
	}

	_, _, err = bskyClient.PostReplyWithQuote(apiCtx, text,
		yearlyURI, yearlyCID, yearlyURI, yearlyCID,
		topPost.URI, topPost.CID,
	)
	if err != nil {
		slog.Warn("daily quote reply failed", "error", err)
		return
	}

	if err := db.SetKeyValue(ctx, "daily_quote_last_date", today); err != nil {
		slog.Warn("persist daily quote date failed", "error", err)
	}
	slog.Info("daily quote reply posted", "date", yesterday, "top_post", topPost.URI)
}

// ---------------------------------------------------------------------------
// Backup
// ---------------------------------------------------------------------------

func runBackup(db *store.Store, dataDir, profile string, retainDays int, s3Cfg *store.S3BackupConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	path, err := db.Backup(ctx, dataDir, profile, retainDays)
	if err != nil {
		slog.Error("local backup failed", "error", err)
	} else {
		slog.Info("local backup complete", "path", path)
	}

	if s3Cfg != nil {
		s3Path, err := db.BackupToS3(ctx, *s3Cfg)
		if err != nil {
			slog.Error("s3 backup failed", "error", err)
		} else {
			slog.Info("s3 backup complete", "path", s3Path)
		}
	}
}
