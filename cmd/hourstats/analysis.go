package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/hydrator"
	"github.com/christophergentle/hourstats-bsky/internal/stats"
	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

// minPostsRequired is the minimum post count per analysis window. Below this,
// the run is marked low-confidence: sentiment is still recorded, but posting to
// Bluesky and charting are skipped to avoid misleading output from tiny samples.
const minPostsRequired = 500

var (
	sentimentAnalyzerOnce sync.Once
	sentimentAnalyzer     *analyzer.SentimentAnalyzer
)

// sharedAnalyzer returns the process-wide VADER analyzer.
//
// analyzer.New() parses the full govader lexicon and emoji dictionary on every
// call, which was pure per-cycle overhead. The analyzer is read-only after
// construction — govader writes Lexicon/EmojiDict/Constants only in
// NewSentimentIntensityAnalyzer, and PolarityScores only reads them — so one
// instance can serve every cycle. Analysis cycles are sequential, so concurrent
// use is not required today; sync.Once keeps initialisation safe regardless.
func sharedAnalyzer() *analyzer.SentimentAnalyzer {
	sentimentAnalyzerOnce.Do(func() {
		sentimentAnalyzer = analyzer.New()
	})
	return sentimentAnalyzer
}

// ---------------------------------------------------------------------------
// 30-minute analysis cycle
// ---------------------------------------------------------------------------

func runAnalysisCycle(ctx context.Context, db *store.Store, handle, password string, dryRun bool, analysisMinutes int, collector *stats.Collector, topicAnalyzer *topics.Analyzer) {
	cycleStart := time.Now()
	runID := fmt.Sprintf("run-%s", time.Now().UTC().Format("20060102-150405"))
	slog.Info("analysis cycle starting", "run_id", runID)

	// Sample memory in-process: the stats snapshot ticker cannot fire while the
	// cycle runs, so the peak is otherwise invisible.
	stopMemSampler := startMemSampler(ctx, 500*time.Millisecond, runID)
	defer func() {
		peak := stopMemSampler()
		// LogEvent already warns on failure. Detach from ctx so the write still
		// lands when the cycle is unwinding because ctx was cancelled.
		_ = collector.LogEvent(context.WithoutCancel(ctx), "cycle_memory_peak", peak.eventDetails(runID))
	}()

	cutoff := time.Now().UTC().Add(-time.Duration(analysisMinutes) * time.Minute)

	posts, err := db.GetPostsSince(ctx, cutoff)
	if err != nil {
		slog.Error("get posts failed", "error", err)
		return
	}
	slog.Info("posts in window", "count", len(posts), "cutoff", cutoff.Format(time.RFC3339))

	if len(posts) == 0 {
		slog.Warn("no posts in analysis window, skipping")
		return
	}

	bskyClient := client.New(handle, password)
	if err := bskyClient.Authenticate(); err != nil {
		slog.Error("bluesky auth failed", "error", err)
		return
	}

	// RunAnalysisCycle reads topic_tokens (no dependency on hydration).
	// Starting here overlaps Gemini latency with the hydration pipeline.
	var topicAnalysisDone <-chan error
	if topicAnalyzer != nil {
		ch := make(chan error, 1)
		topicAnalysisDone = ch
		slog.Info("topics: analysis goroutine started (parallel with hydration)")
		go func() {
			trendStart := time.Now()
			err := topicAnalyzer.RunAnalysisCycle(ctx)
			collector.RecordTrendingDuration(time.Since(trendStart).Milliseconds())
			ch <- err
		}()
	}

	fetcher := hydrator.NewBlueskyFetcher(bskyClient.APIClient())
	h := hydrator.New(fetcher, db, hydrator.Config{})
	result, err := h.Hydrate(ctx, posts)
	if err != nil && ctx.Err() != nil {
		return
	}
	slog.Info("hydration complete",
		"total", result.Total,
		"hydrated", result.Hydrated,
		"filtered", result.Filtered,
		"errors", result.Errors,
	)

	// Exclude posts that failed hydration (no author handle means the
	// hydrator could not resolve them — deleted, private, or API error).
	// Including them would skew sentiment with un-engageable ghost posts.
	hydrated := make([]store.Post, 0, len(posts))
	for _, p := range posts {
		if p.AuthorHandle != "" {
			hydrated = append(hydrated, p)
		}
	}
	if dropped := len(posts) - len(hydrated); dropped > 0 {
		slog.Info("excluded unhydrated posts from analysis", "dropped", dropped, "remaining", len(hydrated))
	}
	posts = hydrated

	// Guard: skip posting when too few posts are available (e.g. Jetstream
	// connection instability).  We still run sentiment + store the data point
	// so there are no gaps in the historical record, but we don't publish a
	// misleading summary or sparkline.
	lowConfidence := len(posts) < minPostsRequired

	if lowConfidence {
		slog.Warn("low post count — sentiment will be recorded but posting skipped",
			"posts", len(posts),
			"min_required", minPostsRequired,
		)
	}

	analyzerPosts := toAnalyzerPosts(posts)
	analyzed, err := sharedAnalyzer().AnalyzePosts(analyzerPosts)
	if err != nil {
		slog.Error("sentiment analysis failed", "error", err)
		return
	}

	overallSentiment, netSentimentPct := calculateOverallSentiment(analyzed)
	rootSentimentPct, replySentimentPct := calculateSplitSentiment(analyzed)

	collector.RecordAnalysis(len(posts), result.Hydrated, result.Errors, overallSentiment, lowConfidence)

	sort.Slice(analyzed, func(i, j int) bool {
		return analyzed[i].EngagementScore > analyzed[j].EngagementScore
	})
	// Select top 5 posts, deduplicating by author so the same handle
	// doesn't appear multiple times (which breaks facet linking).
	var top5 []analyzer.AnalyzedPost
	seenAuthors := make(map[string]bool)
	for _, ap := range analyzed {
		if seenAuthors[ap.Author] {
			continue
		}
		seenAuthors[ap.Author] = true
		top5 = append(top5, ap)
		if len(top5) >= 5 {
			break
		}
	}

	topStorePosts := make([]store.Post, len(top5))
	for i, ap := range top5 {
		topStorePosts[i] = store.Post{
			URI: ap.URI, CID: ap.CID, Text: ap.Text,
			AuthorHandle: ap.Author,
			Likes:        ap.Likes, Reposts: ap.Reposts, Replies: ap.Replies,
			Sentiment: ap.Sentiment, EngagementScore: ap.EngagementScore,
			CreatedAt: ap.CreatedAt,
		}
	}

	status := "complete"
	if lowConfidence {
		status = "low_confidence"
	}
	runState := store.RunState{
		RunID:                   runID,
		Status:                  status,
		AnalysisIntervalMinutes: analysisMinutes,
		CutoffTime:              cutoff,
		TotalPostsRetrieved:     len(posts),
		OverallSentiment:        overallSentiment,
		NetSentimentPercentage:  netSentimentPct,
		TopPosts:                topStorePosts,
		CreatedAt:               time.Now().UTC(),
		UpdatedAt:               time.Now().UTC(),
		TTL:                     time.Now().Add(7 * 24 * time.Hour).Unix(),
	}
	if err := db.CreateRun(ctx, runState); err != nil {
		slog.Error("create run failed", "error", err)
	}

	firehoseSnapshot := int(collector.SwapFirehoseCount())

	avgCompound := netSentimentPct / 100.0
	sdp := store.SentimentDataPoint{
		RunID:                runID,
		Timestamp:            time.Now().UTC(),
		AverageCompoundScore: avgCompound,
		NetSentimentPercent:  netSentimentPct,
		SentimentCategory:    overallSentiment,
		TotalPosts:           len(posts),
		TotalFirehosePosts:   firehoseSnapshot,
		RootSentimentPct:     rootSentimentPct,
		ReplySentimentPct:    replySentimentPct,
		CreatedAt:            time.Now().UTC(),
		TTL:                  time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	if err := db.StoreSentimentDataPoint(ctx, sdp); err != nil {
		slog.Error("store sentiment data point failed", "error", err)
	}

	if lowConfidence {
		slog.Warn("skipping post due to low confidence",
			"posts", len(posts),
			"min_required", minPostsRequired,
			"sentiment", overallSentiment,
			"net_pct", fmt.Sprintf("%.1f%%", netSentimentPct),
		)
		if topicAnalysisDone != nil {
			if err := <-topicAnalysisDone; err != nil {
				slog.Warn("topic analysis failed (low confidence cycle)", "error", err)
			}
		}
	} else if dryRun {
		slog.Info("DRY_RUN: would post summary",
			"sentiment", overallSentiment,
			"net_pct", netSentimentPct,
			"top_count", len(top5),
			"total_posts", len(posts),
		)
		if topicAnalysisDone != nil {
			if err := <-topicAnalysisDone; err != nil {
				slog.Warn("topic analysis failed (dry run cycle)", "error", err)
			}
		}
	} else {
		postedURI, postedCID := postSummary(ctx, bskyClient, top5, overallSentiment, netSentimentPct, analysisMinutes, len(posts))
		if postedURI != "" {
			runState.TopPostURI = postedURI
			runState.TopPostCID = postedCID
			if err := db.UpdateRun(ctx, runState); err != nil {
				slog.Warn("failed to persist run TopPostURI", "error", err, "run_id", runState.RunID)
			}
		}

		rootURI, rootCID := postedURI, postedCID
		sparkURI, sparkCID := postSparkline(ctx, db, bskyClient, rootURI, rootCID, postedURI, postedCID, dryRun)
		slog.Info("timing: sparkline complete", "cycle_elapsed", fmt.Sprintf("%.1fs", time.Since(cycleStart).Seconds()))

		if topicAnalyzer != nil {
			topicWait := time.Now()
			topicErr := <-topicAnalysisDone
			slog.Info("timing: topic analysis goroutine collected",
				"waited", fmt.Sprintf("%.1fs", time.Since(topicWait).Seconds()),
				"cycle_elapsed", fmt.Sprintf("%.1fs", time.Since(cycleStart).Seconds()))
			if topicErr != nil {
				slog.Error("topic analysis cycle failed", "error", topicErr)
			} else if sparkURI != "" && sparkCID != "" {
				if err := topicAnalyzer.RunTrendingPost(ctx, bskyClient, dryRun, rootURI, rootCID, sparkURI, sparkCID); err != nil {
					slog.Error("trending post failed", "error", err)
				} else {
					slog.Info("timing: trending post complete", "cycle_elapsed", fmt.Sprintf("%.1fs", time.Since(cycleStart).Seconds()))
				}
			} else {
				if err := topicAnalyzer.RunTrendingPost(ctx, bskyClient, dryRun, "", "", "", ""); err != nil {
					slog.Error("trending post failed", "error", err)
				} else {
					slog.Info("timing: trending post complete", "cycle_elapsed", fmt.Sprintf("%.1fs", time.Since(cycleStart).Seconds()))
				}
			}
		}
	}

	purged, _ := db.PurgeExpiredPosts(ctx, 3*time.Hour)
	if purged > 0 {
		slog.Info("purged expired posts", "count", purged)
	}

	statsPurged, _ := db.PurgeExpiredStats(ctx, 90*24*time.Hour)
	if statsPurged > 0 {
		slog.Info("purged expired stats", "count", statsPurged)
	}

	collector.RecordCycleDuration(time.Since(cycleStart).Milliseconds())

	slog.Info("analysis cycle complete",
		"run_id", runID,
		"posts", len(posts),
		"sentiment", overallSentiment,
		"net_pct", fmt.Sprintf("%.1f%%", netSentimentPct),
		"cycle_ms", time.Since(cycleStart).Milliseconds(),
	)
}
