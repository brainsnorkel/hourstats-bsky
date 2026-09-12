package client

import (
	"context"
	"fmt"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/denylist"
)

// A denied author is refused outright, and refused before the gate spends a PDS
// round trip on a declaration it was never going to honour.
func TestFeatureGateDenylist(t *testing.T) {
	const (
		deniedURI  = "at://did:plc:denied/app.bsky.feed.post/1"
		allowedURI = "at://did:plc:aaa/app.bsky.feed.post/2"
	)
	body := fmt.Sprintf(`{"posts":[
		{"uri":%q,"cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:denied","handle":"denied.bsky.social"}},
		{"uri":%q,"cid":"c2","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"}}
	]}`, deniedURI, allowedURI)

	vis := newFakeVisibility(nil)
	gate := newTestGate(getPostsServer(t, body).URL, vis)

	var asked []string
	gate.SetDenylist(func(did string) bool {
		asked = append(asked, did)
		return did == "did:plc:denied"
	})

	got, err := gate.Check(context.Background(), "hourly", []string{deniedURI, allowedURI})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	denied := got[deniedURI]
	if denied.OK || denied.Quotable || denied.Reason != ReasonDenied {
		t.Errorf("denied post verdict = %+v, want OK=false Quotable=false Reason=%q", denied, ReasonDenied)
	}
	if denied.AuthorDID != "did:plc:denied" {
		t.Errorf("AuthorDID = %q, want it filled for a denied post", denied.AuthorDID)
	}
	if n := vis.callCount("did:plc:denied"); n != 0 {
		t.Errorf("visibility lookups for the denied author = %d, want 0", n)
	}
	if len(asked) == 0 {
		t.Error("the denylist was never consulted")
	}

	// The rest of the batch is unaffected.
	if allowed := got[allowedURI]; !allowed.OK || !allowed.Quotable || allowed.Reason != "" {
		t.Errorf("allowed post verdict = %+v, want a clean pass", allowed)
	}
}

// The gate reads the process-wide denylist package by default, so a DID added
// there is refused without anything calling SetDenylist.
func TestFeatureGateUsesTheProcessDenylistByDefault(t *testing.T) {
	const uri = "at://did:plc:banned/app.bsky.feed.post/1"
	body := fmt.Sprintf(`{"posts":[{"uri":%q,"cid":"c1","indexedAt":"2026-01-01T00:00:00Z",
		"author":{"did":"did:plc:banned","handle":"banned.bsky.social"}}]}`, uri)

	denylist.Replace([]string{"did:plc:banned"})
	t.Cleanup(func() { denylist.Replace(nil) })

	gate := newTestGate(getPostsServer(t, body).URL, newFakeVisibility(nil))
	got, err := gate.Check(context.Background(), "hourly", []string{uri})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got[uri].OK || got[uri].Reason != ReasonDenied {
		t.Errorf("verdict = %+v, want Reason=%q from the process denylist", got[uri], ReasonDenied)
	}
}

// An empty process denylist, and no override, must behave exactly as before.
func TestFeatureGateWithoutDenylist(t *testing.T) {
	const uri = "at://did:plc:aaa/app.bsky.feed.post/1"
	body := fmt.Sprintf(`{"posts":[{"uri":%q,"cid":"c1","indexedAt":"2026-01-01T00:00:00Z",
		"author":{"did":"did:plc:aaa","handle":"a.bsky.social"}}]}`, uri)

	gate := newTestGate(getPostsServer(t, body).URL, newFakeVisibility(nil))
	got, err := gate.Check(context.Background(), "hourly", []string{uri})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !got[uri].OK || got[uri].Reason != "" {
		t.Errorf("verdict = %+v, want a clean pass with no denylist installed", got[uri])
	}

	// An installed-then-removed denylist is the same as never having one.
	gate.SetDenylist(func(string) bool { return true })
	gate.SetDenylist(nil)
	got, err = gate.Check(context.Background(), "hourly", []string{uri})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !got[uri].OK {
		t.Errorf("verdict = %+v, want a clean pass after the denylist was removed", got[uri])
	}
}

// A denylist that matches everything must not be defeated by an empty DID: an
// author-less post view already fails closed on its own reason.
func TestFeatureGateDenylistNotConsultedForMissingAuthor(t *testing.T) {
	const uri = "at://did:plc:aaa/app.bsky.feed.post/1"
	body := fmt.Sprintf(`{"posts":[{"uri":%q,"cid":"c1","indexedAt":"2026-01-01T00:00:00Z"}]}`, uri)

	gate := newTestGate(getPostsServer(t, body).URL, newFakeVisibility(nil))
	gate.SetDenylist(func(did string) bool {
		if did == "" {
			t.Error("denylist consulted with an empty DID")
		}
		return true
	})

	got, err := gate.Check(context.Background(), "hourly", []string{uri})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got[uri].OK || got[uri].Reason != ReasonVisibilityUnknown {
		t.Errorf("verdict = %+v, want Reason=%q for an author-less post view", got[uri], ReasonVisibilityUnknown)
	}
}
