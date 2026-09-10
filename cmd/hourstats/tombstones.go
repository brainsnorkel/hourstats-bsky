package main

import (
	"sync"
	"time"
)

const (
	// tombstoneTTL is how long a deleted post's URI is remembered. It only has
	// to outlive a reconnect: the cursor rewind replays a few seconds of
	// events, and a stalled consumer can be off by minutes.
	tombstoneTTL = 15 * time.Minute

	// tombstonePruneInterval bounds how often add() sweeps the map, so a
	// firehose-rate caller does not pay for a full scan on every delete.
	tombstonePruneInterval = time.Minute
)

// tombstones remembers recently deleted post URIs so a create replayed after
// its delete — routine after a reconnect rewinds the cursor — is dropped
// instead of being written back into post_buffer.
type tombstones struct {
	mu        sync.Mutex
	m         map[string]time.Time
	lastPrune time.Time
}

func newTombstones() *tombstones {
	return &tombstones{m: make(map[string]time.Time)}
}

// add records uri as deleted at now, pruning expired entries at most once per
// tombstonePruneInterval.
func (t *tombstones) add(uri string, now time.Time) {
	if uri == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = make(map[string]time.Time)
	}
	t.m[uri] = now
	if t.lastPrune.IsZero() {
		t.lastPrune = now
		return
	}
	if now.Sub(t.lastPrune) >= tombstonePruneInterval {
		t.pruneLocked(now, tombstoneTTL)
		t.lastPrune = now
	}
}

// has reports whether uri was deleted recently enough to still be remembered.
// The TTL is checked here rather than left to the sweep: add() only prunes
// once a minute and only when it is called, so a quiet period leaves entries
// well past their TTL in the map, where they would keep suppressing legitimate
// re-creates of the same rkey.
func (t *tombstones) has(uri string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.m[uri]
	if !ok {
		return false
	}
	return time.Since(at) <= tombstoneTTL
}

// prune drops entries older than maxAge and returns how many were removed.
func (t *tombstones) prune(now time.Time, maxAge time.Duration) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pruneLocked(now, maxAge)
}

func (t *tombstones) pruneLocked(now time.Time, maxAge time.Duration) int {
	removed := 0
	for uri, at := range t.m {
		if now.Sub(at) > maxAge {
			delete(t.m, uri)
			removed++
		}
	}
	return removed
}

// len returns the number of remembered URIs.
func (t *tombstones) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.m)
}
