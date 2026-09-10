package store

import (
	"context"
	"testing"
)

func TestV2Cursor_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seq, timeUS, err := s.GetV2Cursor(ctx)
	if err != nil {
		t.Fatalf("GetV2Cursor() on a fresh store: %v", err)
	}
	if seq != 0 || timeUS != 0 {
		t.Errorf("GetV2Cursor() = %d, %d; want 0, 0 before anything is saved", seq, timeUS)
	}

	const (
		wantSeq    = int64(9_876_543_210)
		wantTimeUS = int64(1_789_000_000_123_456)
	)
	if err := s.SaveV2Cursor(ctx, wantSeq, wantTimeUS); err != nil {
		t.Fatalf("SaveV2Cursor(): %v", err)
	}
	seq, timeUS, err = s.GetV2Cursor(ctx)
	if err != nil {
		t.Fatalf("GetV2Cursor(): %v", err)
	}
	if seq != wantSeq || timeUS != wantTimeUS {
		t.Errorf("GetV2Cursor() = %d, %d; want %d, %d", seq, timeUS, wantSeq, wantTimeUS)
	}

	// The upsert must overwrite, not accumulate rows.
	if err := s.SaveV2Cursor(ctx, wantSeq+1, wantTimeUS+1); err != nil {
		t.Fatalf("SaveV2Cursor() second write: %v", err)
	}
	seq, timeUS, err = s.GetV2Cursor(ctx)
	if err != nil {
		t.Fatalf("GetV2Cursor() after overwrite: %v", err)
	}
	if seq != wantSeq+1 || timeUS != wantTimeUS+1 {
		t.Errorf("GetV2Cursor() = %d, %d; want %d, %d", seq, timeUS, wantSeq+1, wantTimeUS+1)
	}
}

// The two protocols must not share a stored cursor: a seq is not a
// microsecond timestamp, so a fallback to v1 has to find its own row intact.
func TestV2Cursor_LeavesV1CursorUntouched(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const v1Cursor = int64(1_725_911_162_329_308)
	if err := s.SaveCursor(ctx, v1Cursor); err != nil {
		t.Fatalf("SaveCursor(): %v", err)
	}
	if err := s.SaveV2Cursor(ctx, 42, 1_789_000_000_000_000); err != nil {
		t.Fatalf("SaveV2Cursor(): %v", err)
	}

	got, err := s.GetCursor(ctx)
	if err != nil {
		t.Fatalf("GetCursor(): %v", err)
	}
	if got != v1Cursor {
		t.Errorf("GetCursor() = %d, want the untouched v1 cursor %d", got, v1Cursor)
	}
}
