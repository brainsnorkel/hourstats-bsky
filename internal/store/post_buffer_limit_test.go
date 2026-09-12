package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// seedWindow inserts n posts one minute apart, oldest first, and returns the
// cutoff that includes all of them.
func seedWindow(t *testing.T, s *Store, n int) (time.Time, []string) {
	t.Helper()
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Duration(n) * time.Minute).Truncate(time.Second)

	posts := make([]Post, 0, n)
	uris := make([]string, 0, n)
	for i := 0; i < n; i++ {
		uri := fmt.Sprintf("at://did:plc:abc/app.bsky.feed.post/%03d", i)
		uris = append(uris, uri)
		posts = append(posts, Post{
			URI:       uri,
			CID:       fmt.Sprintf("cid%03d", i),
			Text:      fmt.Sprintf("post %d", i),
			AuthorDID: "did:plc:abc",
			CreatedAt: base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
		})
	}
	if err := s.InsertPostsBatch(ctx, posts); err != nil {
		t.Fatalf("InsertPostsBatch: %v", err)
	}
	return base.Add(-time.Minute), uris
}

func TestGetPostsSinceLimit_CapBinds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	cutoff, uris := seedWindow(t, s, 10)

	posts, available, err := s.GetPostsSinceLimit(ctx, cutoff, 3)
	if err != nil {
		t.Fatalf("GetPostsSinceLimit: %v", err)
	}
	if available != 10 {
		t.Errorf("available = %d, want 10", available)
	}
	if len(posts) != 3 {
		t.Fatalf("len(posts) = %d, want 3", len(posts))
	}
	// The newest three, re-sorted oldest first.
	want := uris[7:]
	for i, uri := range want {
		if posts[i].URI != uri {
			t.Errorf("posts[%d].URI = %q, want %q", i, posts[i].URI, uri)
		}
	}
	for i := 1; i < len(posts); i++ {
		if posts[i-1].CreatedAt > posts[i].CreatedAt {
			t.Errorf("posts not ascending at %d: %q then %q", i, posts[i-1].CreatedAt, posts[i].CreatedAt)
		}
	}
}

func TestGetPostsSinceLimit_CapDoesNotBind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	cutoff, uris := seedWindow(t, s, 5)

	for _, limit := range []int{0, -1, 5, 100} {
		posts, available, err := s.GetPostsSinceLimit(ctx, cutoff, limit)
		if err != nil {
			t.Fatalf("GetPostsSinceLimit(limit=%d): %v", limit, err)
		}
		if available != 5 {
			t.Errorf("limit=%d: available = %d, want 5", limit, available)
		}
		if len(posts) != len(uris) {
			t.Fatalf("limit=%d: len(posts) = %d, want %d", limit, len(posts), len(uris))
		}
		for i, uri := range uris {
			if posts[i].URI != uri {
				t.Errorf("limit=%d: posts[%d].URI = %q, want %q", limit, i, posts[i].URI, uri)
			}
		}
	}
}

func TestGetPostsSinceLimit_EmptyWindow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	posts, available, err := s.GetPostsSinceLimit(ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("GetPostsSinceLimit: %v", err)
	}
	if available != 0 || len(posts) != 0 {
		t.Errorf("available = %d, len(posts) = %d, want 0 and 0", available, len(posts))
	}
}

func TestCreateRunWindowCapped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, capped := range []bool{true, false} {
		runID := fmt.Sprintf("run-capped-%v", capped)
		if err := s.CreateRun(ctx, RunState{
			RunID:        runID,
			Status:       "low_confidence",
			CutoffTime:   time.Now().UTC(),
			WindowCapped: capped,
		}); err != nil {
			t.Fatalf("CreateRun(capped=%v): %v", capped, err)
		}
		run, err := s.GetRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetRun(%s): %v", runID, err)
		}
		if run.WindowCapped != capped {
			t.Errorf("WindowCapped = %v, want %v", run.WindowCapped, capped)
		}
	}
}
