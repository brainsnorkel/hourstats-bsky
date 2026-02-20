# SQLite Write Bottleneck Fix

HourStats was dropping 10,000–80,000 posts per 30-minute window because the SQLite write path couldn't keep up with the Bluesky firehose. This document explains what went wrong, how it was fixed, and what to do as traffic grows.

## The Problem

Every write flush was taking 2–24 seconds on staging, compared to a target of <200ms. The channel buffer (50K posts) would fill up during each flush, and incoming posts were dropped on the floor. Stats snapshots showed consistent drops every cycle.

```
Before:
  batch_size=150, duration_ms=6000–24000    ← 150 posts taking 6–24 seconds
  10K–80K posts dropped per 30-min window
  WAL file: 828MB (growing without bound)
  DB main file: 3.3GB (3.6GB total on 5GB disk = 77%)

After:
  batch_size=1500, duration_ms=250–1200     ← 1500 posts taking 0.25–1.2 seconds
  0 dropped posts
  WAL file: 1.6MB (checkpointed every 5 min)
```

## Root Cause

The `token_postings` table was the primary bottleneck. It existed as a denormalised index for exemplar post lookups in trending topics — one row per (token, post_uri) pair for every root post. With the Bluesky firehose delivering ~1,500 English posts/min and each root post producing 5–15 tokens, the table was growing by tens of thousands of rows per flush cycle.

The table had:
- A composite primary key `(token, post_uri)`
- Two additional B-tree indexes: `(token, created_at)` and `(created_at)`
- So large that `SELECT COUNT(*)` timed out after 60 seconds

Each flush was inserting N×batch_size rows into this heavily-indexed table. On modernc.org/sqlite (pure Go, no CGO), the per-statement overhead is higher than with cgo-sqlite, making "many small inserts inside a transaction" disproportionately expensive.

Compounding the problem, the WAL was never checkpointed. SQLite's auto-checkpoint requires a brief exclusive lock, but the write connection was always busy flushing. The WAL grew to 828MB, meaning every write had to scan more WAL frames before finding the right page. This created a feedback loop: writes got slower, which meant the writer was busy longer, which prevented checkpointing, which made writes slower.

The purge path for `token_postings` also competed for the write lock — it ran during topic analysis (every 15 min) and had to delete tens of thousands of rows.

## The Fix

Four changes, applied together:

### 1. Remove token_postings from the ingest hot path

`FlushTokenBatch()` in `write_batch.go` no longer inserts into `token_postings`. It only writes to `topic_tokens` (one row per post, tokens stored as a JSON array). `InsertTopicTokens()` in `topic_store.go` (used by tests and the single-insert path) was also cleaned up.

The `GetExemplarCandidates()` query was rewritten to use SQLite's `json_each()` function on the `topic_tokens.tokens` column instead of joining `token_postings`:

```sql
-- Before: join a massive denormalised table
SELECT pb.uri, pb.author_handle, (pb.likes + pb.reposts + pb.replies) AS eng
FROM token_postings tp
JOIN post_buffer pb ON tp.post_uri = pb.uri
WHERE tp.token IN (?, ?, ...)
  AND tp.created_at >= ?
GROUP BY pb.uri
ORDER BY COUNT(DISTINCT tp.token) DESC, eng DESC

-- After: expand JSON inline, no separate table needed
SELECT pb.uri, pb.author_handle, (pb.likes + pb.reposts + pb.replies) AS eng
FROM topic_tokens tt, json_each(tt.tokens) je
JOIN post_buffer pb ON tt.post_uri = pb.uri
WHERE je.value IN (?, ?, ...)
  AND tt.created_at >= ?
  AND pb.author_handle != ''
GROUP BY pb.uri
ORDER BY COUNT(DISTINCT je.value) DESC, eng DESC
```

This query runs on the read connection and only executes during trending post hydration (every 6 hours). The `json_each()` approach scans more data at query time but eliminates all write amplification from the ingest path. Given the 26-hour retention window (~2M tokens at current volume), the scan completes in milliseconds.

### 2. Startup maintenance

`RunStartupMaintenance()` in `store.go` runs once on process start, before the firehose connects:

1. **WAL checkpoint TRUNCATE** — forces all WAL content into the main database file and resets the WAL to zero bytes. This runs **first**, before any heavy deletes, because a bloated WAL makes subsequent writes glacially slow. At startup no other goroutines are running, so the exclusive lock always succeeds. (This ordering was changed after a Feb 2025 incident where a 405MB WAL made the subsequent purge DELETEs take minutes instead of milliseconds.)

2. **DROP and recreate `token_postings`** — clears the bloated table. The schema is preserved in `migrate()` for backwards compatibility, but the table is emptied on every deploy. This took ~22 minutes on the first run against the 3.3GB staging database; future runs are near-instant since the table is always empty.

3. **Purge stale `post_buffer` rows** (older than 3 hours) and **stale `topic_tokens`** (older than 26 hours). This cleans up data that accumulated while the process was down.

### 3. Periodic WAL checkpoints (with pressure-based escalation)

A `maintDB` connection pool (1 connection, 1-second busy_timeout) runs `PRAGMA wal_checkpoint(PASSIVE)` every 5 minutes from the main scheduler loop. PASSIVE mode checkpoints whatever WAL frames it can without blocking writers — if the writer is mid-transaction, it simply does nothing and tries again in 5 minutes.

Using a separate connection pool with a short timeout means the checkpoint never contends with the write path. The write pool has a 30-second busy_timeout; the maintenance pool has 1 second and silently gives up if it can't acquire the lock.

**Pressure-based escalation:** If the WAL file exceeds a configurable threshold (default 50MB, set via `WAL_CHECKPOINT_THRESHOLD_MB`), `RunWALCheckpoint()` escalates from PASSIVE on `maintDB` to TRUNCATE on `writeDB`. The `writeDB` has a 30-second busy_timeout, so TRUNCATE can wait for active readers to finish. This prevents unbounded WAL growth during long analysis cycles where PASSIVE checkpoints silently fail for extended periods.

The escalation decision uses `os.Stat()` on the WAL file (not a PRAGMA) because file size is immediately available without acquiring a database lock. The function returns a `WALCheckpointResult` struct (`Escalated`, `Completed`, `WALBefore`, `WALAfter`) that the caller uses to log `wal_pressure_checkpoint` events for operational visibility.

**Why this matters:** During long analysis cycles (10+ minutes), all `readDB` connections may hold read transactions that prevent PASSIVE checkpoints from making progress. Every 5-minute PASSIVE attempt silently does nothing. The WAL grows by ~2MB/minute under normal firehose load, so a 30-minute analysis cycle can add 60MB+ to the WAL. Without pressure escalation, back-to-back long cycles can push the WAL past 400MB, degrading write performance and risking disk exhaustion.

### 4. Larger, less frequent batches

The write flusher was changed from 150 posts every 500ms to 1,500 posts every 2 seconds. Fewer transactions means fewer `BEGIN`/`COMMIT` cycles, which is where most of the pure-Go SQLite overhead lives. The channel buffer (50K) can absorb 4 seconds of firehose traffic at peak volume, so the 2-second flush interval has plenty of headroom.

## Architecture After the Fix

```
Jetstream WebSocket (~1,500 English posts/min)
        |
        v
  [Consumer goroutine]
  Filter: English, post creates only
  Tokenize root posts (if trending enabled)
        |
        | PendingWrite{Post, TokensJSON}
        v
  [50K buffered channel]
        |
        v
  [Write flusher goroutine]           [Main scheduler goroutine]
  Every 2s or 1500 posts:             Every 5 min:
    BEGIN                               IF wal < threshold:
      INSERT post_buffer (batch)          maintDB: PRAGMA wal_checkpoint(PASSIVE)
    COMMIT                              ELSE (pressure escalation):
    BEGIN                                 writeDB: PRAGMA wal_checkpoint(TRUNCATE)
      INSERT topic_tokens (batch)         log wal_pressure_checkpoint event
    COMMIT
                                      On startup (in this order):
                                        1. writeDB: PRAGMA wal_checkpoint(TRUNCATE)
                                        2. writeDB: DROP/recreate token_postings
                                        3. writeDB: purge stale rows

  Read connections (up to 4 concurrent):
    - Analysis cycle: read post_buffer
    - Topic analysis: read topic_tokens
    - Exemplar hydration: json_each(topic_tokens.tokens) JOIN post_buffer
    - Stats API: read various tables
```

### Connection pools

| Pool | Max conns | Busy timeout | Purpose |
|------|-----------|--------------|---------|
| writeDB | 1 | 30s | All writes (post_buffer, topic_tokens, runs, etc.) |
| readDB | 4 | 30s | All reads (analysis, topics, stats API). `query_only=ON` pragma. |
| maintDB | 1 | 1s | WAL checkpoints only. Short timeout so it never blocks writers. |

### Tables on the write hot path

| Table | Rows per flush | Indexes | Notes |
|-------|---------------|---------|-------|
| post_buffer | ≤1500 | 1 (PK: uri) | UPSERT via `ON CONFLICT` |
| topic_tokens | ≤1500 (root posts only) | 1 (PK: post_uri) | INSERT OR IGNORE |
| token_postings | **0** | — | No longer written during ingest |

## Files Changed

| File | What changed |
|------|-------------|
| `internal/store/store.go` | Added `maintDB` pool, `RunStartupMaintenance()`, `RunWALCheckpoint()` with pressure-based escalation (`WALCheckpointResult`), `walFileSize()` helper |
| `internal/store/write_batch.go` | Removed `token_postings` INSERTs from `FlushTokenBatch()` |
| `internal/store/topic_store.go` | Rewrote `GetExemplarCandidates()` to use `json_each()`. Removed `token_postings` writes from `InsertTopicTokens()` and purges from `PurgeTopicTokens()` |
| `cmd/hourstats/main.go` | Added `RunStartupMaintenance()` call, WAL checkpoint ticker with pressure threshold (`WAL_CHECKPOINT_THRESHOLD_MB`), `wal_pressure_checkpoint` event logging, changed batch 150/500ms → 1500/2s |
| `internal/store/write_batch_test.go` | Updated to expect 0 `token_postings` rows |
| `internal/store/topic_store_test.go` | Updated test names and assertions for new behaviour |

## Next Steps: Staying Ahead of Firehose Growth

Bluesky is growing. The network currently delivers roughly 1,500 English posts per minute through Jetstream. That number will increase as the platform scales. Here's what to monitor and what to do at each threshold.

### Current headroom

With 1,500-post batches flushing in 250–1,200ms and a 50K channel buffer, the system can absorb roughly 25,000 posts per 2-second window before the buffer fills. At 1,500 posts/min (25 posts/sec), we're using about 2% of buffer capacity. The write path itself can sustain ~750 posts/second before flush times exceed the 2-second interval.

### Monitor these signals

1. **`slow write flush` log warnings** — if flush times consistently exceed 1 second at batch_size=1500, the write path is approaching saturation
2. **`dropped_posts` in stats snapshots** — any non-zero value means the channel buffer is filling faster than the flusher can drain it
3. **WAL file size** — should stay under 100MB between checkpoints. The system auto-escalates to TRUNCATE when the WAL exceeds `WAL_CHECKPOINT_THRESHOLD_MB` (default 50MB), logging a `wal_pressure_checkpoint` event. Check recent events via `hourstats-stats summary`. If the WAL grows persistently despite escalation, readers may be holding transactions open longer than the 30-second writeDB timeout
4. **Disk usage** — the 5GB volume is 89% full after the fix. The freed pages from DROP TABLE are available as SQLite free pages but the OS still sees the 3.3GB file. VACUUM would reclaim them but requires ~3.3GB of temporary disk space
5. **post_buffer row count** — currently ~40K rows at any time (2-hour retention). If this grows significantly, post writes are falling behind purges

### Tier 1: Tune existing architecture (up to ~5K English posts/min)

These changes require no architectural rework:

**~~Increase batch size to 1,000–2,000.~~** Done — batch size increased to 1,500. Further increases beyond 2,000 should be tested on staging first — there's a point of diminishing returns where transaction size starts increasing WAL checkpoint time.

**Extend the volume to 10GB.** Run `fly volumes extend vol_XYZ --size 10 -a hourstats-staging`. This gives room for a VACUUM to reclaim the 580MB of free pages and reduces disk pressure. Cost is minimal on Fly.io.

**Switch to `performance-1x` VM.** The shared-cpu-1x gives fractional CPU and shared IOPS. A dedicated CPU (`performance-1x`, 1 vCPU, 2GB RAM, ~$29/month) provides consistent I/O throughput. This is likely the single highest-impact change for write performance.

**Add VACUUM to startup maintenance** (once disk headroom exists). After extending the volume, add `VACUUM` after the WAL checkpoint in `RunStartupMaintenance()`. This compacts the database file and reclaims free pages to the OS. Only safe when disk has ≥1× the DB size in free space.

### Tier 2: Optimise the storage layer (up to ~15K English posts/min)

**Switch to cgo-sqlite (mattn/go-sqlite3 or ncruces/go-sqlite3).** The pure-Go modernc.org/sqlite driver has measurably higher per-statement overhead than cgo drivers. For I/O-bound workloads with many Exec calls per transaction, cgo can provide 2–5× throughput improvement. This requires adding CGO_ENABLED=1 to the Docker build (use Alpine's musl-dev package). The ncruces/go-sqlite3 driver is a good middle ground — it embeds a WASM SQLite build and has lower overhead than modernc without needing full cgo.

**Batch INSERTs with multi-row VALUES.** Instead of preparing a statement and calling Exec 1,500 times, build a single `INSERT INTO post_buffer VALUES (?, ?, ...), (?, ?, ...), ...` with all rows in one statement. This reduces the Go↔SQLite boundary crossings from 1,500 to 1 per transaction. Combined with cgo, this can push throughput to 5,000+ rows/second.

**Reduce `post_buffer` retention.** The current 2-hour retention window means ~200K rows at 1,500 posts/min. If firehose volume triples, that's 600K rows. Reducing retention to 45 minutes (only needs to cover one analysis cycle plus buffer) reduces the table size and speeds up purges.

**Add partial indexes.** `post_buffer` currently has only a PK index. As the table grows, the analysis query (`SELECT ... WHERE inserted_at >= ? AND is_reply = 0`) benefits from a partial index: `CREATE INDEX idx_pb_root_recent ON post_buffer(inserted_at) WHERE is_reply = 0`. This narrows the scan for engagement hydration.

### Tier 3: Architectural changes (beyond ~15K English posts/min)

At extreme volumes, SQLite on a single persistent volume hits fundamental limits: single-writer serialisation and IOPS caps on network-attached storage.

**Write-ahead buffering with a ring file.** Instead of writing every post to SQLite immediately, append to a memory-mapped ring buffer file and batch-flush to SQLite on a longer interval (10–30 seconds). The ring buffer absorbs bursts while SQLite handles steady-state writes. On crash, replay unflushed entries from the ring file.

**Shard by time window.** Use separate SQLite databases for each 30-minute analysis window. The active window receives all writes; completed windows are read-only. This eliminates write/read contention entirely and allows each window to be VACUUM'd independently. The scheduler opens the current window's DB, runs analysis on the previous window's DB, and deletes old windows.

**Move to a client/server database.** If the firehose exceeds what a single machine can write to local storage, the architecture needs a networked database (Postgres on Fly.io, Turso/libSQL, or similar). This is a last resort — it changes the cost model from $5/month to $30+/month and adds network latency to every write.

### What NOT to do

- **Don't add `auto_vacuum`.** It runs on every transaction and adds overhead proportional to the number of freed pages. Manual VACUUM on startup is much cheaper.
- **Don't shard by table.** Splitting post_buffer across multiple tables (e.g., by hour) adds complexity to every query without addressing the core write throughput problem.
- **Don't move tokenisation out of the consumer goroutine.** The `topics.Tokenize()` call adds ~50μs per post. Moving it to a separate goroutine would add channel overhead that exceeds the tokenisation cost.
- **Don't increase the channel buffer beyond 50K.** At 500 bytes per PendingWrite, 50K entries is ~25MB of memory. Doubling it masks the symptom (dropped posts) without fixing the cause (slow writes). If the buffer fills, the write path needs to be faster, not the buffer bigger.
