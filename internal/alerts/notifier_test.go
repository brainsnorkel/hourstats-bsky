package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

// webhookRecorder collects the bodies a notifier POSTs.
type webhookRecorder struct {
	mu     sync.Mutex
	bodies []string
	status int
}

func (w *webhookRecorder) handler() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.mu.Lock()
		w.bodies = append(w.bodies, string(body))
		status := w.status
		w.mu.Unlock()
		if status == 0 {
			status = http.StatusNoContent
		}
		rw.WriteHeader(status)
	}
}

func (w *webhookRecorder) posted() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, len(w.bodies))
	copy(out, w.bodies)
	return out
}

func TestNotify_DiscordBody(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotifier("staging", srv.URL, srv.Client())
	n.Notify(context.Background(), []Condition{{
		Name:     "stale_posts",
		Severity: SeverityWarn,
		Message:  "age guards dropped 9 backfill posts this snapshot (threshold 1)",
	}})

	bodies := rec.posted()
	if len(bodies) != 1 {
		t.Fatalf("posts = %d, want 1", len(bodies))
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatalf("decode body %q: %v", bodies[0], err)
	}
	if len(payload) != 2 {
		t.Errorf("payload = %v, want content and allowed_mentions only", payload)
	}
	content, _ := payload["content"].(string)
	for _, want := range []string{"hourstats-staging", "WARN", "stale_posts", "threshold 1"} {
		if !strings.Contains(content, want) {
			t.Errorf("content = %q, want it to contain %q", content, want)
		}
	}
}

// TestNotify_InfoStaysOutOfTheChannel covers the severity split: info exists so
// /stats/health can name a routine condition, not so the channel can carry it.
func TestNotify_InfoStaysOutOfTheChannel(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotifier("staging", srv.URL, srv.Client())
	n.Notify(context.Background(), []Condition{{Name: "denied_posts", Severity: SeverityInfo, Message: "1"}})

	if posts := rec.posted(); len(posts) != 0 {
		t.Errorf("posts = %v, want none: info is log-only", posts)
	}
}

func TestNotify_SuppressesTheSameConditionWithinAnHour(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	start := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	now := start
	n := NewNotifier("staging", srv.URL, srv.Client())
	n.now = func() time.Time { return now }

	cond := Condition{Name: "capped_posts", Severity: SeverityWarn, Message: "6000"}
	n.Notify(context.Background(), []Condition{cond})
	// The same condition with fresh numbers half an hour later: the key is the
	// name, so this stays quiet.
	now = start.Add(30 * time.Minute)
	n.Notify(context.Background(), []Condition{{Name: cond.Name, Severity: cond.Severity, Message: "7000"}})

	if posts := rec.posted(); len(posts) != 1 {
		t.Fatalf("posts = %d, want 1 (the second is inside the suppression window)", len(posts))
	}

	// Past the window it is news again.
	now = start.Add(suppressWindow + time.Minute)
	n.Notify(context.Background(), []Condition{cond})
	if posts := rec.posted(); len(posts) != 2 {
		t.Fatalf("posts = %d, want 2 once the window has passed", len(posts))
	}
}

// TestNotify_SuppressionIsPerCondition makes sure one noisy condition does not
// mute the others.
func TestNotify_SuppressionIsPerCondition(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotifier("staging", srv.URL, srv.Client())
	n.Notify(context.Background(), []Condition{
		{Name: "capped_posts", Severity: SeverityWarn, Message: "a"},
		{Name: "stale_posts", Severity: SeverityWarn, Message: "b"},
	})
	n.Notify(context.Background(), []Condition{
		{Name: "capped_posts", Severity: SeverityWarn, Message: "a"},
		{Name: "rss_high", Severity: SeverityWarn, Message: "c"},
	})

	if posts := rec.posted(); len(posts) != 3 {
		t.Fatalf("posts = %d, want 3 (two new names, one suppressed repeat)", len(posts))
	}
}

func TestNotify_WebhookErrorStatusDoesNotPanic(t *testing.T) {
	rec := &webhookRecorder{status: http.StatusInternalServerError}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotifier("staging", srv.URL, srv.Client())
	n.Notify(context.Background(), []Condition{{Name: "capped_posts", Severity: SeverityWarn, Message: "6000"}})

	if posts := rec.posted(); len(posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(posts))
	}
}

func TestNotify_UnreachableWebhookDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing is listening now

	n := NewNotifier("staging", url, &http.Client{Timeout: time.Second})
	n.Notify(context.Background(), []Condition{{Name: "capped_posts", Severity: SeverityWarn, Message: "6000"}})
}

// TestNotify_CancelledContextStillPosts covers the shutdown case: the caller's
// context is detached so an alert raised as the process winds down still gets
// its one attempt.
func TestNotify_CancelledContextStillPosts(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	n := NewNotifier("staging", srv.URL, srv.Client())
	n.Notify(ctx, []Condition{{Name: "capped_posts", Severity: SeverityWarn, Message: "6000"}})

	if posts := rec.posted(); len(posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(posts))
	}
}

func TestNotify_NoWebhookConfigured(t *testing.T) {
	n := NewNotifier("staging", "", nil)
	n.Notify(context.Background(), []Condition{{Name: "capped_posts", Severity: SeverityWarn, Message: "6000"}})
}

func TestNotify_NilNotifier(t *testing.T) {
	var n *Notifier
	n.Notify(context.Background(), []Condition{{Name: "capped_posts", Severity: SeverityWarn}})
}

func TestDiscordPayloadMention(t *testing.T) {
	c := Condition{Name: "stale_posts", Severity: SeverityWarn, Message: "m"}
	got := discordPayload("<@123>", "staging", c, nil)
	content, _ := got["content"].(string)
	if !strings.HasPrefix(content, "<@123> **hourstats-staging WARN: stale_posts**") {
		t.Fatalf("content = %q", content)
	}
	am, _ := got["allowed_mentions"].(map[string]any)
	parse, _ := am["parse"].([]string)
	if len(parse) != 2 || parse[0] != "users" || parse[1] != "everyone" {
		t.Fatalf("allowed_mentions = %v", am)
	}
	if plain, _ := discordPayload("", "staging", c, nil)["content"].(string); strings.HasPrefix(plain, " ") || strings.Contains(plain, "<@") {
		t.Fatalf("empty mention altered content: %q", plain)
	}
}

func TestNotifyMentionsOnlyActionable(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	n := NewNotifier("staging", srv.URL, srv.Client())
	n.SetMention("@here")
	n.Notify(context.Background(), []Condition{
		{Name: "stale_posts", Severity: SeverityWarn, Message: "routine"},
		{Name: "rss_high", Severity: SeverityWarn, Actionable: true, Message: "act"},
		{Name: "denied_posts", Severity: SeverityInfo, Message: "fyi"},
	})
	bodies := rec.posted()
	if len(bodies) != 2 {
		t.Fatalf("posts = %d, want 2 (info stays out of Discord)", len(bodies))
	}
	for _, b := range bodies {
		var payload map[string]any
		if err := json.Unmarshal([]byte(b), &payload); err != nil {
			t.Fatal(err)
		}
		content, _ := payload["content"].(string)
		mentioned := strings.HasPrefix(content, "@here ")
		switch {
		case strings.Contains(content, "rss_high"):
			if !mentioned || !strings.Contains(content, "action needed") {
				t.Errorf("actionable warn not mentioned: %q", content)
			}
		default:
			if mentioned || strings.Contains(content, "action needed") {
				t.Errorf("routine condition was mentioned: %q", content)
			}
		}
	}
}

// fakeDirectory resolves the DIDs it was given and fails on everything else,
// counting lookups so the cache can be checked.
type fakeDirectory struct {
	mu      sync.Mutex
	handles map[string]string
	lookups int
}

func (d *fakeDirectory) LookupDID(_ context.Context, did syntax.DID) (*identity.Identity, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lookups++
	handle, ok := d.handles[did.String()]
	if !ok {
		return nil, errors.New("not found")
	}
	return &identity.Identity{DID: did, Handle: syntax.Handle(handle)}, nil
}

func (d *fakeDirectory) LookupHandle(context.Context, syntax.Handle) (*identity.Identity, error) {
	return nil, errors.New("not used")
}

func (d *fakeDirectory) Lookup(context.Context, syntax.AtIdentifier) (*identity.Identity, error) {
	return nil, errors.New("not used")
}

func (d *fakeDirectory) Purge(context.Context, syntax.AtIdentifier) error { return nil }

func (d *fakeDirectory) lookupCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lookups
}

// floodCondition as Evaluate builds it, so the test exercises the real message
// shape rather than a hand-written one.
func testFloodCondition() Condition {
	accounts := []DIDCount{
		{DID: "did:plc:knownaccount0000000000", Count: 1200},
		{DID: "did:plc:unknownaccount00000000", Count: 15},
	}
	return floodCondition("stale_posts", "300001 posts were dropped this half hour.", accounts, 30)
}

// TestNotify_DiscordNamesAccounts: the channel is the operator's own, so the
// Discord message swaps the DIDs for handles where it can, keeps the DID where
// it cannot, and ends with a profile link for the busiest account.
func TestNotify_DiscordNamesAccounts(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	dir := &fakeDirectory{handles: map[string]string{
		"did:plc:knownaccount0000000000": "loud.bsky.social",
	}}
	n := NewNotifier("staging", srv.URL, srv.Client())
	n.SetDirectory(dir)

	n.Notify(context.Background(), []Condition{testFloodCondition()})

	bodies := rec.posted()
	if len(bodies) != 1 {
		t.Fatalf("posts = %d, want 1", len(bodies))
	}
	var payload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if !strings.Contains(payload.Content, "loud.bsky.social 1200 posts (~40.0/min)") {
		t.Errorf("content does not name the resolved handle: %q", payload.Content)
	}
	if strings.Contains(payload.Content, "did:plc:knownaccount0000000000 1200") {
		t.Errorf("content still names the resolved DID: %q", payload.Content)
	}
	if !strings.Contains(payload.Content, "did:plc:unknownaccount00000000 15 posts (~0.5/min)") {
		t.Errorf("content lost the unresolvable account: %q", payload.Content)
	}
	if !strings.HasSuffix(payload.Content, "\nhttps://bsky.app/profile/did:plc:knownaccount0000000000") {
		t.Errorf("content does not end with the top account's profile link: %q", payload.Content)
	}
}

// TestResolveHandles_CachesLookups: a failure is remembered too, or every
// alert pays the budget again for a DID that will never resolve.
func TestResolveHandles_CachesLookups(t *testing.T) {
	dir := &fakeDirectory{handles: map[string]string{"did:plc:knownaccount0000000000": "loud.bsky.social"}}
	n := NewNotifier("staging", "", nil)
	n.SetDirectory(dir)

	accounts := []DIDCount{
		{DID: "did:plc:knownaccount0000000000"},
		{DID: "did:plc:unknownaccount00000000"},
	}
	first := n.resolveHandles(context.Background(), accounts)
	if first["did:plc:knownaccount0000000000"] != "loud.bsky.social" {
		t.Fatalf("names = %v", first)
	}
	if _, ok := first["did:plc:unknownaccount00000000"]; ok {
		t.Errorf("a failed lookup produced a name: %v", first)
	}
	if got := dir.lookupCount(); got != 2 {
		t.Fatalf("lookups = %d, want 2", got)
	}

	if second := n.resolveHandles(context.Background(), accounts); len(second) != 1 {
		t.Errorf("second resolve = %v, want the cached handle", second)
	}
	if got := dir.lookupCount(); got != 2 {
		t.Errorf("lookups = %d, want no new ones inside the TTL", got)
	}

	// Past the TTL the lookups run again.
	n.now = func() time.Time { return time.Now().UTC().Add(2 * handleCacheTTL) }
	n.resolveHandles(context.Background(), accounts)
	if got := dir.lookupCount(); got != 4 {
		t.Errorf("lookups = %d, want the expired entries looked up again", got)
	}
}

// TestLogMessageHidesAccounts: DIDs may go to the operator's channel and to a
// Debug line, but not to the process log at Info or above.
func TestLogMessageHidesAccounts(t *testing.T) {
	c := testFloodCondition()
	got := logMessage(c)
	if strings.Contains(got, "did:plc:") || strings.Contains(got, accountsPrefix) {
		t.Errorf("log message carries accounts: %q", got)
	}
	if got != "300001 posts were dropped this half hour." {
		t.Errorf("log message = %q, want the prose only", got)
	}
	plain := Condition{Name: "rss_high", Message: "the process is using 700MB"}
	if logMessage(plain) != plain.Message {
		t.Errorf("a condition with no accounts was altered: %q", logMessage(plain))
	}
}
