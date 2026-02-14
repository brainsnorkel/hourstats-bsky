// force-trending runs a single topic analysis cycle followed by a trending post.
// It operates against the live SQLite database and posts to Bluesky unless --dry-run is set.
//
// Usage (on Fly via SSH):
//
//	force-trending --db /data/hourstats-staging.db
//	force-trending --db /data/hourstats-staging.db --dry-run
//	force-trending --db /data/hourstats-staging.db --post-only   # skip analysis, just post
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

func main() {
	dbPath := flag.String("db", "/data/hourstats-staging.db", "path to SQLite database")
	dryRun := flag.Bool("dry-run", false, "log what would be posted without posting")
	postOnly := flag.Bool("post-only", false, "skip analysis cycle, just post from existing snapshots")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	handle := os.Getenv("BLUESKY_HANDLE")
	password := os.Getenv("BLUESKY_PASSWORD")
	geminiKey := os.Getenv("GOOGLE_AI_API_KEY")
	geminiModel := os.Getenv("GEMINI_MODEL")

	if handle == "" || password == "" {
		slog.Error("BLUESKY_HANDLE and BLUESKY_PASSWORD must be set")
		os.Exit(1)
	}
	if geminiKey == "" {
		slog.Error("GOOGLE_AI_API_KEY must be set")
		os.Exit(1)
	}

	db, err := store.New(*dbPath)
	if err != nil {
		slog.Error("failed to open database", "path", *dbPath, "error", err)
		os.Exit(1)
	}
	defer db.Close()
	slog.Info("database opened", "path", *dbPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	bskyClient := client.New(handle, password)
	if err := bskyClient.Authenticate(); err != nil {
		slog.Error("bluesky auth failed", "error", err)
		os.Exit(1)
	}
	slog.Info("authenticated with bluesky", "handle", handle)

	analyzer := topics.NewAnalyzer(db, geminiKey, geminiModel)

	if !*postOnly {
		slog.Info("running topic analysis cycle...")
		if err := analyzer.RunAnalysisCycle(ctx); err != nil {
			slog.Error("topic analysis cycle failed", "error", err)
			os.Exit(1)
		}
		slog.Info("topic analysis cycle complete")
	} else {
		slog.Info("skipping analysis cycle (--post-only)")
	}

	slog.Info("running trending post...", "dry_run", *dryRun)
	if err := analyzer.RunTrendingPost(ctx, bskyClient, *dryRun, "", "", "", ""); err != nil {
		slog.Error("trending post failed", "error", err)
		os.Exit(1)
	}

	if *dryRun {
		fmt.Println("✓ Dry run complete — no post was published")
	} else {
		fmt.Println("✓ Trending post published")
	}
}
