package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/hydrator"
	"github.com/christophergentle/hourstats-bsky/internal/jetstream"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func main() {
	profile := envOr("HOURSTATS_PROFILE", "staging")
	dataDir := envOr("DATA_DIR", "/data")
	handle := os.Getenv("BLUESKY_HANDLE")
	password := os.Getenv("BLUESKY_PASSWORD")
	dryRun := envBool("DRY_RUN", false)
	analysisMinutes := envInt("ANALYSIS_INTERVAL_MINUTES", 30)
	backupRetainDays := envInt("BACKUP_RETAIN_DAYS", 7)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	slog.Info("hourstats starting",
		"profile", profile,
		"data_dir", dataDir,
		"dry_run", dryRun,
		"analysis_minutes", analysisMinutes,
		"pid", os.Getpid(),
	)

	if handle == "" || password == "" {
		slog.Error("BLUESKY_HANDLE and BLUESKY_PASSWORD must be set")
		os.Exit(1)
	}

	dbPath := fmt.Sprintf("%s/hourstats-%s.db", dataDir, profile)
	db, err := store.New(dbPath)
	if err != nil {
		slog.Error("failed to open database", "path", dbPath, "error", err)
		os.Exit(1)
	}
	defer db.Close()
	slog.Info("database opened", "path", dbPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go runJetstream(ctx, db)

	analysisTicker := time.NewTicker(time.Duration(analysisMinutes) * time.Minute)
	defer analysisTicker.Stop()

	backupTicker := time.NewTicker(24 * time.Hour)
	defer backupTicker.Stop()

	runBackup(db, dataDir, profile, backupRetainDays)

	slog.Info("scheduler started", "analysis_every", fmt.Sprintf("%dm", analysisMinutes))

	for {
		select {
		case sig := <-sigCh:
			slog.Info("received signal, shutting down", "signal", sig)
			cancel()
			return

		case <-analysisTicker.C:
			runAnalysisCycle(ctx, db, handle, password, dryRun, analysisMinutes)

		case <-backupTicker.C:
			runBackup(db, dataDir, profile, backupRetainDays)
			runDailyAggregation(ctx, db)
		}
	}
}

// ---------------------------------------------------------------------------
// Jetstream consumer
// ---------------------------------------------------------------------------

func runJetstream(ctx context.Context, db *store.Store) {
	cfg := jetstream.ConsumerConfig{
		OnPost: func(evt *jetstream.Event, rec *jetstream.PostRecord) {
			if rec.Reply != nil {
				return
			}
			if strings.TrimSpace(rec.Text) == "" {
				return
			}
			cid := ""
			if evt.Commit != nil {
				cid = evt.Commit.CID
			}
			createdAt := normalizeTimestamp(rec.CreatedAt)
			post := store.Post{
				URI:       evt.PostURI(),
				CID:       cid,
				Text:      rec.Text,
				AuthorDID: evt.DID,
				CreatedAt: createdAt,
			}
			if err := db.InsertPost(ctx, post); err != nil {
				slog.Error("insert post failed", "uri", post.URI, "error", err)
			}
		},
		SaveCursor: func(saveCtx context.Context, cursor int64) error {
			return db.SaveCursor(saveCtx, cursor)
		},
		LoadCursor: func(loadCtx context.Context) (int64, error) {
			return db.GetCursor(loadCtx)
		},
	}

	consumer := jetstream.NewConsumer(cfg)
	if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("jetstream consumer exited with error", "error", err)
	}
}

// ---------------------------------------------------------------------------
// 30-minute analysis cycle
// ---------------------------------------------------------------------------

func runAnalysisCycle(ctx context.Context, db *store.Store, handle, password string, dryRun bool, analysisMinutes int) {
	runID := fmt.Sprintf("run-%s", time.Now().UTC().Format("20060102-150405"))
	slog.Info("analysis cycle starting", "run_id", runID)

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

	posts, err = db.GetPostsSince(ctx, cutoff)
	if err != nil {
		slog.Error("re-fetch posts failed", "error", err)
		return
	}

	analyzerPosts := toAnalyzerPosts(posts)
	sa := analyzer.New()
	analyzed, err := sa.AnalyzePosts(analyzerPosts)
	if err != nil {
		slog.Error("sentiment analysis failed", "error", err)
		return
	}

	overallSentiment, netSentimentPct := calculateOverallSentiment(analyzed)

	sort.Slice(analyzed, func(i, j int) bool {
		return analyzed[i].EngagementScore > analyzed[j].EngagementScore
	})
	top5 := analyzed
	if len(top5) > 5 {
		top5 = top5[:5]
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

	runState := store.RunState{
		RunID:                   runID,
		Status:                  "complete",
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

	avgCompound := netSentimentPct / 100.0
	sdp := store.SentimentDataPoint{
		RunID:                runID,
		Timestamp:            time.Now().UTC(),
		AverageCompoundScore: avgCompound,
		NetSentimentPercent:  netSentimentPct,
		SentimentCategory:    overallSentiment,
		TotalPosts:           len(posts),
		CreatedAt:            time.Now().UTC(),
		TTL:                  time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	if err := db.StoreSentimentDataPoint(ctx, sdp); err != nil {
		slog.Error("store sentiment data point failed", "error", err)
	}

	if dryRun {
		slog.Info("DRY_RUN: would post summary",
			"sentiment", overallSentiment,
			"net_pct", netSentimentPct,
			"top_count", len(top5),
			"total_posts", len(posts),
		)
	} else {
		postedURI, postedCID := postSummary(ctx, bskyClient, top5, overallSentiment, netSentimentPct, analysisMinutes, len(posts))
		if postedURI != "" {
			runState.TopPostURI = postedURI
			runState.TopPostCID = postedCID
			_ = db.UpdateRun(ctx, runState)
		}

		postSparkline(ctx, db, bskyClient, postedURI, postedCID, dryRun)
	}

	purged, _ := db.PurgeExpiredPosts(ctx, 2*time.Hour)
	if purged > 0 {
		slog.Info("purged expired posts", "count", purged)
	}

	slog.Info("analysis cycle complete",
		"run_id", runID,
		"posts", len(posts),
		"sentiment", overallSentiment,
		"net_pct", fmt.Sprintf("%.1f%%", netSentimentPct),
	)
}

// ---------------------------------------------------------------------------
// Sparkline
// ---------------------------------------------------------------------------

func postSparkline(ctx context.Context, db *store.Store, bskyClient *client.BlueskyClient, parentURI, parentCID string, dryRun bool) {
	history, err := db.GetSentimentHistory(ctx, 7*24*time.Hour)
	if err != nil {
		slog.Error("get sentiment history failed", "error", err)
		return
	}
	if len(history) < 2 {
		slog.Info("insufficient data for sparkline", "points", len(history))
		return
	}

	statePoints := toStateSentimentPoints(history)
	gen := sparkline.NewSparklineGenerator(nil)
	imgData, err := gen.GenerateSentimentSparkline(statePoints)
	if err != nil {
		slog.Error("generate sparkline failed", "error", err)
		return
	}

	postText := "Seven day Bluesky sentiment"
	altText := generateSparklineAltText(statePoints)

	if dryRun {
		slog.Info("DRY_RUN: would post sparkline", "points", len(history), "image_bytes", len(imgData))
		return
	}

	if parentURI != "" && parentCID != "" {
		if err := bskyClient.PostWithImageAsReply(ctx, postText, imgData, altText, parentURI, parentCID); err != nil {
			slog.Warn("sparkline reply failed, posting standalone", "error", err)
			_, _, _ = bskyClient.PostWithImage(ctx, postText, imgData, altText)
		}
	} else {
		_, _, _ = bskyClient.PostWithImage(ctx, postText, imgData, altText)
	}
	slog.Info("sparkline posted", "points", len(history))
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
// Backup
// ---------------------------------------------------------------------------

func runBackup(db *store.Store, dataDir, profile string, retainDays int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	path, err := db.Backup(ctx, dataDir, profile, retainDays)
	if err != nil {
		slog.Error("backup failed", "error", err)
	} else {
		slog.Info("backup complete", "path", path)
	}
}

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
// Sentiment calculation
// ---------------------------------------------------------------------------

func calculateOverallSentiment(posts []analyzer.AnalyzedPost) (string, float64) {
	if len(posts) == 0 {
		return "neutral", 0
	}
	var total float64
	for _, p := range posts {
		score := p.SentimentScore
		if score > 1 {
			score = 1
		} else if score < -1 {
			score = -1
		}
		total += score
	}
	avg := total / float64(len(posts))
	category := "neutral"
	if avg >= 0.3 {
		category = "positive"
	} else if avg <= -0.3 {
		category = "negative"
	}
	return category, avg * 100
}

// ---------------------------------------------------------------------------
// Type conversion helpers
// ---------------------------------------------------------------------------

func toAnalyzerPosts(posts []store.Post) []analyzer.Post {
	out := make([]analyzer.Post, len(posts))
	for i, p := range posts {
		out[i] = analyzer.Post{
			URI: p.URI, CID: p.CID, Text: p.Text,
			Author: p.AuthorHandle,
			Likes:  p.Likes, Reposts: p.Reposts, Replies: p.Replies,
			CreatedAt: p.CreatedAt,
		}
	}
	return out
}

func toStateSentimentPoints(points []store.SentimentDataPoint) []state.SentimentDataPoint {
	out := make([]state.SentimentDataPoint, len(points))
	for i, p := range points {
		out[i] = state.SentimentDataPoint{
			RunID:                p.RunID,
			Timestamp:            p.Timestamp,
			AverageCompoundScore: p.AverageCompoundScore,
			NetSentimentPercent:  p.NetSentimentPercent,
			SentimentCategory:    p.SentimentCategory,
			TotalPosts:           p.TotalPosts,
			CreatedAt:            p.CreatedAt,
			TTL:                  p.TTL,
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Math helpers
// ---------------------------------------------------------------------------

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

// ---------------------------------------------------------------------------
// Env helpers
// ---------------------------------------------------------------------------

func normalizeTimestamp(raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, raw)
	}
	if err != nil {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return t.UTC().Format(time.RFC3339)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return strings.EqualFold(v, "true") || v == "1"
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("invalid %s=%q, using default %d", key, v, fallback)
		return fallback
	}
	return n
}
