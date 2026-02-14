package store

import (
	"context"
	"testing"
	"time"
)

func TestGetDatabaseHealth(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	post := Post{
		URI:       "at://did:plc:abc/app.bsky.feed.post/health1",
		CID:       "cid1",
		Text:      "test post",
		AuthorDID: "did:plc:abc",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.InsertPost(ctx, post); err != nil {
		t.Fatalf("InsertPost: %v", err)
	}

	h, err := s.GetDatabaseHealth(ctx)
	if err != nil {
		t.Fatalf("GetDatabaseHealth: %v", err)
	}

	if h.DBSizeBytes == 0 {
		t.Error("expected non-zero DB size")
	}
	if h.PageSize == 0 {
		t.Error("expected non-zero page size")
	}
	if h.PageCount == 0 {
		t.Error("expected non-zero page count")
	}
	if h.CheckedAt.IsZero() {
		t.Error("expected non-zero checked_at")
	}

	found := false
	for _, tbl := range h.Tables {
		if tbl.Name == "post_buffer" {
			found = true
			if tbl.RowCount != 1 {
				t.Errorf("post_buffer: expected 1 row, got %d", tbl.RowCount)
			}
		}
	}
	if !found {
		t.Error("post_buffer not found in tables")
	}
}

func TestGetDatabaseHealth_StaleDetection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	staleInserted := time.Now().UTC().Add(-10 * time.Hour).Format(time.RFC3339)
	freshInserted := time.Now().UTC().Format(time.RFC3339)
	created := time.Now().UTC().Format(time.RFC3339)

	// Insert directly with controlled inserted_at timestamps
	for i, inserted := range []string{staleInserted, freshInserted} {
		_, err := s.writeDB.ExecContext(ctx,
			`INSERT INTO post_buffer (uri, cid, text, author_did, created_at, inserted_at) VALUES (?, ?, ?, ?, ?, ?)`,
			"at://did:plc:abc/app.bsky.feed.post/stale"+string(rune('0'+i)),
			"cid", "text", "did:plc:abc", created, inserted,
		)
		if err != nil {
			t.Fatalf("insert post %d: %v", i, err)
		}
	}

	h, err := s.GetDatabaseHealth(ctx)
	if err != nil {
		t.Fatalf("GetDatabaseHealth: %v", err)
	}

	for _, tbl := range h.Tables {
		if tbl.Name == "post_buffer" {
			if tbl.RowCount != 2 {
				t.Errorf("expected 2 rows, got %d", tbl.RowCount)
			}
			if tbl.StaleRows != 1 {
				t.Errorf("expected 1 stale row, got %d", tbl.StaleRows)
			}
			return
		}
	}
	t.Error("post_buffer not found in health tables")
}
