package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeVisibility answers Lookup from a scripted queue per DID, so the retry on
// an Unknown can be exercised without touching a PDS. A DID with no queue is
// Allowed.
type fakeVisibility struct {
	mu      sync.Mutex
	answers map[string][]Visibility
	calls   map[string]int
}

func newFakeVisibility(answers map[string][]Visibility) *fakeVisibility {
	return &fakeVisibility{answers: answers, calls: map[string]int{}}
}

func (f *fakeVisibility) Lookup(_ context.Context, did string) Visibility {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[did]++
	queue := f.answers[did]
	if len(queue) == 0 {
		return VisibilityAllowed
	}
	v := queue[0]
	if len(queue) > 1 {
		f.answers[did] = queue[1:]
	}
	return v
}

func (f *fakeVisibility) callCount(did string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[did]
}

// newTestGate points a gate at a local XRPC server and a scripted visibility
// resolver, so the getPosts request and response decoding are exercised for
// real while the declaration lookups stay in-process.
func newTestGate(host string, vis visibilityLookup) *FeatureGate {
	g := newTestClient(host).NewFeatureGate(nil)
	g.visibility = vis
	return g
}

// getPostsServer replies to app.bsky.feed.getPosts with the given post views.
func getPostsServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/xrpc/app.bsky.feed.getPosts" {
			t.Errorf("request path = %q, want /xrpc/app.bsky.feed.getPosts", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFeatureGateReasons(t *testing.T) {
	const uri = "at://did:plc:aaa/app.bsky.feed.post/1"

	cases := []struct {
		name         string
		post         string
		visibility   map[string][]Visibility
		wantOK       bool
		wantQuotable bool
		wantReason   string
	}{
		{
			name:         "plain post passes",
			post:         `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"}}`,
			wantOK:       true,
			wantQuotable: true,
		},
		{
			name:       "author blocks this account",
			post:       `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social","viewer":{"blockedBy":true}}}`,
			wantReason: ReasonBlocked,
		},
		{
			name:       "this account blocks the author",
			post:       `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social","viewer":{"blocking":"at://did:plc:me/app.bsky.graph.block/1"}}}`,
			wantReason: ReasonBlocked,
		},
		{
			name:       "blocked via list",
			post:       `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social","viewer":{"blockingByList":{"uri":"at://did:plc:me/app.bsky.graph.list/1","cid":"lc","name":"blocks","purpose":"app.bsky.graph.defs#modlist","indexedAt":"2026-01-01T00:00:00Z"}}}}`,
			wantReason: ReasonBlocked,
		},
		{
			name:       "adult label on the post",
			post:       `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"},"labels":[{"src":"did:plc:mod","uri":"URI","val":"porn","cts":"2026-01-01T00:00:00Z"}]}`,
			wantReason: ReasonAdultLabel,
		},
		{
			name:       "system label on the post",
			post:       `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"},"labels":[{"src":"did:plc:mod","uri":"URI","val":"!hide","cts":"2026-01-01T00:00:00Z"}]}`,
			wantReason: "label:!hide",
		},
		{
			name:       "no-unauthenticated on the author",
			post:       `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social","labels":[{"src":"did:plc:aaa","uri":"did:plc:aaa","val":"!no-unauthenticated","cts":"2026-01-01T00:00:00Z"}]}}`,
			wantReason: "label:!no-unauthenticated",
		},
		{
			name:       "author hides from recommendations",
			post:       `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"}}`,
			visibility: map[string][]Visibility{"did:plc:aaa": {VisibilityHidden}},
			wantReason: ReasonHiddenFromRecommendations,
		},
		{
			name:       "visibility unknown twice",
			post:       `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"}}`,
			visibility: map[string][]Visibility{"did:plc:aaa": {VisibilityUnknown}},
			wantReason: ReasonVisibilityUnknown,
		},
		{
			name:         "quote control only suppresses the embed",
			post:         `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"},"viewer":{"embeddingDisabled":true}}`,
			wantOK:       true,
			wantQuotable: false,
			wantReason:   ReasonQuoteControl,
		},
		{
			name:       "block outranks quote control",
			post:       `{"uri":"URI","cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social","viewer":{"blockedBy":true}},"viewer":{"embeddingDisabled":true}}`,
			wantReason: ReasonBlocked,
		},
		{
			name:       "missing from the authenticated view",
			post:       "",
			wantReason: ReasonMissing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"posts":[]}`
			if tc.post != "" {
				body = fmt.Sprintf(`{"posts":[%s]}`, strings.ReplaceAll(tc.post, "URI", uri))
			}
			gate := newTestGate(getPostsServer(t, body).URL, newFakeVisibility(tc.visibility))

			got, err := gate.Check(context.Background(), "hourly", []string{uri})
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			v := got[uri]
			if v.OK != tc.wantOK || v.Quotable != tc.wantQuotable || v.Reason != tc.wantReason {
				t.Errorf("Check() = %+v, want OK=%v Quotable=%v Reason=%q", v, tc.wantOK, tc.wantQuotable, tc.wantReason)
			}
			if tc.post != "" && v.Reason != ReasonMissing && v.AuthorDID != "did:plc:aaa" {
				t.Errorf("AuthorDID = %q, want it filled even for a rejected post", v.AuthorDID)
			}
		})
	}
}

func TestFeatureGateRetriesUnknownVisibility(t *testing.T) {
	const uri = "at://did:plc:aaa/app.bsky.feed.post/1"
	body := fmt.Sprintf(`{"posts":[{"uri":%q,"cid":"c1","indexedAt":"2026-01-01T00:00:00Z",
		"author":{"did":"did:plc:aaa","handle":"a.bsky.social"}}]}`, uri)

	// The first read fails, the second succeeds: the post is featured.
	vis := newFakeVisibility(map[string][]Visibility{"did:plc:aaa": {VisibilityUnknown, VisibilityAllowed}})
	got, err := newTestGate(getPostsServer(t, body).URL, vis).Check(context.Background(), "hourly", []string{uri})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !got[uri].OK || got[uri].Reason != "" {
		t.Errorf("Check() = %+v, want a clean pass after the visibility retry", got[uri])
	}
	if n := vis.callCount("did:plc:aaa"); n != 2 {
		t.Errorf("visibility lookups = %d, want 2 (one retry)", n)
	}
}

func TestFeatureGateLooksUpEachAuthorOnce(t *testing.T) {
	const (
		uriA = "at://did:plc:aaa/app.bsky.feed.post/1"
		uriB = "at://did:plc:aaa/app.bsky.feed.post/2"
	)
	body := fmt.Sprintf(`{"posts":[
		{"uri":%q,"cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"}},
		{"uri":%q,"cid":"c2","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"}}
	]}`, uriA, uriB)

	vis := newFakeVisibility(nil)
	if _, err := newTestGate(getPostsServer(t, body).URL, vis).Check(context.Background(), "hourly", []string{uriA, uriB}); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if n := vis.callCount("did:plc:aaa"); n != 1 {
		t.Errorf("visibility lookups = %d, want 1 for two posts by the same author", n)
	}
}

func TestFeatureGateMixedBatch(t *testing.T) {
	const (
		okURI      = "at://did:plc:aaa/app.bsky.feed.post/1"
		quoteURI   = "at://did:plc:bbb/app.bsky.feed.post/2"
		hiddenURI  = "at://did:plc:ccc/app.bsky.feed.post/3"
		labelURI   = "at://did:plc:ddd/app.bsky.feed.post/4"
		missingURI = "at://did:plc:eee/app.bsky.feed.post/5"
	)
	body := fmt.Sprintf(`{"posts":[
		{"uri":%q,"cid":"c1","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:aaa","handle":"a.bsky.social"}},
		{"uri":%q,"cid":"c2","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:bbb","handle":"b.bsky.social"},"viewer":{"embeddingDisabled":true}},
		{"uri":%q,"cid":"c3","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:ccc","handle":"c.bsky.social"}},
		{"uri":%q,"cid":"c4","indexedAt":"2026-01-01T00:00:00Z","author":{"did":"did:plc:ddd","handle":"d.bsky.social"},"labels":[{"src":"did:plc:mod","uri":%q,"val":"!takedown","cts":"2026-01-01T00:00:00Z"}]}
	]}`, okURI, quoteURI, hiddenURI, labelURI, labelURI)

	vis := newFakeVisibility(map[string][]Visibility{"did:plc:ccc": {VisibilityHidden}})
	got, err := newTestGate(getPostsServer(t, body).URL, vis).Check(context.Background(), "exemplar",
		[]string{okURI, quoteURI, hiddenURI, labelURI, missingURI})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d verdicts, want 5: %+v", len(got), got)
	}

	want := map[string]Verdict{
		okURI:      {OK: true, Quotable: true},
		quoteURI:   {OK: true, Quotable: false, Reason: ReasonQuoteControl},
		hiddenURI:  {Reason: ReasonHiddenFromRecommendations},
		labelURI:   {Reason: "label:!takedown"},
		missingURI: {Reason: ReasonMissing},
	}
	for uri, w := range want {
		g := got[uri]
		if g.OK != w.OK || g.Quotable != w.Quotable || g.Reason != w.Reason {
			t.Errorf("Check()[%q] = %+v, want OK=%v Quotable=%v Reason=%q", uri, g, w.OK, w.Quotable, w.Reason)
		}
	}
}

func TestFeatureGateEmptyURIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s for an empty URI list", r.URL.Path)
	}))
	defer srv.Close()

	got, err := newTestGate(srv.URL, newFakeVisibility(nil)).Check(context.Background(), "hourly", nil)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want an empty map", got)
	}
}

func TestFeatureGateRejectsOversizedBatch(t *testing.T) {
	uris := make([]string, maxGetPostsURIs+1)
	for i := range uris {
		uris[i] = fmt.Sprintf("at://did:plc:aaa/app.bsky.feed.post/%d", i)
	}
	gate := newTestGate("https://example.invalid", newFakeVisibility(nil))
	if _, err := gate.Check(context.Background(), "hourly", uris); err == nil {
		t.Fatal("expected an error for a batch over the getPosts limit, got nil")
	}
}

func TestFeatureGatePropagatesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"InternalServerError","message":"boom"}`)
	}))
	defer srv.Close()

	gate := newTestGate(srv.URL, newFakeVisibility(nil))
	_, err := gate.Check(context.Background(), "hourly", []string{"at://did:plc:aaa/app.bsky.feed.post/1"})
	if err == nil {
		t.Fatal("expected an error from a 500 response, got nil")
	}
	// Callers decide whether to retry or to publish without featuring anyone,
	// so the message has to name the gate.
	if !strings.Contains(err.Error(), "feature gate") {
		t.Errorf("error = %q, want it to name the feature gate", err)
	}
}

func TestFeatureGateUnauthenticatedClient(t *testing.T) {
	gate := (&BlueskyClient{handle: "hourstats.bsky.social"}).NewFeatureGate(nil)
	if _, err := gate.Check(context.Background(), "hourly", []string{"at://did:plc:aaa/app.bsky.feed.post/1"}); err == nil {
		t.Fatal("expected an error when the client is not authenticated, got nil")
	}
}
