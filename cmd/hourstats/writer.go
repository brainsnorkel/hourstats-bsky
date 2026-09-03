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

// drainBudget bounds the shutdown drain. It must stay under
// shutdownFlusherBudget so main's shutdown sequence observes this goroutine
// returning rather than timing out on it and closing the store underneath.
const drainBudget = 4 * time.Second

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
			collector.IncrementSlowFlush(dur.Milliseconds())
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
			// Drain remaining items from channel before exiting.
			// ctx is already cancelled here, so use a fresh bounded context
			// so that the final FlushPostBatch call actually reaches SQLite.
			// main's shutdown sequence waits for this goroutine to return
			// before closing the store, so these writes always land.
			drainCtx, drainCancel := context.WithTimeout(context.Background(), drainBudget)
			defer drainCancel()

			drainFlush := func() {
				if len(batch) == 0 {
					return
				}
				n := len(batch)
				if err := db.FlushPostBatch(drainCtx, batch); err != nil {
					slog.Error("shutdown drain flush failed", "batch_size", n, "error", err)
				}
				if err := db.FlushTokenBatch(drainCtx, batch); err != nil {
					slog.Warn("shutdown drain token flush failed", "batch_size", n, "error", err)
				}
				batch = batch[:0]
			}

			drained := 0
			for {
				// Checked before the select: a select with a default clause
				// only picks drainCtx.Done() when it happens to be ready at
				// the same instant, so the budget needs an explicit test.
				if drainCtx.Err() != nil {
					drainFlush()
					slog.Warn("shutdown drain budget exceeded, some posts may be lost",
						"shutdown_drain_count", drained, "queued", len(ch))
					return
				}
				select {
				case w := <-ch:
					batch = append(batch, w)
					drained++
					if len(batch) >= maxBatch {
						drainFlush()
					}
				default:
					drainFlush()
					slog.Info("shutdown drain complete", "shutdown_drain_count", drained)
					return
				}
			}
		}
	}
}
