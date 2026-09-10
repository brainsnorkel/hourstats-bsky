package store

import (
	"context"
	"testing"
	"time"
)

func TestFlushWriteBatch_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.FlushWriteBatch(ctx, nil); err != nil {
		t.Fatalf("FlushWriteBatch(nil): %v", err)
	}
	if err := s.FlushWriteBatch(ctx, []PendingWrite{}); err != nil {
		t.Fatalf("FlushWriteBatch(empty): %v", err)
	}
}

func TestFlushWriteBatch_PostsOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	writes := []PendingWrite{
		{
			Post: Post{
				URI: "at://did:plc:a/app.bsky.feed.post/1", CID: "cid1",
				Text: "hello", AuthorDID: "did:plc:a", CreatedAt: now.Format(time.RFC3339),
			},
			CreatedAt: now.Format(time.RFC3339),
		},
		{
			Post: Post{
				URI: "at://did:plc:b/app.bsky.feed.post/2", CID: "cid2",
				Text: "world", AuthorDID: "did:plc:b", CreatedAt: now.Format(time.RFC3339),
				IsReply: true,
			},
			CreatedAt: now.Format(time.RFC3339),
		},
	}

	if err := s.FlushWriteBatch(ctx, writes); err != nil {
		t.Fatalf("FlushWriteBatch: %v", err)
	}

	posts, err := s.GetPostsSince(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostsSince: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}

	var tokenCount int
	s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM topic_tokens`).Scan(&tokenCount)
	if tokenCount != 0 {
		t.Errorf("expected 0 topic_tokens rows, got %d", tokenCount)
	}
}

func TestFlushWriteBatch_WithTokens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	writes := []PendingWrite{
		{
			Post: Post{
				URI: "at://did:plc:a/app.bsky.feed.post/1", CID: "cid1",
				Text: "trending topic here", AuthorDID: "did:plc:a", CreatedAt: now.Format(time.RFC3339),
			},
			TokensJSON: `["trending","topic"]`,
			CreatedAt:  now.Format(time.RFC3339),
		},
		{
			Post: Post{
				URI: "at://did:plc:b/app.bsky.feed.post/2", CID: "cid2",
				Text: "no tokens", AuthorDID: "did:plc:b", CreatedAt: now.Format(time.RFC3339),
			},
			CreatedAt: now.Format(time.RFC3339),
		},
		{
			Post: Post{
				URI: "at://did:plc:c/app.bsky.feed.post/3", CID: "cid3",
				Text: "more tokens", AuthorDID: "did:plc:c", CreatedAt: now.Format(time.RFC3339),
			},
			TokensJSON: `["more","tokens","here"]`,
			CreatedAt:  now.Format(time.RFC3339),
		},
	}

	if err := s.FlushWriteBatch(ctx, writes); err != nil {
		t.Fatalf("FlushWriteBatch: %v", err)
	}

	posts, err := s.GetPostsSince(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostsSince: %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("expected 3 posts, got %d", len(posts))
	}

	var topicCount int
	s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM topic_tokens`).Scan(&topicCount)
	if topicCount != 2 {
		t.Errorf("expected 2 topic_tokens rows, got %d", topicCount)
	}

	var postingCount int
	s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM token_postings`).Scan(&postingCount)
	if postingCount != 0 {
		t.Errorf("expected 0 token_postings rows (no longer written on ingest), got %d", postingCount)
	}
}

func TestFlushWriteBatch_Upsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	uri := "at://did:plc:a/app.bsky.feed.post/1"

	first := []PendingWrite{{
		Post: Post{
			URI: uri, CID: "cid1", Text: "original",
			AuthorDID: "did:plc:a", CreatedAt: now.Format(time.RFC3339),
		},
		CreatedAt: now.Format(time.RFC3339),
	}}
	if err := s.FlushWriteBatch(ctx, first); err != nil {
		t.Fatalf("first flush: %v", err)
	}

	second := []PendingWrite{{
		Post: Post{
			URI: uri, CID: "cid1-updated", Text: "updated",
			AuthorDID: "did:plc:a", CreatedAt: now.Format(time.RFC3339),
		},
		CreatedAt: now.Format(time.RFC3339),
	}}
	if err := s.FlushWriteBatch(ctx, second); err != nil {
		t.Fatalf("second flush: %v", err)
	}

	posts, err := s.GetPostsSince(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostsSince: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post (upsert), got %d", len(posts))
	}
	if posts[0].CID != "cid1-updated" {
		t.Errorf("CID = %q, want cid1-updated (upsert should update)", posts[0].CID)
	}
}

// TestFlushWriteBatch_UpsertPreservesHydration covers the at-least-once nature
// of Jetstream: a reconnect replays the cursor and re-delivers posts we already
// hydrated. The ingest event carries no engagement and no handle, so the upsert
// must leave those columns alone rather than zeroing them.
func TestFlushWriteBatch_UpsertPreservesHydration(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	uri := "at://did:plc:a/app.bsky.feed.post/1"

	ingest := []PendingWrite{{
		Post: Post{
			URI: uri, CID: "cid1", Text: "original",
			AuthorDID: "did:plc:a", CreatedAt: now.Format(time.RFC3339),
		},
		CreatedAt: now.Format(time.RFC3339),
	}}
	if err := s.FlushWriteBatch(ctx, ingest); err != nil {
		t.Fatalf("first flush: %v", err)
	}

	if err := s.UpdatePostEngagement(ctx, uri, 42, 7, 3, "alice.bsky.social"); err != nil {
		t.Fatalf("UpdatePostEngagement: %v", err)
	}

	// Jetstream re-delivers the same post: same zero-valued engagement fields.
	redelivery := []PendingWrite{{
		Post: Post{
			URI: uri, CID: "cid1", Text: "original",
			AuthorDID: "did:plc:a", CreatedAt: now.Format(time.RFC3339),
		},
		CreatedAt: now.Format(time.RFC3339),
	}}
	if err := s.FlushWriteBatch(ctx, redelivery); err != nil {
		t.Fatalf("redelivery flush: %v", err)
	}

	posts, err := s.GetPostsSince(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostsSince: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post (upsert), got %d", len(posts))
	}
	got := posts[0]
	if got.AuthorHandle != "alice.bsky.social" {
		t.Errorf("AuthorHandle = %q, want alice.bsky.social (re-delivery must not blank it)", got.AuthorHandle)
	}
	if got.Likes != 42 || got.Reposts != 7 || got.Replies != 3 {
		t.Errorf("engagement = likes:%d reposts:%d replies:%d, want 42/7/3 (re-delivery must not zero it)",
			got.Likes, got.Reposts, got.Replies)
	}
}

// insertWrite builds a plain ingest write for the batch tests below.
func insertWrite(uri, authorDID, tokensJSON string, now time.Time) PendingWrite {
	return PendingWrite{
		Post: Post{
			URI: uri, CID: "cid", Text: "text", AuthorDID: authorDID,
			CreatedAt: now.Format(time.RFC3339),
		},
		TokensJSON: tokensJSON,
		CreatedAt:  now.Format(time.RFC3339),
	}
}

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.readDB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestFlushPostBatch_DeleteAfterInsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	uriA := "at://did:plc:a/app.bsky.feed.post/1"
	uriB := "at://did:plc:b/app.bsky.feed.post/2"

	res, err := s.FlushPostBatch(ctx, []PendingWrite{
		insertWrite(uriA, "did:plc:a", "", now),
		insertWrite(uriB, "did:plc:b", "", now),
		{Op: WriteDelete, Post: Post{URI: uriA}},
	})
	if err != nil {
		t.Fatalf("FlushPostBatch: %v", err)
	}
	if res.Inserted != 2 || res.Deleted != 1 || res.Purged != 0 {
		t.Errorf("result = %+v, want {Inserted:2 Deleted:1 Purged:0}", res)
	}

	posts, err := s.GetPostsSince(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostsSince: %v", err)
	}
	if len(posts) != 1 || posts[0].URI != uriB {
		t.Fatalf("posts = %+v, want only %s", posts, uriB)
	}
}

// Order within the batch decides the outcome: a re-delivered create after a
// delete (the cursor rewind case) must leave the post in place.
func TestFlushPostBatch_InsertAfterDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	uriA := "at://did:plc:a/app.bsky.feed.post/1"
	res, err := s.FlushPostBatch(ctx, []PendingWrite{
		insertWrite(uriA, "did:plc:a", "", now),
		{Op: WriteDelete, Post: Post{URI: uriA}},
		insertWrite(uriA, "did:plc:a", "", now),
	})
	if err != nil {
		t.Fatalf("FlushPostBatch: %v", err)
	}
	if res.Inserted != 2 || res.Deleted != 1 {
		t.Errorf("result = %+v, want Inserted 2 and Deleted 1", res)
	}

	posts, err := s.GetPostsSince(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostsSince: %v", err)
	}
	if len(posts) != 1 || posts[0].URI != uriA {
		t.Fatalf("posts = %+v, want %s to survive", posts, uriA)
	}
}

func TestFlushPostBatch_DeleteRemovesTokens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	uriA := "at://did:plc:a/app.bsky.feed.post/1"
	uriB := "at://did:plc:b/app.bsky.feed.post/2"

	if err := s.FlushWriteBatch(ctx, []PendingWrite{
		insertWrite(uriA, "did:plc:a", `["alpha"]`, now),
		insertWrite(uriB, "did:plc:b", `["beta"]`, now),
	}); err != nil {
		t.Fatalf("FlushWriteBatch: %v", err)
	}
	if _, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO token_postings (token, post_uri, created_at) VALUES (?, ?, ?), (?, ?, ?)`,
		"alpha", uriA, now.Format(time.RFC3339), "beta", uriB, now.Format(time.RFC3339)); err != nil {
		t.Fatalf("seed token_postings: %v", err)
	}

	if _, err := s.FlushPostBatch(ctx, []PendingWrite{{Op: WriteDelete, Post: Post{URI: uriA}}}); err != nil {
		t.Fatalf("FlushPostBatch(delete): %v", err)
	}

	if n := countRows(t, s, `SELECT COUNT(*) FROM topic_tokens WHERE post_uri = ?`, uriA); n != 0 {
		t.Errorf("topic_tokens for deleted post = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM token_postings WHERE post_uri = ?`, uriA); n != 0 {
		t.Errorf("token_postings for deleted post = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM topic_tokens WHERE post_uri = ?`, uriB); n != 1 {
		t.Errorf("topic_tokens for untouched post = %d, want 1", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM token_postings WHERE post_uri = ?`, uriB); n != 1 {
		t.Errorf("token_postings for untouched post = %d, want 1", n)
	}
}

func TestFlushPostBatch_PurgeAuthor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	gone1 := "at://did:plc:gone/app.bsky.feed.post/1"
	gone2 := "at://did:plc:gone/app.bsky.feed.post/2"
	kept := "at://did:plc:keep/app.bsky.feed.post/3"

	if err := s.FlushWriteBatch(ctx, []PendingWrite{
		insertWrite(gone1, "did:plc:gone", `["alpha"]`, now),
		insertWrite(gone2, "did:plc:gone", `["beta"]`, now),
		insertWrite(kept, "did:plc:keep", `["gamma"]`, now),
	}); err != nil {
		t.Fatalf("FlushWriteBatch: %v", err)
	}
	if _, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO token_postings (token, post_uri, created_at) VALUES (?, ?, ?), (?, ?, ?), (?, ?, ?)`,
		"alpha", gone1, now.Format(time.RFC3339),
		"beta", gone2, now.Format(time.RFC3339),
		"gamma", kept, now.Format(time.RFC3339)); err != nil {
		t.Fatalf("seed token_postings: %v", err)
	}

	res, err := s.FlushPostBatch(ctx, []PendingWrite{
		{Op: WritePurgeAuthor, Post: Post{AuthorDID: "did:plc:gone"}},
	})
	if err != nil {
		t.Fatalf("FlushPostBatch(purge): %v", err)
	}
	if res.Purged != 2 || res.Deleted != 0 || res.Inserted != 0 {
		t.Errorf("result = %+v, want {Inserted:0 Deleted:0 Purged:2}", res)
	}

	posts, err := s.GetPostsSince(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetPostsSince: %v", err)
	}
	if len(posts) != 1 || posts[0].URI != kept {
		t.Fatalf("posts = %+v, want only %s", posts, kept)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM topic_tokens`); n != 1 {
		t.Errorf("topic_tokens rows = %d, want 1", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM token_postings`); n != 1 {
		t.Errorf("token_postings rows = %d, want 1", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM topic_tokens WHERE post_uri = ?`, kept); n != 1 {
		t.Errorf("topic_tokens for the other author = %d, want 1", n)
	}
}

// FlushTokenBatch must not resurrect tokens for a post the same batch deleted.
func TestFlushWriteBatch_TokensSkippedForDeletedPost(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	uriA := "at://did:plc:a/app.bsky.feed.post/1"
	uriB := "at://did:plc:b/app.bsky.feed.post/2"

	if err := s.FlushWriteBatch(ctx, []PendingWrite{
		insertWrite(uriA, "did:plc:a", `["alpha"]`, now),
		insertWrite(uriB, "did:plc:b", `["beta"]`, now),
		{Op: WriteDelete, Post: Post{URI: uriA}},
	}); err != nil {
		t.Fatalf("FlushWriteBatch: %v", err)
	}

	if n := countRows(t, s, `SELECT COUNT(*) FROM topic_tokens WHERE post_uri = ?`, uriA); n != 0 {
		t.Errorf("topic_tokens for deleted post = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM topic_tokens WHERE post_uri = ?`, uriB); n != 1 {
		t.Errorf("topic_tokens for kept post = %d, want 1", n)
	}
}

func TestFlushPostBatch_DeleteOfUnknownPost(t *testing.T) {
	s := newTestStore(t)

	res, err := s.FlushPostBatch(context.Background(), []PendingWrite{
		{Op: WriteDelete, Post: Post{URI: "at://did:plc:x/app.bsky.feed.post/never"}},
		{Op: WritePurgeAuthor, Post: Post{AuthorDID: "did:plc:nobody"}},
	})
	if err != nil {
		t.Fatalf("FlushPostBatch: %v", err)
	}
	if res.Deleted != 0 || res.Purged != 0 {
		t.Errorf("result = %+v, want zero rows affected", res)
	}
}
