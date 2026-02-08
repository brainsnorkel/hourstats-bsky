package store

import (
	"context"
	"fmt"
	"time"
)

// StoreSentimentDataPoint inserts or replaces a sentiment data point.
func (s *Store) StoreSentimentDataPoint(ctx context.Context, dp SentimentDataPoint) error {
	now := nowUTC()
	ttl := time.Now().UTC().Add(8 * 24 * time.Hour).Unix() // 8 days TTL

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sentiment_history (run_id, timestamp, average_compound_score, net_sentiment_percent, sentiment_category, total_posts, created_at, ttl)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(run_id, timestamp) DO UPDATE SET
			average_compound_score=excluded.average_compound_score,
			net_sentiment_percent=excluded.net_sentiment_percent,
			sentiment_category=excluded.sentiment_category,
			total_posts=excluded.total_posts`,
		dp.RunID, timeToStr(dp.Timestamp), dp.AverageCompoundScore,
		dp.NetSentimentPercent, dp.SentimentCategory, dp.TotalPosts,
		now, ttl,
	)
	if err != nil {
		return fmt.Errorf("store sentiment: %w", err)
	}
	return nil
}

// GetSentimentHistory returns sentiment data points from the last `duration`,
// ordered by timestamp ascending.
func (s *Store) GetSentimentHistory(ctx context.Context, duration time.Duration) ([]SentimentDataPoint, error) {
	since := time.Now().UTC().Add(-duration)

	rows, err := s.db.QueryContext(ctx,
		`SELECT run_id, timestamp, average_compound_score, net_sentiment_percent, sentiment_category, total_posts, created_at, ttl
		 FROM sentiment_history
		 WHERE timestamp >= ?
		 ORDER BY timestamp ASC`,
		timeToStr(since),
	)
	if err != nil {
		return nil, fmt.Errorf("query sentiment history: %w", err)
	}
	defer rows.Close()

	var results []SentimentDataPoint
	for rows.Next() {
		var dp SentimentDataPoint
		var tsStr, createdStr string
		if err := rows.Scan(&dp.RunID, &tsStr, &dp.AverageCompoundScore,
			&dp.NetSentimentPercent, &dp.SentimentCategory, &dp.TotalPosts,
			&createdStr, &dp.TTL); err != nil {
			return nil, fmt.Errorf("scan sentiment: %w", err)
		}
		dp.Timestamp = strToTime(tsStr)
		dp.CreatedAt = strToTime(createdStr)
		results = append(results, dp)
	}
	return results, rows.Err()
}

// PurgeExpiredSentimentHistory deletes sentiment records older than olderThan.
func (s *Store) PurgeExpiredSentimentHistory(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM sentiment_history WHERE timestamp < ?`,
		timeToStr(cutoff),
	)
	if err != nil {
		return 0, fmt.Errorf("purge sentiment: %w", err)
	}
	return result.RowsAffected()
}
