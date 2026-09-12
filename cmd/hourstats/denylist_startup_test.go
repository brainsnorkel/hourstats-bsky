package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/denylist"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// publishDenyList is what main calls before any ticker starts: until it existed,
// internal/denylist was only ever populated by the consumer's reload loop, so a
// posting path that ran ahead of Consumer.Run — a REPORTS_RUN_AT_STARTUP report —
// read an empty list and could feature a denied account.
func TestPublishDenyList_UnionsEnvAndStoredRow(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "deny.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// The process-wide set is global, so leave it as it was found.
	t.Cleanup(func() { denylist.Replace(nil) })

	// No row and no environment variable: the empty list, not an error.
	t.Setenv("FIREHOSE_DENY_DIDS", "")
	publishDenyList(ctx, db)
	if denylist.Len() != 0 {
		t.Errorf("denied dids = %d, want 0 with nothing configured", denylist.Len())
	}

	if err := db.SetKeyValue(ctx, denyListKey, "did:plc:stored , did:plc:both"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}
	t.Setenv("FIREHOSE_DENY_DIDS", "did:plc:env,did:plc:both")
	publishDenyList(ctx, db)

	for _, did := range []string{"did:plc:env", "did:plc:stored", "did:plc:both"} {
		if !denylist.Contains(did) {
			t.Errorf("denylist.Contains(%q) = false, want the union of both sources", did)
		}
	}
	if denylist.Len() != 3 {
		t.Errorf("denied dids = %d, want 3 (the duplicate collapses)", denylist.Len())
	}
	if denylist.Contains("did:plc:other") {
		t.Error("an unlisted did must not be denied")
	}
}
