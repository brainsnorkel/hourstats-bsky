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

// ---------------------------------------------------------------------------
// Posting helpers
// ---------------------------------------------------------------------------

func postSummary(ctx context.Context, bskyClient *client.BlueskyClient, top5 []analyzer.AnalyzedPost, overallSentiment string, netPct float64, analysisMinutes, totalPosts int) (string, string) {
	clientPosts := make([]client.Post, len(top5))
	for i, ap := range top5 {
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
	if len(history) < 2 {
		slog.Info("insufficient data for sparkline", "points", len(history))
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
			sparkURI, sparkCID, _ = bskyClient.PostWithImage(ctx, postText, imgData, altText)
		}
	} else {
		sparkURI, sparkCID, _ = bskyClient.PostWithImage(ctx, postText, imgData, altText)
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
