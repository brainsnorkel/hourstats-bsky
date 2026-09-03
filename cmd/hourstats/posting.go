package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// filterHighConfidence drops sentiment data points whose sample size is below
// minPostsRequired. Low-denominator points produce extreme percentages that
// distort the chart; they are retained in sentiment_history for audit but
// excluded from visualisations and aggregations.
func filterHighConfidence(points []store.SentimentDataPoint) []store.SentimentDataPoint {
	filtered := make([]store.SentimentDataPoint, 0, len(points))
	var dropped int
	for _, p := range points {
		if p.TotalPosts < minPostsRequired {
			dropped++
			continue
		}
		filtered = append(filtered, p)
	}
	if dropped > 0 {
		slog.Info("filtered low-confidence sentiment points",
			"dropped", dropped,
			"remaining", len(filtered),
			"min_posts", minPostsRequired,
		)
	}
	return filtered
}

// ---------------------------------------------------------------------------
// Posting helpers
// ---------------------------------------------------------------------------

func postSummary(ctx context.Context, bskyClient *client.BlueskyClient, topPosts []analyzer.AnalyzedPost, overallSentiment string, netPct float64, analysisMinutes, totalPosts int) (string, string) {
	clientPosts := make([]client.Post, len(topPosts))
	for i, ap := range topPosts {
		clientPosts[i] = client.Post{
			URI: ap.URI, CID: ap.CID, Text: ap.Text, Author: ap.Author,
			Likes: ap.Likes, Reposts: ap.Reposts, Replies: ap.Replies,
			CreatedAt: ap.CreatedAt, Sentiment: ap.Sentiment, EngagementScore: ap.EngagementScore,
		}
	}

	uri, cid, err := bskyClient.PostTrendingSummary(clientPosts, overallSentiment, analysisMinutes, totalPosts, netPct)
	if err != nil {
		slog.Error("post summary failed", "error", err)
		return "", ""
	}
	slog.Info("summary posted", "uri", uri)
	return uri, cid
}

// ---------------------------------------------------------------------------
// Sparkline
// ---------------------------------------------------------------------------

func postSparkline(ctx context.Context, db *store.Store, bskyClient *client.BlueskyClient, rootURI, rootCID, parentURI, parentCID string, dryRun bool) (string, string) {
	history, err := db.GetSentimentHistory(ctx, 7*24*time.Hour)
	if err != nil {
		slog.Error("get sentiment history failed", "error", err)
		return "", ""
	}

	history = filterHighConfidence(history)
	if len(history) < 2 {
		slog.Info("insufficient data for sparkline after low-confidence filter", "points", len(history))
		return "", ""
	}

	statePoints := toStateSentimentPoints(history)

	gen := sparkline.NewSparklineGenerator(nil)
	imgData, err := gen.GenerateSentimentSparkline(statePoints)
	if err != nil {
		slog.Error("generate sparkline failed", "error", err)
		return "", ""
	}

	postText := "Seven day Bluesky sentiment"
	altText := generateSparklineAltText(statePoints)

	if dryRun {
		slog.Info("DRY_RUN: would post sparkline", "points", len(history), "image_bytes", len(imgData))
		return "", ""
	}

	var sparkURI, sparkCID string
	if parentURI != "" && parentCID != "" {
		sparkURI, sparkCID, err = bskyClient.PostWithImageAsReply(ctx, postText, imgData, altText, rootURI, rootCID, parentURI, parentCID)
		if err != nil {
			slog.Warn("sparkline reply failed, posting standalone", "error", err)
			var fallbackErr error
			sparkURI, sparkCID, fallbackErr = bskyClient.PostWithImage(ctx, postText, imgData, altText)
			if fallbackErr != nil {
				slog.Warn("sparkline fallback post failed", "error", fallbackErr)
			}
		}
	} else {
		var postErr error
		sparkURI, sparkCID, postErr = bskyClient.PostWithImage(ctx, postText, imgData, altText)
		if postErr != nil {
			slog.Warn("sparkline fallback post failed", "error", postErr)
		}
	}
	if sparkURI == "" || sparkCID == "" {
		slog.Warn("sparkline URI/CID empty; trending attachment will be skipped")
	}
	slog.Info("sparkline posted", "points", len(history))
	return sparkURI, sparkCID
}

func generateSparklineAltText(points []state.SentimentDataPoint) string {
	if len(points) < 2 {
		return "Seven day sentiment trend chart"
	}
	latest := points[len(points)-1]
	var sum, lo, hi float64
	lo = points[0].NetSentimentPercent
	hi = points[0].NetSentimentPercent
	for _, p := range points {
		sum += p.NetSentimentPercent
		if p.NetSentimentPercent < lo {
			lo = p.NetSentimentPercent
		}
		if p.NetSentimentPercent > hi {
			hi = p.NetSentimentPercent
		}
	}
	avg := sum / float64(len(points))
	return fmt.Sprintf("Seven day Bluesky sentiment. Latest: %.1f%%. High: %.1f%%. Low: %.1f%%. Average: %.1f%%.",
		latest.NetSentimentPercent, hi, lo, avg)
}
