package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/stats"
	"github.com/christophergentle/hourstats-bsky/internal/statsapi"
	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

func main() {
	profile := envOr("HOURSTATS_PROFILE", "staging")
	dataDir := envOr("DATA_DIR", "/data")
	handle := os.Getenv("BLUESKY_HANDLE")
	password := os.Getenv("BLUESKY_PASSWORD")
	dryRun := envBool("DRY_RUN", false)
	analysisMinutes := envInt("ANALYSIS_INTERVAL_MINUTES", 30)
	analysisOffsetMinutes := envInt("ANALYSIS_OFFSET_MINUTES", 0)
	backupRetainDays := envInt("BACKUP_RETAIN_DAYS", 1)

	s3BackupBucket := os.Getenv("S3_BACKUP_BUCKET")
	s3BackupRegion := envOr("S3_BACKUP_REGION", "us-west-2")
	s3BackupKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
	s3BackupSecret := os.Getenv("AWS_SECRET_ACCESS_KEY")

	trendingEnabled := envBool("TRENDING_ENABLED", false)
	geminiAPIKey := os.Getenv("GOOGLE_AI_API_KEY")
	geminiModel := envOr("GEMINI_MODEL", "gemini-2.5-pro")
	geminiFallbackModel := envOr("GROUP_FALLBACK_MODEL", "gemini-2.5-flash")

	healthChartHours := envInt("HEALTH_CHART_HOURS", 6)
	healthChartMemoryLimitMB := envInt("HEALTH_CHART_MEMORY_LIMIT_MB", 512)
	walCheckpointThresholdMB := envInt("WAL_CHECKPOINT_THRESHOLD_MB", 50)
	reportsEnabled := envBool("REPORTS_ENABLED", false)
	startupWeekly, startupMonthly, unknownReports := parseStartupReports(envOr("REPORTS_RUN_AT_STARTUP", ""))
	if len(unknownReports) > 0 {
		slog.Warn("REPORTS_RUN_AT_STARTUP has unknown job names, ignoring them", "names", unknownReports)
	}

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
	// Deliberately not `defer db.Close()`: the store must outlive the write
	// flusher drain and the consumer's cursor persist. runShutdown closes it
	// last, and the signal branch is main's only return path.
	slog.Info("database opened", "path", dbPath)
	recordFirehoseCountCutover(context.Background(), db, time.Now())

	if err := db.RunStartupMaintenance(context.Background()); err != nil {
		slog.Warn("startup maintenance failed", "error", err)
	}

	// Initialize stats collector
	collector := stats.New(db, dbPath)
	if err := collector.LogEvent(context.Background(), "app_start", fmt.Sprintf("profile=%s pid=%d", profile, os.Getpid())); err != nil {
		slog.Warn("failed to log app_start event", "error", err)
	}

	// Record schedule intervals so the stats API can compute next-anticipated times.
	_ = db.SetKeyValue(context.Background(), "schedule_sentiment_minutes", strconv.Itoa(analysisMinutes))
	_ = db.SetKeyValue(context.Background(), "schedule_daily_quote_hour", "0")
	_ = db.SetKeyValue(context.Background(), "schedule_yearly_hour", "1")
	if trendingEnabled {
		_ = db.SetKeyValue(context.Background(), "schedule_trending_with_sentiment", "true")
	}

	// Start stats HTTP API
	statsPort := 9111
	if p := os.Getenv("STATS_PORT"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil {
			statsPort = parsed
		}
	}
	statsServer := statsapi.New(db, statsPort, statsapi.HealthChartConfig{
		Hours:         healthChartHours,
		MemoryLimitMB: healthChartMemoryLimitMB,
	})
	if err := statsServer.Start(); err != nil {
		slog.Error("failed to start stats API", "error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	writeCh := make(chan store.PendingWrite, 50000)
	collector.SetWriteChannelFunc(func() int { return len(writeCh) })

	// Both goroutines write through the store, so shutdown must wait for them
	// to return before closing it.
	flusherDone := make(chan struct{})
	go func() {
		defer close(flusherDone)
		runWriteFlusher(ctx, db, writeCh, collector)
	}()

	activeConsumer := &consumerHandle{}
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		runJetstream(ctx, db, trendingEnabled, collector, writeCh, activeConsumer)
	}()

	// Wall-clock aligned tickers: fire at clean UTC clock boundaries
	// so that deploys/restarts don't shift the schedule.
	analysisCh := newWallClockTicker(time.Duration(analysisMinutes)*time.Minute, time.Duration(analysisOffsetMinutes)*time.Minute)
	backupCh := newWallClockTicker(24*time.Hour, 0)
	yearlyPostCh := newDailyTickerAtHour(1)

	var topicAnalyzer *topics.Analyzer
	if trendingEnabled {
		topicAnalyzer = topics.NewAnalyzer(db, geminiAPIKey, geminiModel, geminiFallbackModel)
		slog.Info("trending topics enabled (runs with sentiment cycle)", "model", geminiModel, "fallback_model", geminiFallbackModel)
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

	statsSnapshotCh := newWallClockTicker(30*time.Minute, 0)

	// A nil channel never fires, so the startup report run only exists when
	// asked for. It is how staging exercises the reports without waiting for
	// a Monday or the 1st; the key_value guards and DRY_RUN still apply.
	var reportsStartupCh <-chan time.Time
	switch {
	case reportsEnabled && (startupWeekly || startupMonthly):
		reportsStartupCh = time.After(reportsStartupDelay)
		slog.Info("reports: startup run scheduled",
			"weekly", startupWeekly, "monthly", startupMonthly, "delay", reportsStartupDelay)
	case startupWeekly || startupMonthly:
		slog.Warn("REPORTS_RUN_AT_STARTUP is set but REPORTS_ENABLED is false, ignoring")
	}

	// stallThreshold is the quiet period after which the firehose connection is
	// treated as dead and forcibly reconnected.
	const stallThreshold = 5 * time.Minute

	stallCheckTicker := time.NewTicker(5 * time.Minute)
	defer stallCheckTicker.Stop()

	walCheckpointTicker := time.NewTicker(5 * time.Minute)
	defer walCheckpointTicker.Stop()

	runBackup(db, dataDir, profile, backupRetainDays, s3Cfg)

	// Everything long-running is dispatched off this loop so that the WAL
	// checkpoint, stats snapshot and stall-detection tickers keep firing, and
	// SIGTERM is still read, during the minutes a cycle or daily job takes.
	// cycles guards the analysis cycle; jobs guards the daily and yearly work,
	// which also must not overlap each other — rendering the 365-day chart
	// beside a daily aggregation is a peak-RSS risk on a 1GB VM.
	cycles := &cycleGuard{}
	jobs := &cycleGuard{}

	slog.Info("scheduler started, wall-clock aligned",
		"analysis_every", fmt.Sprintf("%dm", analysisMinutes),
		"backup_every", "24h",
	)

	for {
		select {
		case sig := <-sigCh:
			slog.Info("received signal, shutting down", "signal", sig)
			runShutdown(shutdownHooks{
				Cancel:       cancel,
				WaitFlusher:  func(d time.Duration) bool { return waitClosed(flusherDone, d) },
				WaitConsumer: func(d time.Duration) bool { return waitClosed(consumerDone, d) },
				// ctx is already cancelled by this point, so these waits use a
				// live context: they are bounded by their own budgets.
				WaitCycle: func(d time.Duration) bool { return cycles.Wait(context.Background(), d) },
				WaitJob:   func(d time.Duration) bool { return jobs.Wait(context.Background(), d) },
				Snapshot: func() {
					snapCtx, snapCancel := context.WithTimeout(context.Background(), shutdownSnapshotBudget)
					defer snapCancel()
					if err := collector.TakeSnapshot(snapCtx); err != nil {
						slog.Warn("final stats snapshot failed", "error", err)
					}
				},
				StopStatsAPI: func() {
					apiCtx, apiCancel := context.WithTimeout(context.Background(), shutdownStatsAPIBudget)
					defer apiCancel()
					if err := statsServer.Shutdown(apiCtx); err != nil {
						slog.Warn("stats API shutdown error", "error", err)
					}
				},
				CloseStore: db.Close,
			})
			return

		case <-analysisCh:
			started := cycles.TryStart(func() {
				runAnalysisCycle(ctx, db, handle, password, dryRun, analysisMinutes, collector, topicAnalyzer)
			})
			if !started {
				slog.Error("analysis cycle still running at next tick, skipping this interval",
					"analysis_minutes", analysisMinutes)
				_ = collector.LogEvent(ctx, "cycle_overlap_skipped",
					fmt.Sprintf("interval_minutes=%d", analysisMinutes))
			}

		case <-backupCh:
			// The daily aggregate must include the final cycle of the day
			// (prod runs cycles at :55, this branch at 00:00), so the job waits
			// for an in-flight cycle — inside its own goroutine, so this loop
			// keeps serving SIGTERM and the tickers meanwhile.
			started := startAfterCycle(ctx, jobs, cycles, "daily", jobCycleWait, func() {
				// The sampler is stopped and its peak recorded even if a daily
				// step panics.
				stopDailyMemSampler := startMemSampler(ctx, 500*time.Millisecond, "daily")
				defer func() {
					peak := stopDailyMemSampler()
					// LogEvent already warns on failure. Detach from ctx so the
					// write still lands during shutdown.
					_ = collector.LogEvent(context.WithoutCancel(ctx), "cycle_memory_peak", peak.eventDetails("daily"))
				}()

				runBackup(db, dataDir, profile, backupRetainDays, s3Cfg)
				runDailyAggregation(ctx, db)
				// Rollups run before the quote reply so a posting failure
				// never costs the day's report inputs.
				runReportRollups(ctx, db, time.Now())
				runDailyTopPostQuote(ctx, db, handle, password, dryRun)
				if time.Now().UTC().Weekday() == time.Sunday {
					if err := db.RunVacuum(ctx); err != nil {
						slog.Error("weekly vacuum failed", "error", err)
					}
				}
				// Runs daily; the guard key makes it a no-op until a new
				// complete week exists, so a skipped Monday is caught up
				// the next night instead of lost.
				if reportsEnabled {
					runWeeklyReport(ctx, db, handle, password, dryRun, time.Now(), analysisMinutes)
				}
			})
			if !started {
				slog.Error("background job still running at daily tick, skipping daily cycle")
				_ = collector.LogEvent(ctx, "job_overlap_skipped", "job=daily")
			}

		case <-yearlyPostCh:
			// Rendering the 365-day chart beside a running analysis cycle
			// stacks two memory peaks on a VM that has already OOMed.
			started := startAfterCycle(ctx, jobs, cycles, "yearly", jobCycleWait, func() {
				runYearlyPosting(ctx, db, handle, password, dryRun)
				// Fires daily; the guard key makes it a no-op until a new
				// complete month exists, so a skipped 1st is caught up.
				if reportsEnabled {
					runMonthlyReport(ctx, db, handle, password, dryRun, time.Now())
				}
			})
			if !started {
				slog.Error("background job still running at yearly tick, skipping yearly posting")
				_ = collector.LogEvent(ctx, "job_overlap_skipped", "job=yearly")
			}

		case <-reportsStartupCh:
			started := startAfterCycle(ctx, jobs, cycles, "reports-startup", jobCycleWait, func() {
				now := time.Now()
				runReportRollups(ctx, db, now)
				if startupWeekly {
					runWeeklyReport(ctx, db, handle, password, dryRun, now, analysisMinutes)
				}
				if startupMonthly {
					runMonthlyReport(ctx, db, handle, password, dryRun, now)
				}
			})
			if !started {
				slog.Error("background job still running, skipping startup report run")
				_ = collector.LogEvent(ctx, "job_overlap_skipped", "job=reports-startup")
			}

		case <-statsSnapshotCh:
			if err := collector.TakeSnapshot(ctx); err != nil {
				slog.Error("stats snapshot failed", "error", err)
			}

		case <-stallCheckTicker.C:
			lastPost := collector.LastPostReceived()
			if !lastPost.IsZero() {
				sinceLastPost := time.Since(lastPost)
				if sinceLastPost > stallThreshold {
					// Logging alone left a black-holed connection in place;
					// drop it so the consumer's reconnect path takes over.
					forced := activeConsumer.forceReconnect()
					slog.Warn("jetstream stall detected: no posts received recently",
						"last_post_age", sinceLastPost.Round(time.Second),
						"firehose_total", collector.GetFirehoseCount(),
						"forced_reconnect", forced,
					)
					_ = collector.LogEvent(ctx, "stall_detected",
						fmt.Sprintf("last_post_age=%s forced_reconnect=%t",
							sinceLastPost.Truncate(time.Second), forced))
				}
			}

		case <-walCheckpointTicker.C:
			result := db.RunWALCheckpoint(ctx, int64(walCheckpointThresholdMB)*1024*1024)
			if result.Escalated {
				status := "incomplete"
				if result.Completed {
					status = "complete"
				}
				_ = collector.LogEvent(ctx, "wal_pressure_checkpoint",
					fmt.Sprintf("before=%dMB after=%dMB status=%s",
						result.WALBefore/(1024*1024), result.WALAfter/(1024*1024), status))
			}
		}
	}
}
