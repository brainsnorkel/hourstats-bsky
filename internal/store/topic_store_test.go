package store

import (
	"context"
	"testing"
	"time"
)

func insertPostWithEngagement(t *testing.T, s *Store, ctx context.Context, uri, text, createdAt string, likes int) {
	t.Helper()
	s.InsertPost(ctx, Post{URI: uri, CID: "cid", Text: text, AuthorDID: "did:plc:x", AuthorHandle: "test.bsky.social", CreatedAt: createdAt})
	s.db.ExecContext(ctx, `UPDATE post_buffer SET likes=? WHERE uri=?`, likes, uri)
}

func TestTopicTokens_InsertAndGetSince(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-25 * time.Hour).Format(time.RFC3339)
	mid := now.Add(-12 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339)

	s.InsertTopicTokens(ctx, "at://a/post/1", `["hello","world"]`, old)
	s.InsertTopicTokens(ctx, "at://a/post/2", `["foo","bar"]`, mid)
	s.InsertTopicTokens(ctx, "at://a/post/3", `["baz"]`, recent)

	insertPostWithEngagement(t, s, ctx, "at://a/post/1", "hello world", old, 5)
	insertPostWithEngagement(t, s, ctx, "at://a/post/2", "foo bar", mid, 10)
	insertPostWithEngagement(t, s, ctx, "at://a/post/3", "baz", recent, 3)

	cutoff := now.Add(-13 * time.Hour).Format(time.RFC3339)
	rows, err := s.GetTopicTokensSince(ctx, cutoff)
	if err != nil {
		t.Fatalf("GetTopicTokensSince: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].PostURI != "at://a/post/2" {
		t.Errorf("first row URI = %q, want at://a/post/2", rows[0].PostURI)
	}
	if rows[1].Tokens != `["baz"]` {
		t.Errorf("second row tokens = %q", rows[1].Tokens)
	}
}

func TestTopicTokens_Count(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		ts := now.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339)
		if err := s.InsertTopicTokens(ctx, "at://a/post/"+string(rune('a'+i)), `["tok"]`, ts); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	cutoff := now.Add(-3 * time.Hour).Format(time.RFC3339)
	count, err := s.CountTopicTokensSince(ctx, cutoff)
	if err != nil {
		t.Fatalf("CountTopicTokensSince: %v", err)
	}
	if count != 4 {
		t.Errorf("expected 4, got %d", count)
	}
}

func TestTopicTokens_Purge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-30 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339)

	s.InsertTopicTokens(ctx, "at://old/1", `["a"]`, old)
	s.InsertTopicTokens(ctx, "at://new/1", `["b"]`, recent)

	insertPostWithEngagement(t, s, ctx, "at://old/1", "old post", old, 1)
	insertPostWithEngagement(t, s, ctx, "at://new/1", "new post", recent, 1)

	cutoff := now.Add(-26 * time.Hour).Format(time.RFC3339)
	deleted, err := s.PurgeTopicTokens(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeTopicTokens: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	all, _ := s.GetTopicTokensSince(ctx, "2000-01-01T00:00:00Z")
	if len(all) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(all))
	}
}

func TestGetExemplarCandidates_RankedByEngagement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.InsertTopicTokens(ctx, "at://a/1", `["trump","election","vote"]`, now)
	s.InsertTopicTokens(ctx, "at://a/2", `["weather","rain","cold"]`, now)
	s.InsertTopicTokens(ctx, "at://a/3", `["trump","maga","rally"]`, now)

	s.InsertPost(ctx, Post{URI: "at://a/1", CID: "cid1", Text: "Trump won the election", AuthorDID: "did:plc:1", AuthorHandle: "alice.bsky.social", CreatedAt: now})
	s.InsertPost(ctx, Post{URI: "at://a/2", CID: "cid2", Text: "Rainy day", AuthorDID: "did:plc:2", AuthorHandle: "bob.bsky.social", CreatedAt: now})
	s.InsertPost(ctx, Post{URI: "at://a/3", CID: "cid3", Text: "Trump rally MAGA", AuthorDID: "did:plc:3", AuthorHandle: "charlie.bsky.social", CreatedAt: now})

	s.db.ExecContext(ctx, `UPDATE post_buffer SET likes=100, reposts=50, replies=25 WHERE uri='at://a/1'`)
	s.db.ExecContext(ctx, `UPDATE post_buffer SET likes=5, reposts=2, replies=1 WHERE uri='at://a/3'`)

	candidates, err := s.GetExemplarCandidates(ctx, []string{"trump", "election"}, "2000-01-01T00:00:00Z", 50)
	if err != nil {
		t.Fatalf("GetExemplarCandidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if candidates[0].Handle != "alice.bsky.social" {
		t.Errorf("expected highest engagement first, got %q", candidates[0].Handle)
	}
	if candidates[0].Engagement != 175 {
		t.Errorf("expected engagement 175, got %d", candidates[0].Engagement)
	}
}

func TestGetExemplarCandidates_FiltersMultiHashtag(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.InsertTopicTokens(ctx, "at://a/1", `["trump"]`, now)
	s.InsertTopicTokens(ctx, "at://a/2", `["trump"]`, now)

	s.InsertPost(ctx, Post{URI: "at://a/1", CID: "cid1", Text: "Trump #maga #election #vote", AuthorDID: "did:plc:1", AuthorHandle: "spammy.bsky.social", CreatedAt: now})
	s.InsertPost(ctx, Post{URI: "at://a/2", CID: "cid2", Text: "Trump did a thing", AuthorDID: "did:plc:2", AuthorHandle: "clean.bsky.social", CreatedAt: now})

	s.db.ExecContext(ctx, `UPDATE post_buffer SET likes=500 WHERE uri='at://a/1'`)
	s.db.ExecContext(ctx, `UPDATE post_buffer SET likes=10 WHERE uri='at://a/2'`)

	candidates, err := s.GetExemplarCandidates(ctx, []string{"trump"}, "2000-01-01T00:00:00Z", 50)
	if err != nil {
		t.Fatalf("GetExemplarCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (multi-hashtag filtered), got %d", len(candidates))
	}
	if candidates[0].Handle != "clean.bsky.social" {
		t.Errorf("expected clean post, got %q", candidates[0].Handle)
	}
}

func TestGetExemplarCandidates_NoMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.InsertTopicTokens(ctx, "at://a/1", `["hello","world"]`, now)
	s.InsertPost(ctx, Post{URI: "at://a/1", CID: "cid1", Text: "Hello world", AuthorDID: "did:plc:1", AuthorHandle: "user.bsky.social", CreatedAt: now})

	candidates, err := s.GetExemplarCandidates(ctx, []string{"nonexistent"}, "2000-01-01T00:00:00Z", 50)
	if err != nil {
		t.Fatalf("GetExemplarCandidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(candidates))
	}
}

func TestGetExemplarCandidates_EmptyKeywords(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	candidates, err := s.GetExemplarCandidates(ctx, []string{}, "2000-01-01T00:00:00Z", 50)
	if err != nil {
		t.Fatalf("GetExemplarCandidates: %v", err)
	}
	if candidates != nil {
		t.Errorf("expected nil, got %v", candidates)
	}
}

func TestTopicSnapshots_InsertAndGetSince(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	t1 := now.Add(-2 * time.Hour).Format(time.RFC3339)
	t2 := now.Add(-1 * time.Hour).Format(time.RFC3339)

	s.InsertTopicSnapshot(ctx, t1, 1, "topic-a", "AI", "Artificial intelligence", 500, `["ai","llm"]`, "", "", false)
	s.InsertTopicSnapshot(ctx, t1, 2, "topic-b", "Sports", "Sports talk", 300, `["sports"]`, "", "", false)
	s.InsertTopicSnapshot(ctx, t2, 1, "topic-a", "AI", "Artificial intelligence", 550, `["ai","llm"]`, "at://ex/1", "researcher.bsky.social", false)

	cutoff := now.Add(-3 * time.Hour).Format(time.RFC3339)
	rows, err := s.GetTopicSnapshotsSince(ctx, cutoff)
	if err != nil {
		t.Fatalf("GetTopicSnapshotsSince: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].Rank != 1 || rows[0].Label != "AI" {
		t.Errorf("first row: rank=%d label=%q", rows[0].Rank, rows[0].Label)
	}
	if rows[2].ExemplarHandle != "researcher.bsky.social" {
		t.Errorf("third row exemplar = %q", rows[2].ExemplarHandle)
	}
}

func TestTopicSnapshots_Purge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-50 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339)

	s.InsertTopicSnapshot(ctx, old, 1, "t1", "Old", "old", 100, `[]`, "", "", false)
	s.InsertTopicSnapshot(ctx, recent, 1, "t2", "New", "new", 200, `[]`, "", "", false)

	cutoff := now.Add(-48 * time.Hour).Format(time.RFC3339)
	deleted, err := s.PurgeTopicSnapshots(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeTopicSnapshots: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}
}

func TestTopicSnapshots_UpdateExemplar(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.InsertTopicSnapshot(ctx, now, 1, "topic-x", "Test", "desc", 100, `[]`, "", "", false)

	rows, _ := s.GetTopicSnapshotsSince(ctx, "2000-01-01T00:00:00Z")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if err := s.UpdateSnapshotExemplar(ctx, rows[0].ID, "at://exemplar/1", "topuser.bsky.social"); err != nil {
		t.Fatalf("UpdateSnapshotExemplar: %v", err)
	}

	rows, _ = s.GetTopicSnapshotsSince(ctx, "2000-01-01T00:00:00Z")
	if rows[0].ExemplarURI != "at://exemplar/1" {
		t.Errorf("exemplar URI = %q, want at://exemplar/1", rows[0].ExemplarURI)
	}
	if rows[0].ExemplarHandle != "topuser.bsky.social" {
		t.Errorf("exemplar handle = %q", rows[0].ExemplarHandle)
	}
}

func TestTopicSnapshots_IsMeme(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.InsertTopicSnapshot(ctx, now, 1, "topic-meme", "Post a Banger", "viral phrase", 500, `["post","banger"]`, "", "", true)
	s.InsertTopicSnapshot(ctx, now, 2, "topic-normal", "Politics", "political discussion", 300, `["politics"]`, "", "", false)

	rows, err := s.GetTopicSnapshotsSince(ctx, "2000-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("GetTopicSnapshotsSince: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if !rows[0].IsMeme {
		t.Error("expected first row IsMeme=true")
	}
	if rows[1].IsMeme {
		t.Error("expected second row IsMeme=false")
	}
}

func TestTopicSnapshots_IsMemeDefaultsFalse(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.InsertTopicSnapshot(ctx, now, 1, "topic-a", "Test", "desc", 100, `[]`, "", "", false)

	rows, err := s.GetTopicSnapshotsSince(ctx, "2000-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("GetTopicSnapshotsSince: %v", err)
	}
	if rows[0].IsMeme {
		t.Error("expected IsMeme=false by default")
	}
}

func TestTopicIdentity_UpsertAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	err := s.UpsertTopicIdentity(ctx, "tid-1", "AI Discussion", `["ai","llm","chatgpt"]`, now, now, 1)
	if err != nil {
		t.Fatalf("UpsertTopicIdentity: %v", err)
	}

	rows, err := s.GetRecentTopicIdentities(ctx, "2000-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("GetRecentTopicIdentities: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1, got %d", len(rows))
	}
	if rows[0].TopicID != "tid-1" {
		t.Errorf("TopicID = %q", rows[0].TopicID)
	}
	if rows[0].CanonicalLabel != "AI Discussion" {
		t.Errorf("CanonicalLabel = %q", rows[0].CanonicalLabel)
	}
	if rows[0].PeakRank != 1 {
		t.Errorf("PeakRank = %d, want 1", rows[0].PeakRank)
	}
}

func TestTopicIdentity_Upsert_UpdatesPeakRank(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.UpsertTopicIdentity(ctx, "tid-2", "Sports", `["sports"]`, now, now, 3)
	s.UpsertTopicIdentity(ctx, "tid-2", "Sports Updated", `["sports","nfl"]`, now, now, 1)

	rows, _ := s.GetRecentTopicIdentities(ctx, "2000-01-01T00:00:00Z")
	if len(rows) != 1 {
		t.Fatalf("expected 1, got %d", len(rows))
	}
	if rows[0].PeakRank != 1 {
		t.Errorf("PeakRank = %d, want 1 (better rank)", rows[0].PeakRank)
	}
	if rows[0].CanonicalLabel != "Sports Updated" {
		t.Errorf("CanonicalLabel should be updated to %q, got %q", "Sports Updated", rows[0].CanonicalLabel)
	}
}

func TestTopicIdentity_Upsert_KeepsBetterPeakRank(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.UpsertTopicIdentity(ctx, "tid-3", "Crypto", `["crypto"]`, now, now, 1)
	s.UpsertTopicIdentity(ctx, "tid-3", "Crypto", `["crypto","btc"]`, now, now, 3)

	rows, _ := s.GetRecentTopicIdentities(ctx, "2000-01-01T00:00:00Z")
	if rows[0].PeakRank != 1 {
		t.Errorf("PeakRank = %d, want 1 (should keep better rank)", rows[0].PeakRank)
	}
}

func TestTopicIdentity_Purge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339)

	s.UpsertTopicIdentity(ctx, "old-topic", "Old", `[]`, old, old, 5)
	s.UpsertTopicIdentity(ctx, "new-topic", "New", `[]`, recent, recent, 2)

	cutoff := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	deleted, err := s.PurgeTopicIdentities(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeTopicIdentities: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	remaining, _ := s.GetRecentTopicIdentities(ctx, "2000-01-01T00:00:00Z")
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].TopicID != "new-topic" {
		t.Errorf("wrong topic survived purge: %q", remaining[0].TopicID)
	}
}
