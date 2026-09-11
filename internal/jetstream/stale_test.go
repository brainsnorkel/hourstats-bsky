package jetstream

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// v2PostFrame builds a post create frame with an explicit witness time, record
// createdAt and language tag, so a test can drive the backfill guard.
func v2PostFrame(seq int64, rkey string, witness, createdAt time.Time, lang string) string {
	return fmt.Sprintf(`{"$type":"message","payload":{"$type":"network.bsky.jetstream.subscribeEvents#commit",`+
		`"seq":%d,"did":"did:plc:aaa","time":"%s","rev":"3lrev1","operation":"create",`+
		`"collection":"app.bsky.feed.post","rkey":"%s","cid":"bafycreate",`+
		`"record":{"$type":"app.bsky.feed.post","text":"hello world","createdAt":"%s","langs":["%s"]}}}`,
		seq, witness.UTC().Format(time.RFC3339Nano), rkey,
		createdAt.UTC().Format(time.RFC3339), lang)
}

// serveFrames starts a v2 endpoint that writes the given text frames once and
// then holds the connection open.
func serveFrames(t *testing.T, frames []string) string {
	t.Helper()
	upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for _, f := range frames {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(f)); err != nil {
				return
			}
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return wsEndpoint(srv.URL)
}

// TestConsumerV2_BackfillDropped is the staging case: v2 delivers repo
// backfill through the live tail as ordinary creates with fresh seq numbers
// and a current witness time, so only the record's createdAt tells them apart.
// The English backfill must not reach OnPost and the non-English one must not
// reach OnEarlyReject, or it doubles the firehose and language totals.
func TestConsumerV2_BackfillDropped(t *testing.T) {
	witness := time.Now().UTC().Truncate(time.Second)
	old := witness.AddDate(0, 0, -400)

	endpoint := serveFrames(t, []string{
		v2PostFrame(10, "3fresh", witness, witness.Add(-time.Minute), "en"),
		v2PostFrame(11, "3oldEN", witness.Add(time.Second), old, "en"),
		v2PostFrame(12, "3oldJA", witness.Add(2*time.Second), old, "ja"),
	})

	var (
		mu       sync.Mutex
		posts    []string
		stale    []time.Duration
		rejected []string
	)
	done := make(chan struct{})
	var once sync.Once

	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           endpoint,
		DisableCompression: true,
		OnPost: func(evt *Event, _ *PostRecord) {
			mu.Lock()
			posts = append(posts, evt.PostURI())
			mu.Unlock()
		},
		OnStale: func(_ *Event, _ *PostRecord, age time.Duration) {
			mu.Lock()
			stale = append(stale, age)
			mu.Unlock()
			once.Do(func() { close(done) })
		},
		OnEarlyReject: func(firstLang string) {
			mu.Lock()
			rejected = append(rejected, firstLang)
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the stale create")
	}
	// The Japanese backfill is rejected before parsing, so it never reaches a
	// callback; wait for the consumer's counter instead.
	waitFor(t, func() bool { return consumer.GetStatsReport().PostsStale == 2 })
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || posts[0] != "at://did:plc:aaa/app.bsky.feed.post/3fresh" {
		t.Errorf("posts = %v, want only the fresh create", posts)
	}
	if len(stale) != 1 {
		t.Fatalf("OnStale called %d times, want 1", len(stale))
	}
	if stale[0] < 399*24*time.Hour {
		t.Errorf("reported age = %s, want about 400 days", stale[0])
	}
	if len(rejected) != 0 {
		t.Errorf("OnEarlyReject called with %v, want no calls", rejected)
	}

	report := consumer.GetStatsReport()
	if report.PostsStale != 2 {
		t.Errorf("PostsStale = %d, want 2", report.PostsStale)
	}
	if report.PostsProcessed != 1 {
		t.Errorf("PostsProcessed = %d, want 1", report.PostsProcessed)
	}
	if report.EarlyRejectedNonEnglish != 0 {
		t.Errorf("EarlyRejectedNonEnglish = %d, want 0", report.EarlyRejectedNonEnglish)
	}
}

// TestConsumerV2_LiveNonEnglishStillCounted is the other half: a live
// non-English post is still early-rejected and still counted, so the firehose
// and language totals are unchanged for everything that is not backfill.
func TestConsumerV2_LiveNonEnglishStillCounted(t *testing.T) {
	witness := time.Now().UTC().Truncate(time.Second)
	endpoint := serveFrames(t, []string{
		v2PostFrame(20, "3ja", witness, witness.Add(-time.Minute), "ja"),
	})

	rejected := make(chan string, 1)
	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           endpoint,
		DisableCompression: true,
		OnEarlyReject: func(firstLang string) {
			select {
			case rejected <- firstLang:
			default:
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	select {
	case lang := <-rejected:
		if lang != "ja" {
			t.Errorf("OnEarlyReject lang = %q, want %q", lang, "ja")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the early reject")
	}
	cancel()

	if got := consumer.GetStatsReport().PostsStale; got != 0 {
		t.Errorf("PostsStale = %d, want 0", got)
	}
}

// TestConsumerV2_MaxPostAgeDisabled checks the rollback lever: a negative
// MaxPostAge keeps every create, however old.
func TestConsumerV2_MaxPostAgeDisabled(t *testing.T) {
	witness := time.Now().UTC().Truncate(time.Second)
	endpoint := serveFrames(t, []string{
		v2PostFrame(30, "3old", witness, witness.AddDate(-2, 0, 0), "en"),
	})

	posted := make(chan struct{}, 1)
	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           endpoint,
		DisableCompression: true,
		MaxPostAge:         -1,
		OnPost: func(*Event, *PostRecord) {
			select {
			case posted <- struct{}{}:
			default:
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-posted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: the create was dropped with the age check disabled")
	}
	cancel()
}

func TestPostAge(t *testing.T) {
	witness := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	c := NewConsumer(ConsumerConfig{})
	evt := &Event{TimeUS: witness.UnixMicro()}

	tests := []struct {
		name      string
		createdAt string
		wantStale bool
	}{
		{"live", "2026-09-11T11:59:00Z", false},
		{"at the threshold", "2026-09-11T10:00:00Z", false},
		{"just past the threshold", "2026-09-11T09:59:59Z", true},
		{"backfill from 2024", "2024-03-01T08:00:00Z", true},
		{"nanosecond precision", "2026-09-11T11:59:00.123456789Z", false},
		{"numeric offset", "2026-09-11T13:59:00+02:00", false},
		{"future clock skew", "2026-09-11T12:30:00Z", false},
		{"unparseable", "yesterday", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stale := c.postAge(evt, &PostRecord{CreatedAt: tt.createdAt})
			if stale != tt.wantStale {
				t.Errorf("postAge(%q) stale = %v, want %v", tt.createdAt, stale, tt.wantStale)
			}
		})
	}
}

func TestScanFrameCreatedAt(t *testing.T) {
	frame := v2PostFrame(1, "3abc",
		time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
		time.Date(2025, 6, 13, 9, 30, 0, 0, time.UTC), "ja")
	if got := scanFrameCreatedAt([]byte(frame)); got != "2025-06-13T09:30:00Z" {
		t.Errorf("scanFrameCreatedAt = %q, want %q", got, "2025-06-13T09:30:00Z")
	}
	if got := scanFrameCreatedAt([]byte(`{"record":{"text":"hi"}}`)); got != "" {
		t.Errorf("scanFrameCreatedAt with no key = %q, want empty", got)
	}
	// An unterminated value must not run past the bound.
	if got := scanFrameCreatedAt([]byte(`{"createdAt":"` + string(make([]byte, 200)))); got != "" {
		t.Errorf("scanFrameCreatedAt unterminated = %q, want empty", got)
	}
}

func TestCreatedAtBefore(t *testing.T) {
	const cutoff = "2026-09-11T10:00:00"
	tests := []struct {
		createdAt string
		want      bool
	}{
		{"2025-06-13T09:30:00Z", true},
		{"2026-09-11T09:59:59Z", true},
		{"2026-09-11T10:00:00Z", false},
		{"2026-09-11T12:00:00.123Z", false},
		{"2025-06-13T09:30:00.5Z", true},
		// Not comparable as bytes: an offset, a short value, an odd shape.
		{"2025-06-13T09:30:00+02:00", false},
		{"2025-06-13", false},
		{"not-a-timestampZ", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := createdAtBefore(tt.createdAt, cutoff); got != tt.want {
			t.Errorf("createdAtBefore(%q) = %v, want %v", tt.createdAt, got, tt.want)
		}
	}
}

// waitFor polls cond until it holds or the test times out.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before the deadline")
}
