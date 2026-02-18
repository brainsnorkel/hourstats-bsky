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

	healthChartHours := envInt("HEALTH_CHART_HOURS", 6)
	healthChartMemoryLimitMB := envInt("HEALTH_CHART_MEMORY_LIMIT_MB", 512)

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
	go runWriteFlusher(ctx, db, writeCh, collector)
	go runJetstream(ctx, db, trendingEnabled, collector, writeCh)

	// Wall-clock aligned tickers: fire at clean UTC clock boundaries
	// so that deploys/restarts don't shift the schedule.
	analysisCh := newWallClockTicker(time.Duration(analysisMinutes)*time.Minute, time.Duration(analysisOffsetMinutes)*time.Minute)
	backupCh := newWallClockTicker(24*time.Hour, 0)
	yearlyPostCh := newDailyTickerAtHour(1)

	var topicAnalyzer *topics.Analyzer
	if trendingEnabled {
		topicAnalyzer = topics.NewAnalyzer(db, geminiAPIKey, geminiModel)
		slog.Info("trending topics enabled (runs with sentiment cycle)")
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

	stallCheckTicker := time.NewTicker(5 * time.Minute)
	defer stallCheckTicker.Stop()

	walCheckpointTicker := time.NewTicker(5 * time.Minute)
	defer walCheckpointTicker.Stop()

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
			// Take final snapshot and shut down stats API
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := collector.TakeSnapshot(shutdownCtx); err != nil {
				slog.Warn("final stats snapshot failed", "error", err)
			}
			if err := statsServer.Shutdown(shutdownCtx); err != nil {
				slog.Warn("stats API shutdown error", "error", err)
			}
			shutdownCancel()
			return

		case <-analysisCh:
			runAnalysisCycle(ctx, db, handle, password, dryRun, analysisMinutes, collector, topicAnalyzer)

		case <-backupCh:
			runBackup(db, dataDir, profile, backupRetainDays, s3Cfg)
			runDailyAggregation(ctx, db)
			runDailyTopPostQuote(ctx, db, handle, password, dryRun)
			if time.Now().UTC().Weekday() == time.Sunday {
				if err := db.RunVacuum(ctx); err != nil {
					slog.Error("weekly vacuum failed", "error", err)
				}
			}

		case <-yearlyPostCh:
			runYearlyPosting(ctx, db, handle, password, dryRun)

		case <-statsSnapshotCh:
			if err := collector.TakeSnapshot(ctx); err != nil {
				slog.Error("stats snapshot failed", "error", err)
			}

		case <-stallCheckTicker.C:
			lastPost := collector.LastPostReceived()
			if !lastPost.IsZero() {
				sinceLastPost := time.Since(lastPost)
				if sinceLastPost > 5*time.Minute {
					slog.Warn("jetstream stall detected: no posts received recently",
						"last_post_age", sinceLastPost.Round(time.Second),
						"firehose_total", collector.GetFirehoseCount(),
					)
					_ = collector.LogEvent(ctx, "stall_detected", fmt.Sprintf("last_post_age=%s", time.Since(lastPost).Truncate(time.Second)))
				}
			}

		case <-walCheckpointTicker.C:
			db.RunWALCheckpoint(ctx)
		}
	}
}
