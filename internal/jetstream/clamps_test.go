package jetstream

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestValidateEventIdentity covers the bounds both decode paths apply before a
// DID or an rkey is concatenated into a URI or used as a map key.
func TestValidateEventIdentity(t *testing.T) {
	tests := []struct {
		name    string
		did     string
		rkey    string
		wantErr bool
	}{
		{"ordinary did:plc", "did:plc:abcdefghijklmnopqrstuvwx", "3abc", false},
		{"did:web", "did:web:example.com", "3abc", false},
		{"empty did", "", "3abc", true},
		{"not a did", "plc:abc", "3abc", true},
		{"did-ish prefix only", "didplc:abc", "3abc", true},
		{"did at the limit", "did:plc:" + strings.Repeat("a", maxDIDBytes-8), "3abc", false},
		{"did one past the limit", "did:plc:" + strings.Repeat("a", maxDIDBytes-7), "3abc", true},
		{"rkey at the limit", "did:plc:abc", strings.Repeat("k", maxRkeyBytes), false},
		{"rkey one past the limit", "did:plc:abc", strings.Repeat("k", maxRkeyBytes+1), true},
		{"no rkey (not a commit)", "did:plc:abc", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEventIdentity(tt.did, tt.rkey)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateEventIdentity(%d-byte did, %d-byte rkey) error = %v, want error %v",
					len(tt.did), len(tt.rkey), err, tt.wantErr)
			}
		})
	}
}

// TestDecodeV2FrameRejectsBadIdentity drives the same bounds through the v2
// decoder, where a breach must surface as a decode error (counted in Errors)
// rather than a skipped frame.
func TestDecodeV2FrameRejectsBadIdentity(t *testing.T) {
	frame := func(did, rkey string) string {
		return fmt.Sprintf(`{"$type":"message","payload":{"$type":"%s",`+
			`"seq":1,"did":"%s","time":"2026-09-11T10:00:00Z","rev":"r1","operation":"create",`+
			`"collection":"app.bsky.feed.post","rkey":"%s","cid":"cid1",`+
			`"record":{"$type":"app.bsky.feed.post","text":"hi","createdAt":"2026-09-11T10:00:00Z","langs":["en"]}}}`,
			v2TypeCommit, did, rkey)
	}

	if _, _, err := decodeV2Frame([]byte(frame("did:plc:aaa", "3abc"))); err != nil {
		t.Fatalf("decodeV2Frame(valid) error = %v, want nil", err)
	}

	tests := map[string]string{
		"not a did":    frame("plc:aaa", "3abc"),
		"oversize did": frame("did:plc:"+strings.Repeat("a", maxDIDBytes), "3abc"),
		"oversize rkey": frame("did:plc:aaa",
			strings.Repeat("k", maxRkeyBytes+1)),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := decodeV2Frame([]byte(raw))
			if err == nil {
				t.Fatal("decodeV2Frame() error = nil, want a decode error")
			}
			if errors.Is(err, errSkipFrame) {
				t.Errorf("decodeV2Frame() error = errSkipFrame, want a counted decode error")
			}
		})
	}
}

// TestConsumerV1_RejectsBadIdentity is the v1 half: a frame whose DID is not a
// DID is counted as an error and never dispatched, while the good frame that
// follows it on the same connection still arrives.
func TestConsumerV1_RejectsBadIdentity(t *testing.T) {
	bad := `{"did":"plc:aaa","time_us":1789120800000000,"kind":"commit","commit":{"rev":"r1",` +
		`"operation":"create","collection":"app.bsky.feed.post","rkey":"3bad","record":` +
		`{"$type":"app.bsky.feed.post","text":"hello","createdAt":"2026-09-11T10:00:00Z","langs":["en"]},"cid":"cid1"}}`

	var mu sync.Mutex
	var posts []string
	consumer := NewConsumer(ConsumerConfig{
		Protocol:      ProtocolV1,
		Endpoint:      serveV1Frames(t, [][]string{{bad, createFrame}}),
		MaxPostAge:    -1,
		MaxPostFuture: -1,
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

	report := consumer.GetStatsReport()
	if report.Errors != 1 {
		t.Errorf("Errors = %d, want 1 for the malformed did", report.Errors)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || posts[0] != "at://did:plc:aaa/app.bsky.feed.post/3abc" {
		t.Errorf("posts = %v, want only the well-formed create", posts)
	}
}

// TestConsumerV1_RejectsOversizedFrame and its v2 twin prove the read limit is
// in force on both protocols: a 3 MB message is refused, the connection is
// dropped, and the frames on the next connection are processed normally.
func TestConsumerV1_RejectsOversizedFrame(t *testing.T) {
	endpoint := serveV1Frames(t, [][]string{{oversizedFrame(3 << 20)}, {createFrame}})

	var mu sync.Mutex
	var posts []string
	consumer := NewConsumer(ConsumerConfig{
		Protocol:      ProtocolV1,
		Endpoint:      endpoint,
		MaxPostAge:    -1,
		MaxPostFuture: -1,
		OnPost: func(evt *Event, _ *PostRecord) {
			mu.Lock()
			posts = append(posts, evt.PostURI())
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	waitForSlow(t, func() bool { return consumer.GetStatsReport().PostsProcessed == 1 })

	if got := consumer.GetStatsReport().Reconnects; got < 1 {
		t.Errorf("Reconnects = %d, want at least 1 after the oversized frame", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || posts[0] != "at://did:plc:aaa/app.bsky.feed.post/3abc" {
		t.Errorf("posts = %v, want only the create from the second connection", posts)
	}
}

func TestConsumerV2_RejectsOversizedFrame(t *testing.T) {
	witness := time.Now().UTC().Truncate(time.Second)
	good := v2PostFrame(1, "3abc", witness, witness, "en")
	endpoint := serveV2Connections(t, [][]string{{oversizedFrame(3 << 20)}, {good}})

	var mu sync.Mutex
	var posts []string
	consumer := NewConsumer(ConsumerConfig{
		Protocol:           ProtocolV2,
		Endpoint:           endpoint,
		DisableCompression: true,
		OnPost: func(evt *Event, _ *PostRecord) {
			mu.Lock()
			posts = append(posts, evt.PostURI())
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	waitForSlow(t, func() bool { return consumer.GetStatsReport().PostsProcessed == 1 })

	if got := consumer.GetStatsReport().Reconnects; got < 1 {
		t.Errorf("Reconnects = %d, want at least 1 after the oversized frame", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 {
		t.Errorf("posts = %v, want only the create from the second connection", posts)
	}
}

// TestConsumerV2_FutureDatedDropped is the wire-level half of the future
// guard: a record dated 11 minutes ahead of its witness time never reaches
// OnPost and is counted in both PostsStale and PostsFuture, while one 9
// minutes ahead is ordinary client clock skew and is kept.
func TestConsumerV2_FutureDatedDropped(t *testing.T) {
	witness := time.Now().UTC().Truncate(time.Second)
	frames := []string{
		v2PostFrame(1, "3skew", witness, witness.Add(9*time.Minute), "en"),
		v2PostFrame(2, "3fake", witness, witness.Add(11*time.Minute), "en"),
	}

	var mu sync.Mutex
	var posts, stale []string
	consumer := NewConsumer(ConsumerConfig{
		Protocol:           ProtocolV2,
		Endpoint:           serveFrames(t, frames),
		DisableCompression: true,
		OnPost: func(evt *Event, _ *PostRecord) {
			mu.Lock()
			posts = append(posts, evt.PostURI())
			mu.Unlock()
		},
		OnStale: func(evt *Event, _ *PostRecord, _ time.Duration) {
			mu.Lock()
			stale = append(stale, evt.PostURI())
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	waitFor(t, func() bool { return consumer.GetStatsReport().PostsFuture == 1 })

	report := consumer.GetStatsReport()
	if report.PostsProcessed != 1 {
		t.Errorf("PostsProcessed = %d, want 1 (the 9-minute skew is kept)", report.PostsProcessed)
	}
	if report.PostsStale != 1 {
		t.Errorf("PostsStale = %d, want 1 (a future record is also a dropped create)", report.PostsStale)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || !strings.HasSuffix(posts[0], "3skew") {
		t.Errorf("posts = %v, want only 3skew", posts)
	}
	if len(stale) != 1 || !strings.HasSuffix(stale[0], "3fake") {
		t.Errorf("stale = %v, want only 3fake", stale)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// oversizedFrame builds a single well-formed v1 create frame of at least n
// bytes, so the only thing wrong with it is its size.
func oversizedFrame(n int) string {
	head := `{"did":"did:plc:big","time_us":1789120800000000,"kind":"commit","commit":{"rev":"r1",` +
		`"operation":"create","collection":"app.bsky.feed.post","rkey":"3big","record":` +
		`{"$type":"app.bsky.feed.post","text":"`
	tail := `","createdAt":"2026-09-11T10:00:00Z","langs":["en"]},"cid":"cid1"}}`
	return head + strings.Repeat("a", n) + tail
}

// serveV1Frames starts a v1 endpoint that writes one batch of frames per
// connection, so a test can drive a reconnect: the first connection gets
// batches[0], the second batches[1], and so on (the last batch repeats).
func serveV1Frames(t *testing.T, batches [][]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(framesHandler(t, batches)))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// serveV2Connections is serveV1Frames on the v2 subscribe path.
func serveV2Connections(t *testing.T, batches [][]string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/"+subscribeNSID, framesHandler(t, batches))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return wsEndpoint(srv.URL)
}

func framesHandler(t *testing.T, batches [][]string) http.HandlerFunc {
	t.Helper()
	upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}
	var conns atomic.Int64
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		idx := int(conns.Add(1)) - 1
		if idx >= len(batches) {
			idx = len(batches) - 1
		}
		for _, f := range batches[idx] {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(f)); err != nil {
				return
			}
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}
}

// waitForSlow is waitFor with room for a reconnect backoff.
func waitForSlow(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before the deadline")
}
