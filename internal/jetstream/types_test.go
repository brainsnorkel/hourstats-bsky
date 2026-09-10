package jetstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Wire samples straight from a Jetstream v1 subscription with
// wantedCollections=app.bsky.feed.post.
const (
	createFrame  = `{"did":"did:plc:aaa","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r1","operation":"create","collection":"app.bsky.feed.post","rkey":"3abc","record":{"$type":"app.bsky.feed.post","text":"hello","createdAt":"2026-09-11T10:00:00Z","langs":["en"]},"cid":"cid1"}}`
	deleteFrame  = `{"did":"did:plc:bbb","time_us":1725911162329309,"kind":"commit","commit":{"rev":"r2","operation":"delete","collection":"app.bsky.feed.post","rkey":"3def"}}`
	accountFrame = `{"did":"did:plc:ccc","time_us":1725911162329310,"kind":"account","account":{"active":false,"did":"did:plc:ccc","seq":1,"status":"deactivated","time":"2026-09-11T10:00:01Z"}}`
)

func TestEvent_IsPostDelete(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  bool
	}{
		{"post delete", deleteFrame, true},
		{"post create", createFrame, false},
		{"account event", accountFrame, false},
		{
			name:  "like delete",
			frame: `{"did":"did:plc:a","time_us":1,"kind":"commit","commit":{"rev":"r","operation":"delete","collection":"app.bsky.feed.like","rkey":"k"}}`,
		},
		{
			name:  "post update",
			frame: `{"did":"did:plc:a","time_us":1,"kind":"commit","commit":{"rev":"r","operation":"update","collection":"app.bsky.feed.post","rkey":"k"}}`,
		},
		{
			name:  "identity event",
			frame: `{"did":"did:plc:a","time_us":1,"kind":"identity","identity":{"did":"did:plc:a","handle":"u.bsky.social"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var evt Event
			if err := json.Unmarshal([]byte(tt.frame), &evt); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := evt.IsPostDelete(); got != tt.want {
				t.Errorf("IsPostDelete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvent_IsPostDelete_URI(t *testing.T) {
	var evt Event
	if err := json.Unmarshal([]byte(deleteFrame), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := "at://did:plc:bbb/app.bsky.feed.post/3def"
	if got := evt.PostURI(); got != want {
		t.Errorf("PostURI() = %q, want %q", got, want)
	}
}

func TestEvent_IsAccountInactive(t *testing.T) {
	account := func(active bool, status string) string {
		return `{"did":"did:plc:ccc","time_us":1,"kind":"account","account":{"active":` +
			map[bool]string{true: "true", false: "false"}[active] +
			`,"did":"did:plc:ccc","seq":1,"status":"` + status + `","time":"2026-09-11T10:00:01Z"}}`
	}

	tests := []struct {
		name  string
		frame string
		want  bool
	}{
		{"deactivated", account(false, "deactivated"), true},
		{"deleted", account(false, "deleted"), true},
		{"suspended", account(false, "suspended"), true},
		{"takendown", account(false, "takendown"), true},
		// Sync 1.1 transient hosting states: the account still exists.
		{"desynchronized", account(false, "desynchronized"), false},
		{"throttled", account(false, "throttled"), false},
		{"active with status", account(true, ""), false},
		{"reactivated", account(true, "deactivated"), false},
		// An inactive account with no status at all is still gone.
		{"inactive no status", `{"did":"did:plc:c","time_us":1,"kind":"account","account":{"active":false,"did":"did:plc:c","seq":1}}`, true},
		{"post create", createFrame, false},
		{"account kind without payload", `{"did":"did:plc:c","time_us":1,"kind":"account"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var evt Event
			if err := json.Unmarshal([]byte(tt.frame), &evt); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := evt.IsAccountInactive(); got != tt.want {
				t.Errorf("IsAccountInactive() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The bytes-level pre-filter must never swallow a delete or account frame:
// those drive the purge path and are only recognised after json.Unmarshal.
func TestScanFrameLangKeepsDeleteAndAccountFrames(t *testing.T) {
	tests := []struct {
		name  string
		frame string
	}{
		{"post delete", deleteFrame},
		{"account deactivated", accountFrame},
		{"account takendown", `{"did":"did:plc:c","time_us":1,"kind":"account","account":{"active":false,"did":"did:plc:c","seq":1,"status":"takendown"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reject, lang := scanFrameLang([]byte(tt.frame))
			if reject {
				t.Errorf("scanFrameLang() reject = true, want false (frame must reach the parser)")
			}
			if lang != "" {
				t.Errorf("scanFrameLang() firstLang = %q, want empty", lang)
			}
		})
	}
}

// TestReadLoopDispatchesCreateDeleteAndAccount runs the real read loop against
// a local WebSocket server that emits one frame of each kind.
func TestReadLoopDispatchesCreateDeleteAndAccount(t *testing.T) {
	frames := []string{createFrame, deleteFrame, accountFrame}

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		// Hold the connection open until the client goes away so the read
		// loop is not restarted mid-assertion.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	var (
		mu           sync.Mutex
		posts        []string
		deletes      []string
		inactiveDIDs []string
		statuses     []string
	)
	done := make(chan struct{})
	var once sync.Once

	consumer := NewConsumer(ConsumerConfig{
		Protocol: ProtocolV1,
		Endpoint: "ws" + strings.TrimPrefix(srv.URL, "http"),
		OnPost: func(evt *Event, rec *PostRecord) {
			mu.Lock()
			posts = append(posts, evt.PostURI())
			mu.Unlock()
		},
		OnDelete: func(evt *Event) {
			mu.Lock()
			deletes = append(deletes, evt.PostURI())
			mu.Unlock()
		},
		OnAccountInactive: func(evt *Event) {
			mu.Lock()
			inactiveDIDs = append(inactiveDIDs, evt.DID)
			statuses = append(statuses, evt.Account.Status)
			mu.Unlock()
			once.Do(func() { close(done) })
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the account event")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || posts[0] != "at://did:plc:aaa/app.bsky.feed.post/3abc" {
		t.Errorf("posts = %v, want one create for 3abc", posts)
	}
	if len(deletes) != 1 || deletes[0] != "at://did:plc:bbb/app.bsky.feed.post/3def" {
		t.Errorf("deletes = %v, want one delete for 3def", deletes)
	}
	if len(inactiveDIDs) != 1 || inactiveDIDs[0] != "did:plc:ccc" {
		t.Errorf("inactive = %v, want one account event for did:plc:ccc", inactiveDIDs)
	}
	if len(statuses) != 1 || statuses[0] != "deactivated" {
		t.Errorf("statuses = %v, want [deactivated]", statuses)
	}

	report := consumer.GetStatsReport()
	if report.PostsProcessed != 1 {
		t.Errorf("PostsProcessed = %d, want 1", report.PostsProcessed)
	}
	if report.PostsDeleted != 1 {
		t.Errorf("PostsDeleted = %d, want 1", report.PostsDeleted)
	}
	if report.AccountsInactive != 1 {
		t.Errorf("AccountsInactive = %d, want 1", report.AccountsInactive)
	}
	if report.EventsSkipped != 0 {
		t.Errorf("EventsSkipped = %d, want 0", report.EventsSkipped)
	}
}
