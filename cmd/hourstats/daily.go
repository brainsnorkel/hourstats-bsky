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
	var totalPosts int
	for _, dp := range dayPoints {
		sentiments = append(sentiments, dp.NetSentimentPercent)
		totalPosts += dp.TotalPosts
	}
	sort.Float64s(sentiments)

	avg := mean(sentiments)
	daily := store.DailySentimentDataPoint{
		Date:             yesterday,
		RunID:            fmt.Sprintf("daily-%s", yesterday),
		AverageSentiment: avg,
		MinSentiment:     sentiments[0],
		MaxSentiment:     sentiments[len(sentiments)-1],
		Q1Sentiment:      percentile(sentiments, 0.25),
		MedianSentiment:  percentile(sentiments, 0.50),
		Q3Sentiment:      percentile(sentiments, 0.75),
		TotalRuns:        len(dayPoints),
		TotalPosts:       totalPosts,
		CreatedAt:        time.Now().UTC(),
		TTL:              time.Now().Add(365 * 24 * time.Hour).Unix(),
	}

	if err := db.StoreDailySentiment(ctx, daily); err != nil {
		slog.Error("store daily sentiment failed", "error", err)
		return
	}
	slog.Info("daily aggregation complete", "date", yesterday, "runs", daily.TotalRuns, "avg", fmt.Sprintf("%.1f%%", avg))
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
