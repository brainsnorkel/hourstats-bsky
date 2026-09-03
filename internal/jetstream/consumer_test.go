package jetstream

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFrameIsNonEnglishPost(t *testing.T) {
	tests := []struct {
		name string
		// wantReject true means the pre-filter should drop the frame.
		wantReject bool
		frame      string
	}{
		{
			name:       "english post - keep",
			wantReject: false,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"hello","createdAt":"2024-09-09T19:46:02Z","langs":["en"]},"cid":"cid1"}}`,
		},
		{
			name:       "english-US post - keep",
			wantReject: false,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"hello","createdAt":"2024-09-09T19:46:02Z","langs":["en-US"]},"cid":"cid1"}}`,
		},
		{
			name:       "english-GB post - keep",
			wantReject: false,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"hello","createdAt":"2024-09-09T19:46:02Z","langs":["en-GB"]},"cid":"cid1"}}`,
		},
		{
			name:       "multilingual post with english - keep",
			wantReject: false,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"hello","createdAt":"2024-09-09T19:46:02Z","langs":["ja","en"]},"cid":"cid1"}}`,
		},
		{
			name:       "japanese-only post - reject",
			wantReject: true,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"こんにちは","createdAt":"2024-09-09T19:46:02Z","langs":["ja"]},"cid":"cid1"}}`,
		},
		{
			name:       "portuguese post - reject",
			wantReject: true,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"olá","createdAt":"2024-09-09T19:46:02Z","langs":["pt"]},"cid":"cid1"}}`,
		},
		{
			name:       "no langs field - keep (conservative)",
			wantReject: false,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"hello","createdAt":"2024-09-09T19:46:02Z"},"cid":"cid1"}}`,
		},
		{
			name:       "profile identity event - keep",
			wantReject: false,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"identity","identity":{"did":"did:plc:abc","handle":"user.bsky.social"}}`,
		},
		{
			name:       "account event - keep",
			wantReject: false,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"account","account":{"active":true,"did":"did:plc:abc"}}`,
		},
		{
			name:       "like event (not post) - keep",
			wantReject: false,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.like","rkey":"xyz","cid":"cid1"}}`,
		},
		{
			name:       "post delete (not create) - keep",
			wantReject: false,
			frame:      `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"delete","collection":"app.bsky.feed.post","rkey":"xyz"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := frameIsNonEnglishPost([]byte(tt.frame))
			if got != tt.wantReject {
				t.Errorf("frameIsNonEnglishPost() = %v, want %v", got, tt.wantReject)
			}
		})
	}
}

func TestEvent_IsPostCreate(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{
			name: "post create",
			json: `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"hello","createdAt":"2024-09-09T19:46:02Z"},"cid":"cid123"}}`,
			want: true,
		},
		{
			name: "like create",
			json: `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.like","rkey":"xyz","cid":"cid123"}}`,
			want: false,
		},
		{
			name: "post delete",
			json: `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"delete","collection":"app.bsky.feed.post","rkey":"xyz"}}`,
			want: false,
		},
		{
			name: "identity event",
			json: `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"identity"}`,
			want: false,
		},
		{
			name: "account event",
			json: `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"account"}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event Event
			if err := json.Unmarshal([]byte(tt.json), &event); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := event.IsPostCreate(); got != tt.want {
				t.Errorf("IsPostCreate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvent_PostURI(t *testing.T) {
	raw := `{"did":"did:plc:abc123","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz789","cid":"cid1"}}`
	var event Event
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := "at://did:plc:abc123/app.bsky.feed.post/xyz789"
	if got := event.PostURI(); got != want {
		t.Errorf("PostURI() = %q, want %q", got, want)
	}
}

func TestEvent_ParsePostRecord(t *testing.T) {
	raw := `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"Hello world!","createdAt":"2024-09-09T19:46:02.102Z","langs":["en"]},"cid":"cid1"}}`

	var event Event
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	rec := event.ParsePostRecord()
	if rec == nil {
		t.Fatal("ParsePostRecord returned nil")
	}
	if rec.Text != "Hello world!" {
		t.Errorf("Text = %q, want %q", rec.Text, "Hello world!")
	}
	if rec.CreatedAt != "2024-09-09T19:46:02.102Z" {
		t.Errorf("CreatedAt = %q", rec.CreatedAt)
	}
	if len(rec.Langs) != 1 || rec.Langs[0] != "en" {
		t.Errorf("Langs = %v, want [en]", rec.Langs)
	}
}

func TestEvent_ParsePostRecord_WithReply(t *testing.T) {
	raw := `{"did":"did:plc:abc","time_us":1725911162329308,"kind":"commit","commit":{"rev":"r","operation":"create","collection":"app.bsky.feed.post","rkey":"xyz","record":{"$type":"app.bsky.feed.post","text":"reply text","createdAt":"2024-09-09T19:46:02Z","reply":{"parent":{"uri":"at://did:plc:parent/app.bsky.feed.post/p1","cid":"cidp"},"root":{"uri":"at://did:plc:root/app.bsky.feed.post/r1","cid":"cidr"}}},"cid":"cid1"}}`

	var event Event
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	rec := event.ParsePostRecord()
	if rec == nil {
		t.Fatal("ParsePostRecord returned nil")
	}
	if rec.Reply == nil {
		t.Fatal("Reply is nil")
	}
	if rec.Reply.Parent.URI != "at://did:plc:parent/app.bsky.feed.post/p1" {
		t.Errorf("Parent URI = %q", rec.Reply.Parent.URI)
	}
	if rec.Reply.Root.URI != "at://did:plc:root/app.bsky.feed.post/r1" {
		t.Errorf("Root URI = %q", rec.Reply.Root.URI)
	}
}

func TestEvent_ParsePostRecord_NilCommit(t *testing.T) {
	event := Event{DID: "did:plc:abc", Kind: "identity"}
	if rec := event.ParsePostRecord(); rec != nil {
		t.Errorf("expected nil for non-commit event, got %v", rec)
	}
}

func TestEvent_ParsePostRecord_EmptyRecord(t *testing.T) {
	event := Event{
		DID:  "did:plc:abc",
		Kind: "commit",
		Commit: &Commit{
			Operation:  "create",
			Collection: "app.bsky.feed.post",
		},
	}
	if rec := event.ParsePostRecord(); rec != nil {
		t.Errorf("expected nil for empty record, got %v", rec)
	}
}

func TestConsumerConfig_Defaults(t *testing.T) {
	cfg := ConsumerConfig{}
	cfg.setDefaults()

	if cfg.Endpoint != AllEndpoints[0] {
		t.Errorf("Endpoint = %q, want %q", cfg.Endpoint, AllEndpoints[0])
	}
	if len(cfg.Endpoints) != len(AllEndpoints) {
		t.Errorf("Endpoints length = %d, want %d", len(cfg.Endpoints), len(AllEndpoints))
	}
	if len(cfg.Collections) != 1 || cfg.Collections[0] != DefaultCollection {
		t.Errorf("Collections = %v, want [%s]", cfg.Collections, DefaultCollection)
	}
	if cfg.CursorInterval != DefaultCursorInterval {
		t.Errorf("CursorInterval = %v, want %v", cfg.CursorInterval, DefaultCursorInterval)
	}
}

func TestConsumerConfig_SingleEndpoint(t *testing.T) {
	cfg := ConsumerConfig{Endpoint: "wss://custom.example.com/subscribe"}
	cfg.setDefaults()

	if len(cfg.Endpoints) != 1 || cfg.Endpoints[0] != "wss://custom.example.com/subscribe" {
		t.Errorf("Endpoints = %v, want single custom endpoint", cfg.Endpoints)
	}
}

func TestConsumerConfig_ExplicitEndpoints(t *testing.T) {
	eps := []string{"wss://a.example.com/subscribe", "wss://b.example.com/subscribe"}
	cfg := ConsumerConfig{Endpoints: eps}
	cfg.setDefaults()

	if len(cfg.Endpoints) != 2 {
		t.Fatalf("Endpoints length = %d, want 2", len(cfg.Endpoints))
	}
	if cfg.Endpoint != eps[0] {
		t.Errorf("Endpoint = %q, want %q", cfg.Endpoint, eps[0])
	}
}

func TestConsumer_EndpointRotation(t *testing.T) {
	eps := []string{
		"wss://a.example.com/subscribe",
		"wss://b.example.com/subscribe",
		"wss://c.example.com/subscribe",
	}
	c := NewConsumer(ConsumerConfig{
		Endpoints:   eps,
		Collections: []string{"app.bsky.feed.post"},
	})

	if c.ActiveEndpoint() != eps[0] {
		t.Fatalf("initial endpoint = %q, want %q", c.ActiveEndpoint(), eps[0])
	}

	// Simulate 3 rapid drops (within rotateDropWindow).
	now := time.Now()
	c.dropTimes = []time.Time{
		now.Add(-2 * time.Minute),
		now.Add(-1 * time.Minute),
		now,
	}

	// Should rotate since len(dropTimes) >= rotateAfterDrops.
	if len(c.dropTimes) < rotateAfterDrops {
		t.Fatalf("expected %d+ drops, got %d", rotateAfterDrops, len(c.dropTimes))
	}

	c.endpointIdx = (c.endpointIdx + 1) % len(c.cfg.Endpoints)
	c.dropTimes = nil

	if c.ActiveEndpoint() != eps[1] {
		t.Errorf("after rotation = %q, want %q", c.ActiveEndpoint(), eps[1])
	}

	// Rotate again.
	c.endpointIdx = (c.endpointIdx + 1) % len(c.cfg.Endpoints)
	if c.ActiveEndpoint() != eps[2] {
		t.Errorf("second rotation = %q, want %q", c.ActiveEndpoint(), eps[2])
	}

	// Wraps around.
	c.endpointIdx = (c.endpointIdx + 1) % len(c.cfg.Endpoints)
	if c.ActiveEndpoint() != eps[0] {
		t.Errorf("wrap around = %q, want %q", c.ActiveEndpoint(), eps[0])
	}
}

func TestConsumer_BuildURL(t *testing.T) {
	// Rewind disabled so this stays a test of URL assembly; the rewind itself
	// is covered by TestBuildURLUsesRewoundCursor.
	c := NewConsumer(ConsumerConfig{
		Endpoint:     "wss://jetstream2.us-east.bsky.network/subscribe",
		Collections:  []string{"app.bsky.feed.post"},
		CursorRewind: -1,
	})

	u := c.buildURL()
	if u != "wss://jetstream2.us-east.bsky.network/subscribe?wantedCollections=app.bsky.feed.post" {
		t.Errorf("buildURL() = %q", u)
	}

	c.cursor.Store(1725911162329308)
	u = c.buildURL()
	if u != "wss://jetstream2.us-east.bsky.network/subscribe?cursor=1725911162329308&wantedCollections=app.bsky.feed.post" {
		t.Errorf("buildURL() with cursor = %q", u)
	}
}

func TestRealJetstreamEvent_FullParse(t *testing.T) {
	raw := `{
		"did": "did:plc:eygmaihciaxprqvxpfvl6flk",
		"time_us": 1725911162329308,
		"kind": "commit",
		"commit": {
			"rev": "3l3qo2vutsw2b",
			"operation": "create",
			"collection": "app.bsky.feed.post",
			"rkey": "3l3qo2vuowo2b",
			"record": {
				"$type": "app.bsky.feed.post",
				"createdAt": "2024-09-09T19:46:02.102Z",
				"text": "This is a test post from the firehose"
			},
			"cid": "bafyreidwaivazkwu67xztlmuobx35hs2lnfh3kolmgfmucldvhd3sgzcqi"
		}
	}`

	var event Event
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if event.DID != "did:plc:eygmaihciaxprqvxpfvl6flk" {
		t.Errorf("DID = %q", event.DID)
	}
	if event.TimeUS != 1725911162329308 {
		t.Errorf("TimeUS = %d", event.TimeUS)
	}
	if !event.IsPostCreate() {
		t.Error("should be post create")
	}
	if event.Commit.CID != "bafyreidwaivazkwu67xztlmuobx35hs2lnfh3kolmgfmucldvhd3sgzcqi" {
		t.Errorf("CID = %q", event.Commit.CID)
	}

	want := "at://did:plc:eygmaihciaxprqvxpfvl6flk/app.bsky.feed.post/3l3qo2vuowo2b"
	if got := event.PostURI(); got != want {
		t.Errorf("PostURI() = %q, want %q", got, want)
	}

	rec := event.ParsePostRecord()
	if rec == nil {
		t.Fatal("ParsePostRecord returned nil")
	}
	if rec.Text != "This is a test post from the firehose" {
		t.Errorf("Text = %q", rec.Text)
	}
}

func TestConsumer_GetStatsReport(t *testing.T) {
	eps := []string{
		"wss://a.example.com/subscribe",
		"wss://b.example.com/subscribe",
	}
	c := NewConsumer(ConsumerConfig{
		Endpoints:   eps,
		Collections: []string{"app.bsky.feed.post"},
	})

	// Get initial stats report - should have zero values
	report := c.GetStatsReport()

	if report.EventsReceived != 0 {
		t.Errorf("EventsReceived = %d, want 0", report.EventsReceived)
	}
	if report.PostsProcessed != 0 {
		t.Errorf("PostsProcessed = %d, want 0", report.PostsProcessed)
	}
	if report.EventsSkipped != 0 {
		t.Errorf("EventsSkipped = %d, want 0", report.EventsSkipped)
	}
	if report.Reconnects != 0 {
		t.Errorf("Reconnects = %d, want 0", report.Reconnects)
	}
	if report.Errors != 0 {
		t.Errorf("Errors = %d, want 0", report.Errors)
	}
	if report.EndpointRotations != 0 {
		t.Errorf("EndpointRotations = %d, want 0", report.EndpointRotations)
	}
	if report.ActiveEndpoint != eps[0] {
		t.Errorf("ActiveEndpoint = %q, want %q", report.ActiveEndpoint, eps[0])
	}
	if report.ConnectionUptime != 0 {
		t.Errorf("ConnectionUptime = %v, want 0", report.ConnectionUptime)
	}

	// Simulate some activity
	c.stats.EventsReceived.Add(100)
	c.stats.PostsProcessed.Add(50)
	c.stats.EventsSkipped.Add(30)
	c.stats.Reconnects.Add(2)
	c.stats.Errors.Add(5)
	c.endpointRotations.Add(1)

	// Set connection time to simulate uptime
	c.mu.Lock()
	c.connectedAt = time.Now().Add(-5 * time.Second)
	c.mu.Unlock()

	report = c.GetStatsReport()

	if report.EventsReceived != 100 {
		t.Errorf("EventsReceived = %d, want 100", report.EventsReceived)
	}
	if report.PostsProcessed != 50 {
		t.Errorf("PostsProcessed = %d, want 50", report.PostsProcessed)
	}
	if report.EventsSkipped != 30 {
		t.Errorf("EventsSkipped = %d, want 30", report.EventsSkipped)
	}
	if report.Reconnects != 2 {
		t.Errorf("Reconnects = %d, want 2", report.Reconnects)
	}
	if report.Errors != 5 {
		t.Errorf("Errors = %d, want 5", report.Errors)
	}
	if report.EndpointRotations != 1 {
		t.Errorf("EndpointRotations = %d, want 1", report.EndpointRotations)
	}
	if report.ActiveEndpoint != eps[0] {
		t.Errorf("ActiveEndpoint = %q, want %q", report.ActiveEndpoint, eps[0])
	}
	if report.ConnectionUptime < 4*time.Second || report.ConnectionUptime > 6*time.Second {
		t.Errorf("ConnectionUptime = %v, want ~5s", report.ConnectionUptime)
	}
}
