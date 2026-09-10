package store

import (
	"context"
	"database/sql"
	"fmt"
)

// WriteOp selects what a PendingWrite does to post_buffer.
type WriteOp int

const (
	// WriteInsert upserts Post into post_buffer. It is the zero value, so a
	// PendingWrite built without an Op behaves as it always has.
	WriteInsert WriteOp = iota
	// WriteDelete removes the single post identified by Post.URI, and its
	// topic tokens. Used for firehose delete commits.
	WriteDelete
	// WritePurgeAuthor removes every buffered post by Post.AuthorDID, and
	// their topic tokens. Used when an account goes inactive.
	WritePurgeAuthor
)

// PendingWrite holds a post and its optional topic tokens for batch insertion,
// or a deletion keyed by URI or author DID (see Op).
type PendingWrite struct {
	Op         WriteOp
	Post       Post
	TokensJSON string // empty string = no tokens for this post
	CreatedAt  string
}

// FlushResult reports what one post batch did to post_buffer.
type FlushResult struct {
	Inserted int64
	Deleted  int64
	Purged   int64
}

// FlushWriteBatch inserts posts then tokens in separate transactions so that
// a SQLITE_BUSY on the token transaction does not discard post data.
func (s *Store) FlushWriteBatch(ctx context.Context, writes []PendingWrite) error {
	if len(writes) == 0 {
		return nil
	}

	if _, err := s.FlushPostBatch(ctx, writes); err != nil {
		return err
	}
	return s.FlushTokenBatch(ctx, writes)
}

// FlushPostBatch applies the batch to post_buffer in a single transaction.
// Writes are applied in order, so a delete that follows an insert of the same
// URI wins, and an insert that follows a delete of it also wins — that
// ordering is what makes a firehose replay after a delete come out right.
func (s *Store) FlushPostBatch(ctx context.Context, writes []PendingWrite) (FlushResult, error) {
	var res FlushResult
	if len(writes) == 0 {
		return res, nil
	}

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("begin post batch tx: %w", err)
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
		return res, fmt.Errorf("prepare post stmt: %w", err)
	}
	defer stmt.Close()

	// The per-URI deletes run once per WriteDelete in the batch, and a
	// firehose batch routinely carries hundreds, so they are prepared once
	// for the transaction like the insert above.
	dels, err := prepareDeleteStmts(ctx, tx)
	if err != nil {
		return res, err
	}
	defer dels.close()

	now := nowUTC()
	for _, w := range writes {
		switch w.Op {
		case WriteDelete:
			n, err := deletePostByURI(ctx, dels, w.Post.URI)
			if err != nil {
				return res, err
			}
			res.Deleted += n
		case WritePurgeAuthor:
			n, err := purgeAuthorPosts(ctx, tx, w.Post.AuthorDID)
			if err != nil {
				return res, err
			}
			res.Purged += n
		default:
			isReply := 0
			if w.Post.IsReply {
				isReply = 1
			}
			if _, err := stmt.ExecContext(ctx, w.Post.URI, w.Post.CID, w.Post.Text, w.Post.AuthorDID, w.Post.AuthorHandle,
				w.Post.Likes, w.Post.Reposts, w.Post.Replies, w.Post.Sentiment, w.Post.EngagementScore,
				w.Post.CreatedAt, now, isReply); err != nil {
				return res, fmt.Errorf("insert post %s: %w", w.Post.URI, err)
			}
			res.Inserted++
		}
	}

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("commit post batch tx: %w", err)
	}
	return res, nil
}

// deleteStmts holds the three per-URI DELETEs a WriteDelete runs, prepared
// once for the enclosing transaction and reused for every delete in the batch.
type deleteStmts struct {
	post     *sql.Stmt
	tokens   *sql.Stmt
	postings *sql.Stmt
}

func prepareDeleteStmts(ctx context.Context, tx *sql.Tx) (*deleteStmts, error) {
	d := &deleteStmts{}
	var err error
	if d.post, err = tx.PrepareContext(ctx, `DELETE FROM post_buffer WHERE uri = ?`); err != nil {
		return nil, fmt.Errorf("prepare post delete stmt: %w", err)
	}
	if d.tokens, err = tx.PrepareContext(ctx, `DELETE FROM topic_tokens WHERE post_uri = ?`); err != nil {
		d.close()
		return nil, fmt.Errorf("prepare topic_tokens delete stmt: %w", err)
	}
	if d.postings, err = tx.PrepareContext(ctx, `DELETE FROM token_postings WHERE post_uri = ?`); err != nil {
		d.close()
		return nil, fmt.Errorf("prepare token_postings delete stmt: %w", err)
	}
	return d, nil
}

func (d *deleteStmts) close() {
	for _, s := range []*sql.Stmt{d.post, d.tokens, d.postings} {
		if s != nil {
			s.Close()
		}
	}
}

// deletePostByURI removes one post and its topic tokens, returning the number
// of post_buffer rows removed (0 when the post was never buffered).
//
// The token deletes run even when post_buffer removed nothing: topic_tokens is
// keyed by post_uri and kept for 26h against post_buffer's 2h, so a post
// deleted after the retention purge has no buffer row left but can still have
// tokens feeding trending. token_postings is dropped and recreated empty at
// startup, so both deletes are primary-key lookups that cost nothing when they
// match nothing.
func deletePostByURI(ctx context.Context, d *deleteStmts, uri string) (int64, error) {
	if uri == "" {
		return 0, nil
	}
	result, err := d.post.ExecContext(ctx, uri)
	if err != nil {
		return 0, fmt.Errorf("delete post %s: %w", uri, err)
	}
	n, _ := result.RowsAffected()
	if _, err := d.tokens.ExecContext(ctx, uri); err != nil {
		return 0, fmt.Errorf("delete topic_tokens for %s: %w", uri, err)
	}
	if _, err := d.postings.ExecContext(ctx, uri); err != nil {
		return 0, fmt.Errorf("delete token_postings for %s: %w", uri, err)
	}
	return n, nil
}

// purgeAuthorPosts removes every buffered post by one author and their topic
// tokens, returning the number of post_buffer rows removed. The token deletes
// run first: they resolve the author's URIs through post_buffer, which the
// last statement then empties.
//
// That resolution is also the limit of what an author purge can reach:
// topic_tokens has no author column, so tokens of posts that have already aged
// out of post_buffer (2h) but are still inside the token retention window
// (26h) are not removed here. They expire on their own schedule.
func purgeAuthorPosts(ctx context.Context, tx *sql.Tx, authorDID string) (int64, error) {
	if authorDID == "" {
		return 0, nil
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM topic_tokens WHERE post_uri IN (SELECT uri FROM post_buffer WHERE author_did = ?)`,
		authorDID); err != nil {
		return 0, fmt.Errorf("purge topic_tokens for author: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM token_postings WHERE post_uri IN (SELECT uri FROM post_buffer WHERE author_did = ?)`,
		authorDID); err != nil {
		return 0, fmt.Errorf("purge token_postings for author: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM post_buffer WHERE author_did = ?`, authorDID)
	if err != nil {
		return 0, fmt.Errorf("purge posts for author: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// FlushTokenBatch inserts topic tokens in a single transaction.
// token_postings is no longer maintained on the ingest hot path;
// exemplar queries use json_each on topic_tokens instead.
func (s *Store) FlushTokenBatch(ctx context.Context, writes []PendingWrite) error {
	hasTokens := false
	for _, w := range writes {
		if w.Op == WriteInsert && w.TokensJSON != "" {
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

	// A delete or purge later in the same batch has already removed the post
	// from post_buffer (FlushPostBatch runs first), so its tokens must not be
	// written back here — they would outlive the post for the whole token
	// retention window and still feed trending.
	superseded := supersededTokenWrites(writes)

	for i, w := range writes {
		if w.Op != WriteInsert || w.TokensJSON == "" || superseded[i] {
			continue
		}
		if _, err := tokenStmt.ExecContext(ctx, w.Post.URI, w.TokensJSON, w.CreatedAt); err != nil {
			return fmt.Errorf("insert topic_tokens %s: %w", w.Post.URI, err)
		}
	}

	return tx.Commit()
}

// supersededTokenWrites marks the indexes of insert writes whose post is
// removed again by a later delete or author purge in the same batch. It
// returns nil for the common all-inserts batch.
func supersededTokenWrites(writes []PendingWrite) map[int]bool {
	var superseded map[int]bool
	deletedURIs := make(map[string]bool)
	purgedDIDs := make(map[string]bool)

	// Walk backwards so every removal seen so far is one that happens later
	// in the batch than the insert being examined.
	for i := len(writes) - 1; i >= 0; i-- {
		w := writes[i]
		switch w.Op {
		case WriteDelete:
			deletedURIs[w.Post.URI] = true
		case WritePurgeAuthor:
			purgedDIDs[w.Post.AuthorDID] = true
		default:
			if deletedURIs[w.Post.URI] || purgedDIDs[w.Post.AuthorDID] {
				if superseded == nil {
					superseded = make(map[int]bool)
				}
				superseded[i] = true
			}
		}
	}
	return superseded
}
