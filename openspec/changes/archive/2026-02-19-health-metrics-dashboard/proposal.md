## Why

The OOM kill on prod (Feb 19 — `exit_code=137, oom_killed=true`) highlighted that we have no time-series visibility into memory usage, I/O pressure, or WAL growth. The existing health endpoint (`/stats/health`) is point-in-time only, and the stats CLI (`hourstats-stats health`) shows a single snapshot. When something goes wrong, we're forensically reading log lines for "slow write flush" warnings instead of looking at a chart. Adding per-snapshot system metrics and hourly plots gives us the operational visibility to catch OOM trajectories, WAL bloat, and I/O degradation before they cause outages.

## What Changes

- Extend the stats collector to sample system-level metrics at each snapshot interval:
  - **Memory**: Go heap in-use, heap system, total system bytes, GC pause total, GC count, GC CPU fraction (`runtime.MemStats`)
  - **I/O pressure**: Slow write flush count + max duration per interval, write channel depth at snapshot time
  - **WAL**: WAL file size in bytes
  - **Runtime**: Goroutine count (`runtime.NumGoroutine`)
  - **Buffer**: Post buffer table row count (point-in-time)
- Track analysis cycle duration (total, hydration, trending) as persisted metrics — currently logged but not stored
- Add new columns to the `stats_snapshots` table for these metrics (backward-compatible — new columns with defaults)
- Add a new `/stats/health/history` API endpoint returning time-series health data suitable for plotting
- Add a `hourstats-stats plot` CLI command that renders ASCII sparkline-style charts for memory, GC pressure, I/O pressure, firehose volume, and WAL size over the requested time window
- Add a `/stats/health/chart` API endpoint that returns a PNG chart (reusing the sparkline package's rendering infrastructure)

## Capabilities

### New Capabilities
- `health-metrics-collection`: Sample system metrics at each stats snapshot interval and persist as new columns on the existing `stats_snapshots` table. Metrics fall into five groups:
  - **Memory** — `runtime.MemStats`: HeapInuse, HeapSys, Sys (total process), GC pause total (ns), GC count, GC CPU fraction. Answers "is RAM sufficient?" and "is GC thrashing?"
  - **I/O pressure** — slow write flush count and max flush duration per interval (from writer goroutine), write channel depth at snapshot time (`len(writeCh)` out of 50,000 capacity). Answers "is write throughput the bottleneck?"
  - **WAL** — WAL file size in bytes (from `os.Stat`). Answers "is WAL checkpointing keeping up?"
  - **Runtime** — goroutine count (`runtime.NumGoroutine`). Answers "is there a goroutine leak causing OOM?"
  - **Cycle timing** — analysis cycle total duration, hydration duration, trending duration (ms). Currently logged but not persisted. Answers "is shared-cpu-1x enough?"
- `health-metrics-charting`: Generate multi-panel PNG charts (memory + GC, WAL size, firehose volume + channel depth, write latency) over configurable time windows (1h–48h). Reuse `internal/sparkline` rendering patterns (gg library, axis helpers). Serve via new API endpoint and render in CLI.

### Modified Capabilities
- `operational-controls`: New environment variable `HEALTH_CHART_HOURS` (default: 24) controlling the default chart time window. Stats retention must cover the chart window (currently 48h — sufficient).

## Impact

- **Store layer**: `stats_snapshots` table gains ~13 new columns via `ALTER TABLE ADD COLUMN` migration. All nullable with 0 defaults — fully backward compatible.
  - Memory: `heap_inuse_bytes`, `heap_sys_bytes`, `sys_bytes`, `gc_pause_total_ns`, `gc_count`, `gc_cpu_fraction`
  - I/O: `slow_flush_count`, `slow_flush_max_ms`, `write_channel_depth`
  - WAL: `wal_size_bytes`
  - Runtime: `goroutine_count`
  - Cycle timing: `cycle_duration_ms`, `trending_duration_ms`
- **Stats collector** (`internal/stats/collector.go`): New `SampleSystemMetrics()` method reading `runtime.MemStats` and `runtime.NumGoroutine`. New atomic counters for slow flush count/max duration and write channel depth, fed from the writer goroutine. ~50 lines.
- **Store types** (`internal/store/stats.go`): Extend `StatsSnapshot` struct with new fields. Update INSERT/SELECT queries.
- **Analysis cycle** (`cmd/hourstats/analysis.go`): Record cycle durations to collector instead of just logging them.
- **Stats API** (`internal/statsapi/server.go`): New `GET /stats/health/history` (JSON time-series) and `GET /stats/health/chart` (PNG) endpoints.
- **Sparkline package** (`internal/sparkline/`): New `health_chart_generator.go` — multi-panel line chart renderer. Reuses existing `axis.go` helpers and gg library.
- **CLI** (`cmd/hourstats-stats/main.go`): New `plot` command rendering ASCII charts for terminal display.
- **Writer** (`cmd/hourstats/writer.go`): Expose slow-flush counter and channel depth to the stats collector (currently only logs slow flushes).
- **No new dependencies**: Uses existing `runtime` stdlib and `fogleman/gg` already in go.mod.
- **No breaking changes**: All additions are backward-compatible column defaults and new endpoints.
