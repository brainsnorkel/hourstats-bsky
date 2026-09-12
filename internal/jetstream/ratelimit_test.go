package jetstream

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDIDLimiterCapsAndRefills exercises one bucket: a minute's worth of
// creates is admitted as a burst, the next is capped, and the bucket refills at
// the configured rate rather than all at once.
func TestDIDLimiterCapsAndRefills(t *testing.T) {
	l := newDIDLimiter(6)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	const did = "did:plc:loud"

	for i := 0; i < 6; i++ {
		if ok, _ := l.allow(did, now); !ok {
			t.Fatalf("create %d capped, want the first 6 admitted", i+1)
		}
	}
	if ok, _ := l.allow(did, now); ok {
		t.Fatal("create 7 admitted, want it capped")
	}

	// Ten seconds is a sixth of a minute, so exactly one token returns.
	now = now.Add(10 * time.Second)
	if ok, _ := l.allow(did, now); !ok {
		t.Fatal("create capped after a 10s refill, want one token back")
	}
	if ok, _ := l.allow(did, now); ok {
		t.Fatal("second create at the same instant admitted, want only one token back")
	}

	// The refill is capped at the bucket's capacity: an hour of quiet does not
	// buy an hour's worth of burst.
	now = now.Add(time.Hour)
	for i := 0; i < 6; i++ {
		if ok, _ := l.allow(did, now); !ok {
			t.Fatalf("create %d capped after an hour idle, want a full bucket", i+1)
		}
	}
	if ok, _ := l.allow(did, now); ok {
		t.Fatal("create 7 admitted after an hour idle, want the burst capped at the rate")
	}
}

// TestDIDLimiterIsPerDID: one loud repo must not spend another's budget.
func TestDIDLimiterIsPerDID(t *testing.T) {
	l := newDIDLimiter(1)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	if ok, _ := l.allow("did:plc:a", now); !ok {
		t.Fatal("first create from a capped")
	}
	if ok, _ := l.allow("did:plc:a", now); ok {
		t.Fatal("second create from a admitted, want it capped")
	}
	if ok, _ := l.allow("did:plc:b", now); !ok {
		t.Fatal("first create from b capped; the cap is per DID")
	}
}

// TestDIDLimiterDisabled: newDIDLimiter returns nil for a non-positive rate, so
// the hot path is a single nil check and nothing is ever capped.
func TestDIDLimiterDisabled(t *testing.T) {
	for _, rate := range []int{0, -1} {
		if l := newDIDLimiter(rate); l != nil {
			t.Errorf("newDIDLimiter(%d) = %v, want nil", rate, l)
		}
	}
}

// TestDIDLimiterBoundedMap: once the map is full no new key is added, and the
// create is admitted rather than refused — an untracked repo cannot be capped,
// and the refusal is reported as map_overflow.
func TestDIDLimiterBoundedMap(t *testing.T) {
	l := newDIDLimiter(1)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	for i := 0; i < maxRateLimitDIDs; i++ {
		if ok, _ := l.allow(fmt.Sprintf("did:plc:%d", i), now); !ok {
			t.Fatalf("first create from did %d capped", i)
		}
	}
	if got := l.tracked(); got != maxRateLimitDIDs {
		t.Fatalf("tracked = %d, want %d", got, maxRateLimitDIDs)
	}

	if ok, _ := l.allow("did:plc:one-too-many", now); !ok {
		t.Error("create from an untracked did capped, want it admitted")
	}
	if got := l.tracked(); got != maxRateLimitDIDs {
		t.Errorf("tracked = %d after the overflow, want the map to stay at %d", got, maxRateLimitDIDs)
	}

	summary := l.roll(now.Add(time.Hour))
	if summary == nil {
		t.Fatal("summary = nil, want one reporting the overflow")
	}
	if summary.overflow != 1 {
		t.Errorf("map_overflow = %d, want 1", summary.overflow)
	}
}

// TestDIDLimiterSweepsIdleBuckets: a bucket is reclaimed once its DID has been
// quiet for longer than the idle TTL, so a day of one-post authors does not
// pin the map at its bound.
func TestDIDLimiterSweepsIdleBuckets(t *testing.T) {
	l := newDIDLimiter(60)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	l.allow("did:plc:gone", now)
	if got := l.tracked(); got != 1 {
		t.Fatalf("tracked = %d, want 1", got)
	}

	// Past the idle TTL and past the sweep interval, so the next call sweeps.
	later := now.Add(rateLimitIdleTTL + time.Minute)
	l.allow("did:plc:here", later)
	if got := l.tracked(); got != 1 {
		t.Errorf("tracked = %d, want 1: the idle bucket should have been swept", got)
	}

	// The sweep runs at most once a minute, so a second call inside the window
	// leaves the map alone.
	l.allow("did:plc:third", later.Add(time.Second))
	if got := l.tracked(); got != 2 {
		t.Errorf("tracked = %d, want 2", got)
	}
}

// TestDIDLimiterHourlySummary: the hour that just closed is reported with its
// total, its distinct DIDs and the busiest of them, and the tally then starts
// clean.
func TestDIDLimiterHourlySummary(t *testing.T) {
	l := newDIDLimiter(1)
	hour := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	// One admitted create each, then two capped for a and one for b.
	for _, did := range []string{"did:plc:a", "did:plc:b"} {
		l.allow(did, hour)
	}
	l.allow("did:plc:a", hour)
	l.allow("did:plc:a", hour)
	l.allow("did:plc:b", hour)

	ok, summary := l.allow("did:plc:a", hour.Add(time.Hour))
	if !ok {
		t.Error("the first create of the new hour was capped, want a fresh minute of tokens")
	}
	if summary == nil {
		t.Fatal("summary = nil, want the closed hour")
	}
	if summary.total != 3 {
		t.Errorf("total_capped = %d, want 3", summary.total)
	}
	if summary.distinct != 2 {
		t.Errorf("distinct_dids = %d, want 2", summary.distinct)
	}
	if len(summary.top) == 0 || !strings.HasPrefix(summary.top[0], "did:plc:a=2") {
		t.Errorf("top_dids = %v, want did:plc:a=2 first", summary.top)
	}
	if !summary.hour.Equal(hour) {
		t.Errorf("hour = %v, want %v", summary.hour, hour)
	}

	// The hour that follows capped nothing, so it reports nothing.
	if got := l.roll(hour.Add(2 * time.Hour)); got != nil {
		t.Errorf("summary for a quiet hour = %+v, want nil", got)
	}
}

// TestDispatchCapsCreatesNotDeletes is the protocol-agnostic half: dispatch
// applies the cap to creates only, so a denied-by-rate repo's deletes still
// reach the caller and still remove its rows.
func TestDispatchCapsCreatesNotDeletes(t *testing.T) {
	var mu sync.Mutex
	var posts, deletes []string

	c := NewConsumer(ConsumerConfig{
		MaxPostsPerDIDPerMinute: 2,
		MaxPostAge:              -1,
		MaxPostFuture:           -1,
		OnPost: func(evt *Event, _ *PostRecord) {
			mu.Lock()
			posts = append(posts, evt.PostURI())
			mu.Unlock()
		},
		OnDelete: func(evt *Event) {
			mu.Lock()
			deletes = append(deletes, evt.PostURI())
			mu.Unlock()
		},
	})

	witness := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	create := func(rkey string) *Event {
		return &Event{
			DID:    "did:plc:loud",
			TimeUS: witness.UnixMicro(),
			Kind:   "commit",
			Commit: &Commit{
				Rev:        "r1",
				Operation:  "create",
				Collection: DefaultCollection,
				Rkey:       rkey,
				Record: []byte(`{"$type":"app.bsky.feed.post","text":"hi",` +
					`"createdAt":"2026-09-11T12:00:00Z","langs":["en"]}`),
			},
		}
	}

	for _, rkey := range []string{"3a", "3b", "3c", "3d"} {
		c.dispatch(create(rkey))
	}
	c.dispatch(&Event{
		DID:    "did:plc:loud",
		TimeUS: witness.UnixMicro(),
		Kind:   "commit",
		Commit: &Commit{Rev: "r2", Operation: "delete", Collection: DefaultCollection, Rkey: "3a"},
	})

	report := c.GetStatsReport()
	if report.PostsCapped != 2 {
		t.Errorf("PostsCapped = %d, want 2", report.PostsCapped)
	}
	if report.PostsProcessed != 2 {
		t.Errorf("PostsProcessed = %d, want 2", report.PostsProcessed)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 2 {
		t.Errorf("posts = %v, want the first two creates only", posts)
	}
	if len(deletes) != 1 {
		t.Errorf("deletes = %v, want the delete through despite the cap", deletes)
	}
}

// TestConsumerV2_CapAppliesToTheWire checks the cap on the real read loop, so
// the over-cap creates are dropped before the caller counts them at all.
func TestConsumerV2_CapAppliesToTheWire(t *testing.T) {
	witness := time.Now().UTC().Truncate(time.Second)
	frames := []string{
		v2PostFrame(1, "3a", witness, witness, "en"),
		v2PostFrame(2, "3b", witness, witness, "en"),
		v2PostFrame(3, "3c", witness, witness, "en"),
	}

	var counted int
	var mu sync.Mutex
	consumer := NewConsumer(ConsumerConfig{
		Protocol:                ProtocolV2,
		Endpoint:                serveFrames(t, frames),
		DisableCompression:      true,
		MaxPostsPerDIDPerMinute: 1,
		OnPost: func(*Event, *PostRecord) {
			mu.Lock()
			counted++
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	waitFor(t, func() bool { return consumer.GetStatsReport().PostsCapped == 2 })

	mu.Lock()
	defer mu.Unlock()
	if counted != 1 {
		t.Errorf("OnPost calls = %d, want 1: the over-cap creates must not be counted", counted)
	}
}
