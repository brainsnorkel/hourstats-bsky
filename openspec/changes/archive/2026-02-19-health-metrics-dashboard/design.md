## Context

The bot runs on Fly.io as a single Go binary (shared-cpu-1x, 1024MB RAM after a recent bump from 512MB) consuming the Bluesky Jetstream firehose at ~3,944 posts/min. On Feb 19, 2026, the production VM was OOM-killed (`exit_code=137, oom_killed=true`). Logs showed "slow write flush" warnings with durations up to 22 seconds, batch sizes of 503 posts, and a trending analysis cycle that ran for 1,801 seconds (30 minutes).

The existing stats infrastructure (`internal/stats/collector.go`) takes snapshots every 30 minutes using atomic counters, stored in the `stats_snapshots` SQLite table. It tracks firehose volume, English post counts, consumer errors, and analysis results — but nothing about memory, GC, I/O pressure, WAL growth, or analysis cycle duration. The `/stats/health` API endpoint (`internal/statsapi/server.go`) reads point-in-time database health (WAL size, page count, freelist) but does not store time-series data. The CLI tool (`cmd/hourstats-stats`) has summary, latest, history, events, topics, and health commands but no charting capability.

The write path uses a buffered channel (`make(chan store.PendingWrite, 50000)`) with a dedicated writer goroutine that flushes in batches. When the channel is full, posts are dropped and counted. Slow flushes (>1s) are logged but not exposed to the stats collector. Analysis cycle durations are logged via slog but not persisted.

## Goals / Non-Goals

**Goals:**
- Persist system-level metrics (memory, GC, I/O, WAL, runtime, cycle timing) as new columns on the existing `stats_snapshots` table, sampled at each snapshot interval
- Expose write-path health signals (slow flush count/max, channel depth) from the writer goroutine to the stats collector
- Add API endpoints for time-series health data (JSON) and multi-panel PNG charts
- Add a CLI `plot` command for terminal-based ASCII health charts
- Enable operators to spot OOM trajectories, WAL bloat, and I/O degradation from historical data rather than log forensics

**Non-Goals:**
- Real-time alerting or threshold-based notifications (operators use Fly.io metrics for that)
- Replacing Fly.io's built-in machine metrics dashboard
- Per-goroutine memory profiling or pprof integration
- Changing the snapshot interval frequency (remains aligned with analysis cycle)
- Exposing health charts on Bluesky (these are operational-only, not posted to the feed)
- Historical backfill of metrics for periods before this feature is deployed

## Decisions

### 1. Extend existing table vs new table for health metrics

**Decision**: Add 13 new columns to `stats_snapshots` via `ALTER TABLE ADD COLUMN` migration statements.

Health metrics are point-in-time observations taken at the same instant as existing stats. They belong to the same logical snapshot — a separate table would require joining on timestamp and complicate the existing `GetLatestSnapshot`/`GetSnapshotHistory` queries. The migration pattern is established: `dropped_posts` was added this way (store.go line 410), and the `migrate()` function silently skips "duplicate column" errors, making the ALTER statements idempotent.

All new columns use `INTEGER DEFAULT 0` or `REAL DEFAULT 0` — fully backward-compatible. Old snapshots simply have 0 values for the new columns, which is distinguishable from "not collected" only by timestamp (pre-deployment rows). This is acceptable for operational dashboards.

**Alternatives considered**: Separate `health_snapshots` table with foreign key to `stats_snapshots` — rejected because it doubles query complexity for the common case (viewing a snapshot with its health data) and the existing table has only ~4,300 rows per 90-day retention period (one row per 30 minutes). Column count is not a concern at this scale.

### 2. Memory metrics: which runtime.MemStats fields

**Decision**: Capture `HeapInuse`, `HeapSys`, `Sys`, `PauseTotalNs` (delta), `NumGC` (delta), and `GCCPUFraction`.

- `HeapInuse` — actual heap memory in use. The primary OOM indicator.
- `HeapSys` — memory obtained from the OS for heap. Shows Go's memory reservation behaviour.
- `Sys` — total bytes obtained from the OS (heap + stack + mmap). Closest to what Fly.io sees as process RSS.
- `PauseTotalNs` (stored as delta from last snapshot) — GC pause time per interval. High values indicate GC thrashing.
- `NumGC` (stored as delta from last snapshot) — GC cycle count per interval. Combined with pause total, gives avg pause per GC.
- `GCCPUFraction` — fraction of CPU spent in GC. A single float that summarises GC pressure.

The delta computation for `PauseTotalNs` and `NumGC` follows the existing pattern in the stats collector where cumulative counters (e.g., `TotalFirehosePosts`) are differenced between snapshots. The collector stores the previous cumulative values in atomic fields and subtracts on each `TakeSnapshot()`.

**Alternatives considered**: `HeapAlloc` instead of `HeapInuse` — rejected because `HeapInuse` better represents actual memory pressure (includes allocated + freed-but-not-returned spans). `HeapObjects` count — rejected as too granular for operational dashboards; GC metrics cover the same concern. `runtime.ReadMemStats()` has a stop-the-world cost (~100µs) — acceptable at 30-minute intervals.

### 3. Writer flush metrics: callback vs atomic counters

**Decision**: Add atomic counters to the stats collector (`SlowFlushCount`, `SlowFlushMaxMs`) and have the writer goroutine increment them directly. The collector reads and resets these on each `TakeSnapshot()`.

The writer goroutine already detects slow flushes (>1s threshold) and logs them. Adding two atomic operations per slow flush is negligible overhead. The `SampleWriteChannelDepth(ch chan store.PendingWrite)` method reads `len(ch)` at snapshot time — a point-in-time sample that's cheap and sufficient for trend analysis.

The writer goroutine needs a reference to the stats collector, which is passed during initialization in `main.go`. This follows the existing pattern where `jetstream.Consumer` receives the collector.

**Alternatives considered**: Channel-based reporting from writer to collector — rejected for unnecessary complexity; the writer already has access to the collector via the same main() scope. Periodic sampling goroutine for channel depth — rejected; sampling at snapshot time (every 30 min) is sufficient and avoids a new goroutine.

### 4. Cycle duration tracking: slog parsing vs direct instrumentation

**Decision**: Instrument `runAnalysisCycle()` and the trending analysis function to call `collector.RecordCycleDuration(totalMs, trendingMs)` before returning. The collector stores these as simple int64 fields (not atomic — only one goroutine writes them, and they're read once at snapshot time).

Currently, cycle durations are logged as `slog.Info("analysis cycle complete", "cycle_elapsed", elapsed)`. Parsing logs for metrics is fragile and loses data on restart. Direct instrumentation adds 2 lines of code to existing functions.

**Alternatives considered**: Log parsing/scraping — rejected for fragility. Prometheus metrics export — rejected as overkill for a single-binary system with SQLite storage. The internal stats pipeline already solves this problem.

### 5. WAL size: os.Stat vs PRAGMA

**Decision**: Use `os.Stat(dbPath + "-wal")` to read WAL file size in bytes.

The existing `GetDatabaseHealth()` in `internal/store/health.go` uses `PRAGMA wal_checkpoint(PASSIVE)` which returns page counts. File size in bytes is more intuitive for operators and directly comparable to disk limits. The WAL file path is deterministic (`{db_path}-wal`). If the file doesn't exist (WAL fully checkpointed), size is reported as 0.

**Alternatives considered**: `PRAGMA wal_checkpoint` page count × page size — rejected because it requires an extra PRAGMA call and the math is error-prone. Storing both file size and page count — rejected as redundant.

### 6. Chart rendering: multi-panel PNG layout

**Decision**: A new `health_chart_generator.go` in `internal/sparkline/` renders a 1200×1600 PNG (portrait, same width as existing charts) with 4 stacked panels:

1. **Memory + GC** — Dual Y-axis: HeapInuse/Sys as area fill (left axis, bytes), GC CPU fraction as line (right axis, %). Horizontal dashed line at VM memory limit (from `HEALTH_CHART_MEMORY_LIMIT_MB` env var, default 1024).
2. **I/O Pressure** — Dual Y-axis: slow flush count as bars (left axis), write channel depth as line (right axis, out of 50,000).
3. **Firehose Volume** — Line chart of `TotalFirehosePosts` per interval with `DroppedPosts` as red bars.
4. **WAL + Timing** — Dual Y-axis: WAL size in MB (left axis, area fill), cycle duration in seconds (right axis, line).

Each panel is 1200×350 with 50px gaps. The existing `DefaultConfig()` colour palette (Okabe-Ito, colour-blind safe) is reused. Time axis labels use the existing `drawDayMarkers`/`drawHourMarkers` helpers adapted for configurable time windows. The `niceRange()` axis helper from `axis.go` handles Y-axis scaling.

The generator accepts `[]StatsSnapshot` (the same type returned by `GetSnapshotHistory`) and a `HealthChartConfig` struct with time window, memory limit, and panel toggles.

**Alternatives considered**: Single-panel with all metrics — rejected as unreadable with 8+ data series. Separate PNG per metric group — rejected because a single image is easier to serve and scan. SVG output — rejected because the existing pipeline is PNG-only and Bluesky requires raster images.

### 7. CLI ASCII charts: braille-block sparklines

**Decision**: The `hourstats-stats plot` command renders ASCII sparklines using Unicode braille characters (⠁⠂⠄⡀⠈⠐⠠⢀) for terminal display. Each metric group gets a 60-character-wide single-line sparkline with min/max/current annotations.

Output format:
```
Memory (HeapInuse)  ⣀⣤⣶⣿⣿⣷⣦⣄  142MB → 298MB (cur: 256MB)
GC CPU Fraction     ⠉⠉⠒⠒⠤⠤⣀⣀  0.2% → 1.8% (cur: 0.9%)
WAL Size            ⣀⣀⣤⣤⣶⣿⣷⣦  2MB → 48MB (cur: 12MB)
Channel Depth       ⠉⠉⠉⠒⠤⣀⣀⠤  0 → 12,400 (cur: 340)
Slow Flushes        ⠉⠉⠉⠉⠉⣀⣤⣶  0 → 14 (cur: 3)
```

This requires no external dependencies — just Unicode string building from the snapshot data. The CLI fetches data from the stats API (`/stats/health/history`), not directly from SQLite.

**Alternatives considered**: Full TUI dashboard with ncurses — rejected as overkill for a monitoring tool. Table-only output — rejected because trends are invisible in tabular data. Importing a charting library — rejected to avoid new dependencies.

### 8. API endpoints: two new routes

**Decision**: Add to the existing stats API server (port 9111):

- `GET /stats/health/history?hours=24` — Returns JSON array of health-relevant fields from `stats_snapshots`, ordered chronologically. Query parameter `hours` (default 24, max 48) controls the time window. Response includes all 13 new columns plus `snapshot_time`, `total_firehose_posts`, `english_posts_stored`, and `dropped_posts` for the firehose panel.

- `GET /stats/health/chart?hours=24&format=png` — Returns a PNG image (Content-Type: image/png) rendered by the health chart generator. Query parameter `hours` controls the time window. The `format` parameter is reserved for future SVG support but only `png` is implemented.

Both endpoints use the existing `StatsStore` interface pattern. A new `GetHealthHistory(ctx, since, limit)` method is added to the interface, implemented by the Store.

**Alternatives considered**: WebSocket streaming — rejected; polling every 30 minutes is sufficient and simpler. Separate health API server on a different port — rejected for consistency with existing stats API.

## Risks / Trade-offs

**[Risk] runtime.ReadMemStats() stop-the-world pause** → At 30-minute intervals, the ~100µs pause is negligible. If snapshot frequency ever increases to sub-second, this would need revisiting. Mitigation: the call is made inside `TakeSnapshot()` which already runs in a non-critical path.

**[Risk] Writer goroutine contention from atomic counter updates** → Each slow flush adds 2 atomic operations. At worst case (503-post batches every few seconds during overload), this is <1µs overhead. Mitigation: atomic operations are lock-free; no mutex contention possible.

**[Risk] Chart generation memory spike for large time windows** → A 48-hour window at 30-min intervals = 96 data points. The 1200×1600 PNG uses ~7.3MB of pixel buffer (`gg.NewContext`). Mitigation: this is a transient allocation freed after encoding; the existing sparkline generator uses the same pattern at 1200×800 without issues.

**[Risk] Schema migration on existing production database** → `ALTER TABLE ADD COLUMN` in SQLite does not rewrite the table — it's a metadata-only operation. The existing `migrate()` function handles "duplicate column" errors gracefully. Mitigation: the migration is tested by the fact that every restart runs `migrate()` idempotently.

**[Risk] Old snapshots show 0 for new columns** → Pre-deployment rows have `DEFAULT 0` for all new columns. Charts may show a flat line at 0 transitioning to real data. Mitigation: the chart generator can optionally skip data points where all health columns are 0 (indicating pre-deployment), or start the chart from the first non-zero health snapshot.

**[Trade-off] Point-in-time channel depth vs continuous monitoring** → `len(writeCh)` at snapshot time may miss transient spikes between snapshots. Acceptable for 30-minute intervals — we're looking for sustained pressure trends, not sub-second spikes. The slow flush counter captures the consequence of channel pressure.

**[Trade-off] No goroutine-level memory breakdown** → `runtime.MemStats` gives process-level totals, not per-goroutine attribution. Acceptable for operational dashboards — if we need per-goroutine profiling, that's a pprof task, not a stats dashboard concern.

**[Trade-off] GC delta computation complexity** → Storing deltas requires the collector to maintain previous cumulative values across snapshots. This adds 2 atomic fields but matches the existing pattern for firehose post counting. The alternative (storing cumulative values and computing deltas in queries) would complicate the chart generator and CLI.
