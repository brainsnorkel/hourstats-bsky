package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
)

// fakeSummaryPoster records whether a publish was attempted, and with what.
type fakeSummaryPoster struct {
	calls atomic.Int64
	err   error
	posts []client.Post
}

func (f *fakeSummaryPoster) PostTrendingSummary(posts []client.Post, _ string, _ int, _ int, _ float64) (string, string, error) {
	f.calls.Add(1)
	f.posts = posts
	if f.err != nil {
		return "", "", f.err
	}
	return "at://did:plc:bot/app.bsky.feed.post/summary", "summarycid", nil
}

func testTopPosts() []analyzer.AnalyzedPost {
	return []analyzer.AnalyzedPost{
		{Post: analyzer.Post{
			URI: "at://did:plc:a/app.bsky.feed.post/1", CID: "cid1",
			Text: "hello", Author: "alice.bsky.social", Likes: 10,
		}},
	}
}

// TestPostSummarySkipsOnCancelledContext is the regression case: the Bluesky
// client's PostTrendingSummary takes no context and builds its own
// context.Background(), so without an explicit guard a SIGTERM that lands after
// hydration still published a post whose run and sentiment rows had already
// failed with "context canceled".
func TestPostSummarySkipsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	poster := &fakeSummaryPoster{}
	uri, cid := postSummary(ctx, poster, testTopPosts(), "positive", 12.5, 30, 1000, false)

	if got := poster.calls.Load(); got != 0 {
		t.Errorf("PostTrendingSummary called %d times on a cancelled context, want 0", got)
	}
	if uri != "" || cid != "" {
		t.Errorf("postSummary returned (%q, %q), want empty so no run row records an orphan post", uri, cid)
	}
}

func TestPostSummaryPublishesOnLiveContext(t *testing.T) {
	poster := &fakeSummaryPoster{}
	uri, cid := postSummary(context.Background(), poster, testTopPosts(), "positive", 12.5, 30, 1000, false)

	if got := poster.calls.Load(); got != 1 {
		t.Errorf("PostTrendingSummary called %d times, want 1", got)
	}
	if uri == "" || cid == "" {
		t.Errorf("postSummary returned (%q, %q), want the posted URI and CID", uri, cid)
	}
}

func TestPostSummaryReportsPostFailure(t *testing.T) {
	poster := &fakeSummaryPoster{err: fmt.Errorf("bluesky rejected the post")}
	uri, cid := postSummary(context.Background(), poster, testTopPosts(), "positive", 12.5, 30, 1000, false)

	if got := poster.calls.Load(); got != 1 {
		t.Errorf("PostTrendingSummary called %d times, want 1", got)
	}
	if uri != "" || cid != "" {
		t.Errorf("postSummary returned (%q, %q) after a failure, want empty", uri, cid)
	}
}

// TestPostSummaryMarksQuoteControlledTopPost checks the flag reaches the
// client post the embed decision is made from, and only that one.
func TestPostSummaryMarksQuoteControlledTopPost(t *testing.T) {
	topPosts := append(testTopPosts(), analyzer.AnalyzedPost{Post: analyzer.Post{
		URI: "at://did:plc:b/app.bsky.feed.post/2", CID: "cid2",
		Text: "world", Author: "bob.bsky.social", Likes: 5,
	}})

	poster := &fakeSummaryPoster{}
	postSummary(context.Background(), poster, topPosts, "positive", 12.5, 30, 1000, true)

	if len(poster.posts) != 2 {
		t.Fatalf("PostTrendingSummary got %d posts, want 2", len(poster.posts))
	}
	if !poster.posts[0].QuoteControlled {
		t.Error("top post should be marked QuoteControlled so the embed is dropped")
	}
	if poster.posts[1].QuoteControlled {
		t.Error("only the top post is checked, so #2 must not be marked")
	}
}

func TestPostSummaryLeavesQuoteControlUnsetByDefault(t *testing.T) {
	poster := &fakeSummaryPoster{}
	postSummary(context.Background(), poster, testTopPosts(), "positive", 12.5, 30, 1000, false)

	if len(poster.posts) != 1 {
		t.Fatalf("PostTrendingSummary got %d posts, want 1", len(poster.posts))
	}
	if poster.posts[0].QuoteControlled {
		t.Error("QuoteControlled should stay false when the check found nothing")
	}
}

// TestBlueskyClientSatisfiesSummaryPoster keeps the narrowed interface honest:
// the production client must still be usable at the call site.
func TestBlueskyClientSatisfiesSummaryPoster(t *testing.T) {
	var _ summaryPoster = (*client.BlueskyClient)(nil)
}
