package jetstream

import (
	"sort"
	"sync"
	"time"
)

const (
	// maxOffenderDIDs bounds each of the three per-DID tallies. Once a map is
	// full no new key is added — the DIDs already there keep counting — so a
	// window where every dropped create has a different author cannot grow it
	// without limit. A refused key sets Overflow, so the alert that reads the
	// tally can say the names in it are a sample.
	maxOffenderDIDs = 20_000

	// offenderTopN is how many accounts a tally names. The alert has to fit in
	// a Discord message a person reads at a glance, and a flood is one or two
	// accounts in practice.
	offenderTopN = 5
)

// DIDCount is one account and the number of drops it accounts for.
type DIDCount struct {
	DID   string
	Count int64
}

// Offenders is the per-account breakdown of the three drop counters since the
// last TakeOffenders call: the accounts whose creates were dropped as backfill,
// by the per-DID rate cap, and by the operator denylist. It is what turns
// "300,000 posts were dropped this half hour" into a name to act on.
//
// The lists are the busiest accounts only (offenderTopN each), highest first;
// the totals are the whole window, so a total far above the sum of the list is
// itself information. Overflow reports that at least one account was refused a
// slot in one of the maps, which makes the lists a sample.
type Offenders struct {
	Since  time.Time
	Stale  []DIDCount
	Capped []DIDCount
	Denied []DIDCount

	StaleTotal  int64
	CappedTotal int64
	DeniedTotal int64

	Overflow bool
}

// offenderTracker accumulates the three tallies between reads. It is always
// on — unlike the stale diagnostic and the hourly summaries, which are
// operator-facing log lines behind their own flags — because the alert path
// cannot ask for it after the fact.
type offenderTracker struct {
	mu     sync.Mutex
	since  time.Time
	stale  map[string]int64
	capped map[string]int64
	denied map[string]int64

	staleTotal  int64
	cappedTotal int64
	deniedTotal int64

	overflow bool
}

func newOffenderTracker(now time.Time) *offenderTracker {
	return &offenderTracker{
		since:  now.UTC(),
		stale:  make(map[string]int64),
		capped: make(map[string]int64),
		denied: make(map[string]int64),
	}
}

// addStale counts one create dropped by the age guards (backfill or a record
// dated ahead of its witness time). did may be "", in which case only the
// total moves: a frame whose repo could not be read is still a drop.
func (t *offenderTracker) addStale(did string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.staleTotal++
	t.bumpLocked(t.stale, did)
	t.mu.Unlock()
}

// addCapped counts one create dropped by the per-DID rate cap.
func (t *offenderTracker) addCapped(did string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.cappedTotal++
	t.bumpLocked(t.capped, did)
	t.mu.Unlock()
}

// addDenied counts one create dropped because its repo is on the denylist.
func (t *offenderTracker) addDenied(did string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.deniedTotal++
	t.bumpLocked(t.denied, did)
	t.mu.Unlock()
}

func (t *offenderTracker) bumpLocked(counts map[string]int64, did string) {
	if did == "" {
		return
	}
	if _, seen := counts[did]; seen || len(counts) < maxOffenderDIDs {
		counts[did]++
		return
	}
	t.overflow = true
}

// take returns the tallies and starts a new window at now.
func (t *offenderTracker) take(now time.Time) Offenders {
	if t == nil {
		return Offenders{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	out := Offenders{
		Since:       t.since,
		Stale:       topDIDCountsLocked(t.stale),
		Capped:      topDIDCountsLocked(t.capped),
		Denied:      topDIDCountsLocked(t.denied),
		StaleTotal:  t.staleTotal,
		CappedTotal: t.cappedTotal,
		DeniedTotal: t.deniedTotal,
		Overflow:    t.overflow,
	}

	t.since = now.UTC()
	t.stale = make(map[string]int64)
	t.capped = make(map[string]int64)
	t.denied = make(map[string]int64)
	t.staleTotal, t.cappedTotal, t.deniedTotal = 0, 0, 0
	t.overflow = false
	return out
}

// topDIDCountsLocked returns the busiest accounts, highest first and tie-broken
// by DID so a tally renders the same way twice. It returns nil when the map is
// empty, so an absent breakdown is absent rather than an empty list.
func topDIDCountsLocked(counts map[string]int64) []DIDCount {
	if len(counts) == 0 {
		return nil
	}
	pairs := make([]DIDCount, 0, len(counts))
	for did, count := range counts {
		pairs = append(pairs, DIDCount{DID: did, Count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Count != pairs[j].Count {
			return pairs[i].Count > pairs[j].Count
		}
		return pairs[i].DID < pairs[j].DID
	})
	if len(pairs) > offenderTopN {
		pairs = pairs[:offenderTopN]
	}
	return pairs
}

// ---------------------------------------------------------------------------
// Consumer hooks
// ---------------------------------------------------------------------------

// TakeOffenders returns the accounts behind the stale, capped and denied
// counters since the last call and starts a new window. The stats collector
// calls it once per snapshot, which is what makes the alert's "this half hour"
// true.
func (c *Consumer) TakeOffenders() Offenders {
	return c.offenders.take(time.Now())
}

// noteStaleFrame records the account behind a create the language pre-filter
// dropped as backfill. The DID costs one bounded byte scan here because that
// path never parses the frame; the stale diagnostic's own scan is behind its
// flag and cannot be relied on.
func (c *Consumer) noteStaleFrame(data []byte) {
	c.offenders.addStale(scanFrameDID(data))
}
