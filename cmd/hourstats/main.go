package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/hydrator"
	"github.com/christophergentle/hourstats-bsky/internal/jetstream"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

// firehosePostCount tracks ALL posts seen from the Jetstream firehose
// (before the English filter). It is snapshotted and reset each analysis cycle.
var firehosePostCount atomic.Int64

// lastPostReceived tracks the last time a post was processed from Jetstream.
var lastPostReceived atomic.Int64

func main() {
	profile := envOr("HOURSTATS_PROFILE", "staging")
	dataDir := envOr("DATA_DIR", "/data")
	handle := os.Getenv("BLUESKY_HANDLE")
	password := os.Getenv("BLUESKY_PASSWORD")
	dryRun := envBool("DRY_RUN", false)
	analysisMinutes := envInt("ANALYSIS_INTERVAL_MINUTES", 30)
	backupRetainDays := envInt("BACKUP_RETAIN_DAYS", 1)

	s3BackupBucket := os.Getenv("S3_BACKUP_BUCKET")
	s3BackupRegion := envOr("S3_BACKUP_REGION", "us-west-2")
	s3BackupKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
	s3BackupSecret := os.Getenv("AWS_SECRET_ACCESS_KEY")

	trendingEnabled := envBool("TRENDING_ENABLED", false)
	geminiAPIKey := os.Getenv("GOOGLE_AI_API_KEY")
	trendingInterval := envInt("TRENDING_INTERVAL", 15)
	trendingPostHours := envInt("TRENDING_POST_HOURS", 6)

	if trendingEnabled && geminiAPIKey == "" {
		slog.Error("TRENDING_ENABLED=true but GOOGLE_AI_API_KEY is empty, disabling trending")
		trendingEnabled = false
	}

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

	go runJetstream(ctx, db, trendingEnabled)

	// Wall-clock aligned tickers: fire at clean UTC clock boundaries
	// so that deploys/restarts don't shift the schedule.
	analysisCh := newWallClockTicker(time.Duration(analysisMinutes) * time.Minute)
	backupCh := newWallClockTicker(24 * time.Hour)
	yearlyPostCh := newDailyTickerAtHour(1)

	var topicAnalyzer *topics.Analyzer
	var topicAnalysisCh, trendingPostCh <-chan time.Time
	if trendingEnabled {
		slog.Info("trending topics enabled",
			"analysis_interval", fmt.Sprintf("%dm", trendingInterval),
			"post_interval", fmt.Sprintf("%dh", trendingPostHours),
		)
		topicAnalysisCh = newWallClockTicker(time.Duration(trendingInterval) * time.Minute)
		trendingPostCh = newWallClockTicker(time.Duration(trendingPostHours) * time.Hour)
	}

	var s3Cfg *store.S3BackupConfig
	if s3BackupBucket != "" && s3BackupKeyID != "" && s3BackupSecret != "" {
		s3Cfg = &store.S3BackupConfig{
			Bucket:          s3BackupBucket,
			Region:          s3BackupRegion,
			AccessKeyID:     s3BackupKeyID,
			SecretAccessKey: s3BackupSecret,
			Profile:         profile,
		}
		slog.Info("s3 backup enabled", "bucket", s3BackupBucket, "region", s3BackupRegion)
	}

	stallCheckTicker := time.NewTicker(5 * time.Minute)
	defer stallCheckTicker.Stop()

	runBackup(db, dataDir, profile, backupRetainDays, s3Cfg)

	slog.Info("scheduler started, wall-clock aligned",
		"analysis_every", fmt.Sprintf("%dm", analysisMinutes),
		"backup_every", "24h",
	)

	for {
		select {
		case sig := <-sigCh:
			slog.Info("received signal, shutting down", "signal", sig)
			cancel()
			return

		case <-analysisCh:
			runAnalysisCycle(ctx, db, handle, password, dryRun, analysisMinutes)

		case <-backupCh:
			runBackup(db, dataDir, profile, backupRetainDays, s3Cfg)
			runDailyAggregation(ctx, db)
			runDailyTopPostQuote(ctx, db, handle, password, dryRun)

		case <-yearlyPostCh:
			runYearlyPosting(ctx, db, handle, password, dryRun)

		case <-topicAnalysisCh:
			if topicAnalyzer == nil {
				bskyClient := client.New(handle, password)
				if err := bskyClient.Authenticate(); err != nil {
					slog.Error("trending auth failed", "error", err)
					continue
				}
				fetcher := hydrator.NewBlueskyFetcher(bskyClient.APIClient())
				topicAnalyzer = topics.NewAnalyzer(db, geminiAPIKey, fetcher)
			}
			if err := topicAnalyzer.RunAnalysisCycle(ctx); err != nil {
				slog.Error("topic analysis cycle failed", "error", err)
			}

		case <-trendingPostCh:
			bskyClient := client.New(handle, password)
			if err := bskyClient.Authenticate(); err != nil {
				slog.Error("trending post auth failed", "error", err)
				continue
			}
			if topicAnalyzer == nil {
				fetcher := hydrator.NewBlueskyFetcher(bskyClient.APIClient())
				topicAnalyzer = topics.NewAnalyzer(db, geminiAPIKey, fetcher)
			}
			if err := topicAnalyzer.RunTrendingPost(ctx, bskyClient, dryRun); err != nil {
				slog.Error("trending post failed", "error", err)
			}

		case <-stallCheckTicker.C:
			lastMs := lastPostReceived.Load()
			if lastMs > 0 {
				sinceLastPost := time.Since(time.UnixMilli(lastMs))
				if sinceLastPost > 5*time.Minute {
					slog.Warn("jetstream stall detected: no posts received recently",
						"last_post_age", sinceLastPost.Round(time.Second),
						"firehose_total", firehosePostCount.Load(),
					)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Jetstream consumer
// ---------------------------------------------------------------------------

func runJetstream(ctx context.Context, db *store.Store, trendingEnabled bool) {
	cfg := jetstream.ConsumerConfig{
		OnPost: func(evt *jetstream.Event, rec *jetstream.PostRecord) {
			firehosePostCount.Add(1)
			lastPostReceived.Store(time.Now().UnixMilli())

			if strings.TrimSpace(rec.Text) == "" {
				return
			}
			if !isEnglish(rec.Langs) {
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
				IsReply:   rec.Reply != nil,
			}
			if err := db.InsertPost(ctx, post); err != nil {
				slog.Error("insert post failed", "uri", post.URI, "error", err)
			}

			if trendingEnabled && rec.Reply == nil && !rec.HasAdultContent() {
				toks := topics.Tokenize(rec.Text)
				if len(toks) > 0 {
					tokJSON, _ := json.Marshal(toks)
					if err := db.InsertTopicTokens(ctx, post.URI, string(tokJSON), createdAt); err != nil {
						slog.Warn("insert topic tokens failed", "uri", post.URI, "error", err)
					}
				}
			}
		},
		SaveCursor: func(saveCtx context.Context, cursor int64) error {
			return db.SaveCursor(saveCtx, cursor)
		},
		LoadCursor: func(loadCtx context.Context) (int64, error) {
			return db.GetCursor(loadCtx)
		},
	}

	backoff := 1 * time.Second
	maxBackoff := 60 * time.Second

	for {
		consumer := jetstream.NewConsumer(cfg)
		err := consumer.Run(ctx)
		if ctx.Err() != nil {
			return
		}

		slog.Error("jetstream consumer exited unexpectedly, will restart",
			"error", err,
			"restart_in", backoff,
		)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
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

	// Exclude posts that failed hydration (no author handle means the
	// hydrator could not resolve them — deleted, private, or API error).
	// Including them would skew sentiment with un-engageable ghost posts.
	var hydrated []store.Post
	for _, p := range posts {
		if p.AuthorHandle != "" {
			hydrated = append(hydrated, p)
		}
	}
	if dropped := len(posts) - len(hydrated); dropped > 0 {
		slog.Info("excluded unhydrated posts from analysis", "dropped", dropped, "remaining", len(hydrated))
	}
	posts = hydrated

	analyzerPosts := toAnalyzerPosts(posts)
	sa := analyzer.New()
	analyzed, err := sa.AnalyzePosts(analyzerPosts)
	if err != nil {
		slog.Error("sentiment analysis failed", "error", err)
		return
	}

	overallSentiment, netSentimentPct := calculateOverallSentiment(analyzed)
	rootSentimentPct, replySentimentPct := calculateSplitSentiment(analyzed)

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

	firehoseSnapshot := int(firehosePostCount.Swap(0))

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

		rootURI, rootCID := postedURI, postedCID
		sparkURI, sparkCID := postSparkline(ctx, db, bskyClient, rootURI, rootCID, postedURI, postedCID, dryRun)
		if sparkURI != "" {
			postSentimentTrendline(ctx, db, bskyClient, rootURI, rootCID, sparkURI, sparkCID, dryRun)
		}
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

func postSentimentTrendline(ctx context.Context, db *store.Store, bskyClient *client.BlueskyClient, rootURI, rootCID, parentURI, parentCID string, dryRun bool) (string, string) {
	history, err := db.GetSentimentHistory(ctx, 7*24*time.Hour)
	if err != nil {
		slog.Error("get sentiment history for trendline failed", "error", err)
		return "", ""
	}

	var withSplitData []store.SentimentDataPoint
	for _, dp := range history {
		if dp.RootSentimentPct != 0 || dp.ReplySentimentPct != 0 {
			withSplitData = append(withSplitData, dp)
		}
	}
	if len(withSplitData) < 2 {
		slog.Info("insufficient split sentiment data for trendline", "points", len(withSplitData))
		return "", ""
	}

	gen := sparkline.NewSentimentTrendlineGenerator(nil)
	imgData, err := gen.GenerateSentimentTrendline(withSplitData)
	if err != nil {
		slog.Error("generate sentiment trendline failed", "error", err)
		return "", ""
	}

	postText := "Original vs Reply Sentiment"
	latest := withSplitData[len(withSplitData)-1]
	altText := fmt.Sprintf("Seven day sentiment trendline. Original posts: %.1f%%. Replies: %.1f%%.",
		latest.RootSentimentPct, latest.ReplySentimentPct)

	if dryRun {
		slog.Info("DRY_RUN: would post sentiment trendline", "points", len(withSplitData), "image_bytes", len(imgData))
		return "", ""
	}

	var trendURI, trendCID string
	if parentURI != "" && parentCID != "" {
		trendURI, trendCID, err = bskyClient.PostWithImageAsReply(ctx, postText, imgData, altText, rootURI, rootCID, parentURI, parentCID)
		if err != nil {
			slog.Warn("sentiment trendline reply failed, posting standalone", "error", err)
			trendURI, trendCID, _ = bskyClient.PostWithImage(ctx, postText, imgData, altText)
		}
	} else {
		trendURI, trendCID, _ = bskyClient.PostWithImage(ctx, postText, imgData, altText)
	}
	slog.Info("sentiment trendline posted", "points", len(withSplitData))
	return trendURI, trendCID
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
// Yearly posting (1st of each month)
// ---------------------------------------------------------------------------

func runYearlyPosting(ctx context.Context, db *store.Store, handle, password string, dryRun bool) {
	slog.Info("yearly posting check triggered")

	yearlyData, err := db.GetYearlySentimentData(ctx)
	if err != nil {
		slog.Error("get yearly sentiment data failed", "error", err)
		return
	}
	if len(yearlyData) < 7 {
		slog.Info("insufficient data for yearly chart", "days", len(yearlyData))
		return
	}

	statePoints := toStateYearlyPoints(yearlyData)

	gen := sparkline.NewYearlySparklineGenerator(nil)
	imgData, err := gen.GenerateYearlySentimentSparkline(statePoints)
	if err != nil {
		slog.Error("generate yearly sparkline failed", "error", err)
		return
	}

	postText := buildYearlyPostText(statePoints)
	altText := buildYearlyAltText(statePoints)

	if dryRun {
		slog.Info("DRY_RUN: would post yearly charts", "days", len(yearlyData), "image_bytes", len(imgData))
		return
	}

	bskyClient := client.New(handle, password)
	if err := bskyClient.Authenticate(); err != nil {
		slog.Error("bluesky auth for yearly post failed", "error", err)
		return
	}

	eventDates := buildEventDates(statePoints)
	facets := client.CreateWikipediaLinkFacets(postText, eventDates...)

	var sentimentURI, sentimentCID string
	if len(facets) > 0 {
		sentimentURI, sentimentCID, err = bskyClient.PostWithImage(ctx, postText, imgData, altText, facets)
	} else {
		sentimentURI, sentimentCID, err = bskyClient.PostWithImage(ctx, postText, imgData, altText)
	}
	if err != nil {
		slog.Error("post yearly sentiment chart failed", "error", err)
		return
	}
	slog.Info("yearly sentiment chart posted", "uri", sentimentURI)

	if err := db.SetKeyValue(ctx, "yearly_post_uri", sentimentURI); err != nil {
		slog.Warn("persist yearly post URI failed", "error", err)
	}
	if err := db.SetKeyValue(ctx, "yearly_post_cid", sentimentCID); err != nil {
		slog.Warn("persist yearly post CID failed", "error", err)
	}

	if err := bskyClient.PinPost(ctx, sentimentURI, sentimentCID); err != nil {
		slog.Warn("pin yearly post failed", "error", err)
	}
}

func toStateYearlyPoints(points []store.YearlySparklineDataPoint) []state.YearlySparklineDataPoint {
	out := make([]state.YearlySparklineDataPoint, len(points))
	for i, p := range points {
		out[i] = state.YearlySparklineDataPoint{
			Date:                p.Date,
			AverageSentiment:    p.AverageSentiment,
			MinSentiment:        p.MinSentiment,
			MaxSentiment:        p.MaxSentiment,
			Q1Sentiment:         p.Q1Sentiment,
			MedianSentiment:     p.MedianSentiment,
			Q3Sentiment:         p.Q3Sentiment,
			Timestamp:           p.Timestamp,
			NetSentimentPercent: p.NetSentimentPercent,
		}
	}
	return out
}

func buildYearlyPostText(points []state.YearlySparklineDataPoint) string {
	if len(points) == 0 {
		return "Bluesky Sentiment"
	}
	startDate := points[0].Timestamp.Format("2006-01-02")
	endDate := points[len(points)-1].Timestamp.Format("2006-01-02")
	text := fmt.Sprintf("Bluesky Sentiment %s - %s", startDate, endDate)

	var minSent, maxSent float64
	var minDate, maxDate string
	minSent = points[0].AverageSentiment
	maxSent = points[0].AverageSentiment
	for _, p := range points {
		if p.AverageSentiment < minSent {
			minSent = p.AverageSentiment
			minDate = p.Date
		}
		if p.AverageSentiment > maxSent {
			maxSent = p.AverageSentiment
			maxDate = p.Date
		}
	}

	var extremes []string
	if t, err := time.Parse("2006-01-02", minDate); err == nil {
		extremes = append(extremes, fmt.Sprintf("Lowest: %.1f%% %s events", minSent, t.Format("Jan 2")))
	}
	if t, err := time.Parse("2006-01-02", maxDate); err == nil {
		extremes = append(extremes, fmt.Sprintf("Highest: %.1f%% %s events", maxSent, t.Format("Jan 2")))
	}
	if len(extremes) > 0 {
		text += "\n\n" + strings.Join(extremes, "\n")
	}
	return text
}

func buildYearlyAltText(points []state.YearlySparklineDataPoint) string {
	if len(points) == 0 {
		return "Yearly Bluesky sentiment chart"
	}
	var sum, minS, maxS float64
	var minDate, maxDate string
	minS = points[0].AverageSentiment
	maxS = points[0].AverageSentiment
	for _, p := range points {
		sum += p.AverageSentiment
		if p.AverageSentiment < minS {
			minS = p.AverageSentiment
			minDate = p.Date
		}
		if p.AverageSentiment > maxS {
			maxS = p.AverageSentiment
			maxDate = p.Date
		}
	}
	avg := sum / float64(len(points))
	latest := points[len(points)-1]
	return fmt.Sprintf("Yearly Bluesky sentiment trend. Current: %.1f%% (%s). High: %.1f%% (%s). Low: %.1f%% (%s). Average: %.1f%%.",
		latest.AverageSentiment, latest.Date, maxS, maxDate, minS, minDate, avg)
}

func buildEventDates(points []state.YearlySparklineDataPoint) []client.EventDate {
	if len(points) == 0 {
		return nil
	}
	var minSent, maxSent float64
	var minDate, maxDate string
	minSent = points[0].AverageSentiment
	maxSent = points[0].AverageSentiment
	for _, p := range points {
		if p.AverageSentiment < minSent {
			minSent = p.AverageSentiment
			minDate = p.Date
		}
		if p.AverageSentiment > maxSent {
			maxSent = p.AverageSentiment
			maxDate = p.Date
		}
	}
	_ = minSent
	_ = maxSent

	var dates []client.EventDate
	if t, err := time.Parse("2006-01-02", minDate); err == nil {
		dates = append(dates, client.EventDate{DisplayText: t.Format("Jan 2"), FullDate: minDate})
	}
	if t, err := time.Parse("2006-01-02", maxDate); err == nil {
		dates = append(dates, client.EventDate{DisplayText: t.Format("Jan 2"), FullDate: maxDate})
	}
	return dates
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

	bskyClient := client.New(handle, password)
	if err := bskyClient.Authenticate(); err != nil {
		slog.Error("bluesky auth for daily quote failed", "error", err)
		return
	}

	_, _, err = bskyClient.PostReplyWithQuote(ctx, text,
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

// calculateSplitSentiment calculates separate net sentiment percentages
// for root posts and reply posts. Returns (rootPct, replyPct).
// If a group has no posts, its percentage is 0.
func calculateSplitSentiment(posts []analyzer.AnalyzedPost) (float64, float64) {
	var rootTotal, replyTotal float64
	var rootCount, replyCount int

	for _, p := range posts {
		score := p.SentimentScore
		if score > 1 {
			score = 1
		} else if score < -1 {
			score = -1
		}
		if p.IsReply {
			replyTotal += score
			replyCount++
		} else {
			rootTotal += score
			rootCount++
		}
	}

	var rootPct, replyPct float64
	if rootCount > 0 {
		rootPct = (rootTotal / float64(rootCount)) * 100
	}
	if replyCount > 0 {
		replyPct = (replyTotal / float64(replyCount)) * 100
	}
	return rootPct, replyPct
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
			IsReply:   p.IsReply,
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

func isEnglish(langs []string) bool {
	if len(langs) == 0 {
		return false
	}
	for _, l := range langs {
		if l == "en" || strings.HasPrefix(l, "en-") {
			return true
		}
	}
	return false
}

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

// newWallClockTicker returns a channel that fires at wall-clock aligned UTC
// boundaries. For example, a 30m interval fires at :00 and :30 past the hour;
// a 3h interval fires at 00:00, 03:00, 06:00, etc. This ensures deploys and
// restarts don't shift the posting schedule.
func newDailyTickerAtHour(hour int) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
			if !next.After(now) {
				next = next.AddDate(0, 0, 1)
			}
			delay := next.Sub(now)
			slog.Info("daily ticker scheduled",
				"hour_utc", hour,
				"next_fire", next.Format(time.RFC3339),
				"delay", delay.Round(time.Second),
			)
			timer := time.NewTimer(delay)
			<-timer.C
			ch <- time.Now()
		}
	}()
	return ch
}

func newWallClockTicker(interval time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		for {
			now := time.Now().UTC()
			next := now.Truncate(interval).Add(interval)
			delay := next.Sub(now)
			slog.Info("wall-clock ticker scheduled",
				"interval", interval,
				"next_fire", next.Format(time.RFC3339),
				"delay", delay.Round(time.Second),
			)
			timer := time.NewTimer(delay)
			<-timer.C
			ch <- time.Now()
		}
	}()
	return ch
}
