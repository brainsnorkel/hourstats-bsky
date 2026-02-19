## ADDED Requirements

### Requirement: Health history API endpoint
The system SHALL expose a `GET /stats/health/history` endpoint on the existing stats API server (port 9111). The endpoint SHALL accept a `hours` query parameter (integer, default 24, minimum 1, maximum 48) controlling the time window. The response SHALL be a JSON array of objects ordered chronologically (oldest first), each containing: `snapshot_time`, `heap_inuse_bytes`, `heap_sys_bytes`, `sys_bytes`, `gc_pause_total_ns`, `gc_count`, `gc_cpu_fraction`, `slow_flush_count`, `slow_flush_max_ms`, `write_channel_depth`, `wal_size_bytes`, `goroutine_count`, `cycle_duration_ms`, `trending_duration_ms`, `total_firehose_posts`, `english_posts_stored`, and `dropped_posts`. The endpoint SHALL return HTTP 200 with Content-Type `application/json`.

#### Scenario: Default 24-hour health history
- **WHEN** a client sends `GET /stats/health/history` with no query parameters
- **THEN** the system returns a JSON array of health snapshots from the last 24 hours, ordered oldest-first

#### Scenario: Custom time window
- **WHEN** a client sends `GET /stats/health/history?hours=6`
- **THEN** the system returns health snapshots from the last 6 hours only

#### Scenario: Time window exceeds maximum
- **WHEN** a client sends `GET /stats/health/history?hours=100`
- **THEN** the system clamps the window to 48 hours and returns data for the last 48 hours

#### Scenario: No data available
- **WHEN** the `stats_snapshots` table has no rows in the requested time window
- **THEN** the system returns HTTP 200 with an empty JSON array `[]`

### Requirement: Health chart API endpoint
The system SHALL expose a `GET /stats/health/chart` endpoint on the existing stats API server. The endpoint SHALL accept a `hours` query parameter (integer, default from `HEALTH_CHART_HOURS` env var or 24, minimum 1, maximum 48). The response SHALL be a PNG image (Content-Type `image/png`) rendered by the health chart generator. The endpoint SHALL return HTTP 200 on success, HTTP 500 with a JSON error body if chart generation fails, and HTTP 400 if the `hours` parameter is not a valid integer.

#### Scenario: Default health chart
- **WHEN** a client sends `GET /stats/health/chart` with no query parameters
- **THEN** the system renders a multi-panel PNG chart for the default time window and returns it with Content-Type image/png

#### Scenario: Custom time window chart
- **WHEN** a client sends `GET /stats/health/chart?hours=12`
- **THEN** the system renders a chart covering the last 12 hours of health data

#### Scenario: Chart generation with insufficient data
- **WHEN** fewer than 2 health snapshots exist in the requested time window
- **THEN** the system returns HTTP 200 with a PNG containing a "Not enough data" message (matching the existing sparkline pattern for insufficient data)

#### Scenario: Invalid hours parameter
- **WHEN** a client sends `GET /stats/health/chart?hours=abc`
- **THEN** the system returns HTTP 400 with a JSON error body

### Requirement: Multi-panel health chart renderer
The system SHALL implement a `GenerateHealthChart(snapshots []StatsSnapshot, config HealthChartConfig) ([]byte, error)` function in a new `health_chart_generator.go` file within `internal/sparkline/`. The chart SHALL be a 1200x1600 pixel PNG (portrait orientation) containing 4 stacked panels, each 1200x350 pixels with 50-pixel vertical gaps. The panels SHALL be:

1. **Memory + GC panel**: HeapInuse and Sys as filled area plots (left Y-axis, bytes with auto-scaled units KB/MB/GB), GCCPUFraction as a line plot (right Y-axis, percentage). A horizontal dashed line SHALL indicate the VM memory limit if `HealthChartConfig.MemoryLimitMB` is set.
2. **I/O Pressure panel**: slow_flush_count as bar segments (left Y-axis, count), write_channel_depth as a line plot (right Y-axis, 0-50,000 range).
3. **Firehose Volume panel**: total_firehose_posts as a line plot (left Y-axis), dropped_posts as red bar segments (right Y-axis or overlay).
4. **WAL + Timing panel**: wal_size_bytes as filled area plot (left Y-axis, bytes with auto-scaled units), cycle_duration_ms as a line plot (right Y-axis, seconds).

The X-axis SHALL show time labels using the existing day marker and hour marker patterns from `generator.go`. The colour palette SHALL use the existing Okabe-Ito colour-blind safe colours from `DefaultConfig()`. Each panel SHALL have a title label.

#### Scenario: Full 24-hour chart with all metrics
- **WHEN** `GenerateHealthChart` is called with 48 snapshots spanning 24 hours and all health fields populated
- **THEN** the function returns a valid PNG byte slice with 4 visible panels, each showing plotted data with labelled axes

#### Scenario: Chart with memory limit line
- **WHEN** `HealthChartConfig.MemoryLimitMB` is set to 1024
- **THEN** the Memory + GC panel includes a horizontal dashed red line at the 1024MB mark on the left Y-axis

#### Scenario: Chart with zero slow flushes
- **WHEN** all snapshots have slow_flush_count=0 and slow_flush_max_ms=0
- **THEN** the I/O Pressure panel renders with an empty bar area and the write_channel_depth line is still visible

#### Scenario: Chart with pre-deployment snapshots
- **WHEN** the first N snapshots have all health columns at 0 (pre-deployment) and remaining snapshots have real data
- **THEN** the chart renders the 0-value region as flat lines at the bottom and transitions to real data without visual artefacts

### Requirement: HealthChartConfig type
The system SHALL define a `HealthChartConfig` struct with the following fields: `Hours` (int, time window in hours), `MemoryLimitMB` (int, VM memory limit for reference line, 0 means no line), `Width` (int, default 1200), `Height` (int, default 1600). A `DefaultHealthChartConfig()` function SHALL return the default configuration with Hours=24, MemoryLimitMB=0, Width=1200, Height=1600.

#### Scenario: Default config values
- **WHEN** `DefaultHealthChartConfig()` is called
- **THEN** the returned config has Hours=24, MemoryLimitMB=0, Width=1200, Height=1600

### Requirement: StatsStore interface extension
The system SHALL add a `GetHealthHistory(ctx context.Context, since time.Time, limit int) ([]StatsSnapshot, error)` method to the `StatsStore` interface in `internal/statsapi/server.go`. The implementation in `internal/store/` SHALL query `stats_snapshots` for all columns (existing + new health columns) where `snapshot_time >= since`, ordered by `snapshot_time ASC`, limited to `limit` rows. This method is used by both the `/stats/health/history` and `/stats/health/chart` endpoints.

#### Scenario: Health history query returns chronological data
- **WHEN** `GetHealthHistory` is called with since=24h ago and limit=200
- **THEN** the method returns snapshots ordered oldest-first with all health fields populated

#### Scenario: Health history respects time filter
- **WHEN** `GetHealthHistory` is called with since=6h ago
- **THEN** only snapshots from the last 6 hours are returned

### Requirement: CLI plot command
The system SHALL add a `plot` subcommand to the `hourstats-stats` CLI tool. The command SHALL accept a `--hours` flag (integer, default 24) and a `--endpoint` flag (string, default from environment or `http://localhost:9111`). The command SHALL fetch data from the `/stats/health/history` API endpoint and render ASCII sparklines in the terminal for each metric group. Each metric SHALL be displayed as a single-line Unicode braille sparkline (60 characters wide) with the metric name (left-aligned, 20 chars), the sparkline, and min/max/current annotations. The metrics displayed SHALL be: HeapInuse (as MB), GC CPU Fraction (as %), WAL Size (as MB), Write Channel Depth, Slow Flush Count, Cycle Duration (as seconds), Firehose Posts, and Dropped Posts.

#### Scenario: Default plot output
- **WHEN** `hourstats-stats plot` is executed with no flags
- **THEN** the CLI fetches 24 hours of health history from the API and renders 8 ASCII sparkline rows to stdout

#### Scenario: Custom time window
- **WHEN** `hourstats-stats plot --hours 6` is executed
- **THEN** the CLI fetches 6 hours of data and renders sparklines for that window

#### Scenario: API unreachable
- **WHEN** the stats API is not running at the configured endpoint
- **THEN** the CLI prints an error message to stderr and exits with code 1

#### Scenario: No data in time window
- **WHEN** the API returns an empty array for the requested time window
- **THEN** the CLI prints "No health data available for the last N hours" and exits with code 0
