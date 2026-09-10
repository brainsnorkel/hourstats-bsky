package jetstream

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/klauspost/compress/zstd"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const (
	v2CreateTime = "2026-09-11T12:00:00.123456Z"

	v2CreateFrame = `{"$type":"message","payload":{"$type":"network.bsky.jetstream.subscribeEvents#commit",` +
		`"seq":10,"did":"did:plc:aaa","time":"` + v2CreateTime + `","rev":"3lrev1","operation":"create",` +
		`"collection":"app.bsky.feed.post","rkey":"3abc","cid":"bafycreate",` +
		`"record":{"$type":"app.bsky.feed.post","text":"hello world","createdAt":"2026-09-11T12:00:00Z","langs":["en"]}}}`

	v2DeleteFrame = `{"$type":"message","payload":{"$type":"network.bsky.jetstream.subscribeEvents#commit",` +
		`"seq":11,"did":"did:plc:bbb","time":"2026-09-11T12:00:01.000000Z","rev":"3lrev2","operation":"delete",` +
		`"collection":"app.bsky.feed.post","rkey":"3def"}}`

	// A like carries the liked post's AT URI, so it contains the literal
	// "app.bsky.feed.post" and "operation":"create" that the language
	// pre-filter keys on. The measurement scan must therefore run first.
	v2LikeFrame = `{"$type":"message","payload":{"$type":"network.bsky.jetstream.subscribeEvents#commit",` +
		`"seq":12,"did":"did:plc:ccc","time":"2026-09-11T12:00:02.000000Z","rev":"3lrev3","operation":"create",` +
		`"collection":"app.bsky.feed.like","rkey":"3ghi","cid":"bafylike",` +
		`"record":{"$type":"app.bsky.feed.like","createdAt":"2026-09-11T12:00:02Z",` +
		`"subject":{"uri":"at://did:plc:aaa/app.bsky.feed.post/3abc","cid":"bafycreate"}}}}`

	v2InfoFrame = `{"$type":"message","payload":{"$type":"network.bsky.jetstream.subscribeEvents#info",` +
		`"name":"OutdatedCursor","message":"resumed from seq 10"}}`

	v2UnknownEnvelopeFrame = `{"$type":"heartbeat","payload":{}}`

	v2AccountFrame = `{"$type":"message","payload":{"$type":"network.bsky.jetstream.subscribeEvents#account",` +
		`"seq":13,"did":"did:plc:ddd","time":"2026-09-11T12:00:03.000000Z",` +
		`"account":{"active":false,"did":"did:plc:ddd","seq":99,"status":"deactivated","time":"2026-09-11T12:00:03Z"}}}`

	v2NonEnglishFrame = `{"$type":"message","payload":{"$type":"network.bsky.jetstream.subscribeEvents#commit",` +
		`"seq":14,"did":"did:plc:eee","time":"2026-09-11T12:00:04.000000Z","rev":"3lrev4","operation":"create",` +
		`"collection":"app.bsky.feed.post","rkey":"3jkl","cid":"bafyja",` +
		`"record":{"$type":"app.bsky.feed.post","text":"こんにちは","createdAt":"2026-09-11T12:00:04Z","langs":["ja"]}}}`
)

// testDictionary builds a structured zstd dictionary with the given ID from
// samples shaped like the frames the fake server sends.
func testDictionary(t *testing.T, id uint32) []byte {
	t.Helper()
	samples := [][]byte{
		[]byte(v2CreateFrame), []byte(v2DeleteFrame), []byte(v2LikeFrame),
		[]byte(v2AccountFrame), []byte(v2InfoFrame), []byte(v2NonEnglishFrame),
	}
	// History must be at least 8 bytes; the frames themselves are the corpus.
	history := []byte(v2CreateFrame + v2DeleteFrame + v2AccountFrame)
	dict, err := zstd.BuildDict(zstd.BuildDictOptions{
		ID:       id,
		Contents: samples,
		History:  history,
		// The zstd default repeat offsets; the zero value is rejected by the
		// encoder as "invalid offset in dictionary".
		Offsets: [3]int{1, 4, 8},
	})
	if err != nil {
		t.Fatalf("BuildDict: %v", err)
	}
	if got, err := parseDictID(dict); err != nil || got != id {
		t.Fatalf("parseDictID(built dict) = %d, %v; want %d, nil", got, err, id)
	}
	return dict
}

func compressFrames(t *testing.T, dict []byte, frames []string) [][]byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderDict(dict), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer enc.Close()
	out := make([][]byte, 0, len(frames))
	for _, f := range frames {
		out = append(out, enc.EncodeAll([]byte(f), nil))
	}
	return out
}

// ---------------------------------------------------------------------------
// Dictionary header
// ---------------------------------------------------------------------------

func TestParseDictID(t *testing.T) {
	good := make([]byte, 16)
	binary.LittleEndian.PutUint32(good[:4], zstdDictMagic)
	binary.LittleEndian.PutUint32(good[4:8], 20260709)
	id, err := parseDictID(good)
	if err != nil || id != 20260709 {
		t.Fatalf("parseDictID() = %d, %v; want 20260709, nil", id, err)
	}

	// The magic is little-endian 37 A4 30 EC on the wire.
	if good[0] != 0x37 || good[1] != 0xA4 || good[2] != 0x30 || good[3] != 0xEC {
		t.Errorf("magic bytes = % X, want 37 A4 30 EC", good[:4])
	}

	zeroID := make([]byte, 16)
	binary.LittleEndian.PutUint32(zeroID[:4], zstdDictMagic)

	for name, blob := range map[string][]byte{
		"nil":       nil,
		"short":     {1, 2, 3},
		"bad magic": {1, 2, 3, 4, 5, 6, 7, 8},
		"zero id":   zeroID,
	} {
		if _, err := parseDictID(blob); err == nil {
			t.Errorf("parseDictID(%s) = nil error, want an error", name)
		}
	}
}

func TestDictionaryURL(t *testing.T) {
	got, err := dictionaryURL("wss://jetstream.us-west.bsky.network/xrpc/" + subscribeNSID)
	if err != nil {
		t.Fatalf("dictionaryURL: %v", err)
	}
	want := "https://jetstream.us-west.bsky.network/xrpc/" + getZstdDictionaryNSID
	if got != want {
		t.Errorf("dictionaryURL() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Frame decoding
// ---------------------------------------------------------------------------

func TestDecodeV2Frame(t *testing.T) {
	wantTime, err := time.Parse(time.RFC3339Nano, v2CreateTime)
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}

	event, info, derr := decodeV2Frame([]byte(v2CreateFrame))
	if derr != nil || info != nil {
		t.Fatalf("decodeV2Frame(create) = _, %v, %v; want an event", info, derr)
	}
	if !event.IsPostCreate() {
		t.Errorf("IsPostCreate() = false for a create commit")
	}
	if event.Seq != 10 {
		t.Errorf("Seq = %d, want 10", event.Seq)
	}
	if event.TimeUS != wantTime.UnixMicro() {
		t.Errorf("TimeUS = %d, want %d", event.TimeUS, wantTime.UnixMicro())
	}
	if event.PostURI() != "at://did:plc:aaa/app.bsky.feed.post/3abc" {
		t.Errorf("PostURI() = %q", event.PostURI())
	}
	if rec := event.ParsePostRecord(); rec == nil || rec.Text != "hello world" {
		t.Errorf("ParsePostRecord() = %+v, want the post text", rec)
	}

	event, _, derr = decodeV2Frame([]byte(v2DeleteFrame))
	if derr != nil {
		t.Fatalf("decodeV2Frame(delete): %v", derr)
	}
	if !event.IsPostDelete() {
		t.Errorf("IsPostDelete() = false for a delete commit")
	}

	event, _, derr = decodeV2Frame([]byte(v2AccountFrame))
	if derr != nil {
		t.Fatalf("decodeV2Frame(account): %v", derr)
	}
	if !event.IsAccountInactive() {
		t.Errorf("IsAccountInactive() = false for a deactivated account")
	}

	_, info, derr = decodeV2Frame([]byte(v2InfoFrame))
	if derr == nil || info == nil || info.Name != "OutdatedCursor" {
		t.Errorf("decodeV2Frame(info) = _, %+v, %v; want an info advisory", info, derr)
	}

	if _, _, derr = decodeV2Frame([]byte(v2UnknownEnvelopeFrame)); derr == nil {
		t.Errorf("decodeV2Frame(unknown envelope) = nil error, want errSkipFrame")
	}

	_, _, derr = decodeV2Frame([]byte(`{"$type":"error","error":"ConsumerTooSlow","message":"too far behind"}`))
	var streamErr *v2StreamError
	if derr == nil || !asStreamError(derr, &streamErr) || streamErr.Code != "ConsumerTooSlow" {
		t.Errorf("decodeV2Frame(error frame) = %v, want a *v2StreamError", derr)
	}

	// A v1 frame dialled as v2 must surface, not look healthy.
	if _, _, derr = decodeV2Frame([]byte(`{"did":"did:plc:a","time_us":1,"kind":"commit"}`)); derr == nil {
		t.Errorf("decodeV2Frame(v1 frame) = nil error, want a decode error")
	}
}

// asStreamError keeps the errors.As call out of the assertion above.
func asStreamError(err error, target **v2StreamError) bool {
	se, ok := err.(*v2StreamError)
	if ok {
		*target = se
	}
	return ok
}

// TestScanFrameLangOnV2Frames pins the bytes-level pre-filter to the v2 wire
// shape: its three guards all appear in a v2 commit frame.
func TestScanFrameLangOnV2Frames(t *testing.T) {
	if reject, lang := scanFrameLang([]byte(v2NonEnglishFrame)); !reject || lang != "ja" {
		t.Errorf("scanFrameLang(v2 ja create) = %v, %q; want true, \"ja\"", reject, lang)
	}
	if reject, _ := scanFrameLang([]byte(v2CreateFrame)); reject {
		t.Errorf("scanFrameLang(v2 en create) = true, want the frame kept")
	}
	if reject, _ := scanFrameLang([]byte(v2DeleteFrame)); reject {
		t.Errorf("scanFrameLang(v2 delete) = true, want the frame kept")
	}
	if reject, _ := scanFrameLang([]byte(v2AccountFrame)); reject {
		t.Errorf("scanFrameLang(v2 account) = true, want the frame kept")
	}
}

// ---------------------------------------------------------------------------
// URL assembly
// ---------------------------------------------------------------------------

func TestBuildURLV2(t *testing.T) {
	c := NewConsumer(ConsumerConfig{
		Endpoint:         "wss://jetstream.us-west.bsky.network/xrpc/" + subscribeNSID,
		Collections:      []string{"app.bsky.feed.post"},
		ExtraCollections: []string{"app.bsky.feed.like"},
	})

	q := mustQuery(t, c.buildURLV2())
	if got := q["collections"]; len(got) != 2 || got[0] != "app.bsky.feed.post" || got[1] != "app.bsky.feed.like" {
		t.Errorf("collections = %v, want the wanted and measured collections", got)
	}
	if got := q["kinds"]; len(got) != 2 || got[0] != "commit" || got[1] != "account" {
		t.Errorf("kinds = %v, want [commit account]", got)
	}
	if _, ok := q["cursor"]; ok {
		t.Errorf("cursor = %v, want no cursor before any event", q["cursor"])
	}

	c.seq.Store(4242)
	q = mustQuery(t, c.buildURLV2())
	if got := q.Get("cursor"); got != "4242" {
		t.Errorf("cursor = %q, want the seq 4242", got)
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Query()
}

// ---------------------------------------------------------------------------
// Full compressed session
// ---------------------------------------------------------------------------

func TestConsumerV2_CompressedSession(t *testing.T) {
	const dictID = 20260911
	dict := testDictionary(t, dictID)
	frames := compressFrames(t, dict, []string{
		v2CreateFrame, v2DeleteFrame, v2LikeFrame,
		v2InfoFrame, v2UnknownEnvelopeFrame, v2NonEnglishFrame, v2AccountFrame,
	})

	var (
		mu          sync.Mutex
		gotQuery    url.Values
		gotProtocol string
	)

	upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/"+getZstdDictionaryNSID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(dict)
	})
	mux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotQuery = r.URL.Query()
		gotProtocol = r.Header.Get("Sec-Websocket-Protocol")
		mu.Unlock()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for _, f := range frames {
			if err := conn.WriteMessage(websocket.BinaryMessage, f); err != nil {
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
	defer srv.Close()

	var (
		cbMu       sync.Mutex
		posts      []string
		postTimeUS int64
		deletes    []string
		inactive   []string
		rejected   []string
		savedSeq   int64
		savedTime  int64
	)
	done := make(chan struct{})
	var once sync.Once

	consumer := NewConsumer(ConsumerConfig{
		Endpoint:         wsEndpoint(srv.URL),
		Collections:      []string{"app.bsky.feed.post"},
		ExtraCollections: []string{"app.bsky.feed.like"},
		CursorInterval:   20 * time.Millisecond,
		OnPost: func(evt *Event, rec *PostRecord) {
			cbMu.Lock()
			posts = append(posts, evt.PostURI())
			postTimeUS = evt.TimeUS
			cbMu.Unlock()
		},
		OnDelete: func(evt *Event) {
			cbMu.Lock()
			deletes = append(deletes, evt.PostURI())
			cbMu.Unlock()
		},
		OnEarlyReject: func(firstLang string) {
			cbMu.Lock()
			rejected = append(rejected, firstLang)
			cbMu.Unlock()
		},
		OnAccountInactive: func(evt *Event) {
			cbMu.Lock()
			inactive = append(inactive, evt.DID)
			cbMu.Unlock()
			once.Do(func() { close(done) })
		},
		SaveCursorV2: func(_ context.Context, seq, timeUS int64) error {
			cbMu.Lock()
			savedSeq, savedTime = seq, timeUS
			cbMu.Unlock()
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan struct{})
	go func() { _ = consumer.Run(ctx); close(runDone) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the account event")
	}
	// Sampled while the connection is still up: the report describes the live
	// connection, so its protocol and compression reset when Run returns.
	report := consumer.GetStatsReport()
	cancel()
	<-runDone

	mu.Lock()
	query, subproto := gotQuery, gotProtocol
	mu.Unlock()

	if !strings.Contains(subproto, subscribeSubprotocol) {
		t.Errorf("Sec-WebSocket-Protocol = %q, want it to offer %q", subproto, subscribeSubprotocol)
	}
	if got := query["collections"]; len(got) != 2 {
		t.Errorf("collections = %v, want the wanted and measured collections", got)
	}
	if got := query["kinds"]; len(got) != 2 {
		t.Errorf("kinds = %v, want [commit account]", got)
	}
	if got := query.Get("zstdDictionary"); got != fmt.Sprint(dictID) {
		t.Errorf("zstdDictionary = %q, want %d", got, dictID)
	}

	cbMu.Lock()
	defer cbMu.Unlock()

	if len(posts) != 1 || posts[0] != "at://did:plc:aaa/app.bsky.feed.post/3abc" {
		t.Errorf("posts = %v, want one create for 3abc", posts)
	}
	wantTime, _ := time.Parse(time.RFC3339Nano, v2CreateTime)
	if postTimeUS != wantTime.UnixMicro() {
		t.Errorf("post TimeUS = %d, want %d", postTimeUS, wantTime.UnixMicro())
	}
	if len(deletes) != 1 || deletes[0] != "at://did:plc:bbb/app.bsky.feed.post/3def" {
		t.Errorf("deletes = %v, want one delete for 3def", deletes)
	}
	if len(inactive) != 1 || inactive[0] != "did:plc:ddd" {
		t.Errorf("inactive = %v, want one account purge", inactive)
	}
	if len(rejected) != 1 || rejected[0] != "ja" {
		t.Errorf("early rejects = %v, want one ja post", rejected)
	}

	if report.Protocol != ProtocolV2 {
		t.Errorf("Protocol = %q, want %q", report.Protocol, ProtocolV2)
	}
	if !report.Compressed {
		t.Errorf("Compressed = false, want a dictionary zstd connection")
	}
	if report.EventsByCollection["app.bsky.feed.like"] != 1 {
		t.Errorf("EventsByCollection = %v, want one like", report.EventsByCollection)
	}
	if report.BytesReceived <= 0 || report.BytesDecompressed <= report.BytesReceived {
		t.Errorf("BytesReceived = %d, BytesDecompressed = %d; want compressed < decompressed",
			report.BytesReceived, report.BytesDecompressed)
	}

	// The account frame is the last one that advances the cursor: measured,
	// early-rejected and advisory frames deliberately do not.
	if savedSeq != 13 {
		t.Errorf("saved seq = %d, want 13", savedSeq)
	}
	accountTime, _ := time.Parse(time.RFC3339Nano, "2026-09-11T12:00:03.000000Z")
	if savedTime != accountTime.UnixMicro() {
		t.Errorf("saved cursor time = %d, want %d", savedTime, accountTime.UnixMicro())
	}
}

// ---------------------------------------------------------------------------
// Pre-upgrade refusals
// ---------------------------------------------------------------------------

func TestConsumerV2_CursorTooOldStartsFromTip(t *testing.T) {
	var (
		mu       sync.Mutex
		cursors  []string
		attempts int
	)

	upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		mu.Unlock()

		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"CursorTooOld","message":"floor seq is 5000"}`))
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(v2CreateFrame)); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	done := make(chan struct{})
	var once sync.Once
	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           wsEndpoint(srv.URL),
		DisableCompression: true,
		LoadCursorV2: func(context.Context) (int64, int64, error) {
			return 999, time.Now().UnixMicro(), nil
		},
		OnPost: func(*Event, *PostRecord) { once.Do(func() { close(done) }) },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the post after the cursor was refused")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if len(cursors) < 2 {
		t.Fatalf("dial attempts = %d, want at least 2", len(cursors))
	}
	if cursors[0] != "999" {
		t.Errorf("first dial cursor = %q, want the persisted seq 999", cursors[0])
	}
	if cursors[1] != "" {
		t.Errorf("second dial cursor = %q, want no cursor (live tip)", cursors[1])
	}
}

func TestConsumerV2_UnknownDictionaryRefetches(t *testing.T) {
	const (
		staleDictID   = 20260101
		currentDictID = 20260911
	)
	staleDict := testDictionary(t, staleDictID)
	currentDict := testDictionary(t, currentDictID)
	frames := compressFrames(t, currentDict, []string{v2CreateFrame})

	var (
		mu         sync.Mutex
		dictFetch  int
		dictParams []string
	)

	upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/"+getZstdDictionaryNSID, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		dictFetch++
		first := dictFetch == 1
		mu.Unlock()
		if first {
			// The dictionary this consumer pins before the server rotates it.
			_, _ = w.Write(staleDict)
			return
		}
		_, _ = w.Write(currentDict)
	})
	mux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("zstdDictionary")
		mu.Lock()
		dictParams = append(dictParams, id)
		mu.Unlock()

		if id != fmt.Sprint(currentDictID) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"error":"UnknownZstdDictionary","message":"current dictionary is %d"}`, currentDictID)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for _, f := range frames {
			if err := conn.WriteMessage(websocket.BinaryMessage, f); err != nil {
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
	defer srv.Close()

	done := make(chan struct{})
	var once sync.Once
	consumer := NewConsumer(ConsumerConfig{
		Endpoint: wsEndpoint(srv.URL),
		OnPost:   func(*Event, *PostRecord) { once.Do(func() { close(done) }) },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the post after the dictionary was refused")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if dictFetch < 2 {
		t.Errorf("dictionary fetches = %d, want the refused dictionary to be refetched", dictFetch)
	}
	if len(dictParams) < 2 {
		t.Fatalf("dial attempts = %d, want at least 2", len(dictParams))
	}
	if dictParams[0] != fmt.Sprint(staleDictID) {
		t.Errorf("first dial zstdDictionary = %q, want %d", dictParams[0], staleDictID)
	}
	if dictParams[1] != fmt.Sprint(currentDictID) {
		t.Errorf("second dial zstdDictionary = %q, want the rotated %d", dictParams[1], currentDictID)
	}
}

// wsEndpoint turns an httptest base URL into the v2 subscribe endpoint.
func wsEndpoint(base string) string {
	return "ws" + strings.TrimPrefix(base, "http") + "/xrpc/" + subscribeNSID
}
