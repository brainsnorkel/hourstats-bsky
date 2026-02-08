package store

import (
	"context"
	"testing"
	"time"
)

func TestTopicTokens_InsertAndGetSince(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-25 * time.Hour).Format(time.RFC3339)
	mid := now.Add(-12 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339)

	if err := s.InsertTopicTokens(ctx, "at://a/post/1", `["hello","world"]`, old); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	if err := s.InsertTopicTokens(ctx, "at://a/post/2", `["foo","bar"]`, mid); err != nil {
		t.Fatalf("insert mid: %v", err)
	}
	if err := s.InsertTopicTokens(ctx, "at://a/post/3", `["baz"]`, recent); err != nil {
		t.Fatalf("insert recent: %v", err)
	}

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

func TestTopicTokens_URIsByKeywords(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.InsertTopicTokens(ctx, "at://a/1", `["trump","election","vote"]`, now)
	s.InsertTopicTokens(ctx, "at://a/2", `["weather","rain","cold"]`, now)
	s.InsertTopicTokens(ctx, "at://a/3", `["trump","maga","rally"]`, now)

	uris, err := s.GetTopicTokenURIsByKeywords(ctx, []string{"trump", "election"}, "2000-01-01T00:00:00Z", 50)
	if err != nil {
		t.Fatalf("GetTopicTokenURIsByKeywords: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("expected 2 URIs, got %d: %v", len(uris), uris)
	}
}

func TestTopicTokens_URIsByKeywords_NoMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	s.InsertTopicTokens(ctx, "at://a/1", `["hello","world"]`, now)

	uris, err := s.GetTopicTokenURIsByKeywords(ctx, []string{"nonexistent"}, "2000-01-01T00:00:00Z", 50)
	if err != nil {
		t.Fatalf("GetTopicTokenURIsByKeywords: %v", err)
	}
	if len(uris) != 0 {
		t.Errorf("expected 0 URIs, got %d", len(uris))
	}
}

func TestTopicTokens_URIsByKeywords_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	uris, err := s.GetTopicTokenURIsByKeywords(ctx, []string{}, "2000-01-01T00:00:00Z", 50)
	if err != nil {
		t.Fatalf("GetTopicTokenURIsByKeywords: %v", err)
	}
	if uris != nil {
		t.Errorf("expected nil, got %v", uris)
	}
}

func TestTopicSnapshots_InsertAndGetSince(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	t1 := now.Add(-2 * time.Hour).Format(time.RFC3339)
	t2 := now.Add(-1 * time.Hour).Format(time.RFC3339)

	s.InsertTopicSnapshot(ctx, t1, 1, "topic-a", "AI", "Artificial intelligence", 500, `["ai","llm"]`, "", "")
	s.InsertTopicSnapshot(ctx, t1, 2, "topic-b", "Sports", "Sports talk", 300, `["sports"]`, "", "")
	s.InsertTopicSnapshot(ctx, t2, 1, "topic-a", "AI", "Artificial intelligence", 550, `["ai","llm"]`, "at://ex/1", "researcher.bsky.social")

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

	s.InsertTopicSnapshot(ctx, old, 1, "t1", "Old", "old", 100, `[]`, "", "")
	s.InsertTopicSnapshot(ctx, recent, 1, "t2", "New", "new", 200, `[]`, "", "")

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
	s.InsertTopicSnapshot(ctx, now, 1, "topic-x", "Test", "desc", 100, `[]`, "", "")

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
