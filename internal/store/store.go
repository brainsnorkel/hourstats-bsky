// Package store provides a SQLite-based storage layer for hourstats,
// replacing the DynamoDB-based internal/state package for the Fly.io deployment.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
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
	CreatedAt            time.Time
	TTL                  int64
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

// New opens (or creates) a SQLite database at dbPath with separate read/write pools.
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

	readParams := url.Values{}
	readParams.Add("_pragma", "busy_timeout(30000)")
	readParams.Add("_pragma", "journal_mode(WAL)")
	readParams.Add("_pragma", "foreign_keys(ON)")
	readParams.Add("_pragma", "query_only(ON)")
	readDSN := fmt.Sprintf("file:%s?%s", dbPath, readParams.Encode())

	readDB, err := sql.Open("sqlite", readDSN)
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	readDB.SetMaxOpenConns(4)
	readDB.SetMaxIdleConns(4)

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
	s.writeDB.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_token_postings_token_created ON token_postings(token, created_at)")
	s.writeDB.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_token_postings_created_at ON token_postings(created_at)")

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

	if _, err := s.writeDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		slog.Warn("startup WAL checkpoint failed", "error", err)
	} else {
		slog.Info("startup maintenance: WAL checkpoint complete")
	}

	slog.Info("startup maintenance complete")
	return nil
}

func (s *Store) RunWALCheckpoint(ctx context.Context) {
	s.maintDB.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
}

// RunVacuum reclaims freelist pages from high-churn tables (post_buffer, topic_tokens).
// This rewrites the entire database file, briefly blocking all readers.
// Call during low-traffic periods (e.g. weekly at midnight UTC).
func (s *Store) RunVacuum(ctx context.Context) error {
	slog.Info("vacuum: starting")
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
