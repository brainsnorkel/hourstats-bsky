// Package store provides a SQLite-based storage layer for hourstats,
// replacing the DynamoDB-based internal/state package for the Fly.io deployment.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps separate read, write, and maintenance SQLite connection pools for hourstats.
// The write pool is limited to 1 connection to serialize writes at the Go level,
// while the read pool allows up to 4 concurrent readers via WAL mode.
// The maintenance pool has a short busy timeout for opportunistic WAL checkpoints.
type Store struct {
	readDB  *sql.DB
	writeDB *sql.DB
	maintDB *sql.DB // low-priority maintenance connection for WAL checkpoints
	dbPath  string
}

// Post represents a Bluesky post in the buffer.
type Post struct {
	URI             string
	CID             string
	Text            string
	AuthorDID       string
	AuthorHandle    string
	Likes           int
	Reposts         int
	Replies         int
	Sentiment       string
	EngagementScore float64
	CreatedAt       string
	IsReply         bool
}

// RunState tracks a single 30-minute analysis cycle.
type RunState struct {
	RunID                   string
	Status                  string
	AnalysisIntervalMinutes int
	CutoffTime              time.Time
	TotalPostsRetrieved     int
	OverallSentiment        string
	NetSentimentPercentage  float64
	TopPosts                []Post
	TopPostURI              string
	TopPostCID              string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	TTL                     int64
}

type SentimentDataPoint struct {
	RunID                string
	Timestamp            time.Time
	AverageCompoundScore float64
	NetSentimentPercent  float64
	SentimentCategory    string
	TotalPosts           int
	TotalFirehosePosts   int
	RootSentimentPct     float64
	ReplySentimentPct    float64
	// TopTopic is the rank-1 trending topic label for the cycle, set once
	// topic analysis completes. Empty when trending is disabled or failed.
	TopTopic  string
	CreatedAt time.Time
	TTL       int64
}

// DailySentimentDataPoint aggregates a full day of runs.
type DailySentimentDataPoint struct {
	Date             string
	RunID            string
	AverageSentiment float64
	MinSentiment     float64
	MaxSentiment     float64
	Q1Sentiment      float64
	MedianSentiment  float64
	Q3Sentiment      float64
	TotalRuns        int
	TotalPosts       int
	CreatedAt        time.Time
	TTL              int64
}

// YearlySparklineDataPoint is used by the yearly chart generator.
type YearlySparklineDataPoint struct {
	Date                string
	AverageSentiment    float64
	MinSentiment        float64
	MaxSentiment        float64
	Q1Sentiment         float64
	MedianSentiment     float64
	Q3Sentiment         float64
	Timestamp           time.Time
	NetSentimentPercent float64
}

type DailyPostCount struct {
	Date               time.Time
	Count              int
	TotalFirehosePosts int
}

type WeeklyPostTotal struct {
	WeekStart          time.Time
	Count              int
	TotalFirehosePosts int
}

// envInt reads an integer environment variable, returning fallback if the
// variable is unset or fails to parse as an integer. Invalid values are
// logged as a warning and fall back to the default.
func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid env value, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return n
}

// envString reads a string environment variable, validating it
// case-insensitively against allowed. Returns the matching entry from
// allowed, or fallback if the variable is unset or matches nothing.
func envString(key, fallback string, allowed []string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	for _, a := range allowed {
		if strings.EqualFold(v, a) {
			return a
		}
	}
	slog.Warn("invalid env value, using default", "key", key, "value", v, "default", fallback, "allowed", allowed)
	return fallback
}

// clampInt restricts n to [lo, hi], logging a warning if clamping occurred.
func clampInt(key string, n, lo, hi int) int {
	if n < lo {
		slog.Warn("env value below minimum, clamping", "key", key, "value", n, "clamped_to", lo)
		return lo
	}
	if n > hi {
		slog.Warn("env value above maximum, clamping", "key", key, "value", n, "clamped_to", hi)
		return hi
	}
	return n
}

// New opens (or creates) a SQLite database at dbPath with separate read/write pools.
//
// The read pool's memory footprint is tunable via environment variables so
// operators can fit it within constrained VM memory without a rebuild. All
// default to today's hardcoded values:
//   - SQLITE_MMAP_MB (default 128): mmap_size in MB; 0 disables mmap (set explicitly).
//   - SQLITE_READ_CONNS (default 4, clamped to 1..8): max/idle read connections.
//   - SQLITE_READ_CACHE_MB (default 20): per-connection page cache size in MB.
//   - SQLITE_TEMP_STORE (default "MEMORY"): "MEMORY" or "FILE".
func New(dbPath string) (*Store, error) {
	writeParams := url.Values{}
	writeParams.Add("_pragma", "busy_timeout(30000)")
	writeParams.Add("_pragma", "journal_mode(WAL)")
	writeParams.Add("_pragma", "synchronous(NORMAL)")
	writeParams.Add("_pragma", "foreign_keys(ON)")
	writeDSN := fmt.Sprintf("file:%s?%s", dbPath, writeParams.Encode())

	writeDB, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	mmapMB := envInt("SQLITE_MMAP_MB", 128)
	readConns := clampInt("SQLITE_READ_CONNS", envInt("SQLITE_READ_CONNS", 4), 1, 8)
	readCacheMB := envInt("SQLITE_READ_CACHE_MB", 20)
	tempStore := envString("SQLITE_TEMP_STORE", "MEMORY", []string{"MEMORY", "FILE"})

	readParams := url.Values{}
	readParams.Add("_pragma", "busy_timeout(30000)")
	readParams.Add("_pragma", "journal_mode(WAL)")
	readParams.Add("_pragma", "foreign_keys(ON)")
	readParams.Add("_pragma", "query_only(ON)")
	readParams.Add("_pragma", fmt.Sprintf("cache_size(-%d)", readCacheMB*1024))
	readParams.Add("_pragma", fmt.Sprintf("temp_store(%s)", tempStore))
	// Always set mmap_size explicitly (0 disables) so the effective value does
	// not depend on the library's compiled-in default.
	readParams.Add("_pragma", fmt.Sprintf("mmap_size(%d)", mmapMB*1048576))
	readDSN := fmt.Sprintf("file:%s?%s", dbPath, readParams.Encode())

	readDB, err := sql.Open("sqlite", readDSN)
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	readDB.SetMaxOpenConns(readConns)
	readDB.SetMaxIdleConns(readConns)
	slog.Info("readDB pragmas set",
		"cache_size_mb", readCacheMB,
		"temp_store", tempStore,
		"mmap_size_mb", mmapMB,
		"read_conns", readConns,
	)

	maintParams := url.Values{}
	maintParams.Add("_pragma", "busy_timeout(1000)")
	maintParams.Add("_pragma", "journal_mode(WAL)")
	maintDSN := fmt.Sprintf("file:%s?%s", dbPath, maintParams.Encode())

	maintDB, err := sql.Open("sqlite", maintDSN)
	if err != nil {
		writeDB.Close()
		readDB.Close()
		return nil, fmt.Errorf("open maint db: %w", err)
	}
	maintDB.SetMaxOpenConns(1)
	maintDB.SetMaxIdleConns(1)

	s := &Store{readDB: readDB, writeDB: writeDB, maintDB: maintDB, dbPath: dbPath}
	if err := s.migrate(); err != nil {
		writeDB.Close()
		readDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes both underlying database connections.
func (s *Store) Close() error {
	mErr := s.maintDB.Close()
	wErr := s.writeDB.Close()
	rErr := s.readDB.Close()
	if mErr != nil {
		return mErr
	}
	if wErr != nil {
		return wErr
	}
	return rErr
}

func (s *Store) RunStartupMaintenance(ctx context.Context) error {
	slog.Info("startup maintenance: starting")

	walBefore := s.walFileSize()
	slog.Info("startup maintenance: WAL checkpoint", "wal_mb", walBefore/(1024*1024))
	if _, err := s.writeDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		slog.Warn("startup WAL checkpoint failed", "error", err, "wal_mb", walBefore/(1024*1024))
	} else {
		walAfter := s.walFileSize()
		slog.Info("startup maintenance: WAL checkpoint complete",
			"wal_before_mb", walBefore/(1024*1024), "wal_after_mb", walAfter/(1024*1024))
	}

	slog.Info("startup maintenance: cleaning derived tables")
	if _, err := s.writeDB.ExecContext(ctx, "DROP TABLE IF EXISTS token_postings"); err != nil {
		slog.Warn("drop token_postings failed", "error", err)
	}
	if _, err := s.writeDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS token_postings (
		token TEXT NOT NULL,
		post_uri TEXT NOT NULL,
		created_at TEXT NOT NULL,
		PRIMARY KEY (token, post_uri)
	)`); err != nil {
		return fmt.Errorf("recreate token_postings: %w", err)
	}
	if _, err := s.writeDB.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_token_postings_token_created ON token_postings(token, created_at)"); err != nil {
		slog.Error("failed to create index", "index", "idx_token_postings_token_created", "error", err)
	}
	if _, err := s.writeDB.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_token_postings_created_at ON token_postings(created_at)"); err != nil {
		slog.Error("failed to create index", "index", "idx_token_postings_created_at", "error", err)
	}
	if _, err := s.writeDB.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_post_buffer_created_at ON post_buffer(created_at)"); err != nil {
		slog.Error("failed to create index", "index", "idx_post_buffer_created_at", "error", err)
	}

	cutoff := time.Now().UTC().Add(-3 * time.Hour).Format(time.RFC3339)
	result, _ := s.writeDB.ExecContext(ctx, "DELETE FROM post_buffer WHERE inserted_at < ?", cutoff)
	if result != nil {
		if n, _ := result.RowsAffected(); n > 0 {
			slog.Info("startup maintenance: purged stale posts", "count", n)
		}
	}

	tokenCutoff := time.Now().UTC().Add(-26 * time.Hour).Format(time.RFC3339)
	result, _ = s.writeDB.ExecContext(ctx, "DELETE FROM topic_tokens WHERE created_at < ?", tokenCutoff)
	if result != nil {
		if n, _ := result.RowsAffected(); n > 0 {
			slog.Info("startup maintenance: purged stale topic_tokens", "count", n)
		}
	}

	slog.Info("startup maintenance complete")
	return nil
}

// WALCheckpointResult reports what happened during a WAL checkpoint attempt.
type WALCheckpointResult struct {
	Escalated bool
	Completed bool
	WALBefore int64
	WALAfter  int64
}

// RunWALCheckpoint runs an opportunistic WAL checkpoint. If the WAL exceeds
// pressureThreshold bytes, it escalates from PASSIVE (non-blocking, on the
// low-priority maintDB) to TRUNCATE (blocking, on the writeDB with 30 s busy
// timeout) to prevent unbounded WAL growth during long-running read transactions.
func (s *Store) RunWALCheckpoint(ctx context.Context, pressureThreshold int64) WALCheckpointResult {
	walSize := s.walFileSize()

	if walSize < pressureThreshold {
		var busy, logFrames, checkpointed int
		row := s.maintDB.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
		if err := row.Scan(&busy, &logFrames, &checkpointed); err != nil {
			return WALCheckpointResult{}
		}
		if logFrames > 0 {
			slog.Debug("wal checkpoint (passive)", "wal_mb", walSize/(1024*1024),
				"log_frames", logFrames, "checkpointed", checkpointed)
		}
		return WALCheckpointResult{}
	}

	slog.Warn("wal pressure: forcing truncate checkpoint",
		"wal_mb", walSize/(1024*1024), "threshold_mb", pressureThreshold/(1024*1024))

	var busy, logFrames, checkpointed int
	row := s.writeDB.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err := row.Scan(&busy, &logFrames, &checkpointed); err != nil {
		slog.Error("wal checkpoint (truncate) failed", "error", err,
			"wal_mb", walSize/(1024*1024))
		return WALCheckpointResult{Escalated: true, WALBefore: walSize, WALAfter: walSize}
	}

	afterSize := s.walFileSize()
	completed := busy == 0
	if !completed {
		slog.Warn("wal checkpoint (truncate) incomplete — readers blocking",
			"wal_before_mb", walSize/(1024*1024),
			"wal_after_mb", afterSize/(1024*1024),
			"log_frames", logFrames, "checkpointed", checkpointed)
	} else {
		slog.Info("wal checkpoint (truncate) complete",
			"wal_before_mb", walSize/(1024*1024),
			"wal_after_mb", afterSize/(1024*1024))
	}
	return WALCheckpointResult{Escalated: true, Completed: completed, WALBefore: walSize, WALAfter: afterSize}
}

func (s *Store) walFileSize() int64 {
	if info, err := os.Stat(s.dbPath + "-wal"); err == nil {
		return info.Size()
	}
	return 0
}

// RunVacuum reclaims freelist pages from high-churn tables (post_buffer, topic_tokens).
// This rewrites the entire database file, briefly blocking all readers, so it
// only runs when the freelist is actually large enough to be worth the stall:
// on prod an unconditional weekly VACUUM rewrote 644 MB to reclaim ~10 MB and
// blocked writes for 160 s. VACUUM_FREELIST_PCT (default 20) is the share of
// total pages the freelist must exceed. Call during low-traffic periods.
func (s *Store) RunVacuum(ctx context.Context) error {
	var freelistPages, pageCount int64
	if err := s.writeDB.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freelistPages); err != nil {
		return fmt.Errorf("vacuum: freelist_count: %w", err)
	}
	if err := s.writeDB.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return fmt.Errorf("vacuum: page_count: %w", err)
	}

	thresholdPct := envInt("VACUUM_FREELIST_PCT", 20)
	var freelistPct float64
	if pageCount > 0 {
		freelistPct = float64(freelistPages) / float64(pageCount) * 100
	}
	if freelistPct <= float64(thresholdPct) {
		slog.Info("vacuum: skipped, freelist below threshold",
			"freelist_pages", freelistPages,
			"page_count", pageCount,
			"freelist_pct", math.Round(freelistPct*10)/10,
			"threshold_pct", thresholdPct)
		return nil
	}

	slog.Info("vacuum: starting",
		"freelist_pages", freelistPages,
		"page_count", pageCount,
		"freelist_pct", math.Round(freelistPct*10)/10,
		"threshold_pct", thresholdPct)
	start := time.Now()
	if _, err := s.writeDB.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	slog.Info("vacuum: complete", "elapsed", time.Since(start).Round(time.Millisecond))
	return nil
}

// DB returns the read pool for external callers (e.g. statsapi).
func (s *Store) DB() *sql.DB {
	return s.readDB
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS post_buffer (
			uri TEXT PRIMARY KEY,
			cid TEXT NOT NULL,
			text TEXT NOT NULL,
			author_did TEXT NOT NULL,
			author_handle TEXT NOT NULL DEFAULT '',
			likes INTEGER NOT NULL DEFAULT 0,
			reposts INTEGER NOT NULL DEFAULT 0,
			replies INTEGER NOT NULL DEFAULT 0,
			sentiment TEXT NOT NULL DEFAULT '',
			engagement_score REAL NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			inserted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_post_buffer_inserted_at ON post_buffer(inserted_at)`,

		`CREATE TABLE IF NOT EXISTS cursor (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			cursor_value INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		)`,

		`CREATE TABLE IF NOT EXISTS runs (
			run_id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'initializing',
			analysis_interval_minutes INTEGER NOT NULL DEFAULT 30,
			cutoff_time TEXT NOT NULL,
			total_posts_retrieved INTEGER NOT NULL DEFAULT 0,
			overall_sentiment TEXT NOT NULL DEFAULT '',
			net_sentiment_percentage REAL NOT NULL DEFAULT 0,
			top_posts TEXT NOT NULL DEFAULT '[]',
			top_post_uri TEXT NOT NULL DEFAULT '',
			top_post_cid TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			ttl INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS sentiment_history (
			run_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			average_compound_score REAL NOT NULL DEFAULT 0,
			net_sentiment_percent REAL NOT NULL DEFAULT 0,
			sentiment_category TEXT NOT NULL DEFAULT '',
			total_posts INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			ttl INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (run_id, timestamp)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sentiment_history_timestamp ON sentiment_history(timestamp)`,

		`ALTER TABLE sentiment_history ADD COLUMN total_firehose_posts INTEGER NOT NULL DEFAULT 0`,

		`ALTER TABLE post_buffer ADD COLUMN is_reply INTEGER NOT NULL DEFAULT 0`,

		`ALTER TABLE sentiment_history ADD COLUMN root_sentiment_pct REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE sentiment_history ADD COLUMN reply_sentiment_pct REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE sentiment_history ADD COLUMN top_topic TEXT NOT NULL DEFAULT ''`,

		`CREATE TABLE IF NOT EXISTS daily_sentiment (
			date TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			average_sentiment REAL NOT NULL DEFAULT 0,
			min_sentiment REAL NOT NULL DEFAULT 0,
			max_sentiment REAL NOT NULL DEFAULT 0,
			q1_sentiment REAL NOT NULL DEFAULT 0,
			median_sentiment REAL NOT NULL DEFAULT 0,
			q3_sentiment REAL NOT NULL DEFAULT 0,
			total_runs INTEGER NOT NULL DEFAULT 0,
			total_posts INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			ttl INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS key_value (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		)`,

		// Trending topics tables
		`CREATE TABLE IF NOT EXISTS topic_tokens (
			post_uri TEXT PRIMARY KEY,
			tokens TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_topic_tokens_created_at ON topic_tokens(created_at)`,

		`CREATE TABLE IF NOT EXISTS topic_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			snapshot_time TEXT NOT NULL,
			rank INTEGER NOT NULL,
			topic_id TEXT NOT NULL,
			label TEXT NOT NULL,
			description TEXT NOT NULL,
			unique_author_count INTEGER NOT NULL,
			keywords TEXT NOT NULL,
			exemplar_uri TEXT NOT NULL DEFAULT '',
			exemplar_handle TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_topic_snapshots_time ON topic_snapshots(snapshot_time)`,

		`ALTER TABLE topic_snapshots ADD COLUMN is_meme INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE topic_snapshots ADD COLUMN justification TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE topic_snapshots ADD COLUMN synonyms TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE topic_snapshots RENAME COLUMN post_count TO unique_author_count`,

		`CREATE TABLE IF NOT EXISTS topic_identity (
			topic_id TEXT PRIMARY KEY,
			canonical_label TEXT NOT NULL,
			keywords TEXT NOT NULL,
			first_seen TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			peak_rank INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS token_postings (
			token TEXT NOT NULL,
			post_uri TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (token, post_uri)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_token_postings_token_created ON token_postings(token, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_token_postings_created_at ON token_postings(created_at)`,

		`CREATE TABLE IF NOT EXISTS stats_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			snapshot_time TEXT NOT NULL,
			active_endpoint TEXT NOT NULL DEFAULT '',
			endpoint_rotations INTEGER NOT NULL DEFAULT 0,
			reconnect_count INTEGER NOT NULL DEFAULT 0,
			connection_uptime_seconds INTEGER NOT NULL DEFAULT 0,
			events_received INTEGER NOT NULL DEFAULT 0,
			posts_processed INTEGER NOT NULL DEFAULT 0,
			events_skipped INTEGER NOT NULL DEFAULT 0,
			consumer_errors INTEGER NOT NULL DEFAULT 0,
			total_firehose_posts INTEGER NOT NULL DEFAULT 0,
			english_posts_stored INTEGER NOT NULL DEFAULT 0,
			root_posts INTEGER NOT NULL DEFAULT 0,
			reply_posts INTEGER NOT NULL DEFAULT 0,
			posts_per_minute_avg REAL NOT NULL DEFAULT 0,
			analysis_ran INTEGER NOT NULL DEFAULT 0,
			posts_considered INTEGER NOT NULL DEFAULT 0,
			posts_hydrated INTEGER NOT NULL DEFAULT 0,
			hydration_errors INTEGER NOT NULL DEFAULT 0,
			sentiment_result TEXT NOT NULL DEFAULT '',
			posting_skipped INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_stats_snapshots_time ON stats_snapshots(snapshot_time)`,

		`CREATE TABLE IF NOT EXISTS stats_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_time TEXT NOT NULL,
			event_type TEXT NOT NULL,
			details TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_stats_events_time ON stats_events(event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_stats_events_type ON stats_events(event_type)`,

		`ALTER TABLE stats_snapshots ADD COLUMN dropped_posts INTEGER NOT NULL DEFAULT 0`,

		// Health metrics columns (hs-21g health-metrics-dashboard)
		`ALTER TABLE stats_snapshots ADD COLUMN heap_inuse_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN heap_sys_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN sys_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN gc_pause_total_ns INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN gc_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN gc_cpu_fraction REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN slow_flush_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN slow_flush_max_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN write_channel_depth INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN wal_size_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN goroutine_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN cycle_duration_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN trending_duration_ms INTEGER NOT NULL DEFAULT 0`,

		// Firehose reconstruction and RSS-aware memory accounting
		`ALTER TABLE stats_snapshots ADD COLUMN early_rejected_non_english INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN rss_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN heap_released_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE stats_snapshots ADD COLUMN stack_inuse_bytes INTEGER NOT NULL DEFAULT 0`,
	}

	for _, stmt := range stmts {
		if _, err := s.writeDB.Exec(stmt); err != nil {
			// Idempotent migrations: "duplicate column" (re-add) and "no such column" (re-rename) are safe to skip.
			if strings.Contains(err.Error(), "duplicate column") || strings.Contains(err.Error(), "no such column") {
				continue
			}
			return fmt.Errorf("exec %q: %w", stmt[:60], err)
		}
	}
	return nil
}

// purgeInChunks deletes rows matching query in batches of 1000, yielding
// briefly between chunks so other writers can acquire the lock.
// The query MUST contain a LIMIT clause (e.g. "DELETE FROM t WHERE rowid IN
// (SELECT rowid FROM t WHERE ... LIMIT 1000)").
func purgeInChunks(ctx context.Context, db *sql.DB, query string, args ...any) (int64, error) {
	var total int64
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		var result sql.Result
		err := withRetry(ctx, func() error {
			var execErr error
			result, execErr = db.ExecContext(ctx, query, args...)
			return execErr
		})
		if err != nil {
			return total, err
		}
		n, _ := result.RowsAffected()
		total += n
		if n < 1000 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return total, nil
}

// timeToStr formats a time.Time as RFC3339 in UTC.
func timeToStr(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// strToTime parses an RFC3339 string; returns zero time on error.
func strToTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// marshalJSON marshals v to a JSON string.
func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalPosts parses a JSON array of Post.
func unmarshalPosts(data string) []Post {
	var posts []Post
	if err := json.Unmarshal([]byte(data), &posts); err != nil {
		return nil
	}
	return posts
}

// nowUTC returns the current UTC time string.
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func withRetry(ctx context.Context, fn func() error) error {
	backoffs := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	var lastErr error
	for i := 0; i <= len(backoffs); i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !strings.Contains(lastErr.Error(), "database is locked") {
			return lastErr
		}
		if i < len(backoffs) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoffs[i]):
			}
		}
	}
	return lastErr
}

// Ensure context is used (import guard).
var _ context.Context
