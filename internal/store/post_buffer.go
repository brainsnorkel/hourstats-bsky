package store

import (
	"context"
	"fmt"
	"time"
)

// InsertPost inserts a single post into the buffer (upsert on URI).
func (s *Store) InsertPost(ctx context.Context, post Post) error {
	const q = `INSERT INTO post_buffer (uri, cid, text, author_did, author_handle, likes, reposts, replies, sentiment, engagement_score, created_at, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uri) DO UPDATE SET
			cid=excluded.cid,
			text=excluded.text,
			author_did=excluded.author_did,
			author_handle=excluded.author_handle,
			likes=excluded.likes,
			reposts=excluded.reposts,
			replies=excluded.replies,
			sentiment=excluded.sentiment,
			engagement_score=excluded.engagement_score`

	_, err := s.db.ExecContext(ctx, q,
		post.URI, post.CID, post.Text, post.AuthorDID, post.AuthorHandle,
		post.Likes, post.Reposts, post.Replies, post.Sentiment, post.EngagementScore,
		post.CreatedAt, nowUTC(),
	)
	if err != nil {
		return fmt.Errorf("insert post: %w", err)
	}
	return nil
}

// InsertPostsBatch inserts multiple posts in a single transaction.
func (s *Store) InsertPostsBatch(ctx context.Context, posts []Post) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO post_buffer (uri, cid, text, author_did, author_handle, likes, reposts, replies, sentiment, engagement_score, created_at, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uri) DO UPDATE SET
			cid=excluded.cid,
			text=excluded.text,
			author_did=excluded.author_did,
			author_handle=excluded.author_handle,
			likes=excluded.likes,
			reposts=excluded.reposts,
			replies=excluded.replies,
			sentiment=excluded.sentiment,
			engagement_score=excluded.engagement_score`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	now := nowUTC()
	for _, p := range posts {
		_, err := stmt.ExecContext(ctx, p.URI, p.CID, p.Text, p.AuthorDID, p.AuthorHandle,
			p.Likes, p.Reposts, p.Replies, p.Sentiment, p.EngagementScore,
			p.CreatedAt, now)
		if err != nil {
			return fmt.Errorf("insert post %s: %w", p.URI, err)
		}
	}

	return tx.Commit()
}

// GetPostsSince returns all posts with created_at >= since, ordered by created_at.
func (s *Store) GetPostsSince(ctx context.Context, since time.Time) ([]Post, error) {
	const q = `SELECT uri, cid, text, author_did, author_handle, likes, reposts, replies, sentiment, engagement_score, created_at
		FROM post_buffer
		WHERE created_at >= ?
		ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, q, timeToStr(since))
	if err != nil {
		return nil, fmt.Errorf("query posts: %w", err)
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.URI, &p.CID, &p.Text, &p.AuthorDID, &p.AuthorHandle,
			&p.Likes, &p.Reposts, &p.Replies, &p.Sentiment, &p.EngagementScore,
			&p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan post: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// GetPostCount returns the number of posts with created_at >= since.
func (s *Store) GetPostCount(ctx context.Context, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM post_buffer WHERE created_at >= ?`,
		timeToStr(since),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count posts: %w", err)
	}
	return count, nil
}

// UpdatePostEngagement updates engagement metrics and author handle for a post.
func (s *Store) UpdatePostEngagement(ctx context.Context, uri string, likes, reposts, replies int, authorHandle, sentiment string, engagementScore float64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE post_buffer SET likes=?, reposts=?, replies=?, author_handle=?, sentiment=?, engagement_score=? WHERE uri=?`,
		likes, reposts, replies, authorHandle, sentiment, engagementScore, uri,
	)
	if err != nil {
		return fmt.Errorf("update engagement: %w", err)
	}
	return nil
}

// PurgeExpiredPosts deletes posts older than olderThan and returns the count removed.
func (s *Store) PurgeExpiredPosts(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM post_buffer WHERE inserted_at < ?`,
		timeToStr(cutoff),
	)
	if err != nil {
		return 0, fmt.Errorf("purge posts: %w", err)
	}
	return result.RowsAffected()
}
