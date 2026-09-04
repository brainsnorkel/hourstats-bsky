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

	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO sentiment_history (run_id, timestamp, average_compound_score, net_sentiment_percent, sentiment_category, total_posts, total_firehose_posts, root_sentiment_pct, reply_sentiment_pct, top_topic, created_at, ttl)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(run_id, timestamp) DO UPDATE SET
			average_compound_score=excluded.average_compound_score,
			net_sentiment_percent=excluded.net_sentiment_percent,
			sentiment_category=excluded.sentiment_category,
			total_posts=excluded.total_posts,
			total_firehose_posts=excluded.total_firehose_posts,
			root_sentiment_pct=excluded.root_sentiment_pct,
			reply_sentiment_pct=excluded.reply_sentiment_pct,
			top_topic=CASE WHEN excluded.top_topic = '' THEN sentiment_history.top_topic ELSE excluded.top_topic END`,
		// top_topic: SetSentimentTopTopic is the only production writer; the
		// CASE keeps a re-store of the same point from erasing its label.
		dp.RunID, timeToStr(dp.Timestamp), dp.AverageCompoundScore,
		dp.NetSentimentPercent, dp.SentimentCategory, dp.TotalPosts,
		dp.TotalFirehosePosts, dp.RootSentimentPct, dp.ReplySentimentPct,
		dp.TopTopic, now, ttl,
	)
	if err != nil {
		return fmt.Errorf("store sentiment: %w", err)
	}
	return nil
}

// SetSentimentTopTopic records the rank-1 trending topic label against the
// sentiment row for runID and reports whether a row was updated. The
// sentiment point is stored before topic analysis is collected, so the label
// is attached in a second step.
func (s *Store) SetSentimentTopTopic(ctx context.Context, runID, label string) (bool, error) {
	result, err := s.writeDB.ExecContext(ctx,
		`UPDATE sentiment_history SET top_topic = ? WHERE run_id = ?`, label, runID)
	if err != nil {
		return false, fmt.Errorf("set sentiment top topic: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("set sentiment top topic rows: %w", err)
	}
	return n > 0, nil
}

// GetSentimentHistory returns sentiment data points from the last `duration`,
// ordered by timestamp ascending.
func (s *Store) GetSentimentHistory(ctx context.Context, duration time.Duration) ([]SentimentDataPoint, error) {
	since := time.Now().UTC().Add(-duration)

	rows, err := s.readDB.QueryContext(ctx,
		`SELECT run_id, timestamp, average_compound_score, net_sentiment_percent, sentiment_category, total_posts, total_firehose_posts, root_sentiment_pct, reply_sentiment_pct, COALESCE(top_topic, ''), created_at, ttl
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
			&dp.TotalFirehosePosts, &dp.RootSentimentPct, &dp.ReplySentimentPct,
			&dp.TopTopic, &createdStr, &dp.TTL); err != nil {
			return nil, fmt.Errorf("scan sentiment: %w", err)
		}
		dp.Timestamp = strToTime(tsStr)
		dp.CreatedAt = strToTime(createdStr)
		results = append(results, dp)
	}
	return results, rows.Err()
}

// GetDailyPostCounts returns the total posts per completed UTC day
// within the given duration. The current (incomplete) day is excluded.
func (s *Store) GetDailyPostCounts(ctx context.Context, duration time.Duration) ([]DailyPostCount, error) {
	since := time.Now().UTC().Add(-duration)
	today := time.Now().UTC().Truncate(24 * time.Hour)

	rows, err := s.readDB.QueryContext(ctx,
		`SELECT strftime('%Y-%m-%d', timestamp) AS day_bucket,
		        SUM(total_posts) AS total,
		        SUM(total_firehose_posts) AS total_firehose
		 FROM sentiment_history
		 WHERE timestamp >= ? AND timestamp < ?
		 GROUP BY day_bucket
		 ORDER BY day_bucket ASC`,
		timeToStr(since), timeToStr(today),
	)
	if err != nil {
		return nil, fmt.Errorf("query daily post counts: %w", err)
	}
	defer rows.Close()

	var results []DailyPostCount
	for rows.Next() {
		var dateStr string
		var count, firehoseCount int
		if err := rows.Scan(&dateStr, &count, &firehoseCount); err != nil {
			return nil, fmt.Errorf("scan daily post count: %w", err)
		}
		t, _ := time.Parse("2006-01-02", dateStr)
		results = append(results, DailyPostCount{Date: t, Count: count, TotalFirehosePosts: firehoseCount})
	}
	return results, rows.Err()
}

// PurgeExpiredSentimentHistory deletes sentiment records older than olderThan.
func (s *Store) PurgeExpiredSentimentHistory(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	result, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM sentiment_history WHERE timestamp < ?`,
		timeToStr(cutoff),
	)
	if err != nil {
		return 0, fmt.Errorf("purge sentiment: %w", err)
	}
	return result.RowsAffected()
}
