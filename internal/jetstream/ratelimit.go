package jetstream

import (
	"log/slog"
	"sync"
	"time"
)

const (
	// maxRateLimitDIDs bounds the bucket map. Once it is full no new key is
	// added — the DIDs already there keep their buckets — so a stream where
	// every create has a different author cannot grow the map without limit.
	// A create whose DID could not be admitted is allowed through and counted
	// as an overflow: the limiter exists to clip a single loud repo, not to
	// gate the firehose.
	maxRateLimitDIDs = 100_000

	// rateLimitIdleTTL is how long a bucket survives without a create from its
	// DID before the sweep reclaims it.
	rateLimitIdleTTL = 3 * time.Minute

	// rateLimitSweepInterval is how often the idle sweep runs, on the read
	// goroutine that is already holding the lock.
	rateLimitSweepInterval = time.Minute
)

// didBucket is one repo's token bucket: capacity equals the per-minute rate, so
// a repo may burst up to a minute's worth and then proceeds at the rate.
type didBucket struct {
	tokens float64
	last   time.Time
}

// didLimiter caps how many post creates one repo may contribute per minute. It
// is protocol-agnostic: dispatch consults it for every create, whichever wire
// protocol delivered it. It is nil when the cap is disabled.
type didLimiter struct {
	// rate is both the tokens added per minute and the bucket capacity.
	rate float64

	mu      sync.Mutex
	buckets map[string]*didBucket
	sweptAt time.Time

	// Hourly tally of what was capped. The sweep can retire and re-admit keys
	// within the hour, so the tally gets the same bound the stale diagnostic
	// uses rather than relying on the bucket map's.
	hourStart time.Time
	total     int64
	dids      map[string]int64
	didsFull  bool
	overflow  int64
}

// newDIDLimiter returns nil when the cap is off, so every call site is a single
// nil check on the hot path.
func newDIDLimiter(perMinute int) *didLimiter {
	if perMinute <= 0 {
		return nil
	}
	return &didLimiter{
		rate:    float64(perMinute),
		buckets: make(map[string]*didBucket),
		dids:    make(map[string]int64),
	}
}

// cappedSummary is one closed hour of the tally.
type cappedSummary struct {
	hour     time.Time
	total    int64
	distinct int
	top      []string
	overflow int64
	didsFull bool
}

// attrs renders the summary as slog key-value pairs.
func (s *cappedSummary) attrs() []any {
	attrs := []any{
		"hour", s.hour.Format(time.RFC3339),
		"total_capped", s.total,
		"distinct_dids", s.distinct,
		"top_dids", s.top,
		"map_overflow", s.overflow,
	}
	if s.didsFull {
		attrs = append(attrs, "dids_truncated", true)
	}
	return attrs
}

// allow reports whether a create from did may proceed. It also returns the
// summary of the hour that just ended, when this call opened a new one.
func (l *didLimiter) allow(did string, now time.Time) (bool, *cappedSummary) {
	l.mu.Lock()
	defer l.mu.Unlock()

	hour := now.UTC().Truncate(time.Hour)
	var summary *cappedSummary
	switch {
	case l.hourStart.IsZero():
		l.hourStart = hour
	case hour.After(l.hourStart):
		summary = l.rollLocked(hour)
	}

	l.sweepLocked(now)

	bucket := l.buckets[did]
	if bucket == nil {
		if len(l.buckets) >= maxRateLimitDIDs {
			// No room to track this repo, so it cannot be capped either.
			l.overflow++
			return true, summary
		}
		bucket = &didBucket{tokens: l.rate, last: now}
		l.buckets[did] = bucket
	} else if elapsed := now.Sub(bucket.last); elapsed > 0 {
		bucket.tokens += elapsed.Minutes() * l.rate
		if bucket.tokens > l.rate {
			bucket.tokens = l.rate
		}
	}
	bucket.last = now

	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, summary
	}

	l.total++
	if _, seen := l.dids[did]; seen || len(l.dids) < maxStaleDIDs {
		l.dids[did]++
	} else {
		l.didsFull = true
	}
	return false, summary
}

// sweepLocked drops buckets whose DID has been quiet for longer than the idle
// TTL, at most once per rateLimitSweepInterval.
func (l *didLimiter) sweepLocked(now time.Time) {
	if !l.sweptAt.IsZero() && now.Sub(l.sweptAt) < rateLimitSweepInterval {
		return
	}
	l.sweptAt = now
	for did, bucket := range l.buckets {
		if now.Sub(bucket.last) > rateLimitIdleTTL {
			delete(l.buckets, did)
		}
	}
}

// roll closes the current hour and opens hour, returning the closed hour's
// summary.
func (l *didLimiter) roll(hour time.Time) *cappedSummary {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rollLocked(hour.UTC().Truncate(time.Hour))
}

// rollLocked closes the current hour and opens hour, returning the closed
// hour's summary. It returns nil when nothing was capped and no key was
// refused.
func (l *didLimiter) rollLocked(hour time.Time) *cappedSummary {
	var summary *cappedSummary
	if l.total > 0 || l.overflow > 0 {
		summary = &cappedSummary{
			hour:     l.hourStart,
			total:    l.total,
			distinct: len(l.dids),
			top:      topDIDsLocked(l.dids),
			overflow: l.overflow,
			didsFull: l.didsFull,
		}
	}
	l.hourStart = hour
	l.total = 0
	l.dids = make(map[string]int64)
	l.didsFull = false
	l.overflow = 0
	return summary
}

// tracked is the number of buckets currently held, for tests and diagnostics.
func (l *didLimiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// ---------------------------------------------------------------------------
// Consumer hook
// ---------------------------------------------------------------------------

// allowDIDRate reports whether a create may proceed under the per-DID cap,
// counting and logging the ones it drops. It is a no-op when the cap is off.
func (c *Consumer) allowDIDRate(event *Event) bool {
	if c.limiter == nil {
		return true
	}
	now := time.Now()
	if event.TimeUS > 0 {
		now = time.UnixMicro(event.TimeUS)
	}
	ok, summary := c.limiter.allow(event.DID, now)
	if summary != nil {
		slog.Info("capped posts hourly summary", summary.attrs()...)
	}
	if !ok {
		c.stats.PostsCapped.Add(1)
		c.offenders.addCapped(event.DID)
	}
	return ok
}
