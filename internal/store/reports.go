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
		`SELECT uri, cid, author_handle, likes, reposts, replies, engagement_score, date
		 FROM daily_top_post
		 WHERE date >= ? AND date <= ?
		 ORDER BY engagement_score DESC, date DESC LIMIT 1`, startDate, endDate,
	).Scan(&p.URI, &p.CID, &p.AuthorHandle, &p.Likes, &p.Reposts, &p.Replies, &p.EngagementScore, &p.CreatedAt)
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

// PurgeReportRollups deletes topic_daily and daily_top_post rows older than
// olderThan and returns the total removed.
func (s *Store) PurgeReportRollups(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format("2006-01-02")
	var total int64
	for _, table := range []string{"topic_daily", "daily_top_post"} {
		result, err := s.writeDB.ExecContext(ctx, `DELETE FROM `+table+` WHERE date < ?`, cutoff)
		if err != nil {
			return total, fmt.Errorf("purge %s: %w", table, err)
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return total, nil
}
