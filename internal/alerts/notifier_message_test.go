package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPostMessage_Body: a free-form message is the content alone, with no
// mention kinds allowed, so an "@here" in the text pings nobody, and it is
// not subject to the per-condition suppression window.
func TestPostMessage_Body(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotifier("prod", srv.URL+"/api/webhooks/123/secret-token", srv.Client())
	if err := n.PostMessage(context.Background(), "preview @here text"); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	bodies := rec.posted()
	if len(bodies) != 1 {
		t.Fatalf("posts = %d, want 1", len(bodies))
	}
	var payload struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatalf("decode body %q: %v", bodies[0], err)
	}
	if payload.Content != "preview @here text" {
		t.Errorf("content = %q", payload.Content)
	}
	if payload.AllowedMentions.Parse == nil || len(payload.AllowedMentions.Parse) != 0 {
		t.Errorf("allowed_mentions.parse = %v, want an empty list", payload.AllowedMentions.Parse)
	}
	if !strings.Contains(bodies[0], `"parse":[]`) {
		t.Errorf("body %q does not carry an explicit empty parse list", bodies[0])
	}
	var keys map[string]any
	_ = json.Unmarshal([]byte(bodies[0]), &keys)
	if len(keys) != 2 {
		t.Errorf("payload keys = %v, want content and allowed_mentions only", keys)
	}
	// A second message straight after is not suppressed: this is not an alert.
	if err := n.PostMessage(context.Background(), "again"); err != nil {
		t.Fatalf("second PostMessage: %v", err)
	}
	if got := rec.posted(); len(got) != 2 {
		t.Errorf("posts after second message = %d, want 2", len(got))
	}
}

// TestPostMessage_Truncates: Discord refuses content over 2000 characters, so
// a long message is cut to messageMaxRunes with an ellipsis.
func TestPostMessage_Truncates(t *testing.T) {
	rec := &webhookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotifier("prod", srv.URL, srv.Client())
	if err := n.PostMessage(context.Background(), strings.Repeat("é", 3000)); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(rec.posted()[0]), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	content, _ := payload["content"].(string)
	got := []rune(content)
	if len(got) != messageMaxRunes {
		t.Errorf("content runes = %d, want %d", len(got), messageMaxRunes)
	}
	if got[len(got)-1] != '…' {
		t.Errorf("truncated content does not end with an ellipsis: %q", string(got[len(got)-5:]))
	}
}

// TestPostMessage_Errors: every failure is an error to the caller, and none of
// them names the webhook URL, which is the secret.
func TestPostMessage_Errors(t *testing.T) {
	const secretPath = "/api/webhooks/123/secret-token"

	t.Run("non-2xx status", func(t *testing.T) {
		rec := &webhookRecorder{status: http.StatusInternalServerError}
		srv := httptest.NewServer(rec.handler())
		defer srv.Close()
		n := NewNotifier("prod", srv.URL+secretPath, srv.Client())
		err := n.PostMessage(context.Background(), "x")
		if err == nil || !strings.Contains(err.Error(), "status 500") {
			t.Fatalf("err = %v, want a status 500 error", err)
		}
		if strings.Contains(err.Error(), secretPath) {
			t.Errorf("error names the webhook path: %v", err)
		}
	})

	t.Run("transport failure", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		webhook := srv.URL + secretPath
		client := srv.Client()
		srv.Close()
		n := NewNotifier("prod", webhook, client)
		err := n.PostMessage(context.Background(), "x")
		if err == nil {
			t.Fatal("PostMessage against a closed server returned nil")
		}
		if strings.Contains(err.Error(), secretPath) {
			t.Errorf("error names the webhook path: %v", err)
		}
		if !strings.Contains(err.Error(), "Post: ") {
			t.Errorf("error does not keep the operation: %v", err)
		}
	})

	t.Run("request cannot be built", func(t *testing.T) {
		n := NewNotifier("prod", "https://discord.example"+secretPath+"\n", nil)
		err := n.PostMessage(context.Background(), "x")
		if err == nil {
			t.Fatal("PostMessage with a malformed URL returned nil")
		}
		if strings.Contains(err.Error(), "secret-token") {
			t.Errorf("error names the webhook secret: %v", err)
		}
	})

	t.Run("unset webhook", func(t *testing.T) {
		n := NewNotifier("prod", "", nil)
		if err := n.PostMessage(context.Background(), "x"); !errors.Is(err, ErrNoWebhook) {
			t.Errorf("err = %v, want ErrNoWebhook", err)
		}
	})

	t.Run("nil notifier", func(t *testing.T) {
		var n *Notifier
		if err := n.PostMessage(context.Background(), "x"); !errors.Is(err, ErrNoWebhook) {
			t.Errorf("err = %v, want ErrNoWebhook", err)
		}
	})
}
