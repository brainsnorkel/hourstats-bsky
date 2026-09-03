package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
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

// summaryPoster is the slice of the Bluesky client that postSummary needs,
// narrowed to one method so the shutdown guard below can be tested.
type summaryPoster interface {
	PostTrendingSummary(posts []client.Post, overallSentiment string, analysisIntervalMinutes int, totalPosts int, netSentimentPercentage float64) (string, string, error)
}

// postSummary publishes the cycle summary. quoteControlled marks the #1 post
// as one whose author disabled quoting, which suppresses the quote embed and
// annotates its line instead of rendering as "Removed by author".
func postSummary(ctx context.Context, bskyClient summaryPoster, topPosts []analyzer.AnalyzedPost, overallSentiment string, netPct float64, analysisMinutes, totalPosts int, quoteControlled bool) (string, string) {
	// PostTrendingSummary takes no context and builds its own
	// context.Background(), so a ctx already cancelled by SIGTERM would still
	// publish while the surrounding DB writes fail with "context canceled" —
	// an orphan post plus a hole in sentiment_history. Refuse to publish.
	if err := ctx.Err(); err != nil {
		slog.Warn("shutdown in progress, skipping summary post", "error", err)
		return "", ""
	}

	clientPosts := make([]client.Post, len(topPosts))
	for i, ap := range topPosts {
		clientPosts[i] = client.Post{
			URI: ap.URI, CID: ap.CID, Text: ap.Text, Author: ap.Author,
			Likes: ap.Likes, Reposts: ap.Reposts, Replies: ap.Replies,
			CreatedAt: ap.CreatedAt, Sentiment: ap.Sentiment, EngagementScore: ap.EngagementScore,
		}
	}
	if quoteControlled && len(clientPosts) > 0 {
		clientPosts[0].QuoteControlled = true
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
