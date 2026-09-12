package jetstream

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

	// A witnessed event time is sent as a unix-microsecond cursor, rewound by
	// CursorRewind. The seq is never sent: it belongs to the instance that
	// issued it.
	witness := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC).UnixMicro()
	c.seq.Store(4242)
	c.cursor.Store(witness)
	q = mustQuery(t, c.buildURLV2())
	want := strconv.FormatInt(witness-DefaultCursorRewind.Microseconds(), 10)
	if got := q.Get("cursor"); got != want {
		t.Errorf("cursor = %q, want the witnessed time rewound by %s (%s)", got, DefaultCursorRewind, want)
	}
	if got, _ := strconv.ParseInt(q.Get("cursor"), 10, 64); got < minTimestampCursorV2 {
		t.Errorf("cursor = %d, under the %d threshold: the server would read it as a seq",
			got, int64(minTimestampCursorV2))
	}

	// A rewind that would take the cursor under the threshold is dropped
	// rather than sent as a value the server reads as a seq.
	c.cursor.Store(minTimestampCursorV2 + 1)
	q = mustQuery(t, c.buildURLV2())
	if _, ok := q["cursor"]; ok {
		t.Errorf("cursor = %v, want none when the rewind falls under the timestamp threshold", q["cursor"])
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
	witnessed := time.Now().UnixMicro()
	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           wsEndpoint(srv.URL),
		DisableCompression: true,
		LoadCursorV2: func(context.Context) (int64, int64, error) {
			return 999, witnessed, nil
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
	want := strconv.FormatInt(witnessed-DefaultCursorRewind.Microseconds(), 10)
	if cursors[0] != want {
		t.Errorf("first dial cursor = %q, want the persisted time rewound (%s), never the seq 999", cursors[0], want)
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

// v2CreateFrameSeq builds a create frame with a given seq and rkey, so a test
// can drive the read loop's dedup floor.
func v2CreateFrameSeq(seq int64, rkey string) string {
	return fmt.Sprintf(`{"$type":"message","payload":{"$type":"network.bsky.jetstream.subscribeEvents#commit",`+
		`"seq":%d,"did":"did:plc:aaa","time":"%s","rev":"3lrev1","operation":"create",`+
		`"collection":"app.bsky.feed.post","rkey":"%s","cid":"bafycreate",`+
		`"record":{"$type":"app.bsky.feed.post","text":"hello world","createdAt":"2026-09-11T12:00:00Z","langs":["en"]}}}`,
		seq, v2CreateTime, rkey)
}

// TestConsumerV2_EndpointRotationResetsSeqFloor covers the rotation: a
// rotation follows repeated instability, so the new endpoint is dialled at its
// live tip with no cursor at all, and the seq floor does not travel with it.
// The first endpoint only drops the connection; after the rotation the second
// serves seq 5 while the persisted cursor named seq 100, and its events must
// still be delivered.
func TestConsumerV2_EndpointRotationResetsSeqFloor(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}

	// The unstable endpoint: upgrade, then hang up at once.
	badMux := http.NewServeMux()
	badMux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.Close()
	})
	bad := httptest.NewServer(badMux)
	defer bad.Close()

	var (
		mu         sync.Mutex
		goodCursor []string
	)
	goodMux := http.NewServeMux()
	goodMux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		goodCursor = append(goodCursor, r.URL.Query().Get("cursor"))
		mu.Unlock()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Seq 5, far below the floor of 100 the consumer resumed with.
		if err := conn.WriteMessage(websocket.TextMessage, []byte(v2CreateFrameSeq(5, "3low"))); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	good := httptest.NewServer(goodMux)
	defer good.Close()

	done := make(chan struct{})
	var once sync.Once
	consumer := NewConsumer(ConsumerConfig{
		Endpoints:          []string{wsEndpoint(bad.URL), wsEndpoint(good.URL)},
		DisableCompression: true,
		LoadCursorV2: func(context.Context) (int64, int64, error) {
			return 100, time.Now().UnixMicro(), nil
		},
		OnPost: func(*Event, *PostRecord) { once.Do(func() { close(done) }) },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for a post from the rotated endpoint: the seq floor was not reset")
	}
	cancel()

	if got := consumer.endpointRotations.Load(); got == 0 {
		t.Fatal("endpoint rotations = 0, want at least 1")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(goodCursor) == 0 {
		t.Fatal("the rotated endpoint was never dialled")
	}
	if goodCursor[0] != "" {
		t.Errorf("first dial cursor on the rotated endpoint = %q, want none (live tip)", goodCursor[0])
	}
}

// TestConsumerV2_SeqFloorGuardResetsAfterRun covers the in-session guard: a
// floor nothing in the rest of the stream can clear must not silently swallow
// the connection.
func TestConsumerV2_SeqFloorGuardResetsAfterRun(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// One frame sets the floor at 100; every frame after it is below the
		// floor, so the guard must fire on the last of them.
		if err := conn.WriteMessage(websocket.TextMessage, []byte(v2CreateFrameSeq(100, "3high"))); err != nil {
			return
		}
		frame := []byte(v2CreateFrameSeq(5, "3low"))
		for i := 0; i < maxConsecutiveSeqDrops; i++ {
			if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
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

	posts := make(chan string, 4)
	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           wsEndpoint(srv.URL),
		DisableCompression: true,
		OnPost:             func(evt *Event, _ *PostRecord) { posts <- evt.PostURI() },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	if got := waitForPost(t, posts, "the frame that sets the floor"); !strings.HasSuffix(got, "3high") {
		t.Fatalf("first post = %q, want the seq 100 frame", got)
	}
	got := waitForPost(t, posts, "the guard to reset the floor")
	if !strings.HasSuffix(got, "3low") {
		t.Errorf("second post = %q, want the frame that tripped the guard", got)
	}
	cancel()
}

// waitForPost returns the next post URI the consumer dispatched, failing the
// test if none arrives.
func waitForPost(t *testing.T, posts <-chan string, what string) string {
	t.Helper()
	select {
	case uri := <-posts:
		return uri
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return ""
	}
}

// TestConsumerV2_ReconnectResumesByTimestamp: a seq names an event only within
// the instance that issued it, so the reconnect must ask for the time of the
// last event it saw, rewound, and never for that event's seq.
func TestConsumerV2_ReconnectResumesByTimestamp(t *testing.T) {
	// A production-scale seq: sent to a different instance it would name an
	// event tens of millions of frames away.
	const liveSeq = 25_797_075_281

	var (
		mu      sync.Mutex
		cursors []string
	)
	upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		n := len(cursors)
		mu.Unlock()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		frame := v2CreateFrameSeq(liveSeq, fmt.Sprintf("3conn%d", n))
		if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
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

	posts := make(chan string, 4)
	consumer := NewConsumer(ConsumerConfig{
		Endpoint:           wsEndpoint(srv.URL),
		DisableCompression: true,
		OnPost:             func(evt *Event, _ *PostRecord) { posts <- evt.PostURI() },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	if got := waitForPost(t, posts, "the first connection's post"); !strings.HasSuffix(got, "3conn1") {
		t.Fatalf("first post = %q, want the first connection's frame", got)
	}
	if !consumer.ForceReconnect() {
		t.Fatal("ForceReconnect() = false, want the live connection to be closed")
	}
	if got := waitForPost(t, posts, "the second connection's post"); !strings.HasSuffix(got, "3conn2") {
		t.Errorf("second post = %q, want the reconnected connection's frame", got)
	}
	cancel()

	witness, err := time.Parse(time.RFC3339Nano, v2CreateTime)
	if err != nil {
		t.Fatalf("parse %q: %v", v2CreateTime, err)
	}
	want := strconv.FormatInt(witness.UnixMicro()-DefaultCursorRewind.Microseconds(), 10)

	mu.Lock()
	defer mu.Unlock()
	if len(cursors) < 2 {
		t.Fatalf("dial attempts = %d, want at least 2", len(cursors))
	}
	if cursors[0] != "" {
		t.Errorf("first dial cursor = %q, want none (live tip)", cursors[0])
	}
	if cursors[1] != want {
		t.Errorf("second dial cursor = %q, want the last event time rewound by %s (%s), not the seq %d",
			cursors[1], DefaultCursorRewind, want, int64(liveSeq))
	}
}

// TestConsumerV2_ReconnectAcrossInstanceSeqSpaces: the hostname fronts several
// instances numbering their events independently, so the one that answers a
// reconnect can be tens of millions of seqs either side of the last one seen.
// The dedup floor is per connection, so the new connection's first frame is
// delivered immediately either way -- no 10,000-frame stall when it is behind,
// no silent acceptance of a gap when it is ahead.
func TestConsumerV2_ReconnectAcrossInstanceSeqSpaces(t *testing.T) {
	const (
		firstSeq = int64(25_797_075_281)
		delta    = int64(40_000_000)
	)
	for _, tc := range []struct {
		name     string
		otherSeq int64
	}{
		{"instance behind", firstSeq - delta},
		{"instance ahead", firstSeq + delta},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu    sync.Mutex
				dials int
			)
			upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}
			mux := http.NewServeMux()
			mux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				dials++
				n := dials
				mu.Unlock()

				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				seq, rkey := firstSeq, "3first"
				if n > 1 {
					seq, rkey = tc.otherSeq, "3second"
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(v2CreateFrameSeq(seq, rkey))); err != nil {
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

			posts := make(chan string, 4)
			consumer := NewConsumer(ConsumerConfig{
				Endpoint:           wsEndpoint(srv.URL),
				DisableCompression: true,
				OnPost:             func(evt *Event, _ *PostRecord) { posts <- evt.PostURI() },
			})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _ = consumer.Run(ctx) }()

			if got := waitForPost(t, posts, "the first instance's post"); !strings.HasSuffix(got, "3first") {
				t.Fatalf("first post = %q, want the first instance's frame", got)
			}
			if !consumer.ForceReconnect() {
				t.Fatal("ForceReconnect() = false, want the live connection to be closed")
			}
			if got := waitForPost(t, posts, "the other instance's post"); !strings.HasSuffix(got, "3second") {
				t.Errorf("second post = %q, want the frame from seq %d", got, tc.otherSeq)
			}
			cancel()
		})
	}
}
