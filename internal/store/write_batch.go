package store

import (
	"context"
	"fmt"
)

// PendingWrite holds a post and its optional topic tokens for batch insertion.
type PendingWrite struct {
	Post       Post
	TokensJSON string // empty string = no tokens for this post
	CreatedAt  string
}

// FlushWriteBatch inserts posts then tokens in separate transactions so that
// a SQLITE_BUSY on the token transaction does not discard post data.
func (s *Store) FlushWriteBatch(ctx context.Context, writes []PendingWrite) error {
	if len(writes) == 0 {
		return nil
	}

	if err := s.FlushPostBatch(ctx, writes); err != nil {
		return err
	}
	return s.FlushTokenBatch(ctx, writes)
}

// FlushPostBatch inserts posts into post_buffer in a single transaction.
func (s *Store) FlushPostBatch(ctx context.Context, writes []PendingWrite) error {
	if len(writes) == 0 {
		return nil
	}

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin post batch tx: %w", err)
	}
	defer tx.Rollback()

	// author_handle, likes, reposts and replies are deliberately absent from the
	// SET list. This is the ingest path: Jetstream events carry none of them
	// (see the store.Post built in cmd/hourstats/jetstream_consumer.go), so an
	// at-least-once re-delivery — routine after a firehose reconnect replays the
	// cursor — used to overwrite the hydrator's engagement counts with zeros and
	// blank the handle. Trending exemplar selection reads those columns.
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO post_buffer (uri, cid, text, author_did, author_handle, likes, reposts, replies, sentiment, engagement_score, created_at, inserted_at, is_reply)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uri) DO UPDATE SET
			cid=excluded.cid,
			text=excluded.text,
			author_did=excluded.author_did,
			sentiment=excluded.sentiment,
			engagement_score=excluded.engagement_score,
			is_reply=excluded.is_reply`)
	if err != nil {
		return fmt.Errorf("prepare post stmt: %w", err)
	}
	defer stmt.Close()

	now := nowUTC()
	for _, w := range writes {
		isReply := 0
		if w.Post.IsReply {
			isReply = 1
		}
		if _, err := stmt.ExecContext(ctx, w.Post.URI, w.Post.CID, w.Post.Text, w.Post.AuthorDID, w.Post.AuthorHandle,
			w.Post.Likes, w.Post.Reposts, w.Post.Replies, w.Post.Sentiment, w.Post.EngagementScore,
			w.Post.CreatedAt, now, isReply); err != nil {
			return fmt.Errorf("insert post %s: %w", w.Post.URI, err)
		}
	}

	return tx.Commit()
}

// FlushTokenBatch inserts topic tokens in a single transaction.
// token_postings is no longer maintained on the ingest hot path;
// exemplar queries use json_each on topic_tokens instead.
func (s *Store) FlushTokenBatch(ctx context.Context, writes []PendingWrite) error {
	hasTokens := false
	for _, w := range writes {
		if w.TokensJSON != "" {
			hasTokens = true
			break
		}
	}
	if !hasTokens {
		return nil
	}

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin token batch tx: %w", err)
	}
	defer tx.Rollback()

	tokenStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO topic_tokens (post_uri, tokens, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare token stmt: %w", err)
	}
	defer tokenStmt.Close()

	for _, w := range writes {
		if w.TokensJSON == "" {
			continue
		}
		if _, err := tokenStmt.ExecContext(ctx, w.Post.URI, w.TokensJSON, w.CreatedAt); err != nil {
			return fmt.Errorf("insert topic_tokens %s: %w", w.Post.URI, err)
		}
	}

	return tx.Commit()
}
