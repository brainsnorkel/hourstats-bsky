package jetstream

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// captureHandler is a slog.Handler that keeps the records a test cares about.
// It is mutex-guarded because the consumer logs from its own goroutine while
// the test reads.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

// countMsg returns how many captured records carry msg.
func (h *captureHandler) countMsg(msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.records {
		if r.Message == msg {
			n++
		}
	}
	return n
}

// attrsForMsg returns the attributes of the first record carrying msg.
func (h *captureHandler) attrsForMsg(msg string) map[string]slog.Value {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message != msg {
			continue
		}
		out := make(map[string]slog.Value, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			out[a.Key] = a.Value
			return true
		})
		return out
	}
	return nil
}

// captureLogs installs the handler as the default logger for the test.
func captureLogs(t *testing.T) *captureHandler {
	t.Helper()
	h := &captureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// TestConsumerV2_StaleSampleRateLimit drives three backfilled creates through
// a consumer with a budget of two samples an hour: the guard must drop all
// three but log only two lines.
func TestConsumerV2_StaleSampleRateLimit(t *testing.T) {
	logs := captureLogs(t)

	witness := time.Now().UTC().Truncate(time.Second)
	old := witness.AddDate(0, 0, -400)

	endpoint := serveFrames(t, []string{
		v2PostFrame(30, "3old1", witness, old, "en"),
		v2PostFrame(31, "3old2", witness.Add(time.Second), old, "en"),
		v2PostFrame(32, "3old3", witness.Add(2*time.Second), old, "en"),
	})

	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           endpoint,
		DisableCompression: true,
		StaleSamplePerHour: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	waitFor(t, func() bool { return consumer.GetStatsReport().PostsStale == 3 })
	cancel()

	if got := logs.countMsg("stale create sample"); got != 2 {
		t.Errorf("sample lines = %d, want 2 (the per-hour budget)", got)
	}

	attrs := logs.attrsForMsg("stale create sample")
	if attrs == nil {
		t.Fatal("no stale create sample line was logged")
	}
	if got := attrs["did"].String(); got != "did:plc:aaa" {
		t.Errorf("did = %q, want did:plc:aaa", got)
	}
	if got := attrs["path"].String(); got != "parsed" {
		t.Errorf("path = %q, want parsed", got)
	}
	if got := attrs["created_at"].String(); got != old.Format(time.RFC3339) {
		t.Errorf("created_at = %q, want %q", got, old.Format(time.RFC3339))
	}
	if got := attrs["age_hours"].Float64(); got < 9500 || got > 9700 {
		t.Errorf("age_hours = %v, want about 9600 (400 days)", got)
	}
	if got := attrs["collection"].String(); got != DefaultCollection {
		t.Errorf("collection = %q, want %q", got, DefaultCollection)
	}
	if got := attrs["seq"].Int64(); got != 30 {
		t.Errorf("seq = %d, want 30", got)
	}
	if got := attrs["rev"].String(); got != "3lrev1" {
		t.Errorf("rev = %q, want 3lrev1", got)
	}
	if got := attrs["has_reply"].Bool(); got {
		t.Errorf("has_reply = %v, want false", got)
	}
	if got := attrs["text_len"].Int64(); got != int64(len("hello world")) {
		t.Errorf("text_len = %d, want %d", got, len("hello world"))
	}
}

// TestStaleDiagHourlySummary calls the tally directly with a fake clock: three
// stale creates from one DID must summarise as 3 from 1 distinct DID, with the
// oldest and newest record timestamps of that hour.
func TestStaleDiagHourlySummary(t *testing.T) {
	d := newStaleDiag(2)
	hour := time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC)
	oldest := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	newest := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	for i, created := range []time.Time{newest, oldest, newest.Add(-time.Hour)} {
		summary, sample := d.observe(hour.Add(time.Duration(i)*time.Minute), "did:plc:aaa", created)
		if summary != nil {
			t.Fatalf("observe %d returned a summary inside the hour", i)
		}
		if want := i < 2; sample != want {
			t.Errorf("observe %d sample = %v, want %v", i, sample, want)
		}
	}

	summary := d.roll(hour.Add(time.Hour))
	if summary == nil {
		t.Fatal("roll returned no summary")
	}
	if summary.total != 3 {
		t.Errorf("total_stale = %d, want 3", summary.total)
	}
	if summary.distinct != 1 {
		t.Errorf("distinct_dids = %d, want 1", summary.distinct)
	}
	if len(summary.top) != 1 || summary.top[0] != "did:plc:aaa=3" {
		t.Errorf("top_dids = %v, want [did:plc:aaa=3]", summary.top)
	}
	if !summary.oldest.Equal(oldest) {
		t.Errorf("oldest = %s, want %s", summary.oldest, oldest)
	}
	if !summary.newest.Equal(newest) {
		t.Errorf("newest = %s, want %s", summary.newest, newest)
	}

	// The new hour starts with a fresh tally and a fresh sample budget.
	if _, sample := d.observe(hour.Add(time.Hour), "did:plc:bbb", newest); !sample {
		t.Error("the first event of a new hour was not sampled")
	}
	if next := d.roll(hour.Add(2 * time.Hour)); next == nil || next.total != 1 {
		t.Errorf("second hour = %+v, want a summary of 1", next)
	}
}

// TestStaleDiagHourRollover checks that crossing the hour boundary through
// observe returns the closed hour rather than waiting for a caller to roll it.
func TestStaleDiagHourRollover(t *testing.T) {
	d := newStaleDiag(1)
	hour := time.Date(2026, 9, 12, 4, 30, 0, 0, time.UTC)
	created := time.Date(2025, 9, 12, 4, 0, 0, 0, time.UTC)

	if summary, _ := d.observe(hour, "did:plc:aaa", created); summary != nil {
		t.Fatal("the first event closed an hour")
	}
	summary, sample := d.observe(hour.Add(time.Hour), "did:plc:bbb", created)
	if summary == nil {
		t.Fatal("crossing the hour boundary returned no summary")
	}
	if summary.total != 1 || summary.distinct != 1 {
		t.Errorf("summary = %d stale from %d dids, want 1 from 1", summary.total, summary.distinct)
	}
	if !sample {
		t.Error("the first event of the new hour was not sampled")
	}
}

// TestStaleDiagDIDCap checks the bound: past maxStaleDIDs no new key is added,
// the ones already tallied keep counting, and the summary says so.
func TestStaleDiagDIDCap(t *testing.T) {
	d := newStaleDiag(1)
	hour := time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC)
	for i := 0; i < maxStaleDIDs+10; i++ {
		d.observe(hour, fmt.Sprintf("did:plc:%d", i), time.Time{})
	}
	d.observe(hour, "did:plc:0", time.Time{})

	summary := d.roll(hour.Add(time.Hour))
	if summary.distinct != maxStaleDIDs {
		t.Errorf("distinct_dids = %d, want the cap %d", summary.distinct, maxStaleDIDs)
	}
	if summary.total != int64(maxStaleDIDs+11) {
		t.Errorf("total_stale = %d, want every event counted", summary.total)
	}
	if !summary.didsFull {
		t.Error("didsFull = false, want the summary to report the cap was hit")
	}
	if len(summary.top) == 0 || summary.top[0] != "did:plc:0=2" {
		t.Errorf("top_dids[0] = %v, want the twice-seen did:plc:0", summary.top)
	}
}

// TestStaleSampleOffByDefault is the production guarantee: with no budget, a
// backfilled create is still dropped and counted but logs nothing.
func TestStaleSampleOffByDefault(t *testing.T) {
	logs := captureLogs(t)

	witness := time.Now().UTC().Truncate(time.Second)
	endpoint := serveFrames(t, []string{
		v2PostFrame(40, "3old", witness, witness.AddDate(0, 0, -400), "en"),
	})

	consumer := NewConsumer(ConsumerConfig{Endpoint: endpoint, DisableCompression: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	waitFor(t, func() bool { return consumer.GetStatsReport().PostsStale == 1 })
	cancel()

	if got := logs.countMsg("stale create sample"); got != 0 {
		t.Errorf("sample lines = %d, want 0 with the diagnostic off", got)
	}
}

// TestConsumerV2_StaleSamplePrefilterPath covers the other stale path: a
// non-English backfill is dropped by the bytes-level pre-filter, which can
// still name the DID and the record timestamp without parsing the frame.
func TestConsumerV2_StaleSamplePrefilterPath(t *testing.T) {
	logs := captureLogs(t)

	witness := time.Now().UTC().Truncate(time.Second)
	old := witness.AddDate(0, 0, -400)
	endpoint := serveFrames(t, []string{
		// A live frame first, so the witness clock the pre-filter ages
		// against comes from the stream rather than the wall clock.
		v2PostFrame(50, "3live", witness, witness.Add(-time.Minute), "en"),
		v2PostFrame(51, "3oldja", witness.Add(time.Second), old, "ja"),
	})

	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           endpoint,
		DisableCompression: true,
		StaleSamplePerHour: 5,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	waitFor(t, func() bool { return consumer.GetStatsReport().PostsStale == 1 })
	cancel()

	attrs := logs.attrsForMsg("stale create sample")
	if attrs == nil {
		t.Fatal("no stale create sample line was logged")
	}
	if got := attrs["path"].String(); got != "prefilter" {
		t.Errorf("path = %q, want prefilter", got)
	}
	if got := attrs["did"].String(); got != "did:plc:aaa" {
		t.Errorf("did = %q, want did:plc:aaa", got)
	}
	if got := attrs["created_at"].String(); got != old.Format(time.RFC3339) {
		t.Errorf("created_at = %q, want %q", got, old.Format(time.RFC3339))
	}
	if got := attrs["first_lang"].String(); got != "ja" {
		t.Errorf("first_lang = %q, want ja", got)
	}
	if got := attrs["age_hours"].Float64(); got < 9500 || got > 9700 {
		t.Errorf("age_hours = %v, want about 9600 (400 days)", got)
	}
}

// TestStaleSummaryLineCarriesKindCounts is the correlation requirement: the
// hourly summary names the measured-kind volume for the same hour, and the
// baseline moves forward so the next hour is not cumulative.
func TestStaleSummaryLineCarriesKindCounts(t *testing.T) {
	logs := captureLogs(t)

	c := NewConsumer(ConsumerConfig{
		Endpoint:           "wss://jetstream.us-west.bsky.network/xrpc/" + subscribeNSID,
		ExtraKinds:         []string{"sync", "identity"},
		StaleSamplePerHour: 1,
	})
	c.countExtraKind("sync", 100)
	c.countExtraKind("sync", 100)
	c.countExtraKind("identity", 50)

	hour := time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC)
	c.diag.observe(hour, "did:plc:aaa", hour.AddDate(-1, 0, 0))
	c.logStaleSummary(c.diag.roll(hour.Add(time.Hour)))

	attrs := logs.attrsForMsg("stale create hourly summary")
	if attrs == nil {
		t.Fatal("no stale create hourly summary line was logged")
	}
	if got := attrs["total_stale"].Int64(); got != 1 {
		t.Errorf("total_stale = %d, want 1", got)
	}
	if got := attrs["distinct_dids"].Int64(); got != 1 {
		t.Errorf("distinct_dids = %d, want 1", got)
	}
	if got := attrs["hour"].String(); got != hour.Format(time.RFC3339) {
		t.Errorf("hour = %q, want %q", got, hour.Format(time.RFC3339))
	}
	if got := attrs["oldest_created_at"].String(); got != hour.AddDate(-1, 0, 0).Format(time.RFC3339) {
		t.Errorf("oldest_created_at = %q, want the record timestamp", got)
	}
	if got := attrs["sync.events"].Int64(); got != 2 {
		t.Errorf("sync.events = %d, want 2", got)
	}
	if got := attrs["identity.events"].Int64(); got != 1 {
		t.Errorf("identity.events = %d, want 1", got)
	}

	// A second summary reports the delta, not the run total.
	c.countExtraKind("sync", 100)
	c.diag.observe(hour.Add(time.Hour), "did:plc:bbb", hour)
	c.logStaleSummary(c.diag.roll(hour.Add(2 * time.Hour)))
	if got := logs.countMsg("stale create hourly summary"); got != 2 {
		t.Fatalf("summary lines = %d, want 2", got)
	}
	logs.mu.Lock()
	last := logs.records[len(logs.records)-1]
	logs.mu.Unlock()
	var syncEvents int64
	last.Attrs(func(a slog.Attr) bool {
		if a.Key == "sync.events" {
			syncEvents = a.Value.Int64()
		}
		return true
	})
	if syncEvents != 1 {
		t.Errorf("second hour sync.events = %d, want 1 (the delta)", syncEvents)
	}
}

func TestScanFrameDID(t *testing.T) {
	frame := v2PostFrame(1, "3abc", time.Now(), time.Now(), "en")
	if got := scanFrameDID([]byte(frame)); got != "did:plc:aaa" {
		t.Errorf("scanFrameDID = %q, want did:plc:aaa", got)
	}
	if got := scanFrameDID([]byte(`{"seq":1}`)); got != "" {
		t.Errorf("scanFrameDID with no did = %q, want empty", got)
	}
	if got := scanFrameDID([]byte(`{"did":"` + string(make([]byte, 200)))); got != "" {
		t.Errorf("scanFrameDID with an unterminated did = %q, want empty", got)
	}
}
