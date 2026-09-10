package main

import (
	"sync"
	"testing"
	"time"
)

func TestTombstonesAddAndHas(t *testing.T) {
	tombs := newTombstones()
	now := time.Now().UTC()

	if tombs.has("at://did:plc:a/app.bsky.feed.post/1") {
		t.Fatal("empty set reported a hit")
	}

	tombs.add("at://did:plc:a/app.bsky.feed.post/1", now)
	if !tombs.has("at://did:plc:a/app.bsky.feed.post/1") {
		t.Error("added URI not found")
	}
	if tombs.has("at://did:plc:a/app.bsky.feed.post/2") {
		t.Error("unrelated URI reported a hit")
	}
	if got := tombs.len(); got != 1 {
		t.Errorf("len() = %d, want 1", got)
	}

	// An empty URI is not worth remembering.
	tombs.add("", now)
	if got := tombs.len(); got != 1 {
		t.Errorf("len() after empty add = %d, want 1", got)
	}
}

func TestTombstonesPrune(t *testing.T) {
	tombs := newTombstones()
	start := time.Now().UTC()

	// Both adds land inside one prune interval, so nothing is swept until the
	// explicit prune below.
	tombs.add("old", start)
	tombs.add("fresh", start.Add(30*time.Second))
	if got := tombs.len(); got != 2 {
		t.Fatalf("len() = %d, want 2", got)
	}

	now := start.Add(90 * time.Second)
	if removed := tombs.prune(now, time.Minute); removed != 1 {
		t.Errorf("prune removed %d, want 1", removed)
	}
	if tombs.has("old") {
		t.Error("expired URI survived the prune")
	}
	if !tombs.has("fresh") {
		t.Error("fresh URI was pruned")
	}
	if got := tombs.len(); got != 1 {
		t.Errorf("len() = %d, want 1", got)
	}
}

// add prunes at most once per interval, so a firehose-rate caller does not
// sweep the map on every delete — but expired entries still go eventually.
func TestTombstonesAddPrunesOncePerInterval(t *testing.T) {
	tombs := newTombstones()
	start := time.Now().UTC()

	tombs.add("first", start)
	tombs.add("stale", start.Add(time.Second))
	if got := tombs.len(); got != 2 {
		t.Fatalf("len() = %d, want 2", got)
	}

	// Well inside the prune interval: nothing is swept.
	tombs.add("second", start.Add(30*time.Second))
	if got := tombs.len(); got != 3 {
		t.Errorf("len() = %d, want 3 (no prune inside the interval)", got)
	}

	// Past the interval and past the TTL for the first two entries.
	tombs.add("third", start.Add(tombstoneTTL+2*time.Minute))
	if tombs.has("first") || tombs.has("stale") || tombs.has("second") {
		t.Error("expired URIs survived the interval prune")
	}
	if !tombs.has("third") {
		t.Error("newest URI was pruned")
	}
}

func TestTombstonesConcurrentAccess(t *testing.T) {
	tombs := newTombstones()
	now := time.Now().UTC()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			uri := string(rune('a'+i)) + "://post"
			for j := 0; j < 100; j++ {
				tombs.add(uri, now)
				tombs.has(uri)
				tombs.len()
			}
		}(i)
	}
	wg.Wait()

	if got := tombs.len(); got != 8 {
		t.Errorf("len() = %d, want 8", got)
	}
}
