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
