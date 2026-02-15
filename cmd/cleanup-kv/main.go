// cleanup-kv removes prod-specific key-value entries from a staging database.
// It uses the same Go SQLite driver as the bot, so WAL locking works correctly
// even when the bot is running — no need to kill the bot first.
//
// Usage (on Fly via SSH):
//
//	cleanup-kv --db /data/hourstats-staging.db
//	cleanup-kv --db /data/hourstats-staging.db --dry-run
//	cleanup-kv --db /data/hourstats-staging.db --key yearly_post_uri --key yearly_post_cid
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// defaultKeys are prod-specific KV entries that must be removed after syncing
// a prod snapshot to staging. These point to prod threads/state that staging
// cannot use and would cause staging to post replies to prod threads.
var defaultKeys = []string{
	"yearly_post_uri",
	"yearly_post_cid",
	"daily_quote_last_date",
	"daily_quote_post_uri",
	"trending_post_last_time",
	"schedule_trending_post_hours",
}

type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	dbPath := flag.String("db", "/data/hourstats-staging.db", "path to SQLite database")
	dryRun := flag.Bool("dry-run", false, "show what would be deleted without deleting")

	var keys stringSlice
	flag.Var(&keys, "key", "specific key to delete (can be repeated; defaults to all prod-specific keys)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if len(keys) == 0 {
		keys = defaultKeys
	}

	slog.Info("cleanup-kv starting", "db", *dbPath, "dry_run", *dryRun, "keys", keys)

	db, err := store.New(*dbPath)
	if err != nil {
		slog.Error("failed to open database", "path", *dbPath, "error", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var found int
	for _, key := range keys {
		entry, err := db.GetKeyValueWithTimestamp(ctx, key)
		if err != nil {
			slog.Error("failed to read key", "key", key, "error", err)
			os.Exit(1)
		}
		if entry != nil {
			found++
			slog.Info("found prod key", "key", entry.Key, "value", entry.Value, "updated_at", entry.UpdatedAt)
		} else {
			slog.Info("key not present", "key", key)
		}
	}

	if found == 0 {
		fmt.Println("✓ No prod-specific keys found — database is clean")
		os.Exit(0)
	}

	if *dryRun {
		fmt.Printf("✓ Dry run: would delete %d key(s)\n", found)
		os.Exit(0)
	}

	deleted, err := db.DeleteKeyValues(ctx, keys)
	if err != nil {
		slog.Error("failed to delete keys", "error", err)
		os.Exit(1)
	}

	slog.Info("deleted prod-specific keys", "count", deleted)

	for _, key := range keys {
		entry, err := db.GetKeyValueWithTimestamp(ctx, key)
		if err != nil {
			slog.Error("verification failed", "key", key, "error", err)
			os.Exit(1)
		}
		if entry != nil {
			slog.Error("key still exists after deletion", "key", key, "value", entry.Value)
			os.Exit(1)
		}
	}

	fmt.Printf("✓ Cleaned %d prod-specific key(s) — verified\n", deleted)
}
