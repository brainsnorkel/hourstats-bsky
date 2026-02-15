package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/stats"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// Write buffer flusher — batches firehose writes to reduce SQLite contention
// ---------------------------------------------------------------------------

func runWriteFlusher(ctx context.Context, db *store.Store, ch <-chan store.PendingWrite, collector *stats.Collector) {
	const (
		maxBatch  = 1500
		flushFreq = 2 * time.Second
	)
	ticker := time.NewTicker(flushFreq)
	defer ticker.Stop()

	batch := make([]store.PendingWrite, 0, maxBatch)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		n := len(batch)
		start := time.Now()
		if err := db.FlushPostBatch(ctx, batch); err != nil {
			slog.Error("flush post batch failed", "batch_size", n, "error", err)
			_ = collector.LogEvent(ctx, "batch_flush_error", fmt.Sprintf("size=%d err=%v", n, err))
		}
		if err := db.FlushTokenBatch(ctx, batch); err != nil {
			slog.Warn("flush token batch failed", "batch_size", n, "error", err)
		}
		if dur := time.Since(start); dur > 1*time.Second {
			slog.Warn("slow write flush", "batch_size", n, "duration_ms", dur.Milliseconds())
		}
		batch = batch[:0]
	}

	for {
		select {
		case w, ok := <-ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, w)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			// Drain remaining items from channel before exiting
			for {
				select {
				case w := <-ch:
					batch = append(batch, w)
				default:
					flush()
					return
				}
			}
		}
	}
}
