package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// StoreDailySentiment inserts or replaces a daily sentiment record.
func (s *Store) StoreDailySentiment(ctx context.Context, dp DailySentimentDataPoint) error {
	now := nowUTC()
	ttl := time.Now().UTC().Add(3 * 365 * 24 * time.Hour).Unix() // 3 years TTL

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO daily_sentiment (date, run_id, average_sentiment, min_sentiment, max_sentiment, q1_sentiment, median_sentiment, q3_sentiment, total_runs, total_posts, created_at, ttl)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(date) DO UPDATE SET
			run_id=excluded.run_id,
			average_sentiment=excluded.average_sentiment,
			min_sentiment=excluded.min_sentiment,
			max_sentiment=excluded.max_sentiment,
			q1_sentiment=excluded.q1_sentiment,
			median_sentiment=excluded.median_sentiment,
			q3_sentiment=excluded.q3_sentiment,
			total_runs=excluded.total_runs,
			total_posts=excluded.total_posts`,
		dp.Date, dp.RunID, dp.AverageSentiment, dp.MinSentiment, dp.MaxSentiment,
		dp.Q1Sentiment, dp.MedianSentiment, dp.Q3Sentiment,
		dp.TotalRuns, dp.TotalPosts, now, ttl,
	)
	if err != nil {
		return fmt.Errorf("store daily sentiment: %w", err)
	}
	return nil
}

// GetDailySentimentHistory returns daily sentiment records for the last N days.
func (s *Store) GetDailySentimentHistory(ctx context.Context, days int) ([]DailySentimentDataPoint, error) {
	startDate := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

	rows, err := s.db.QueryContext(ctx,
		`SELECT date, run_id, average_sentiment, min_sentiment, max_sentiment, q1_sentiment, median_sentiment, q3_sentiment, total_runs, total_posts, created_at, ttl
		 FROM daily_sentiment
		 WHERE date >= ?
		 ORDER BY date ASC`,
		startDate,
	)
	if err != nil {
		return nil, fmt.Errorf("query daily sentiment: %w", err)
	}
	defer rows.Close()

	var results []DailySentimentDataPoint
	for rows.Next() {
		var dp DailySentimentDataPoint
		var createdStr string
		if err := rows.Scan(&dp.Date, &dp.RunID, &dp.AverageSentiment,
			&dp.MinSentiment, &dp.MaxSentiment, &dp.Q1Sentiment,
			&dp.MedianSentiment, &dp.Q3Sentiment, &dp.TotalRuns, &dp.TotalPosts,
			&createdStr, &dp.TTL); err != nil {
			return nil, fmt.Errorf("scan daily sentiment: %w", err)
		}
		dp.CreatedAt = strToTime(createdStr)
		results = append(results, dp)
	}
	return results, rows.Err()
}

// GetYearlySentimentData returns 365 days of data converted to YearlySparklineDataPoint.
func (s *Store) GetYearlySentimentData(ctx context.Context) ([]YearlySparklineDataPoint, error) {
	daily, err := s.GetDailySentimentHistory(ctx, 365)
	if err != nil {
		return nil, err
	}

	var results []YearlySparklineDataPoint
	for _, d := range daily {
		t, _ := time.Parse("2006-01-02", d.Date)
		results = append(results, YearlySparklineDataPoint{
			Date:                d.Date,
			AverageSentiment:    d.AverageSentiment,
			MinSentiment:        d.MinSentiment,
			MaxSentiment:        d.MaxSentiment,
			Q1Sentiment:         d.Q1Sentiment,
			MedianSentiment:     d.MedianSentiment,
			Q3Sentiment:         d.Q3Sentiment,
			Timestamp:           t,
			NetSentimentPercent: d.AverageSentiment,
		})
	}
	return results, nil
}

// GetDailySentimentForDate retrieves the daily sentiment for a specific date.
func (s *Store) GetDailySentimentForDate(ctx context.Context, date string) (*DailySentimentDataPoint, error) {
	var dp DailySentimentDataPoint
	var createdStr string

	err := s.db.QueryRowContext(ctx,
		`SELECT date, run_id, average_sentiment, min_sentiment, max_sentiment, q1_sentiment, median_sentiment, q3_sentiment, total_runs, total_posts, created_at, ttl
		 FROM daily_sentiment WHERE date = ?`,
		date,
	).Scan(&dp.Date, &dp.RunID, &dp.AverageSentiment,
		&dp.MinSentiment, &dp.MaxSentiment, &dp.Q1Sentiment,
		&dp.MedianSentiment, &dp.Q3Sentiment, &dp.TotalRuns, &dp.TotalPosts,
		&createdStr, &dp.TTL)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("daily sentiment not found for date: %s", date)
	}
	if err != nil {
		return nil, fmt.Errorf("get daily sentiment: %w", err)
	}

	dp.CreatedAt = strToTime(createdStr)
	return &dp, nil
}

func (s *Store) GetWeeklyPostTotals(ctx context.Context) ([]WeeklyPostTotal, error) {
	now := time.Now().UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	currentWeekStart := now.AddDate(0, 0, -(weekday - 1)).Truncate(24 * time.Hour)

	rows, err := s.db.QueryContext(ctx,
		`SELECT strftime('%Y-%m-%d', timestamp) AS day,
		        SUM(total_posts) AS en_total,
		        SUM(total_firehose_posts) AS firehose_total
		 FROM sentiment_history
		 WHERE timestamp < ?
		 GROUP BY day
		 ORDER BY day ASC`,
		timeToStr(currentWeekStart),
	)
	if err != nil {
		return nil, fmt.Errorf("query weekly post totals: %w", err)
	}
	defer rows.Close()

	weekMap := make(map[string]*WeeklyPostTotal)
	var weekOrder []string
	for rows.Next() {
		var dateStr string
		var enPosts, firehosePosts int
		if err := rows.Scan(&dateStr, &enPosts, &firehosePosts); err != nil {
			return nil, fmt.Errorf("scan daily post total: %w", err)
		}
		t, _ := time.Parse("2006-01-02", dateStr)
		wd := int(t.Weekday())
		if wd == 0 {
			wd = 7
		}
		monday := t.AddDate(0, 0, -(wd - 1))
		key := monday.Format("2006-01-02")
		if _, ok := weekMap[key]; !ok {
			weekMap[key] = &WeeklyPostTotal{WeekStart: monday}
			weekOrder = append(weekOrder, key)
		}
		weekMap[key].Count += enPosts
		weekMap[key].TotalFirehosePosts += firehosePosts
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	results := make([]WeeklyPostTotal, 0, len(weekOrder))
	for _, key := range weekOrder {
		results = append(results, *weekMap[key])
	}
	return results, nil
}
