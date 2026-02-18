package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
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
	DroppedPosts            int       `json:"dropped_posts"`
	HeapInuseBytes          int64     `json:"heap_inuse_bytes"`
	HeapSysBytes            int64     `json:"heap_sys_bytes"`
	SysBytes                int64     `json:"sys_bytes"`
	GCPauseTotalNs          int64     `json:"gc_pause_total_ns"`
	GCCount                 int64     `json:"gc_count"`
	GCCPUFraction           float64   `json:"gc_cpu_fraction"`
	SlowFlushCount          int       `json:"slow_flush_count"`
	SlowFlushMaxMs          int64     `json:"slow_flush_max_ms"`
	WriteChannelDepth       int       `json:"write_channel_depth"`
	WALSizeBytes            int64     `json:"wal_size_bytes"`
	GoroutineCount          int       `json:"goroutine_count"`
	CycleDurationMs         int64     `json:"cycle_duration_ms"`
	TrendingDurationMs      int64     `json:"trending_duration_ms"`
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
	LastPosted      string `json:"last_posted"`
	Summary         string `json:"summary"`
	URI             string `json:"uri,omitempty"`
	NextAnticipated string `json:"next_anticipated,omitempty"`
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

	// 4. Trending topics — prefer actual post time from key_value, fall back to snapshot time
	trendingEntry, err := s.GetKeyValueWithTimestamp(ctx, "trending_post_last_time")
	if err != nil {
		return nil, fmt.Errorf("posting activity: %w", err)
	}
	snapshotTime, topicCount, err := s.GetLatestTopicSnapshotTime(ctx)
	if err != nil {
		return nil, fmt.Errorf("posting activity: %w", err)
	}
	if trendingEntry != nil {
		summary := "Trending topics post"
		if topicCount > 0 {
			summary = fmt.Sprintf("%d trending topics", topicCount)
		}
		pa.TrendingTopics = &PostingEntry{
			LastPosted: trendingEntry.Value,
			Summary:    summary,
		}
	} else if snapshotTime != "" {
		pa.TrendingTopics = &PostingEntry{
			LastPosted: snapshotTime,
			Summary:    fmt.Sprintf("%d trending topics (analysis)", topicCount),
		}
	}

	// Compute next anticipated times from schedule intervals stored in key_value.
	pa.fillNextAnticipated(ctx, s)

	return pa, nil
}

// fillNextAnticipated reads schedule intervals from key_value and computes
// wall-clock aligned next fire times for each post type.
func (pa *PostingActivity) fillNextAnticipated(ctx context.Context, s *Store) {
	now := time.Now().UTC()

	// Sentiment: wall-clock aligned to N minutes
	if v, err := s.getKeyValueOpt(ctx, "schedule_sentiment_minutes"); err == nil && v != "" {
		if mins, err := strconv.Atoi(v); err == nil && mins > 0 {
			d := time.Duration(mins) * time.Minute
			next := now.Truncate(d).Add(d)
			if pa.SentimentSummary == nil {
				pa.SentimentSummary = &PostingEntry{Summary: "(no data)"}
			}
			pa.SentimentSummary.NextAnticipated = timeToStr(next)
		}
	}

	// Daily quote: fires at 00:00 UTC daily (same as backup ticker)
	if v, err := s.getKeyValueOpt(ctx, "schedule_daily_quote_hour"); err == nil && v != "" {
		if hour, err := strconv.Atoi(v); err == nil {
			next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
			if !next.After(now) {
				next = next.Add(24 * time.Hour)
			}
			if pa.DailyQuote == nil {
				pa.DailyQuote = &PostingEntry{Summary: "(no data)"}
			}
			pa.DailyQuote.NextAnticipated = timeToStr(next)
		}
	}

	// Yearly chart: fires at 01:00 UTC daily
	if v, err := s.getKeyValueOpt(ctx, "schedule_yearly_hour"); err == nil && v != "" {
		if hour, err := strconv.Atoi(v); err == nil {
			next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
			if !next.After(now) {
				next = next.Add(24 * time.Hour)
			}
			if pa.YearlyChart == nil {
				pa.YearlyChart = &PostingEntry{Summary: "(no data)"}
			}
			pa.YearlyChart.NextAnticipated = timeToStr(next)
		}
	}

	// Trending post: wall-clock aligned to N hours
	if v, err := s.getKeyValueOpt(ctx, "schedule_trending_post_hours"); err == nil && v != "" {
		if hours, err := strconv.Atoi(v); err == nil && hours > 0 {
			d := time.Duration(hours) * time.Hour
			next := now.Truncate(d).Add(d)
			if pa.TrendingTopics == nil {
				pa.TrendingTopics = &PostingEntry{Summary: "(no data)"}
			}
			pa.TrendingTopics.NextAnticipated = timeToStr(next)
		}
	}
}

// getKeyValueOpt returns the value for a key, or ("", nil) if not found.
func (s *Store) getKeyValueOpt(ctx context.Context, key string) (string, error) {
	entry, err := s.GetKeyValueWithTimestamp(ctx, key)
	if err != nil {
		return "", err
	}
	if entry == nil {
		return "", nil
	}
	return entry.Value, nil
}

// InsertStatsSnapshot inserts a new stats snapshot record.
func (s *Store) InsertStatsSnapshot(ctx context.Context, snap *StatsSnapshot) error {
	result, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO stats_snapshots (snapshot_time, active_endpoint, endpoint_rotations, reconnect_count,
			connection_uptime_seconds, events_received, posts_processed, events_skipped, consumer_errors,
			total_firehose_posts, english_posts_stored, root_posts, reply_posts, posts_per_minute_avg,
			analysis_ran, posts_considered, posts_hydrated, hydration_errors, sentiment_result, posting_skipped,
			dropped_posts, heap_inuse_bytes, heap_sys_bytes, sys_bytes, gc_pause_total_ns, gc_count,
			gc_cpu_fraction, slow_flush_count, slow_flush_max_ms, write_channel_depth, wal_size_bytes,
			goroutine_count, cycle_duration_ms, trending_duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		timeToStr(snap.SnapshotTime), snap.ActiveEndpoint, snap.EndpointRotations, snap.ReconnectCount,
		snap.ConnectionUptimeSeconds, snap.EventsReceived, snap.PostsProcessed, snap.EventsSkipped,
		snap.ConsumerErrors, snap.TotalFirehosePosts, snap.EnglishPostsStored, snap.RootPosts,
		snap.ReplyPosts, snap.PostsPerMinuteAvg, snap.AnalysisRan, snap.PostsConsidered,
		snap.PostsHydrated, snap.HydrationErrors, snap.SentimentResult, snap.PostingSkipped,
		snap.DroppedPosts, snap.HeapInuseBytes, snap.HeapSysBytes, snap.SysBytes, snap.GCPauseTotalNs,
		snap.GCCount, snap.GCCPUFraction, snap.SlowFlushCount, snap.SlowFlushMaxMs,
		snap.WriteChannelDepth, snap.WALSizeBytes, snap.GoroutineCount, snap.CycleDurationMs,
		snap.TrendingDurationMs,
	)
	if err != nil {
		return fmt.Errorf("insert stats snapshot: %w", err)
	}
	snap.ID, _ = result.LastInsertId()
	return nil
}

// InsertStatsEvent inserts a new stats event record.
func (s *Store) InsertStatsEvent(ctx context.Context, e *StatsEvent) error {
	result, err := s.writeDB.ExecContext(ctx,
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

	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, snapshot_time, active_endpoint, endpoint_rotations, reconnect_count,
			connection_uptime_seconds, events_received, posts_processed, events_skipped, consumer_errors,
			total_firehose_posts, english_posts_stored, root_posts, reply_posts, posts_per_minute_avg,
			analysis_ran, posts_considered, posts_hydrated, hydration_errors, sentiment_result, posting_skipped,
			dropped_posts, heap_inuse_bytes, heap_sys_bytes, sys_bytes, gc_pause_total_ns, gc_count,
			gc_cpu_fraction, slow_flush_count, slow_flush_max_ms, write_channel_depth, wal_size_bytes,
			goroutine_count, cycle_duration_ms, trending_duration_ms
		 FROM stats_snapshots
		 ORDER BY snapshot_time DESC
		 LIMIT 1`,
	).Scan(
		&snap.ID, &snapshotTimeStr, &snap.ActiveEndpoint, &snap.EndpointRotations, &snap.ReconnectCount,
		&snap.ConnectionUptimeSeconds, &snap.EventsReceived, &snap.PostsProcessed, &snap.EventsSkipped,
		&snap.ConsumerErrors, &snap.TotalFirehosePosts, &snap.EnglishPostsStored, &snap.RootPosts,
		&snap.ReplyPosts, &snap.PostsPerMinuteAvg, &snap.AnalysisRan, &snap.PostsConsidered,
		&snap.PostsHydrated, &snap.HydrationErrors, &snap.SentimentResult, &snap.PostingSkipped,
		&snap.DroppedPosts, &snap.HeapInuseBytes, &snap.HeapSysBytes, &snap.SysBytes,
		&snap.GCPauseTotalNs, &snap.GCCount, &snap.GCCPUFraction, &snap.SlowFlushCount,
		&snap.SlowFlushMaxMs, &snap.WriteChannelDepth, &snap.WALSizeBytes, &snap.GoroutineCount,
		&snap.CycleDurationMs, &snap.TrendingDurationMs,
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
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, snapshot_time, active_endpoint, endpoint_rotations, reconnect_count,
			connection_uptime_seconds, events_received, posts_processed, events_skipped, consumer_errors,
			total_firehose_posts, english_posts_stored, root_posts, reply_posts, posts_per_minute_avg,
			analysis_ran, posts_considered, posts_hydrated, hydration_errors, sentiment_result, posting_skipped,
			dropped_posts, heap_inuse_bytes, heap_sys_bytes, sys_bytes, gc_pause_total_ns, gc_count,
			gc_cpu_fraction, slow_flush_count, slow_flush_max_ms, write_channel_depth, wal_size_bytes,
			goroutine_count, cycle_duration_ms, trending_duration_ms
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
			&snap.DroppedPosts, &snap.HeapInuseBytes, &snap.HeapSysBytes, &snap.SysBytes,
			&snap.GCPauseTotalNs, &snap.GCCount, &snap.GCCPUFraction, &snap.SlowFlushCount,
			&snap.SlowFlushMaxMs, &snap.WriteChannelDepth, &snap.WALSizeBytes, &snap.GoroutineCount,
			&snap.CycleDurationMs, &snap.TrendingDurationMs,
		); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		snap.SnapshotTime = strToTime(snapshotTimeStr)
		results = append(results, snap)
	}
	return results, rows.Err()
}

// GetHealthHistory returns snapshots since the given time, ordered by snapshot_time ASC (oldest first).
// Designed for time-series charting where chronological order is required.
func (s *Store) GetHealthHistory(ctx context.Context, since time.Time, limit int) ([]StatsSnapshot, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, snapshot_time, active_endpoint, endpoint_rotations, reconnect_count,
			connection_uptime_seconds, events_received, posts_processed, events_skipped, consumer_errors,
			total_firehose_posts, english_posts_stored, root_posts, reply_posts, posts_per_minute_avg,
			analysis_ran, posts_considered, posts_hydrated, hydration_errors, sentiment_result, posting_skipped,
			dropped_posts, heap_inuse_bytes, heap_sys_bytes, sys_bytes, gc_pause_total_ns, gc_count,
			gc_cpu_fraction, slow_flush_count, slow_flush_max_ms, write_channel_depth, wal_size_bytes,
			goroutine_count, cycle_duration_ms, trending_duration_ms
		 FROM stats_snapshots
		 WHERE snapshot_time >= ?
		 ORDER BY snapshot_time ASC
		 LIMIT ?`,
		timeToStr(since), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query health history: %w", err)
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
			&snap.DroppedPosts, &snap.HeapInuseBytes, &snap.HeapSysBytes, &snap.SysBytes,
			&snap.GCPauseTotalNs, &snap.GCCount, &snap.GCCPUFraction, &snap.SlowFlushCount,
			&snap.SlowFlushMaxMs, &snap.WriteChannelDepth, &snap.WALSizeBytes, &snap.GoroutineCount,
			&snap.CycleDurationMs, &snap.TrendingDurationMs,
		); err != nil {
			return nil, fmt.Errorf("scan health snapshot: %w", err)
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

	rows, err := s.readDB.QueryContext(ctx, query, args...)
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
	result1, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM stats_snapshots WHERE snapshot_time < ?`,
		cutoffStr,
	)
	if err != nil {
		return 0, fmt.Errorf("purge stats snapshots: %w", err)
	}
	count1, _ := result1.RowsAffected()

	// Delete from stats_events
	result2, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM stats_events WHERE event_time < ?`,
		cutoffStr,
	)
	if err != nil {
		return 0, fmt.Errorf("purge stats events: %w", err)
	}
	count2, _ := result2.RowsAffected()

	return count1 + count2, nil
}
