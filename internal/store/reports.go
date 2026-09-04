package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Rollups behind the weekly and monthly report posts. topic_snapshots and
// runs are purged after 48h, so the daily cycle condenses each finished UTC
// day into topic_daily and daily_top_post, which keep 400 days.

// dayBounds returns the RFC3339 timestamps [start, end) covering a
// YYYY-MM-DD date in UTC.
func dayBounds(date string) (string, string, error) {
	start, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", "", fmt.Errorf("parse date %q: %w", date, err)
	}
	return start.Format(time.RFC3339), start.Add(24 * time.Hour).Format(time.RFC3339), nil
}

// The SELECT below keeps its WHERE clause on purpose: SQLite's grammar needs
// one to tell the upsert's ON CONFLICT apart from a join's ON.
//
// RollupTopicDaily condenses the day's topic_snapshots into topic_daily and
// returns how many topics were written. It merges with any existing rows for
// the date rather than replacing them, so a re-run over a partially purged
// window (appearances can only be undercounted, never overcounted) keeps the
// better figures: max appearances, min best_rank, max authors, latest label.
func (s *Store) RollupTopicDaily(ctx context.Context, date string) (int64, error) {
	start, end, err := dayBounds(date)
	if err != nil {
		return 0, err
	}
	result, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO topic_daily (date, topic_id, label, appearances, best_rank, max_authors)
		 SELECT ?, t.topic_id,
		        (SELECT label FROM topic_snapshots l
		          WHERE l.topic_id = t.topic_id AND l.snapshot_time >= ? AND l.snapshot_time < ?
		          ORDER BY l.snapshot_time DESC LIMIT 1),
		        COUNT(DISTINCT t.snapshot_time), MIN(t.rank), MAX(t.unique_author_count)
		 FROM topic_snapshots t
		 WHERE t.snapshot_time >= ? AND t.snapshot_time < ?
		 GROUP BY t.topic_id
		 ON CONFLICT(date, topic_id) DO UPDATE SET
			label=excluded.label,
			appearances=MAX(topic_daily.appearances, excluded.appearances),
			best_rank=MIN(topic_daily.best_rank, excluded.best_rank),
			max_authors=MAX(topic_daily.max_authors, excluded.max_authors)`,
		date, start, end, start, end,
	)
	if err != nil {
		return 0, fmt.Errorf("rollup topic_daily %s: %w", date, err)
	}
	return result.RowsAffected()
}

// GetTopicDaily returns the rolled-up topics for one date, best first.
func (s *Store) GetTopicDaily(ctx context.Context, date string) ([]TopicDailyRow, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT date, topic_id, label, appearances, best_rank, max_authors
		 FROM topic_daily WHERE date = ?
		 ORDER BY appearances DESC, best_rank ASC, topic_id ASC`, date)
	if err != nil {
		return nil, fmt.Errorf("query topic_daily %s: %w", date, err)
	}
	defer rows.Close()
	var out []TopicDailyRow
	for rows.Next() {
		var r TopicDailyRow
		if err := rows.Scan(&r.Date, &r.TopicID, &r.Label, &r.Appearances, &r.BestRank, &r.MaxAuthors); err != nil {
			return nil, fmt.Errorf("scan topic_daily: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetTopTopicForRange returns the topic that appeared in the most hourly
// snapshots between startDate and endDate inclusive, with its total
// appearances. Ties go to the better peak rank. label is "" when the range
// holds no topic data.
func (s *Store) GetTopTopicForRange(ctx context.Context, startDate, endDate string) (label string, appearances int, err error) {
	var topicID string
	var bestRank int
	err = s.readDB.QueryRowContext(ctx,
		`SELECT topic_id, SUM(appearances), MIN(best_rank)
		 FROM topic_daily
		 WHERE date >= ? AND date <= ?
		 GROUP BY topic_id
		 ORDER BY SUM(appearances) DESC, MIN(best_rank) ASC, topic_id ASC
		 LIMIT 1`, startDate, endDate,
	).Scan(&topicID, &appearances, &bestRank)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("query top topic %s..%s: %w", startDate, endDate, err)
	}
	// The label may have been refined over the range; use the latest one.
	err = s.readDB.QueryRowContext(ctx,
		`SELECT label FROM topic_daily
		 WHERE topic_id = ? AND date >= ? AND date <= ?
		 ORDER BY date DESC LIMIT 1`, topicID, startDate, endDate,
	).Scan(&label)
	if err != nil {
		return "", 0, fmt.Errorf("query top topic label %s: %w", topicID, err)
	}
	return label, appearances, nil
}

// StoreDailyTopPost records the most engaged post of a date. A re-run only
// replaces the stored row when it found a higher engagement score, so a
// partial window never demotes a complete one.
func (s *Store) StoreDailyTopPost(ctx context.Context, date string, p Post) error {
	if _, _, err := dayBounds(date); err != nil {
		return err
	}
	if p.URI == "" || p.CID == "" {
		return fmt.Errorf("store daily top post %s: post has no URI/CID", date)
	}
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO daily_top_post (date, uri, cid, author_handle, likes, reposts, replies, engagement_score)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(date) DO UPDATE SET
			uri=excluded.uri, cid=excluded.cid, author_handle=excluded.author_handle,
			likes=excluded.likes, reposts=excluded.reposts, replies=excluded.replies,
			engagement_score=excluded.engagement_score
		 WHERE excluded.engagement_score > daily_top_post.engagement_score`,
		date, p.URI, p.CID, p.AuthorHandle, p.Likes, p.Reposts, p.Replies, p.EngagementScore,
	)
	if err != nil {
		return fmt.Errorf("store daily top post %s: %w", date, err)
	}
	return nil
}

// GetTopPostForRange returns the highest-scoring daily top post between
// startDate and endDate inclusive, or nil when none is stored.
func (s *Store) GetTopPostForRange(ctx context.Context, startDate, endDate string) (*Post, error) {
	var p Post
	err := s.readDB.QueryRowContext(ctx,
		`SELECT uri, cid, author_handle, likes, reposts, replies, engagement_score
		 FROM daily_top_post
		 WHERE date >= ? AND date <= ?
		 ORDER BY engagement_score DESC, date DESC LIMIT 1`, startDate, endDate,
	).Scan(&p.URI, &p.CID, &p.AuthorHandle, &p.Likes, &p.Reposts, &p.Replies, &p.EngagementScore)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query top post %s..%s: %w", startDate, endDate, err)
	}
	return &p, nil
}

// HasDailyTopPost reports whether a top post is stored for the date.
func (s *Store) HasDailyTopPost(ctx context.Context, date string) (bool, error) {
	var n int
	if err := s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM daily_top_post WHERE date = ?`, date).Scan(&n); err != nil {
		return false, fmt.Errorf("count daily_top_post %s: %w", date, err)
	}
	return n > 0, nil
}

// UpdateDailyFirehoseTotal sets the firehose volume on an existing
// daily_sentiment row, used to backfill rows written before the column
// existed. It reports whether a row was updated.
func (s *Store) UpdateDailyFirehoseTotal(ctx context.Context, date string, total int) (bool, error) {
	result, err := s.writeDB.ExecContext(ctx,
		`UPDATE daily_sentiment SET total_firehose_posts = ? WHERE date = ?`, total, date)
	if err != nil {
		return false, fmt.Errorf("update daily firehose total %s: %w", date, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetDailySentimentMissingFirehose returns daily rows on or after startDate
// whose firehose total is still zero, oldest first.
func (s *Store) GetDailySentimentMissingFirehose(ctx context.Context, startDate string) ([]DailySentimentDataPoint, error) {
	return s.queryDailySentiment(ctx,
		`SELECT `+dailySentimentColumns+`
		 FROM daily_sentiment
		 WHERE date >= ? AND COALESCE(total_firehose_posts, 0) = 0
		 ORDER BY date ASC`, startDate)
}

// DayCycleTotals sums one UTC day's sentiment_history cycles that meet the
// minimum post count: how many cycles, their English posts and their
// firehose posts. Matching Cycles and EnglishPosts against the daily row
// proves the day is fully covered.
type DayCycleTotals struct {
	Cycles        int
	EnglishPosts  int
	FirehosePosts int
}

func (s *Store) GetDayCycleTotals(ctx context.Context, date string, minPosts int) (DayCycleTotals, error) {
	start, end, err := dayBounds(date)
	if err != nil {
		return DayCycleTotals{}, err
	}
	var t DayCycleTotals
	err = s.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(total_posts), 0), COALESCE(SUM(total_firehose_posts), 0)
		 FROM sentiment_history
		 WHERE timestamp >= ? AND timestamp < ? AND total_posts >= ?`,
		start, end, minPosts,
	).Scan(&t.Cycles, &t.EnglishPosts, &t.FirehosePosts)
	if err != nil {
		return DayCycleTotals{}, fmt.Errorf("day cycle totals %s: %w", date, err)
	}
	return t, nil
}

// GetDatesMissingTopPost returns daily_sentiment dates on or after startDate
// that have no daily_top_post row, oldest first.
func (s *Store) GetDatesMissingTopPost(ctx context.Context, startDate string) ([]string, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT date FROM daily_sentiment
		 WHERE date >= ? AND date NOT IN (SELECT date FROM daily_top_post)
		 ORDER BY date ASC`, startDate)
	if err != nil {
		return nil, fmt.Errorf("query dates missing top post: %w", err)
	}
	defer rows.Close()
	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("scan date: %w", err)
		}
		dates = append(dates, d)
	}
	return dates, rows.Err()
}

// GetEarliestRunDate returns the YYYY-MM-DD of the oldest runs row, or ""
// when there are none.
func (s *Store) GetEarliestRunDate(ctx context.Context) (string, error) {
	var earliest sql.NullString
	if err := s.readDB.QueryRowContext(ctx, `SELECT MIN(created_at) FROM runs`).Scan(&earliest); err != nil {
		return "", fmt.Errorf("query earliest run: %w", err)
	}
	if !earliest.Valid || len(earliest.String) < 10 {
		return "", nil
	}
	return earliest.String[:10], nil
}

// PurgeReportRollups deletes topic_daily and daily_top_post rows older than
// olderThan and returns the total removed.
func (s *Store) PurgeReportRollups(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format("2006-01-02")
	var total int64
	for _, table := range []string{"topic_daily", "daily_top_post", "language_daily"} {
		result, err := s.writeDB.ExecContext(ctx, `DELETE FROM `+table+` WHERE date < ?`, cutoff)
		if err != nil {
			return total, fmt.Errorf("purge %s: %w", table, err)
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return total, nil
}
