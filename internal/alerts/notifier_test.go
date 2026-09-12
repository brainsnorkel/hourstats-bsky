package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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
func TestNotify_InfoReachesTheChannelWithoutMention(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotifier("staging", srv.URL, srv.Client())
	n.Notify(context.Background(), []Condition{{Name: "denied_posts", Severity: SeverityInfo, Message: "1"}})

	if posts := rec.posted(); len(posts) != 1 || strings.Contains(posts[0], "@here") || strings.Contains(posts[0], "action needed") {
		t.Errorf("posts = %v, want exactly one routine post without a mention", posts)
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
	got := discordPayload("<@123>", "staging", c)
	content, _ := got["content"].(string)
	if !strings.HasPrefix(content, "<@123> **hourstats-staging WARN: stale_posts**") {
		t.Fatalf("content = %q", content)
	}
	am, _ := got["allowed_mentions"].(map[string]any)
	parse, _ := am["parse"].([]string)
	if len(parse) != 2 || parse[0] != "users" || parse[1] != "everyone" {
		t.Fatalf("allowed_mentions = %v", am)
	}
	if plain, _ := discordPayload("", "staging", c)["content"].(string); strings.HasPrefix(plain, " ") || strings.Contains(plain, "<@") {
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
	if len(bodies) != 3 {
		t.Fatalf("posts = %d, want 3 (info reaches Discord too)", len(bodies))
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
