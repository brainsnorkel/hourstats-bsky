package jetstream

import (
	"testing"
	"time"
)

// TestOffenderTrackerSortsAndTruncates: the alert names a handful of accounts,
// so the tally must hand back the busiest ones in order rather than whatever
// the map iteration produced.
func TestOffenderTrackerSortsAndTruncates(t *testing.T) {
	tr := newOffenderTracker(time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC))

	for i := 0; i < 7; i++ {
		did := string(rune('a' + i))
		for n := 0; n <= i; n++ {
			tr.addStale("did:plc:" + did)
		}
	}
	tr.addCapped("did:plc:loud")
	tr.addCapped("did:plc:loud")
	tr.addCapped("did:plc:quiet")
	tr.addDenied("did:plc:denied")

	got := tr.take(time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC))

	if len(got.Stale) != offenderTopN {
		t.Fatalf("stale accounts = %d, want %d", len(got.Stale), offenderTopN)
	}
	wantOrder := []string{"did:plc:g", "did:plc:f", "did:plc:e", "did:plc:d", "did:plc:c"}
	for i, want := range wantOrder {
		if got.Stale[i].DID != want {
			t.Errorf("stale[%d] = %q, want %q", i, got.Stale[i].DID, want)
		}
	}
	if got.Stale[0].Count != 7 {
		t.Errorf("busiest stale count = %d, want 7", got.Stale[0].Count)
	}
	if got.StaleTotal != 28 {
		t.Errorf("StaleTotal = %d, want 28 (every drop, not just the named ones)", got.StaleTotal)
	}

	if len(got.Capped) != 2 || got.Capped[0].DID != "did:plc:loud" || got.Capped[0].Count != 2 {
		t.Errorf("capped = %+v, want loud=2 first", got.Capped)
	}
	if got.CappedTotal != 3 {
		t.Errorf("CappedTotal = %d, want 3", got.CappedTotal)
	}
	if len(got.Denied) != 1 || got.Denied[0].DID != "did:plc:denied" || got.DeniedTotal != 1 {
		t.Errorf("denied = %+v, total = %d", got.Denied, got.DeniedTotal)
	}
	if got.Overflow {
		t.Error("Overflow = true with room to spare")
	}
	if !got.Since.Equal(time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("Since = %v, want the tracker's start", got.Since)
	}
}

// TestOffenderTrackerResetsOnTake: the counts are per alert window, so a
// second read of a quiet window must not repeat the first one's accounts.
func TestOffenderTrackerResetsOnTake(t *testing.T) {
	start := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	tr := newOffenderTracker(start)
	tr.addStale("did:plc:a")
	tr.addCapped("did:plc:a")
	tr.addDenied("did:plc:a")

	next := start.Add(30 * time.Minute)
	if first := tr.take(next); len(first.Stale) != 1 {
		t.Fatalf("first take stale = %+v, want one account", first.Stale)
	}

	second := tr.take(next.Add(30 * time.Minute))
	if second.Stale != nil || second.Capped != nil || second.Denied != nil {
		t.Errorf("second take = %+v, want empty lists", second)
	}
	if second.StaleTotal != 0 || second.CappedTotal != 0 || second.DeniedTotal != 0 {
		t.Errorf("second take totals = %d/%d/%d, want zeros",
			second.StaleTotal, second.CappedTotal, second.DeniedTotal)
	}
	if !second.Since.Equal(next) {
		t.Errorf("Since = %v, want the previous take's time %v", second.Since, next)
	}
}

// TestOffenderTrackerBounded: a flood where every drop has a different author
// must not grow the map without limit. The DIDs already in it keep counting,
// and the refusal is reported so the alert's list reads as a sample.
func TestOffenderTrackerBounded(t *testing.T) {
	tr := newOffenderTracker(time.Now())

	tr.addStale("did:plc:first")
	for i := 0; i < maxOffenderDIDs+50; i++ {
		tr.addStale("did:plc:filler" + string(rune(i%256)) + string(rune(i/256)))
	}
	tr.addStale("did:plc:first")

	got := tr.take(time.Now())
	if !got.Overflow {
		t.Error("Overflow = false after the map filled")
	}
	if got.StaleTotal != int64(maxOffenderDIDs+52) {
		t.Errorf("StaleTotal = %d, want every drop counted", got.StaleTotal)
	}
	if got.Stale[0].DID != "did:plc:first" || got.Stale[0].Count != 2 {
		t.Errorf("busiest = %+v, want the pre-existing key to keep counting", got.Stale[0])
	}
}

// TestOffenderTrackerIgnoresEmptyDID: a frame whose repo could not be read is
// still a drop, but it is not an account to name.
func TestOffenderTrackerIgnoresEmptyDID(t *testing.T) {
	tr := newOffenderTracker(time.Now())
	tr.addStale("")
	got := tr.take(time.Now())
	if got.StaleTotal != 1 {
		t.Errorf("StaleTotal = %d, want 1", got.StaleTotal)
	}
	if got.Stale != nil {
		t.Errorf("stale accounts = %+v, want none", got.Stale)
	}
}

// TestConsumerTakeOffenders covers the wiring: each of the three drop paths
// must reach the tally, including the pre-filter one that never parses the
// frame, and the tally must be on without any diagnostic flag set.
func TestConsumerTakeOffenders(t *testing.T) {
	c := NewConsumer(ConsumerConfig{
		MaxPostsPerDIDPerMinute: 1,
		MaxPostAge:              time.Hour,
		MaxPostFuture:           -1,
	})

	witness := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	// Stale: the parsed path, through dispatch's age guard.
	c.dispatch(capTestEvent("did:plc:importer", "1", witness, witness.Add(-48*time.Hour), `"en"`))
	// Stale: the pre-filter path, which only has the frame's bytes.
	c.noteStaleFrame([]byte(`{"did":"did:plc:importer","commit":{"operation":"create"}}`))
	// Capped: the second create from the same repo inside a minute.
	c.dispatch(capTestEvent("did:plc:loud", "2", witness, witness, `"en"`))
	c.dispatch(capTestEvent("did:plc:loud", "3", witness, witness, `"en"`))
	// Denied.
	c.countDeniedCreate("did:plc:blocked")

	got := c.TakeOffenders()
	if len(got.Stale) != 1 || got.Stale[0].DID != "did:plc:importer" || got.Stale[0].Count != 2 {
		t.Errorf("stale = %+v, want did:plc:importer twice", got.Stale)
	}
	if len(got.Capped) != 1 || got.Capped[0].DID != "did:plc:loud" || got.Capped[0].Count != 1 {
		t.Errorf("capped = %+v, want did:plc:loud once", got.Capped)
	}
	if len(got.Denied) != 1 || got.Denied[0].DID != "did:plc:blocked" {
		t.Errorf("denied = %+v, want did:plc:blocked", got.Denied)
	}
	if got.Since.IsZero() {
		t.Error("Since is zero, so no rate can be computed from it")
	}

	if after := c.TakeOffenders(); after.Stale != nil || after.Capped != nil || after.Denied != nil {
		t.Errorf("second take = %+v, want empty lists", after)
	}
}
