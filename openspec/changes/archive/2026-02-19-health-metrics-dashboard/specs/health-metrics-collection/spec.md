## ADDED Requirements

### Requirement: Memory metrics sampling
The system SHALL call `runtime.ReadMemStats()` during each `TakeSnapshot()` invocation and persist the following fields to the `stats_snapshots` table: `heap_inuse_bytes` (MemStats.HeapInuse), `heap_sys_bytes` (MemStats.HeapSys), and `sys_bytes` (MemStats.Sys). All values SHALL be stored as INTEGER (bytes).

#### Scenario: Memory metrics captured at snapshot time
- **WHEN** the stats collector executes `TakeSnapshot()` at the 30-minute analysis interval
- **THEN** the system reads `runtime.MemStats` and stores HeapInuse, HeapSys, and Sys as integer byte values in the corresponding columns of the new `stats_snapshots` row

#### Scenario: Memory metrics are non-negative
- **WHEN** `runtime.ReadMemStats()` is called
- **THEN** all three memory fields SHALL be >= 0 (guaranteed by the runtime, but the schema uses INTEGER DEFAULT 0)

### Requirement: GC pressure metrics sampling
The system SHALL compute per-interval GC deltas and persist them during each `TakeSnapshot()` invocation. The collector SHALL maintain the previous cumulative values of `runtime.MemStats.PauseTotalNs` and `runtime.MemStats.NumGC` as atomic fields. At each snapshot, the system SHALL store: `gc_pause_total_ns` (delta of PauseTotalNs since last snapshot, INTEGER), `gc_count` (delta of NumGC since last snapshot, INTEGER), and `gc_cpu_fraction` (MemStats.GCCPUFraction, REAL). On the first snapshot after startup (no previous values), deltas SHALL be stored as 0.

#### Scenario: GC deltas computed between consecutive snapshots
- **WHEN** two consecutive snapshots are taken 30 minutes apart
- **AND** PauseTotalNs increased from 500,000,000 to 750,000,000 between snapshots
- **AND** NumGC increased from 100 to 115
- **THEN** the second snapshot stores gc_pause_total_ns=250,000,000, gc_count=15, and gc_cpu_fraction as the current GCCPUFraction value

#### Scenario: First snapshot after startup
- **WHEN** the first `TakeSnapshot()` runs after the process starts
- **AND** no previous cumulative GC values exist
- **THEN** gc_pause_total_ns and gc_count SHALL be stored as 0, and gc_cpu_fraction SHALL reflect the current GCCPUFraction value

### Requirement: Write path I/O pressure metrics
The system SHALL expose slow write flush statistics from the writer goroutine to the stats collector via atomic counters. The writer goroutine SHALL call `collector.IncrementSlowFlush(durationMs)` each time a batch flush exceeds 1 second. This method SHALL atomically increment the slow flush count and update the max flush duration if the new value is larger. At each `TakeSnapshot()`, the system SHALL read and reset these counters, storing: `slow_flush_count` (INTEGER, number of slow flushes since last snapshot), `slow_flush_max_ms` (INTEGER, maximum flush duration in milliseconds since last snapshot).

#### Scenario: Slow flushes accumulated between snapshots
- **WHEN** the writer goroutine detects 3 slow flushes with durations 1,200ms, 3,500ms, and 2,100ms between two snapshots
- **THEN** the snapshot stores slow_flush_count=3 and slow_flush_max_ms=3500

#### Scenario: No slow flushes in interval
- **WHEN** no batch flushes exceed 1 second between two snapshots
- **THEN** the snapshot stores slow_flush_count=0 and slow_flush_max_ms=0

#### Scenario: Counters reset after snapshot
- **WHEN** a snapshot reads slow_flush_count=5 and slow_flush_max_ms=4200
- **THEN** both counters are atomically reset to 0 for the next interval

### Requirement: Write channel depth sampling
The system SHALL sample the write channel depth at snapshot time by calling `len(writeCh)` on the buffered channel (capacity 50,000). The stats collector SHALL expose a `SampleWriteChannelDepth(depth int)` method called from `TakeSnapshot()`. The value SHALL be persisted as `write_channel_depth` (INTEGER) in the `stats_snapshots` table.

#### Scenario: Channel depth captured at snapshot
- **WHEN** `TakeSnapshot()` runs and the write channel currently holds 12,340 pending writes
- **THEN** the snapshot stores write_channel_depth=12340

#### Scenario: Empty channel
- **WHEN** `TakeSnapshot()` runs and the write channel is empty
- **THEN** the snapshot stores write_channel_depth=0

### Requirement: WAL file size sampling
The system SHALL read the WAL file size in bytes using `os.Stat(dbPath + "-wal")` during each `TakeSnapshot()` invocation. If the WAL file does not exist (fully checkpointed), the size SHALL be reported as 0. The value SHALL be persisted as `wal_size_bytes` (INTEGER) in the `stats_snapshots` table. The database path SHALL be provided to the collector at initialization time.

#### Scenario: WAL file exists with data
- **WHEN** `TakeSnapshot()` runs and the WAL file is 48,234,496 bytes
- **THEN** the snapshot stores wal_size_bytes=48234496

#### Scenario: WAL file does not exist
- **WHEN** `TakeSnapshot()` runs and `os.Stat` returns a "file not found" error for the WAL file
- **THEN** the snapshot stores wal_size_bytes=0

#### Scenario: WAL file stat error (non-ENOENT)
- **WHEN** `os.Stat` fails with a permission or I/O error
- **THEN** the system SHALL log a warning and store wal_size_bytes=0

### Requirement: Goroutine count sampling
The system SHALL call `runtime.NumGoroutine()` during each `TakeSnapshot()` invocation and persist the value as `goroutine_count` (INTEGER) in the `stats_snapshots` table.

#### Scenario: Goroutine count captured
- **WHEN** `TakeSnapshot()` runs and the process has 47 active goroutines
- **THEN** the snapshot stores goroutine_count=47

### Requirement: Analysis cycle duration tracking
The system SHALL persist analysis cycle durations as metrics in the `stats_snapshots` table. The `runAnalysisCycle()` function SHALL call `collector.RecordCycleDuration(totalMs int64)` before returning. The trending analysis function SHALL call `collector.RecordTrendingDuration(trendingMs int64)` before returning. These values SHALL be stored as `cycle_duration_ms` (INTEGER) and `trending_duration_ms` (INTEGER). If no analysis cycle ran during a snapshot interval, both values SHALL be 0.

#### Scenario: Analysis cycle with trending
- **WHEN** an analysis cycle completes in 45,200ms total with trending taking 12,800ms
- **THEN** the next snapshot stores cycle_duration_ms=45200 and trending_duration_ms=12800

#### Scenario: Analysis cycle without trending
- **WHEN** an analysis cycle completes in 8,500ms and trending is disabled
- **THEN** the next snapshot stores cycle_duration_ms=8500 and trending_duration_ms=0

#### Scenario: No analysis cycle in interval
- **WHEN** no analysis cycle ran between two snapshots (e.g., the snapshot interval is shorter than the analysis interval)
- **THEN** the snapshot stores cycle_duration_ms=0 and trending_duration_ms=0

### Requirement: Schema migration for health columns
The system SHALL add 13 new columns to the `stats_snapshots` table via `ALTER TABLE ADD COLUMN` statements in the `migrate()` function. Each column SHALL have a DEFAULT of 0. The columns SHALL be: `heap_inuse_bytes INTEGER DEFAULT 0`, `heap_sys_bytes INTEGER DEFAULT 0`, `sys_bytes INTEGER DEFAULT 0`, `gc_pause_total_ns INTEGER DEFAULT 0`, `gc_count INTEGER DEFAULT 0`, `gc_cpu_fraction REAL DEFAULT 0`, `slow_flush_count INTEGER DEFAULT 0`, `slow_flush_max_ms INTEGER DEFAULT 0`, `write_channel_depth INTEGER DEFAULT 0`, `wal_size_bytes INTEGER DEFAULT 0`, `goroutine_count INTEGER DEFAULT 0`, `cycle_duration_ms INTEGER DEFAULT 0`, `trending_duration_ms INTEGER DEFAULT 0`. The migration SHALL be idempotent — duplicate column errors SHALL be silently ignored, matching the existing migration pattern.

#### Scenario: Fresh database migration
- **WHEN** `migrate()` runs on a database that has the `stats_snapshots` table but none of the new columns
- **THEN** all 13 columns are added with their default values

#### Scenario: Idempotent re-migration
- **WHEN** `migrate()` runs on a database that already has all 13 columns
- **THEN** the "duplicate column" errors are silently ignored and no schema changes occur

#### Scenario: Partial migration recovery
- **WHEN** a previous migration was interrupted after adding 7 of 13 columns
- **THEN** the next `migrate()` call adds the remaining 6 columns and silently skips the 7 that already exist

### Requirement: Extended StatsSnapshot struct
The system SHALL extend the `StatsSnapshot` struct in `internal/store/stats.go` with 13 new fields matching the new columns. The `InsertStatsSnapshot()` method SHALL include all new fields in the INSERT statement. The `GetLatestSnapshot()` and `GetSnapshotHistory()` methods SHALL include all new fields in the SELECT statement. JSON serialization tags SHALL use the column names (e.g., `json:"heap_inuse_bytes"`).

#### Scenario: Snapshot with health data inserted and retrieved
- **WHEN** a `StatsSnapshot` is inserted with heap_inuse_bytes=268435456 and goroutine_count=47
- **AND** `GetLatestSnapshot()` is called
- **THEN** the returned snapshot has heap_inuse_bytes=268435456 and goroutine_count=47

#### Scenario: Legacy snapshot retrieval
- **WHEN** `GetSnapshotHistory()` retrieves a pre-migration snapshot row
- **THEN** all 13 new fields are 0 (from the DEFAULT 0 column definitions)
