// Package store provides a SQLite-based storage layer for hourstats,
// replacing the DynamoDB-based internal/state package for the Fly.io deployment.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite database connection with all hourstats storage operations.
type Store struct {
	db *sql.DB
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

// New opens (or creates) a SQLite database at dbPath, enables WAL mode,
// and runs schema migrations.
func New(dbPath string) (*Store, error) {
	params := url.Values{}
	params.Add("_pragma", "busy_timeout(30000)")
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "synchronous(NORMAL)")
	params.Add("_pragma", "foreign_keys(ON)")
	dsn := fmt.Sprintf("file:%s?%s", dbPath, params.Encode())

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Limit connection pool to reduce SQLite write contention.
	// WAL mode allows concurrent reads, but only one writer at a time.
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB for advanced use cases.
func (s *Store) DB() *sql.DB {
	return s.db
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
			post_count INTEGER NOT NULL,
			keywords TEXT NOT NULL,
			exemplar_uri TEXT NOT NULL DEFAULT '',
			exemplar_handle TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_topic_snapshots_time ON topic_snapshots(snapshot_time)`,

		`CREATE TABLE IF NOT EXISTS topic_identity (
			topic_id TEXT PRIMARY KEY,
			canonical_label TEXT NOT NULL,
			keywords TEXT NOT NULL,
			first_seen TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			peak_rank INTEGER NOT NULL
		)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			// ALTER TABLE fails with "duplicate column" on repeat runs — safe to ignore.
			if strings.Contains(err.Error(), "duplicate column") {
				continue
			}
			return fmt.Errorf("exec %q: %w", stmt[:60], err)
		}
	}
	return nil
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

// Ensure context is used (import guard).
var _ context.Context
