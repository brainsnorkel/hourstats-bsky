package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	profile := os.Getenv("HOURSTATS_PROFILE")
	if profile == "" {
		profile = "staging"
	}

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "/data"
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("hourstats starting",
		"profile", profile,
		"data_dir", dataDir,
		"pid", os.Getpid(),
	)

	dbPath := fmt.Sprintf("%s/hourstats-%s.db", dataDir, profile)
	slog.Info("database path", "path", dbPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case sig := <-sigCh:
			slog.Info("received signal, shutting down", "signal", sig)
			return
		case <-ticker.C:
			slog.Info("heartbeat", "profile", profile, "uptime", "ok")
		}
	}
}
