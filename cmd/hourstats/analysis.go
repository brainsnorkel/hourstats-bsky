package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/hydrator"
	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/stats"
	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

// minPostsRequired is the minimum post count per analysis window. Below this,
// the run is marked low-confidence: sentiment is still recorded, but posting to
// Bluesky and charting are skipped to avoid misleading output from tiny samples.
const minPostsRequired = 500

// topPostCount is how many top-engagement posts are listed in the summary post.
const topPostCount = 3

// topPostCandidates is how many are ranked and put to the feature gate, so a
// rejected post is replaced by the next one down rather than shortening the
// list. It stays inside the gate's per-call limit of 25 URIs.
const topPostCandidates = 10

var (
	sentimentAnalyzerOnce sync.Once
	sentimentAnalyzer     *analyzer.SentimentAnalyzer

	emojiAnalyzerOnce sync.Once
	emojiAnalyzer     *analyzer.SentimentAnalyzer

	hydrationFetcherOnce sync.Once
	hydrationFetcher     hydrator.PostFetcher // nil when HYDRATION_HOST=pds
	hydrationHost        string
)

// sharedHydrationFetcher returns the process-wide fetcher used for engagement
// hydration, or nil when HYDRATION_HOST=pds selects the authenticated PDS
// client instead.
//
// app.bsky.feed.getPosts is a public read, so it is routed to the cached
// appview host by default; that keeps hydration off bsky.social's per-IP
// budget. The fetcher (and therefore its connection pool) is built once: a
// per-cycle http.Transport would leak idle connections between cycles.
func sharedHydrationFetcher() (hydrator.PostFetcher, string) {
	hydrationFetcherOnce.Do(func() {
		hydrationHost = envOr("HYDRATION_HOST", hydrator.DefaultPublicHost)
		if hydrationHost == "pds" {
			slog.Info("hydration client configured", "host", "pds (authenticated)")
			return
		}
		hydrationFetcher = hydrator.NewPublicFetcher(hydrationHost)
		slog.Info("hydration client configured", "host", hydrationHost, "request_timeout", "15s")
	})
	return hydrationFetcher, hydrationHost
}

// sharedAnalyzer returns the process-wide stock VADER analyzer.
//
// analyzer.New() parses the full govader lexicon and emoji dictionary on every
// call, which was pure per-cycle overhead. The analyzer is read-only after
// construction — govader writes Lexicon/EmojiDict/Constants only in
// NewSentimentIntensityAnalyzer, and PolarityScores only reads them — so one
// instance can serve every cycle. Analysis cycles are sequential, so concurrent
// use is not required today; sync.Once keeps initialisation safe regardless.
//
// Since 2026-09-11 this is the second scorer: its net percent is recorded in
// sentiment_history.net_sentiment_pct_stock so the pre-switch series stays
// comparable, and nothing posted to Bluesky reads it.
func sharedAnalyzer() *analyzer.SentimentAnalyzer {
	sentimentAnalyzerOnce.Do(func() {
		sentimentAnalyzer = analyzer.New()
	})
	return sentimentAnalyzer
}

// sharedEmojiAnalyzer returns the process-wide emoji-aware VADER analyzer,
// which produces the headline sentiment for every cycle.
//
// It is built and reused on the same terms as sharedAnalyzer: construction
// parses the full lexicon, and the instance is read-only afterwards.
func sharedEmojiAnalyzer() *analyzer.SentimentAnalyzer {
	emojiAnalyzerOnce.Do(func() {
		emojiAnalyzer = analyzer.NewEmojiAware()
	})
	return emojiAnalyzer
}

// scorerV2Key marks the first UTC day whose cycles were scored by the
// emoji-aware headline scorer. Reports and any later recalibration use it to
// tell the two scales apart; cmd/realign moved everything before it.
const scorerV2Key = "sentiment_scorer_v2_since"

// recordScorerV2Cutover stores the cutover date on the first start of a build
// whose headline comes from the emoji-aware analyzer — which is every build
// since 2026-09-11, so this only has to be idempotent, not conditional.
func recordScorerV2Cutover(ctx context.Context, db *store.Store, now time.Time) {
	if v, _ := db.GetKeyValue(ctx, scorerV2Key); v != "" {
		return
	}
	since := utcDate(now).Format(dateFormat)
	if err := db.SetKeyValue(ctx, scorerV2Key, since); err != nil {
		slog.Warn("record sentiment scorer cutover failed", "error", err)
		return
	}
	slog.Info("sentiment scorer cutover recorded", "key", scorerV2Key, "since", since)
}

// windowScores is one analysis window scored by both scorers: the headline
// figures from the emoji-aware analyzer, and stock VADER's net percent beside
// them for continuity with the pre-2026-09-11 series.
type windowScores struct {
	// Analyzed is the headline scoring, one entry per input post.
	Analyzed []analyzer.AnalyzedPost
	Category string
	NetPct   float64
	RootPct  float64
	ReplyPct float64
	// StockNetPct is stock VADER's net percent for the same window, nil when
	// that pass failed.
	StockNetPct *float64
}

// scoreWindow scores posts with both analyzers. An error from the headline
// scorer is fatal to the cycle; a failure of the stock pass only costs the
// second column, so it is logged and StockNetPct is left nil.
func scoreWindow(posts []analyzer.Post, runID string) (windowScores, error) {
	analyzed, err := sharedEmojiAnalyzer().AnalyzePosts(posts)
	if err != nil {
		return windowScores{}, err
	}
	category, netPct := calculateOverallSentiment(analyzed)
	rootPct, replyPct := calculateSplitSentiment(analyzed)
	scores := windowScores{
		Analyzed: analyzed,
		Category: category,
		NetPct:   netPct,
		RootPct:  rootPct,
		ReplyPct: replyPct,
	}

	stockStart := time.Now()
	stockAnalyzed, stockErr := sharedAnalyzer().AnalyzePosts(posts)
	stockMS := time.Since(stockStart).Milliseconds()
	if stockErr != nil {
		slog.Warn("stock sentiment analysis failed", "error", stockErr, "run_id", runID)
		return scores, nil
	}
	_, stockPct := calculateOverallSentiment(stockAnalyzed)
	scores.StockNetPct = &stockPct
	slog.Info("sentiment scorers",
		"run_id", runID,
		"net_pct", netPct,
		"net_pct_stock", stockPct,
		"delta", fmt.Sprintf("%.2f", netPct-stockPct),
		"stock_ms", stockMS,
	)
	return scores, nil
}

// topicAnalysisOutcome carries the result of the parallel topic analysis
// goroutine. snapshotTime is the snapshot this cycle wrote (empty when none was
// produced) and gates whether a trending post may be published.
type topicAnalysisOutcome struct {
	snapshotTime string
	err          error
}

// topicWaitBeforeSparkline bounds how long the cycle waits for topic analysis
// before rendering the sparkline. Topic analysis overlaps hydration and in
// production finishes minutes before this point, so the wait is normally
// zero; the bound keeps a slow grouping call from ever delaying the chart.
const topicWaitBeforeSparkline = 60 * time.Second

// awaitTopicOutcome receives the topic analysis outcome, giving up after
// timeout or when ctx is cancelled. The second result reports whether an
// outcome was received; on false the value stays in the channel for a later
// receive.
func awaitTopicOutcome(ctx context.Context, done <-chan topicAnalysisOutcome, timeout time.Duration) (topicAnalysisOutcome, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case outcome := <-done:
		return outcome, true
	case <-timer.C:
		return topicAnalysisOutcome{}, false
	case <-ctx.Done():
		return topicAnalysisOutcome{}, false
	}
}

// topTopicStore is the slice of *store.Store that recordTopTopic needs.
type topTopicStore interface {
	GetTopicLabelAt(ctx context.Context, snapshotTime string, rank int) (string, error)
	SetSentimentTopTopic(ctx context.Context, runID, label string) (bool, error)
}

// recordTopTopic attaches this cycle's rank-1 trending topic to its
// sentiment_history row so the trending reply can name the topic beside the
// weekly high and low.
// Nothing is written when the cycle produced no snapshot or is shutting down
// (the writes would fail with "context canceled" anyway).
func recordTopTopic(ctx context.Context, db topTopicStore, runID string, outcome topicAnalysisOutcome) {
	if ctx.Err() != nil || outcome.err != nil || outcome.snapshotTime == "" {
		return
	}
	label, err := db.GetTopicLabelAt(ctx, outcome.snapshotTime, 1)
	if err != nil {
		slog.Warn("top topic lookup failed", "error", err, "run_id", runID, "snapshot_time", outcome.snapshotTime)
		return
	}
	if label == "" {
		return
	}
	updated, err := db.SetSentimentTopTopic(ctx, runID, label)
	if err != nil {
		slog.Warn("failed to record top topic on sentiment row", "error", err, "run_id", runID)
		return
	}
	if !updated {
		// The sentiment insert earlier in the cycle failed and was only logged.
		slog.Warn("no sentiment row to annotate with top topic", "run_id", runID, "topic", label)
		return
	}
	slog.Info("sentiment row annotated with top topic", "run_id", runID, "topic", label)
}

// ---------------------------------------------------------------------------
// 30-minute analysis cycle
// ---------------------------------------------------------------------------

func runAnalysisCycle(ctx context.Context, db *store.Store, handle, password string, dryRun bool, analysisMinutes int, collector *stats.Collector, topicAnalyzer *topics.Analyzer, guard *memGuard) {
	cycleStart := time.Now()
	runID := fmt.Sprintf("run-%s", time.Now().UTC().Format("20060102-150405"))
	slog.Info("analysis cycle starting", "run_id", runID)

	// cycleCtx cancels only this cycle's heavy work. The memory guard trips it
	// when RSS approaches the machine ceiling, so the process survives what
	// would otherwise be an OOM kill; the parent ctx still means shutdown, and
	// the bookkeeping writes below stay on it.
	cycleCtx, cancelCycle := context.WithCancel(ctx)
	defer cancelCycle()
	var guardTripped atomic.Bool

	// logGuardAbort names the memory guard at each early return a cancelled
	// cycleCtx can cause, so a trip is never mistaken for a shutdown.
	logGuardAbort := func(stage string) {
		if guardTripped.Load() {
			slog.Error("analysis cycle aborted by memory guard", "run_id", runID, "stage", stage)
		}
	}

	// Sample memory in-process: the stats snapshot ticker cannot fire while the
	// cycle runs, so the peak is otherwise invisible. The sampler runs on the
	// parent ctx so it keeps recording while a tripped cycle unwinds.
	stopMemSampler := startMemSamplerWithOptions(ctx, 500*time.Millisecond, runID, defaultMemSchedule, memSamplerOptions{
		Guard: guard.forCycle(func(memSample) {
			guardTripped.Store(true)
			cancelCycle()
		}),
		OnCheckpoint: func(s memSample) {
			_ = collector.LogEvent(context.WithoutCancel(ctx), "cycle_memory_tick", s.eventDetails())
		},
	})
	defer func() {
		peak := stopMemSampler()
		// LogEvent already warns on failure. Detach from ctx so the write still
		// lands when the cycle is unwinding because ctx was cancelled.
		_ = collector.LogEvent(context.WithoutCancel(ctx), "cycle_memory_peak", peak.eventDetails(runID))
	}()

	cutoff := time.Now().UTC().Add(-time.Duration(analysisMinutes) * time.Minute)

	posts, err := db.GetPostsSince(cycleCtx, cutoff)
	if err != nil {
		slog.Error("get posts failed", "error", err)
		logGuardAbort("get_posts")
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
	if guardTripped.Load() {
		// A trip during authentication: stop before hydration allocates.
		logGuardAbort("authenticate")
		return
	}

	// RunAnalysisCycle reads topic_tokens (no dependency on hydration).
	// Starting here overlaps Gemini latency with the hydration pipeline.
	// It hands back the snapshot time it wrote so the trending post can only
	// publish this cycle's topics, never a stale snapshot.
	var topicAnalysisDone <-chan topicAnalysisOutcome
	if topicAnalyzer != nil {
		// Rebound each cycle: the gate needs viewer state, which only an
		// authenticated client sees, and this cycle's client is the one that
		// just authenticated.
		topicAnalyzer.SetExemplarGate(newExemplarGate(bskyClient))
		ch := make(chan topicAnalysisOutcome, 1)
		topicAnalysisDone = ch
		slog.Info("topics: analysis goroutine started (parallel with hydration)")
		go func() {
			trendStart := time.Now()
			snapshotTime, err := topicAnalyzer.RunAnalysisCycle(cycleCtx)
			collector.RecordTrendingDuration(time.Since(trendStart).Milliseconds())
			ch <- topicAnalysisOutcome{snapshotTime: snapshotTime, err: err}
		}()
	}

	fetcher, host := sharedHydrationFetcher()
	if fetcher == nil {
		fetcher = hydrator.NewBlueskyFetcher(bskyClient.APIClient())
	}
	h := hydrator.New(fetcher, db, hydrator.Config{})
	result, err := h.Hydrate(cycleCtx, posts)
	if err != nil && cycleCtx.Err() != nil {
		logGuardAbort("hydration")
		return
	}
	hydrationTimedOut := errors.Is(err, hydrator.ErrHydrationTimedOut)
	if hydrationTimedOut {
		slog.Error("hydration timed out",
			"host", host,
			"total", result.Total,
			"hydrated", result.Hydrated,
			"filtered", result.Filtered,
			"errors", result.Errors,
			"error", err,
		)
		_ = collector.LogEvent(ctx, "hydration_timeout",
			fmt.Sprintf("run_id=%s total=%d hydrated=%d filtered=%d errors=%d retries=%d rate_limited=%d",
				runID, result.Total, result.Hydrated, result.Filtered, result.Errors, result.Retries, result.RateLimited))
	}
	slog.Info("hydration complete",
		"host", host,
		"total", result.Total,
		"hydrated", result.Hydrated,
		"filtered", result.Filtered,
		"errors", result.Errors,
		"retries", result.Retries,
		"rate_limited", result.RateLimited,
	)

	// Exclude posts that failed hydration (no author handle means the
	// hydrator could not resolve them — deleted, private, or API error).
	// Including them would skew sentiment with un-engageable ghost posts.
	windowPosts := len(posts)
	hydrated := make([]store.Post, 0, len(posts))
	for _, p := range posts {
		if p.AuthorHandle != "" {
			hydrated = append(hydrated, p)
		}
	}
	unhydrated := windowPosts - len(hydrated)
	if unhydrated > 0 {
		slog.Info("excluded unhydrated posts from analysis", "dropped", unhydrated, "remaining", len(hydrated))
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

	// If too much of the window never got hydrated, the surviving sample is not
	// representative, so take the low-confidence path: record sentiment, skip
	// posting. This applies however the posts were lost — a timeout, a
	// non-retryable 4xx storm, or retries exhausted early all leave the same
	// hole, and only the timeout used to be caught here.
	//
	// The default of 10% clears prod's routine exclusion rate (~3.6%, from posts
	// deleted or moderated between ingest and hydration) with room to spare, so
	// healthy cycles are unaffected.
	if windowPosts > 0 {
		maxUnhydratedPct := envInt("HYDRATION_MAX_UNHYDRATED_PCT", 10)
		unhydratedPct := float64(unhydrated) * 100 / float64(windowPosts)
		if unhydratedPct > float64(maxUnhydratedPct) {
			lowConfidence = true
			slog.Warn("too much of the window went unhydrated — posting skipped",
				"unhydrated", unhydrated,
				"window_posts", windowPosts,
				"unhydrated_pct", fmt.Sprintf("%.1f%%", unhydratedPct),
				"max_pct", maxUnhydratedPct,
				"hydration_timed_out", hydrationTimedOut,
			)
		}
	}

	// The headline — category, net percent and the root/reply split — comes
	// from the emoji-aware scorer; stock VADER scores the same window and its
	// net percent is kept beside it on the sentiment row.
	scores, err := scoreWindow(toAnalyzerPosts(posts), runID)
	if err != nil {
		slog.Error("sentiment analysis failed", "error", err)
		return
	}
	analyzed := scores.Analyzed
	overallSentiment, netSentimentPct := scores.Category, scores.NetPct
	rootSentimentPct, replySentimentPct := scores.RootPct, scores.ReplyPct

	// A trip this late has already cost the cycle its posts, but the sentiment
	// is computed and the parent ctx is live, so the hour is recorded rather
	// than left as a hole — marked low_confidence, since the window may have
	// been cut short.
	if guardTripped.Load() {
		lowConfidence = true
		slog.Error("analysis cycle aborted by memory guard, recording sentiment as low confidence",
			"run_id", runID,
			"stage", "post_analysis",
			"posts", len(posts),
		)
	}

	collector.RecordAnalysis(len(posts), result.Hydrated, result.Errors, overallSentiment, lowConfidence)

	sort.Slice(analyzed, func(i, j int) bool {
		return analyzed[i].EngagementScore > analyzed[j].EngagementScore
	})
	// Select the candidates, deduplicating by author so the same handle
	// doesn't appear multiple times (which breaks facet linking). More are
	// ranked than are listed so the feature gate has somewhere to fall back to.
	var candidates []analyzer.AnalyzedPost
	seenAuthors := make(map[string]bool)
	for _, ap := range analyzed {
		if seenAuthors[ap.Author] {
			continue
		}
		seenAuthors[ap.Author] = true
		candidates = append(candidates, ap)
		if len(candidates) >= topPostCandidates {
			break
		}
	}

	// Gate before the run row is written, not just before posting: the daily
	// and weekly reports read their top post out of `runs`, so a post that
	// cannot be featured must not be recorded as this cycle's.
	topPosts, quoteControlled, gateOK := gateTopPosts(cycleCtx, newFeatureGate(bskyClient), candidates)
	if !gateOK {
		// topPosts is empty, so the run row below records no top post either
		// and the daily and weekly reports cannot inherit an ungated one.
		slog.Warn("feature gate unavailable, posting summary without top posts", "run_id", runID)
	}

	topStorePosts := make([]store.Post, len(topPosts))
	for i, ap := range topPosts {
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

	firehoseSnapshot := int(collector.FirehoseSinceAnalysis())

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
		// The emoji column now mirrors the headline; it is still written so
		// the shadow-era series continues without a gap.
		NetSentimentPctEmoji: &netSentimentPct,
		NetSentimentPctStock: scores.StockNetPct,
		CreatedAt:            time.Now().UTC(),
		TTL:                  time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	if err := db.StoreSentimentDataPoint(ctx, sdp); err != nil {
		slog.Error("store sentiment data point failed", "error", err)
	}
	// Language mix of the firehose since the previous cycle, keyed to the
	// same timestamp as the sentiment row so the daily rollup lines up.
	if langs := collector.LanguagesSinceAnalysis(); len(langs) > 0 {
		if err := db.StoreLanguageCounts(ctx, sdp.Timestamp, langs); err != nil {
			// Hand the counts back so the next cycle carries them rather
			// than losing an hour of the language mix.
			collector.RestoreLanguages(langs)
			slog.Error("store language counts failed, carried to next cycle", "error", err)
		} else {
			slog.Info("language counts stored", "run_id", runID, "languages", len(langs))
		}
	}

	if guardTripped.Load() {
		// Nothing is published and the topic goroutine is not waited for: it
		// sees the cancelled cycleCtx and exits into its buffered channel,
		// which is the whole point of stopping here.
		slog.Error("memory guard tripped, skipping all posts for this cycle",
			"run_id", runID,
			"posts", len(posts),
			"sentiment", overallSentiment,
		)
	} else if lowConfidence {
		slog.Warn("skipping post due to low confidence",
			"posts", len(posts),
			"min_required", minPostsRequired,
			"sentiment", overallSentiment,
			"net_pct", fmt.Sprintf("%.1f%%", netSentimentPct),
		)
		if topicAnalysisDone != nil {
			outcome := <-topicAnalysisDone
			if outcome.err != nil {
				slog.Warn("topic analysis failed (low confidence cycle)", "error", outcome.err)
			}
			recordTopTopic(ctx, db, runID, outcome)
		}
	} else if dryRun {
		slog.Info("DRY_RUN: would post summary",
			"sentiment", overallSentiment,
			"net_pct", netSentimentPct,
			"top_count", len(topPosts),
			"total_posts", len(posts),
		)
		if topicAnalysisDone != nil {
			outcome := <-topicAnalysisDone
			if outcome.err != nil {
				slog.Warn("topic analysis failed (dry run cycle)", "error", outcome.err)
			}
			recordTopTopic(ctx, db, runID, outcome)
		}
	} else if ctx.Err() != nil {
		// Shutdown landed after hydration. The run and sentiment rows above
		// have already failed with "context canceled", so publishing now would
		// leave an orphan post and a hole in sentiment_history.
		slog.Warn("shutdown in progress, skipping all posts for this cycle",
			"run_id", runID,
			"error", ctx.Err(),
		)
		if topicAnalysisDone != nil {
			if outcome := <-topicAnalysisDone; outcome.err != nil {
				slog.Warn("topic analysis failed (shutdown cycle)", "error", outcome.err)
			}
		}
	} else {
		// The listed posts have already cleared the feature gate. What is left
		// to decide is the embed: a quote-controlled #1 would render as
		// "Removed by author". An unreachable gate has already emptied
		// topPosts, so the summary goes out as aggregate lines alone.
		if quoteControlled {
			slog.Info("top post cannot be quoted (quote control), posting without embed", "uri", topPosts[0].URI)
		}

		postedURI, postedCID := postSummary(ctx, bskyClient, topPosts, overallSentiment, netSentimentPct, analysisMinutes, len(posts), quoteControlled)
		if postedURI != "" {
			runState.TopPostURI = postedURI
			runState.TopPostCID = postedCID
			if err := db.UpdateRun(ctx, runState); err != nil {
				slog.Warn("failed to persist run TopPostURI", "error", err, "run_id", runState.RunID)
			}
		}

		// The posting block is minutes long and renders two charts, so a trip
		// can land inside it. The summary is already out and cannot be
		// unposted, but the sparkline and trending replies are exactly the
		// allocations worth not making. The topic goroutine is abandoned, not
		// waited for: its channel is buffered, so its send cannot block, and
		// it sees the cancelled cycle context on its own.
		if guardTripped.Load() {
			slog.Error("memory guard tripped after the summary post, skipping sparkline and trending",
				"run_id", runID,
				"summary_uri", postedURI,
			)
			return
		}

		// Collect this cycle's topics before the sparkline so the trending
		// reply's week high/low footer can name the newest point's topic when
		// that point is the weekly extreme. The trending post itself still
		// goes out after the sparkline.
		var outcome topicAnalysisOutcome
		topicsCollected := false
		if topicAnalysisDone != nil {
			topicWait := time.Now()
			outcome, topicsCollected = awaitTopicOutcome(ctx, topicAnalysisDone, topicWaitBeforeSparkline)
			if topicsCollected {
				slog.Info("timing: topic analysis goroutine collected",
					"waited", fmt.Sprintf("%.1fs", time.Since(topicWait).Seconds()),
					"cycle_elapsed", fmt.Sprintf("%.1fs", time.Since(cycleStart).Seconds()))
				recordTopTopic(ctx, db, runID, outcome)
			} else if ctx.Err() != nil {
				slog.Info("shutdown during topic wait, this cycle's topic will not be named with the week extremes")
			} else {
				slog.Warn("topic analysis still running, this cycle's topic will not be named with the week extremes",
					"waited", fmt.Sprintf("%.1fs", time.Since(topicWait).Seconds()))
			}
		}

		// One read of the seven-day history feeds both the chart and the
		// trending post's week high/low footer.
		var weekPoints []state.SentimentDataPoint
		if history, histErr := db.GetSentimentHistory(ctx, 7*24*time.Hour); histErr != nil {
			slog.Error("get sentiment history failed, skipping sparkline and week extremes", "error", histErr)
		} else {
			weekPoints = toStateSentimentPoints(filterHighConfidence(history))
		}

		rootURI, rootCID := postedURI, postedCID
		sparkURI, sparkCID := postSparkline(ctx, bskyClient, weekPoints, rootURI, rootCID, postedURI, postedCID, dryRun)
		slog.Info("timing: sparkline complete", "cycle_elapsed", fmt.Sprintf("%.1fs", time.Since(cycleStart).Seconds()))

		if topicAnalysisDone != nil {
			if !topicsCollected {
				topicWait := time.Now()
				outcome = <-topicAnalysisDone
				slog.Info("timing: topic analysis goroutine collected (after sparkline)",
					"waited", fmt.Sprintf("%.1fs", time.Since(topicWait).Seconds()),
					"cycle_elapsed", fmt.Sprintf("%.1fs", time.Since(cycleStart).Seconds()))
				recordTopTopic(ctx, db, runID, outcome)
			}
			if outcome.err != nil {
				slog.Error("topic analysis cycle failed", "error", outcome.err)
			} else if ctx.Err() != nil {
				// Cancellation can land mid-chain; RunTrendingPost's own DB read
				// would fail anyway, but say why rather than log a bare error.
				slog.Warn("shutdown in progress, skipping trending post", "run_id", runID, "error", ctx.Err())
			} else {
				// Reply under the sparkline when we have one, otherwise post
				// standalone. Either way the snapshot must be this cycle's.
				// A standalone post also drops the week high/low footer: the
				// figures only read as a caption on the chart above them.
				trendRoot, trendRootCID, trendParent, trendParentCID := rootURI, rootCID, sparkURI, sparkCID
				trendExtremes := weekExtremes(weekPoints)
				if sparkURI == "" || sparkCID == "" {
					trendRoot, trendRootCID, trendParent, trendParentCID = "", "", "", ""
					trendExtremes = nil
				}
				if err := topicAnalyzer.RunTrendingPost(ctx, bskyClient, dryRun, outcome.snapshotTime, trendRoot, trendRootCID, trendParent, trendParentCID, trendExtremes); err != nil {
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
