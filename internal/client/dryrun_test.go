package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every write method must refuse before it reaches the network, so a call site
// that forgets to check DRY_RUN cannot post. The server fails the test if it is
// contacted at all.
func TestDryRunSuppressesEveryWrite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("dry run still called the API: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.SetDryRun(true)
	if !c.DryRun() {
		t.Fatal("DryRun() = false after SetDryRun(true)")
	}

	ctx := context.Background()
	png := []byte{0x89, 0x50, 0x4E, 0x47}

	writes := map[string]func() error{
		"PostText": func() error {
			return c.PostText(ctx, "hello")
		},
		"PostWithFacets": func() error {
			return c.PostWithFacets(ctx, "hello", nil)
		},
		"PostWithFacetsRef": func() error {
			_, _, err := c.PostWithFacetsRef(ctx, "hello", nil)
			return err
		},
		"PostWithFacetsAsReply": func() error {
			_, _, err := c.PostWithFacetsAsReply(ctx, "hello", nil, "r", "rc", "p", "pc")
			return err
		},
		"PostWithImage": func() error {
			_, _, err := c.PostWithImage(ctx, "hello", png, "alt")
			return err
		},
		"PostWithImageAsReply": func() error {
			_, _, err := c.PostWithImageAsReply(ctx, "hello", png, "alt", "r", "rc", "p", "pc")
			return err
		},
		"PostReplyWithQuote": func() error {
			_, _, err := c.PostReplyWithQuote(ctx, "hello", "r", "rc", "p", "pc", "q", "qc")
			return err
		},
		"PostTrendingSummary": func() error {
			_, _, err := c.PostTrendingSummary([]Post{{URI: "at://did:plc:a/app.bsky.feed.post/1", CID: "c", Author: "a.bsky.social"}}, "positive", 30, 100, 5.0)
			return err
		},
		"UploadImage": func() error {
			_, err := c.UploadImage(ctx, png, "alt")
			return err
		},
		"PinPost": func() error {
			return c.PinPost(ctx, "at://did:plc:a/app.bsky.feed.post/1", "cid")
		},
	}

	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			err := write()
			if !errors.Is(err, ErrDryRun) {
				t.Errorf("%s returned %v, want ErrDryRun", name, err)
			}
		})
	}
}

// Off by default: a client nobody told about dry run must still post, and the
// sentinel must not leak into a normal run.
func TestDryRunOffByDefault(t *testing.T) {
	var record map[string]any
	srv := captureCreateRecord(t, &record)
	defer srv.Close()

	c := newTestClient(srv.URL)
	if c.DryRun() {
		t.Fatal("DryRun() = true on a fresh client")
	}
	if err := c.PostText(context.Background(), "hello"); err != nil {
		t.Fatalf("PostText: %v", err)
	}
	if record["text"] != "hello" {
		t.Errorf("posted record text = %v, want hello", record["text"])
	}
}

func TestSetDryRunCanBeTurnedOff(t *testing.T) {
	var record map[string]any
	srv := captureCreateRecord(t, &record)
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.SetDryRun(true)
	if err := c.PostText(context.Background(), "suppressed"); !errors.Is(err, ErrDryRun) {
		t.Fatalf("PostText with dry run on returned %v, want ErrDryRun", err)
	}
	c.SetDryRun(false)
	if err := c.PostText(context.Background(), "posted"); err != nil {
		t.Fatalf("PostText with dry run off: %v", err)
	}
	if record["text"] != "posted" {
		t.Errorf("posted record text = %v, want posted", record["text"])
	}
}
