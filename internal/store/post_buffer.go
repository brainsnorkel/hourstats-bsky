package store

import (
	"context"
	"fmt"
	"time"
)

// InsertPost inserts a single post into the buffer (upsert on URI).
func (s *Store) InsertPost(ctx context.Context, post Post) error {
	const q = `INSERT INTO post_buffer (uri, cid, text, author_did, author_handle, likes, reposts, replies, sentiment, engagement_score, created_at, inserted_at, is_reply)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uri) DO UPDATE SET
			cid=excluded.cid,
			text=excluded.text,
			author_did=excluded.author_did,
			author_handle=excluded.author_handle,
			likes=excluded.likes,
			reposts=excluded.reposts,
			replies=excluded.replies,
			sentiment=excluded.sentiment,
			engagement_score=excluded.engagement_score,
			is_reply=excluded.is_reply`

	isReply := 0
	if post.IsReply {
		isReply = 1
	}

	_, err := s.writeDB.ExecContext(ctx, q,
		post.URI, post.CID, post.Text, post.AuthorDID, post.AuthorHandle,
		post.Likes, post.Reposts, post.Replies, post.Sentiment, post.EngagementScore,
		post.CreatedAt, nowUTC(), isReply,
	)
	if err != nil {
		return fmt.Errorf("insert post: %w", err)
	}
	return nil
}

// InsertPostsBatch inserts multiple posts in a single transaction.
func (s *Store) InsertPostsBatch(ctx context.Context, posts []Post) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO post_buffer (uri, cid, text, author_did, author_handle, likes, reposts, replies, sentiment, engagement_score, created_at, inserted_at, is_reply)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uri) DO UPDATE SET
			cid=excluded.cid,
			text=excluded.text,
			author_did=excluded.author_did,
			author_handle=excluded.author_handle,
			likes=excluded.likes,
			reposts=excluded.reposts,
			replies=excluded.replies,
			sentiment=excluded.sentiment,
			engagement_score=excluded.engagement_score,
			is_reply=excluded.is_reply`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	now := nowUTC()
	for _, p := range posts {
		isReply := 0
		if p.IsReply {
			isReply = 1
		}
		_, err := stmt.ExecContext(ctx, p.URI, p.CID, p.Text, p.AuthorDID, p.AuthorHandle,
			p.Likes, p.Reposts, p.Replies, p.Sentiment, p.EngagementScore,
			p.CreatedAt, now, isReply)
		if err != nil {
			return fmt.Errorf("insert post %s: %w", p.URI, err)
		}
	}

	return tx.Commit()
}

// GetPostsSince returns all posts with created_at >= since, ordered by created_at.
func (s *Store) GetPostsSince(ctx context.Context, since time.Time) ([]Post, error) {
	const q = `SELECT uri, cid, text, author_did, author_handle, likes, reposts, replies, sentiment, engagement_score, created_at, is_reply
		FROM post_buffer
		WHERE created_at >= ?
		ORDER BY created_at ASC`

	rows, err := s.readDB.QueryContext(ctx, q, timeToStr(since))
	if err != nil {
		return nil, fmt.Errorf("query posts: %w", err)
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		var isReply int
		if err := rows.Scan(&p.URI, &p.CID, &p.Text, &p.AuthorDID, &p.AuthorHandle,
			&p.Likes, &p.Reposts, &p.Replies, &p.Sentiment, &p.EngagementScore,
			&p.CreatedAt, &isReply); err != nil {
			return nil, fmt.Errorf("scan post: %w", err)
		}
		p.IsReply = isReply != 0
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// GetPostCount returns the number of posts with created_at >= since.
func (s *Store) GetPostCount(ctx context.Context, since time.Time) (int, error) {
	var count int
	err := s.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM post_buffer WHERE created_at >= ?`,
		timeToStr(since),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count posts: %w", err)
	}
	return count, nil
}

// UpdatePostEngagement updates engagement metrics and author handle for a post.
//
// The sentiment and engagement_score columns are deliberately not touched: the
// hydrator only ever wrote the zero values the ingest INSERT already stored, and
// nothing reads them back (GetPostsSince scans them, but toAnalyzerPosts drops
// them). Rewriting them made every hydration UPDATE wider than it needed to be.
func (s *Store) UpdatePostEngagement(ctx context.Context, uri string, likes, reposts, replies int, authorHandle string) error {
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE post_buffer SET likes=?, reposts=?, replies=?, author_handle=? WHERE uri=?`,
		likes, reposts, replies, authorHandle, uri,
	)
	if err != nil {
		return fmt.Errorf("update engagement: %w", err)
	}
	return nil
}

// PurgeExpiredPosts deletes posts older than olderThan in chunks, returning the count removed.
func (s *Store) PurgeExpiredPosts(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := timeToStr(time.Now().UTC().Add(-olderThan))
	return purgeInChunks(ctx, s.writeDB,
		`DELETE FROM post_buffer WHERE rowid IN (SELECT rowid FROM post_buffer WHERE inserted_at < ? LIMIT 1000)`,
		cutoff,
	)
}
