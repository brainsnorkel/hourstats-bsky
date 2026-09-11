package jetstream

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// v2KindFrame builds a bare #identity or #sync frame: the envelope fields the
// kinds share, with no commit payload.
func v2KindFrame(kind string, seq int64, witness time.Time) string {
	return fmt.Sprintf(`{"$type":"message","payload":{"$type":"%s#%s",`+
		`"seq":%d,"did":"did:plc:bbb","time":"%s"}}`,
		subscribeNSID, kind, seq, witness.UTC().Format(time.RFC3339Nano))
}

// TestBuildURLV2_ExtraKinds checks the subscription asks for the measured
// kinds on top of the two the bot consumes, and that commit/account/garbage
// are filtered out rather than sent back to the server.
func TestBuildURLV2_ExtraKinds(t *testing.T) {
	c := NewConsumer(ConsumerConfig{
		Endpoint:   "wss://jetstream.us-west.bsky.network/xrpc/" + subscribeNSID,
		ExtraKinds: []string{"sync", "identity", "commit", "account", "nonsense"},
	})

	q := mustQuery(t, c.buildURLV2())
	got := q["kinds"]
	want := []string{"commit", "account", "sync", "identity"}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}

// TestConsumerV2_ExtraKindsCounted is the measurement contract: a #sync and an
// #identity frame are counted by kind and dropped without being decoded, while
// the post create on the same connection is delivered as usual.
func TestConsumerV2_ExtraKindsCounted(t *testing.T) {
	witness := time.Now().UTC().Truncate(time.Second)

	endpoint := serveFrames(t, []string{
		v2KindFrame("sync", 60, witness),
		v2KindFrame("identity", 61, witness.Add(time.Second)),
		v2PostFrame(62, "3live", witness.Add(2*time.Second), witness.Add(time.Minute*-1), "en"),
	})

	var (
		mu    sync.Mutex
		posts []string
	)
	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           endpoint,
		DisableCompression: true,
		ExtraKinds:         []string{"sync", "identity"},
		OnPost: func(evt *Event, _ *PostRecord) {
			mu.Lock()
			posts = append(posts, evt.PostURI())
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	waitFor(t, func() bool { return consumer.GetStatsReport().PostsProcessed == 1 })
	cancel()

	report := consumer.GetStatsReport()
	if report.EventsByKind["sync"] != 1 {
		t.Errorf("EventsByKind[sync] = %d, want 1", report.EventsByKind["sync"])
	}
	if report.EventsByKind["identity"] != 1 {
		t.Errorf("EventsByKind[identity] = %d, want 1", report.EventsByKind["identity"])
	}
	if report.EventsSkipped != 0 {
		t.Errorf("EventsSkipped = %d, want 0: measured kinds are dropped before dispatch", report.EventsSkipped)
	}
	if report.Errors != 0 {
		t.Errorf("Errors = %d, want 0", report.Errors)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || posts[0] != "at://did:plc:aaa/app.bsky.feed.post/3live" {
		t.Errorf("posts = %v, want the one live create", posts)
	}
}

// TestConsumerV2_ExtraKindsOffByDefault is the production guarantee: with no
// ExtraKinds the report carries no kind map and the URL is unchanged.
func TestConsumerV2_ExtraKindsOffByDefault(t *testing.T) {
	c := NewConsumer(ConsumerConfig{
		Endpoint: "wss://jetstream.us-west.bsky.network/xrpc/" + subscribeNSID,
	})
	if got := mustQuery(t, c.buildURLV2())["kinds"]; len(got) != 2 {
		t.Errorf("kinds = %v, want only [commit account]", got)
	}
	if got := c.GetStatsReport().EventsByKind; got != nil {
		t.Errorf("EventsByKind = %v, want nil with kind measurement off", got)
	}
}
