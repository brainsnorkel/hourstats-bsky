package jetstream

import (
	"bytes"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	// maxStaleDIDs bounds the per-hour DID tally. Once it is full no new key
	// is added — the DIDs already there keep counting — so a diagnostic left
	// on cannot grow without limit on a stream where every stale create has a
	// different author.
	maxStaleDIDs = 50000

	// staleTopDIDs is how many of the busiest DIDs the hourly summary names.
	staleTopDIDs = 10
)

// staleDiag is the staging-only diagnostic behind ConsumerConfig's
// StaleSamplePerHour: a token-limited sample of the creates the MaxPostAge
// guard drops, plus an hourly tally of the DIDs behind them. It is nil when
// the diagnostic is off, which is what prod runs.
type staleDiag struct {
	perHour int

	mu        sync.Mutex
	hourStart time.Time
	sampled   int
	total     int64
	dids      map[string]int64
	didsFull  bool
	oldest    time.Time
	newest    time.Time
}

// newStaleDiag returns nil when the diagnostic is off, so every call site is a
// single nil check on the hot path.
func newStaleDiag(perHour int) *staleDiag {
	if perHour <= 0 {
		return nil
	}
	return &staleDiag{perHour: perHour, dids: make(map[string]int64)}
}

// staleSummary is one closed hour of the tally.
type staleSummary struct {
	hour     time.Time
	total    int64
	distinct int
	top      []string
	oldest   time.Time
	newest   time.Time
	didsFull bool

	// kinds are the measured extra-kind frame counts for the same hour, so
	// the two diagnostics can be read against each other on one line.
	kinds map[string]int64
}

// observe registers one dropped stale create. created is the record's own
// timestamp, or the zero time when it could not be read. It returns the
// summary of the hour that just ended, when this event opens a new one, and
// whether this event falls within the hour's sample budget.
func (d *staleDiag) observe(now time.Time, did string, created time.Time) (*staleSummary, bool) {
	hour := now.UTC().Truncate(time.Hour)

	d.mu.Lock()
	defer d.mu.Unlock()

	var summary *staleSummary
	switch {
	case d.hourStart.IsZero():
		d.hourStart = hour
	case hour.After(d.hourStart):
		summary = d.rollLocked(hour)
	}

	d.total++
	if did != "" {
		if _, seen := d.dids[did]; seen || len(d.dids) < maxStaleDIDs {
			d.dids[did]++
		} else {
			d.didsFull = true
		}
	}
	if !created.IsZero() {
		if d.oldest.IsZero() || created.Before(d.oldest) {
			d.oldest = created
		}
		if d.newest.IsZero() || created.After(d.newest) {
			d.newest = created
		}
	}

	sample := d.sampled < d.perHour
	if sample {
		d.sampled++
	}
	return summary, sample
}

// roll closes the current hour and opens hour, returning the closed hour's
// summary. It returns nil when the closed hour saw nothing.
func (d *staleDiag) roll(hour time.Time) *staleSummary {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rollLocked(hour.UTC().Truncate(time.Hour))
}

func (d *staleDiag) rollLocked(hour time.Time) *staleSummary {
	var summary *staleSummary
	if d.total > 0 {
		summary = &staleSummary{
			hour:     d.hourStart,
			total:    d.total,
			distinct: len(d.dids),
			top:      topDIDsLocked(d.dids),
			oldest:   d.oldest,
			newest:   d.newest,
			didsFull: d.didsFull,
		}
	}
	d.hourStart = hour
	d.sampled = 0
	d.total = 0
	d.dids = make(map[string]int64)
	d.didsFull = false
	d.oldest = time.Time{}
	d.newest = time.Time{}
	return summary
}

// topDIDsLocked returns the busiest DIDs as `<did>=<count>`, highest first and
// tie-broken by DID so the line is stable between runs.
func topDIDsLocked(dids map[string]int64) []string {
	type didCount struct {
		did   string
		count int64
	}
	pairs := make([]didCount, 0, len(dids))
	for did, count := range dids {
		pairs = append(pairs, didCount{did, count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].did < pairs[j].did
	})
	if len(pairs) > staleTopDIDs {
		pairs = pairs[:staleTopDIDs]
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p.did+"="+strconv.FormatInt(p.count, 10))
	}
	return out
}

// attrs renders the summary as slog key-value pairs.
func (s *staleSummary) attrs() []any {
	attrs := []any{
		"hour", s.hour.Format(time.RFC3339),
		"total_stale", s.total,
		"distinct_dids", s.distinct,
		"top_dids", s.top,
		"oldest_created_at", formatTimeOrEmpty(s.oldest),
		"newest_created_at", formatTimeOrEmpty(s.newest),
	}
	if s.didsFull {
		attrs = append(attrs, "dids_truncated", true)
	}
	kinds := make([]string, 0, len(s.kinds))
	for kind := range s.kinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		attrs = append(attrs, kind+".events", s.kinds[kind])
	}
	return attrs
}

func formatTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ---------------------------------------------------------------------------
// Consumer hooks
// ---------------------------------------------------------------------------

// recordStale feeds the diagnostic from the parsed path, where the whole event
// is in hand. created is derived from the witness time and the measured age
// rather than re-parsing the record.
func (c *Consumer) recordStale(event *Event, record *PostRecord, age time.Duration) {
	if c.diag == nil {
		return
	}
	witness := time.UnixMicro(event.TimeUS).UTC()
	summary, sample := c.diag.observe(witness, event.DID, witness.Add(-age))
	c.logStaleSummary(summary)
	if !sample {
		return
	}
	rev, collection := "", ""
	if event.Commit != nil {
		rev, collection = event.Commit.Rev, event.Commit.Collection
	}
	slog.Info("stale create sample",
		"path", "parsed",
		"did", event.DID,
		"created_at", record.CreatedAt,
		"witness_time", witness.Format(time.RFC3339),
		"age_hours", ageHours(age),
		"rev", rev,
		"seq", event.Seq,
		"collection", collection,
		"has_reply", record.Reply != nil,
		"text_len", len([]rune(record.Text)),
	)
}

// recordStaleFrame feeds the diagnostic from the bytes-level pre-filter, which
// drops a non-English backfill before any parse. Only the DID and createdAt
// are recoverable by byte scan, so rev, seq, has_reply and text_len are absent
// on these lines; the first language tag the scan already read takes their
// place, since it is the question this path exists to answer.
func (c *Consumer) recordStaleFrame(data []byte, firstLang string) {
	if c.diag == nil {
		return
	}
	witness := time.Now().UTC()
	if timeUS := c.cursor.Load(); timeUS > 0 {
		witness = time.UnixMicro(timeUS).UTC()
	}
	rawCreated := scanFrameCreatedAt(data)
	created, _ := parsePostCreatedAt(rawCreated)
	did := scanFrameDID(data)

	summary, sample := c.diag.observe(witness, did, created)
	c.logStaleSummary(summary)
	if !sample {
		return
	}
	age := time.Duration(0)
	if !created.IsZero() {
		age = witness.Sub(created)
	}
	slog.Info("stale create sample",
		"path", "prefilter",
		"did", did,
		"created_at", rawCreated,
		"witness_time", witness.Format(time.RFC3339),
		"age_hours", ageHours(age),
		"collection", DefaultCollection,
		"first_lang", firstLang,
	)
}

// logStaleSummary emits a closed hour, attaching the extra-kind counts for the
// same hour when kind measurement is on.
func (c *Consumer) logStaleSummary(summary *staleSummary) {
	if summary == nil {
		return
	}
	summary.kinds = c.kindCountsSinceSummary()
	slog.Info("stale create hourly summary", summary.attrs()...)
}

// ageHours renders an age in hours to one decimal, the resolution that
// separates "a few minutes of clock skew" from "a repo backfill".
func ageHours(age time.Duration) float64 {
	return float64(int64(age.Hours()*10+0.5)) / 10
}

// scanFrameDID returns the first `"did":"..."` string value in a raw frame,
// which in both wire protocols is the repo the event belongs to. It returns ""
// when the key is absent or the value runs past the bound.
func scanFrameDID(data []byte) string {
	const key = `"did":"`
	idx := bytes.Index(data, []byte(key))
	if idx < 0 {
		return ""
	}
	pos := idx + len(key)
	// Comfortably longer than did:plc: plus a 24-char identifier, and long
	// enough for the did:web forms that appear on the network.
	limit := pos + 128
	if limit > len(data) {
		limit = len(data)
	}
	for i := pos; i < limit; i++ {
		if data[i] == '"' {
			return string(data[pos:i])
		}
	}
	return ""
}
