package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreateRun inserts a new run state record.
func (s *Store) CreateRun(ctx context.Context, run RunState) error {
	now := nowUTC()
	topPostsJSON := marshalJSON(run.TopPosts)
	ttl := time.Now().UTC().Add(48 * time.Hour).Unix()

	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO runs (run_id, status, analysis_interval_minutes, cutoff_time, total_posts_retrieved,
			overall_sentiment, net_sentiment_percentage, top_posts, top_post_uri, top_post_cid,
			created_at, updated_at, ttl)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.Status, run.AnalysisIntervalMinutes, timeToStr(run.CutoffTime),
		run.TotalPostsRetrieved, run.OverallSentiment, run.NetSentimentPercentage,
		topPostsJSON, run.TopPostURI, run.TopPostCID,
		now, now, ttl,
	)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

// UpdateRun updates an existing run state record.
func (s *Store) UpdateRun(ctx context.Context, run RunState) error {
	topPostsJSON := marshalJSON(run.TopPosts)

	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE runs SET status=?, total_posts_retrieved=?, overall_sentiment=?,
			net_sentiment_percentage=?, top_posts=?, top_post_uri=?, top_post_cid=?,
			updated_at=?
		 WHERE run_id=?`,
		run.Status, run.TotalPostsRetrieved, run.OverallSentiment,
		run.NetSentimentPercentage, topPostsJSON, run.TopPostURI, run.TopPostCID,
		nowUTC(), run.RunID,
	)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	return nil
}

// GetRun retrieves a run state by ID.
func (s *Store) GetRun(ctx context.Context, runID string) (*RunState, error) {
	var (
		run          RunState
		cutoffStr    string
		createdStr   string
		updatedStr   string
		topPostsJSON string
	)

	err := s.readDB.QueryRowContext(ctx,
		`SELECT run_id, status, analysis_interval_minutes, cutoff_time, total_posts_retrieved,
			overall_sentiment, net_sentiment_percentage, top_posts, top_post_uri, top_post_cid,
			created_at, updated_at, ttl
		 FROM runs WHERE run_id = ?`,
		runID,
	).Scan(
		&run.RunID, &run.Status, &run.AnalysisIntervalMinutes, &cutoffStr,
		&run.TotalPostsRetrieved, &run.OverallSentiment, &run.NetSentimentPercentage,
		&topPostsJSON, &run.TopPostURI, &run.TopPostCID,
		&createdStr, &updatedStr, &run.TTL,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("run not found: %s", runID)
	}
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}

	run.CutoffTime = strToTime(cutoffStr)
	run.CreatedAt = strToTime(createdStr)
	run.UpdatedAt = strToTime(updatedStr)
	run.TopPosts = unmarshalPosts(topPostsJSON)

	return &run, nil
}

// GetLatestCompletedRun returns the most recent run with status='complete'.
// Returns nil, nil if no completed runs exist.
func (s *Store) GetLatestCompletedRun(ctx context.Context) (*RunState, error) {
	var (
		run          RunState
		cutoffStr    string
		createdStr   string
		updatedStr   string
		topPostsJSON string
	)

	err := s.readDB.QueryRowContext(ctx,
		`SELECT run_id, status, analysis_interval_minutes, cutoff_time, total_posts_retrieved,
			overall_sentiment, net_sentiment_percentage, top_posts, top_post_uri, top_post_cid,
			created_at, updated_at, ttl
		 FROM runs WHERE status = 'complete' ORDER BY created_at DESC LIMIT 1`,
	).Scan(
		&run.RunID, &run.Status, &run.AnalysisIntervalMinutes, &cutoffStr,
		&run.TotalPostsRetrieved, &run.OverallSentiment, &run.NetSentimentPercentage,
		&topPostsJSON, &run.TopPostURI, &run.TopPostCID,
		&createdStr, &updatedStr, &run.TTL,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest completed run: %w", err)
	}

	run.CutoffTime = strToTime(cutoffStr)
	run.CreatedAt = strToTime(createdStr)
	run.UpdatedAt = strToTime(updatedStr)
	run.TopPosts = unmarshalPosts(topPostsJSON)

	return &run, nil
}

// PurgeExpiredRuns deletes runs older than olderThan and returns the count removed.
func (s *Store) PurgeExpiredRuns(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	result, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM runs WHERE created_at < ?`,
		timeToStr(cutoff),
	)
	if err != nil {
		return 0, fmt.Errorf("purge runs: %w", err)
	}
	return result.RowsAffected()
}
