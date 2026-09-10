package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
)

// fakeGate answers from a fixed verdict table. A URI with no entry passes.
type fakeGate struct {
	calls    atomic.Int64
	verdicts map[string]client.Verdict
	err      error
}

func (f *fakeGate) Check(_ context.Context, _ string, uris []string) (map[string]client.Verdict, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]client.Verdict, len(uris))
	for _, uri := range uris {
		if v, ok := f.verdicts[uri]; ok {
			out[uri] = v
			continue
		}
		out[uri] = client.Verdict{OK: true, Quotable: true}
	}
	return out, nil
}

// gateCandidates builds n ranked candidates, one per author.
func gateCandidates(n int) []analyzer.AnalyzedPost {
	posts := make([]analyzer.AnalyzedPost, n)
	for i := range posts {
		posts[i] = analyzer.AnalyzedPost{Post: analyzer.Post{
			URI:    fmt.Sprintf("at://did:plc:%d/app.bsky.feed.post/%d", i, i),
			CID:    fmt.Sprintf("cid%d", i),
			Author: fmt.Sprintf("user%d.bsky.social", i),
		}}
	}
	return posts
}

func TestGateTopPostsKeepsRankOrder(t *testing.T) {
	candidates := gateCandidates(topPostCandidates)

	top, quoteControlled, ok := gateTopPosts(context.Background(), &fakeGate{}, candidates)
	if !ok {
		t.Fatal("gateTopPosts reported the gate unavailable, want it available")
	}
	if quoteControlled {
		t.Error("quoteControlled = true, want false when nothing is quote-controlled")
	}
	if len(top) != topPostCount {
		t.Fatalf("kept %d posts, want %d", len(top), topPostCount)
	}
	for i := range top {
		if top[i].URI != candidates[i].URI {
			t.Errorf("top[%d] = %q, want %q (rank order)", i, top[i].URI, candidates[i].URI)
		}
	}
}

func TestGateTopPostsSkipsRejectedAndBackfills(t *testing.T) {
	candidates := gateCandidates(topPostCandidates)
	gate := &fakeGate{verdicts: map[string]client.Verdict{
		candidates[0].URI: {Reason: client.ReasonHiddenFromRecommendations},
		candidates[2].URI: {Reason: client.ReasonBlocked},
	}}

	top, _, ok := gateTopPosts(context.Background(), gate, candidates)
	if !ok {
		t.Fatal("gateTopPosts reported the gate unavailable, want it available")
	}
	if len(top) != topPostCount {
		t.Fatalf("kept %d posts, want %d backfilled from further down", len(top), topPostCount)
	}
	want := []string{candidates[1].URI, candidates[3].URI, candidates[4].URI}
	for i, uri := range want {
		if top[i].URI != uri {
			t.Errorf("top[%d] = %q, want %q", i, top[i].URI, uri)
		}
	}
	if got := gate.calls.Load(); got != 1 {
		t.Errorf("gate calls = %d, want 1 for the whole candidate list", got)
	}
}

// A postgate forbids quoting, not listing: the post stays at rank 1 and only
// the embed is dropped.
func TestGateTopPostsQuoteControlledRankOneStaysListed(t *testing.T) {
	candidates := gateCandidates(topPostCandidates)
	gate := &fakeGate{verdicts: map[string]client.Verdict{
		candidates[0].URI: {OK: true, Quotable: false, Reason: client.ReasonQuoteControl},
	}}

	top, quoteControlled, ok := gateTopPosts(context.Background(), gate, candidates)
	if !ok {
		t.Fatal("gateTopPosts reported the gate unavailable, want it available")
	}
	if !quoteControlled {
		t.Error("quoteControlled = false, want true so the summary drops the embed")
	}
	if top[0].URI != candidates[0].URI {
		t.Errorf("top[0] = %q, want the quote-controlled post still listed first", top[0].URI)
	}
}

// A rank-2 postgate must not suppress the rank-1 embed.
func TestGateTopPostsQuoteControlBelowRankOneIgnored(t *testing.T) {
	candidates := gateCandidates(topPostCandidates)
	gate := &fakeGate{verdicts: map[string]client.Verdict{
		candidates[1].URI: {OK: true, Quotable: false, Reason: client.ReasonQuoteControl},
	}}

	_, quoteControlled, _ := gateTopPosts(context.Background(), gate, candidates)
	if quoteControlled {
		t.Error("quoteControlled = true, want false: only the quoted rank-1 post decides the embed")
	}
}

func TestGateTopPostsUnavailableRetriesThenFallsBack(t *testing.T) {
	candidates := gateCandidates(topPostCandidates)
	gate := &fakeGate{err: errors.New("appview down")}

	top, quoteControlled, ok := gateTopPosts(context.Background(), gate, candidates)
	if ok {
		t.Fatal("gateTopPosts reported the gate available, want unavailable")
	}
	if got := gate.calls.Load(); got != 2 {
		t.Errorf("gate calls = %d, want 2 (one retry)", got)
	}
	if len(top) != topPostCount {
		t.Fatalf("kept %d posts, want the original %d", len(top), topPostCount)
	}
	if !quoteControlled {
		t.Error("quoteControlled = false, want true so no embed goes out with an unchecked post")
	}
}

func TestGateTopPostsAllRejected(t *testing.T) {
	candidates := gateCandidates(2)
	gate := &fakeGate{verdicts: map[string]client.Verdict{
		candidates[0].URI: {Reason: client.ReasonMissing},
		candidates[1].URI: {Reason: client.ReasonAdultLabel},
	}}

	top, _, ok := gateTopPosts(context.Background(), gate, candidates)
	if !ok {
		t.Fatal("gateTopPosts reported the gate unavailable, want it available")
	}
	if len(top) != 0 {
		t.Errorf("kept %d posts, want none", len(top))
	}
}

func TestGateTopPostsNoCandidates(t *testing.T) {
	gate := &fakeGate{}
	top, _, ok := gateTopPosts(context.Background(), gate, nil)
	if !ok || len(top) != 0 {
		t.Errorf("gateTopPosts(nil) = (%v, ok=%v), want no posts and ok", top, ok)
	}
	if gate.calls.Load() != 0 {
		t.Error("the gate was called with no candidates")
	}
}

func TestExemplarGateMapsVerdictToOK(t *testing.T) {
	const (
		okURI      = "at://did:plc:a/app.bsky.feed.post/1"
		quotedURI  = "at://did:plc:b/app.bsky.feed.post/2"
		blockedURI = "at://did:plc:c/app.bsky.feed.post/3"
	)
	inner := &fakeGate{verdicts: map[string]client.Verdict{
		// An exemplar is a link, never a quote embed, so quote control alone
		// must not disqualify one.
		quotedURI:  {OK: true, Quotable: false, Reason: client.ReasonQuoteControl},
		blockedURI: {Reason: client.ReasonBlocked},
	}}

	allowed, err := exemplarGate{gate: inner}.Check(context.Background(), "exemplar",
		[]string{okURI, quotedURI, blockedURI})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	want := map[string]bool{okURI: true, quotedURI: true, blockedURI: false}
	for uri, w := range want {
		if allowed[uri] != w {
			t.Errorf("Check()[%q] = %v, want %v", uri, allowed[uri], w)
		}
	}
}
