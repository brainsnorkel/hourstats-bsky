package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// PendingWrite holds a post and its optional topic tokens for batch insertion.
type PendingWrite struct {
	Post       Post
	TokensJSON string // empty string = no tokens for this post
	CreatedAt  string
}

// FlushWriteBatch inserts all pending writes in a single transaction.
// Posts go to post_buffer (upsert), tokens to topic_tokens + token_postings.
func (s *Store) FlushWriteBatch(ctx context.Context, writes []PendingWrite) error {
	if len(writes) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin batch tx: %w", err)
	}
	defer tx.Rollback()

	postStmt, err := tx.PrepareContext(ctx, `INSERT INTO post_buffer (uri, cid, text, author_did, author_handle, likes, reposts, replies, sentiment, engagement_score, created_at, inserted_at, is_reply)
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
		return fmt.Errorf("prepare post stmt: %w", err)
	}
	defer postStmt.Close()

	tokenStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO topic_tokens (post_uri, tokens, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare token stmt: %w", err)
	}
	defer tokenStmt.Close()

	postingStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO token_postings (token, post_uri, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare posting stmt: %w", err)
	}
	defer postingStmt.Close()

	now := nowUTC()
	for _, w := range writes {
		isReply := 0
		if w.Post.IsReply {
			isReply = 1
		}
		if _, err := postStmt.ExecContext(ctx, w.Post.URI, w.Post.CID, w.Post.Text, w.Post.AuthorDID, w.Post.AuthorHandle,
			w.Post.Likes, w.Post.Reposts, w.Post.Replies, w.Post.Sentiment, w.Post.EngagementScore,
			w.Post.CreatedAt, now, isReply); err != nil {
			return fmt.Errorf("insert post %s: %w", w.Post.URI, err)
		}

		if w.TokensJSON == "" {
			continue
		}

		if _, err := tokenStmt.ExecContext(ctx, w.Post.URI, w.TokensJSON, w.CreatedAt); err != nil {
			return fmt.Errorf("insert topic_tokens %s: %w", w.Post.URI, err)
		}

		var tokens []string
		if err := json.Unmarshal([]byte(w.TokensJSON), &tokens); err != nil {
			continue
		}
		for _, tok := range tokens {
			if _, err := postingStmt.ExecContext(ctx, tok, w.Post.URI, w.CreatedAt); err != nil {
				return fmt.Errorf("insert token_posting: %w", err)
			}
		}
	}

	return tx.Commit()
}
