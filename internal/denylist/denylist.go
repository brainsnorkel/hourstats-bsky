// Package denylist holds the operator DID denylist: the set of repos whose
// post creates the bot refuses to ingest. It is a process-wide set rather than
// a field on the consumer so that the parts of the bot which must honour the
// same decision — the firehose read loops and the posting feature gate — can
// read it without importing each other.
//
// The set is published as an immutable snapshot behind an atomic pointer, so
// Contains is lock-free on the firehose hot path and a reload never blocks it.
package denylist

import (
	"strings"
	"sync/atomic"
)

// current is the active snapshot. A nil pointer means "never populated", which
// reads the same as an empty set.
var current atomic.Pointer[map[string]struct{}]

// Replace publishes dids as the whole deny set, replacing whatever was there.
// Blank entries are dropped and surrounding whitespace trimmed, so a list
// hand-edited over `fly ssh` behaves like the environment variable form.
func Replace(dids []string) {
	set := make(map[string]struct{}, len(dids))
	for _, did := range dids {
		if did = strings.TrimSpace(did); did != "" {
			set[did] = struct{}{}
		}
	}
	current.Store(&set)
}

// Contains reports whether did is denied.
func Contains(did string) bool {
	set := current.Load()
	if set == nil || len(*set) == 0 || did == "" {
		return false
	}
	_, denied := (*set)[did]
	return denied
}

// Len is the number of denied DIDs. It is the cheap gate callers use to skip
// the work of looking a DID up at all, since the list is normally empty.
func Len() int {
	set := current.Load()
	if set == nil {
		return 0
	}
	return len(*set)
}
