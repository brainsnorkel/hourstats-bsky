package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// StatsSnapshot represents a point-in-time capture of system statistics.
type StatsSnapshot struct {
	ID                      int64     `json:"id"`
	SnapshotTime            time.Time `json:"snapshot_time"`
	ActiveEndpoint          string    `json:"active_endpoint"`
	EndpointRotations       int       `json:"endpoint_rotations"`
	ReconnectCount          int       `json:"reconnect_count"`
	ConnectionUptimeSeconds int       `json:"connection_uptime_seconds"`
	EventsReceived          int       `json:"events_received"`
	PostsProcessed          int       `json:"posts_processed"`
	EventsSkipped           int       `json:"events_skipped"`
	ConsumerErrors          int       `json:"consumer_errors"`
	TotalFirehosePosts      int       `json:"total_firehose_posts"`
	EnglishPostsStored      int       `json:"english_posts_stored"`
	RootPosts               int       `json:"root_posts"`
	ReplyPosts              int       `json:"reply_posts"`
	PostsPerMinuteAvg       float64   `json:"posts_per_minute_avg"`
	AnalysisRan             int       `json:"analysis_ran"`
	PostsConsidered         int       `json:"posts_considered"`
	PostsHydrated           int       `json:"posts_hydrated"`
	HydrationErrors         int       `json:"hydration_errors"`
	SentimentResult         string    `json:"sentiment_result"`
	PostingSkipped          int       `json:"posting_skipped"`
}

// StatsEvent represents a logged event in the system.
type StatsEvent struct {
	ID        int64     `json:"id"`
	EventTime time.Time `json:"event_time"`
	EventType string    `json:"event_type"`
	Details   string    `json:"details"`
}

// PostingEntry describes the last time a particular post type was made.
type PostingEntry struct {
	LastPosted string `json:"last_posted"`
	Summary    string `json:"summary"`
	URI        string `json:"uri,omitempty"`
}

// PostingActivity aggregates the most recent posting times for each post type.
type PostingActivity struct {
	SentimentSummary *PostingEntry `json:"sentiment_summary"`
	YearlyChart      *PostingEntry `json:"yearly_chart"`
	DailyQuote       *PostingEntry `json:"daily_quote"`
	TrendingTopics   *PostingEntry `json:"trending_topics"`
}

// GetPostingActivity assembles posting activity from multiple tables.
func (s *Store) GetPostingActivity(ctx context.Context) (*PostingActivity, error) {
	pa := &PostingActivity{}

	// 1. Sentiment summary — latest completed run
	run, err := s.GetLatestCompletedRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("posting activity: %w", err)
	}
	if run != nil {
		pa.SentimentSummary = &PostingEntry{
			LastPosted: timeToStr(run.CreatedAt),
			Summary:    fmt.Sprintf("%s (%.1f%%), %d posts", run.OverallSentiment, run.NetSentimentPercentage, run.TotalPostsRetrieved),
			URI:        run.TopPostURI,
		}
	}

	// 2. Yearly chart — key_value "yearly_post_uri"
	yearlyEntry, err := s.GetKeyValueWithTimestamp(ctx, "yearly_post_uri")
	if err != nil {
		return nil, fmt.Errorf("posting activity: %w", err)
	}
	if yearlyEntry != nil {
		pa.YearlyChart = &PostingEntry{
			LastPosted: yearlyEntry.UpdatedAt,
			Summary:    "Yearly sentiment chart",
			URI:        yearlyEntry.Value,
		}
	}

	// 3. Daily quote — key_value "daily_quote_last_date"
	quoteEntry, err := s.GetKeyValueWithTimestamp(ctx, "daily_quote_last_date")
	if err != nil {
		return nil, fmt.Errorf("posting activity: %w", err)
	}
	if quoteEntry != nil {
		pa.DailyQuote = &PostingEntry{
			LastPosted: quoteEntry.UpdatedAt,
			Summary:    fmt.Sprintf("Top post quote for %s", quoteEntry.Value),
		}
	}

	// 4. Trending topics — latest topic snapshot
	snapshotTime, topicCount, err := s.GetLatestTopicSnapshotTime(ctx)
	if err != nil {
		return nil, fmt.Errorf("posting activity: %w", err)
	}
	if snapshotTime != "" {
		pa.TrendingTopics = &PostingEntry{
			LastPosted: snapshotTime,
			Summary:    fmt.Sprintf("%d trending topics", topicCount),
		}
	}

	return pa, nil
}

// InsertStatsSnapshot inserts a new stats snapshot record.
func (s *Store) InsertStatsSnapshot(ctx context.Context, snap *StatsSnapshot) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO stats_snapshots (snapshot_time, active_endpoint, endpoint_rotations, reconnect_count,
			connection_uptime_seconds, events_received, posts_processed, events_skipped, consumer_errors,
			total_firehose_posts, english_posts_stored, root_posts, reply_posts, posts_per_minute_avg,
			analysis_ran, posts_considered, posts_hydrated, hydration_errors, sentiment_result, posting_skipped)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		timeToStr(snap.SnapshotTime), snap.ActiveEndpoint, snap.EndpointRotations, snap.ReconnectCount,
		snap.ConnectionUptimeSeconds, snap.EventsReceived, snap.PostsProcessed, snap.EventsSkipped,
		snap.ConsumerErrors, snap.TotalFirehosePosts, snap.EnglishPostsStored, snap.RootPosts,
		snap.ReplyPosts, snap.PostsPerMinuteAvg, snap.AnalysisRan, snap.PostsConsidered,
		snap.PostsHydrated, snap.HydrationErrors, snap.SentimentResult, snap.PostingSkipped,
	)
	if err != nil {
		return fmt.Errorf("insert stats snapshot: %w", err)
	}
	snap.ID, _ = result.LastInsertId()
	return nil
}

// InsertStatsEvent inserts a new stats event record.
func (s *Store) InsertStatsEvent(ctx context.Context, e *StatsEvent) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO stats_events (event_time, event_type, details)
		 VALUES (?, ?, ?)`,
		timeToStr(e.EventTime), e.EventType, e.Details,
	)
	if err != nil {
		return fmt.Errorf("insert stats event: %w", err)
	}
	e.ID, _ = result.LastInsertId()
	return nil
}

// GetLatestSnapshot returns the most recent snapshot by snapshot_time.
// Returns nil, nil if no snapshots exist.
func (s *Store) GetLatestSnapshot(ctx context.Context) (*StatsSnapshot, error) {
	var snap StatsSnapshot
	var snapshotTimeStr string

	err := s.db.QueryRowContext(ctx,
		`SELECT id, snapshot_time, active_endpoint, endpoint_rotations, reconnect_count,
			connection_uptime_seconds, events_received, posts_processed, events_skipped, consumer_errors,
			total_firehose_posts, english_posts_stored, root_posts, reply_posts, posts_per_minute_avg,
			analysis_ran, posts_considered, posts_hydrated, hydration_errors, sentiment_result, posting_skipped
		 FROM stats_snapshots
		 ORDER BY snapshot_time DESC
		 LIMIT 1`,
	).Scan(
		&snap.ID, &snapshotTimeStr, &snap.ActiveEndpoint, &snap.EndpointRotations, &snap.ReconnectCount,
		&snap.ConnectionUptimeSeconds, &snap.EventsReceived, &snap.PostsProcessed, &snap.EventsSkipped,
		&snap.ConsumerErrors, &snap.TotalFirehosePosts, &snap.EnglishPostsStored, &snap.RootPosts,
		&snap.ReplyPosts, &snap.PostsPerMinuteAvg, &snap.AnalysisRan, &snap.PostsConsidered,
		&snap.PostsHydrated, &snap.HydrationErrors, &snap.SentimentResult, &snap.PostingSkipped,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest snapshot: %w", err)
	}

	snap.SnapshotTime = strToTime(snapshotTimeStr)
	return &snap, nil
}

// GetSnapshotHistory returns snapshots since the given time, ordered by snapshot_time DESC, limited to `limit` rows.
func (s *Store) GetSnapshotHistory(ctx context.Context, since time.Time, limit int) ([]StatsSnapshot, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, snapshot_time, active_endpoint, endpoint_rotations, reconnect_count,
			connection_uptime_seconds, events_received, posts_processed, events_skipped, consumer_errors,
			total_firehose_posts, english_posts_stored, root_posts, reply_posts, posts_per_minute_avg,
			analysis_ran, posts_considered, posts_hydrated, hydration_errors, sentiment_result, posting_skipped
		 FROM stats_snapshots
		 WHERE snapshot_time >= ?
		 ORDER BY snapshot_time DESC
		 LIMIT ?`,
		timeToStr(since), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query snapshot history: %w", err)
	}
	defer rows.Close()

	var results []StatsSnapshot
	for rows.Next() {
		var snap StatsSnapshot
		var snapshotTimeStr string
		if err := rows.Scan(
			&snap.ID, &snapshotTimeStr, &snap.ActiveEndpoint, &snap.EndpointRotations, &snap.ReconnectCount,
			&snap.ConnectionUptimeSeconds, &snap.EventsReceived, &snap.PostsProcessed, &snap.EventsSkipped,
			&snap.ConsumerErrors, &snap.TotalFirehosePosts, &snap.EnglishPostsStored, &snap.RootPosts,
			&snap.ReplyPosts, &snap.PostsPerMinuteAvg, &snap.AnalysisRan, &snap.PostsConsidered,
			&snap.PostsHydrated, &snap.HydrationErrors, &snap.SentimentResult, &snap.PostingSkipped,
		); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		snap.SnapshotTime = strToTime(snapshotTimeStr)
		results = append(results, snap)
	}
	return results, rows.Err()
}

// GetEvents returns events since the given time. If eventType is non-empty, filter by it.
// Results are ordered by event_time DESC, limited to `limit` rows.
func (s *Store) GetEvents(ctx context.Context, since time.Time, eventType string, limit int) ([]StatsEvent, error) {
	query := `SELECT id, event_time, event_type, details
		 FROM stats_events
		 WHERE event_time >= ?`
	args := []interface{}{timeToStr(since)}

	if eventType != "" {
		query += ` AND event_type = ?`
		args = append(args, eventType)
	}

	query += ` ORDER BY event_time DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var results []StatsEvent
	for rows.Next() {
		var e StatsEvent
		var eventTimeStr string
		if err := rows.Scan(&e.ID, &eventTimeStr, &e.EventType, &e.Details); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.EventTime = strToTime(eventTimeStr)
		results = append(results, e)
	}
	return results, rows.Err()
}

// PurgeExpiredStats deletes stats records from both tables where time < now - olderThan.
// Returns total rows deleted across both tables.
func (s *Store) PurgeExpiredStats(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	cutoffStr := timeToStr(cutoff)

	// Delete from stats_snapshots
	result1, err := s.db.ExecContext(ctx,
		`DELETE FROM stats_snapshots WHERE snapshot_time < ?`,
		cutoffStr,
	)
	if err != nil {
		return 0, fmt.Errorf("purge stats snapshots: %w", err)
	}
	count1, _ := result1.RowsAffected()

	// Delete from stats_events
	result2, err := s.db.ExecContext(ctx,
		`DELETE FROM stats_events WHERE event_time < ?`,
		cutoffStr,
	)
	if err != nil {
		return 0, fmt.Errorf("purge stats events: %w", err)
	}
	count2, _ := result2.RowsAffected()

	return count1 + count2, nil
}
