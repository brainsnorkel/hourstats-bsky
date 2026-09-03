package hydrator

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockFetcher struct {
	mu       sync.Mutex
	calls    [][]string // recorded URI batches
	response func(uris []string) ([]*bsky.FeedDefs_PostView, error)
}

func (m *mockFetcher) GetPosts(_ context.Context, uris []string) ([]*bsky.FeedDefs_PostView, error) {
	m.mu.Lock()
	m.calls = append(m.calls, uris)
	m.mu.Unlock()
	if m.response != nil {
		return m.response(uris)
	}
	return nil, nil
}

type updateCall struct {
	URI, AuthorHandle       string
	Likes, Reposts, Replies int
}

type mockUpdater struct {
	mu    sync.Mutex
	calls []updateCall
	err   error // if set, every call returns this
}

func (m *mockUpdater) UpdatePostEngagement(_ context.Context, uri string, likes, reposts, replies int, authorHandle string) error {
	m.mu.Lock()
	m.calls = append(m.calls, updateCall{
		URI: uri, Likes: likes, Reposts: reposts, Replies: replies,
		AuthorHandle: authorHandle,
	})
	m.mu.Unlock()
	return m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func i64(v int64) *int64 { return &v }

func makePosts(n int) []store.Post {
	posts := make([]store.Post, n)
	for i := range posts {
		posts[i] = store.Post{URI: fmt.Sprintf("at://did:plc:test/app.bsky.feed.post/%d", i)}
	}
	return posts
}

func makeViews(uris []string) []*bsky.FeedDefs_PostView {
	views := make([]*bsky.FeedDefs_PostView, len(uris))
	for i, u := range uris {
		views[i] = &bsky.FeedDefs_PostView{
			Uri:         u,
			LikeCount:   i64(int64(i + 1)),
			RepostCount: i64(int64(i + 2)),
			ReplyCount:  i64(int64(i + 3)),
			Author:      &bsky.ActorDefs_ProfileViewBasic{Handle: fmt.Sprintf("user%d.bsky.social", i)},
		}
	}
	return views
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHydrateEmpty(t *testing.T) {
	h := New(&mockFetcher{}, &mockUpdater{}, Config{})
	res, err := h.Hydrate(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Total != 0 || res.Hydrated != 0 || res.Filtered != 0 || res.Errors != 0 {
		t.Fatalf("expected zero result, got %+v", res)
	}
}

func TestHydrateSingleBatch(t *testing.T) {
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			return makeViews(uris), nil
		},
	}
	updater := &mockUpdater{}
	h := New(fetcher, updater, Config{RateLimit: 1000}) // fast for tests

	posts := makePosts(5)
	res, err := h.Hydrate(context.Background(), posts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Total != 5 {
		t.Errorf("Total = %d, want 5", res.Total)
	}
	if res.Hydrated != 5 {
		t.Errorf("Hydrated = %d, want 5", res.Hydrated)
	}
	if res.Errors != 0 {
		t.Errorf("Errors = %d, want 0", res.Errors)
	}

	// Should have been a single batch call.
	fetcher.mu.Lock()
	if len(fetcher.calls) != 1 {
		t.Errorf("fetcher called %d times, want 1", len(fetcher.calls))
	}
	fetcher.mu.Unlock()
}

func TestHydrateBatching(t *testing.T) {
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			return makeViews(uris), nil
		},
	}
	updater := &mockUpdater{}
	h := New(fetcher, updater, Config{BatchSize: 25, RateLimit: 1000})

	posts := makePosts(60)
	res, err := h.Hydrate(context.Background(), posts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Total != 60 {
		t.Errorf("Total = %d, want 60", res.Total)
	}
	if res.Hydrated != 60 {
		t.Errorf("Hydrated = %d, want 60", res.Hydrated)
	}

	// Should have been 3 batches: 25 + 25 + 10.
	fetcher.mu.Lock()
	if len(fetcher.calls) != 3 {
		t.Errorf("fetcher called %d times, want 3", len(fetcher.calls))
	}
	fetcher.mu.Unlock()
}

func TestHydrateAdultContentFiltered(t *testing.T) {
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			views := makeViews(uris)
			// Tag first view with an adult label.
			if len(views) > 0 {
				views[0].Labels = []*atproto.LabelDefs_Label{{Val: "porn"}}
			}
			return views, nil
		},
	}
	updater := &mockUpdater{}
	h := New(fetcher, updater, Config{RateLimit: 1000})

	posts := makePosts(3)
	res, err := h.Hydrate(context.Background(), posts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Filtered != 1 {
		t.Errorf("Filtered = %d, want 1", res.Filtered)
	}
	if res.Hydrated != 2 {
		t.Errorf("Hydrated = %d, want 2", res.Hydrated)
	}
}

func TestHydrateNilCounters(t *testing.T) {
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			return []*bsky.FeedDefs_PostView{{
				Uri:    uris[0],
				Author: &bsky.ActorDefs_ProfileViewBasic{Handle: "nil.bsky.social"},
				// All count fields are nil.
			}}, nil
		},
	}
	updater := &mockUpdater{}
	h := New(fetcher, updater, Config{RateLimit: 1000})

	posts := makePosts(1)
	res, err := h.Hydrate(context.Background(), posts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Hydrated != 1 {
		t.Errorf("Hydrated = %d, want 1", res.Hydrated)
	}

	updater.mu.Lock()
	defer updater.mu.Unlock()
	if len(updater.calls) != 1 {
		t.Fatalf("updater called %d times, want 1", len(updater.calls))
	}
	c := updater.calls[0]
	if c.Likes != 0 || c.Reposts != 0 || c.Replies != 0 {
		t.Errorf("expected all zeros, got likes=%d reposts=%d replies=%d", c.Likes, c.Reposts, c.Replies)
	}
}

func TestHydrateAPIError(t *testing.T) {
	callNum := 0
	var mu sync.Mutex
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			mu.Lock()
			n := callNum
			callNum++
			mu.Unlock()
			if n == 0 {
				return nil, fmt.Errorf("simulated API failure")
			}
			return makeViews(uris), nil
		},
	}
	updater := &mockUpdater{}
	// 2 batches: first fails, second succeeds.
	h := New(fetcher, updater, Config{BatchSize: 5, MaxConcurrency: 1, RateLimit: 1000})

	posts := makePosts(10)
	res, err := h.Hydrate(context.Background(), posts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Errors < 5 {
		t.Errorf("Errors = %d, want >= 5 (one failed batch of 5)", res.Errors)
	}
	if res.Hydrated != 5 {
		t.Errorf("Hydrated = %d, want 5", res.Hydrated)
	}
}

func TestHydrateContextCancelled(t *testing.T) {
	// Fetcher that blocks until context is done.
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			time.Sleep(2 * time.Second) // simulate slow API
			return makeViews(uris), nil
		},
	}
	updater := &mockUpdater{}
	h := New(fetcher, updater, Config{BatchSize: 5, MaxConcurrency: 1, RateLimit: 1000, Timeout: 0})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	posts := makePosts(50) // lots of batches, will be cut short
	res, err := h.Hydrate(ctx, posts)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	// Partial results — not all 50 should be hydrated.
	if res.Hydrated >= 50 {
		t.Errorf("expected partial hydration, got %d", res.Hydrated)
	}
	// Batches abandoned on cancellation must still be accounted for, otherwise
	// a truncated run is indistinguishable from a clean one.
	if res.Errors == 0 {
		t.Error("Errors = 0, want > 0 (abandoned batches must be charged)")
	}
	if got := res.Hydrated + res.Filtered + res.Errors; got != res.Total {
		t.Errorf("accounting mismatch: hydrated(%d) + filtered(%d) + errors(%d) = %d, want Total = %d",
			res.Hydrated, res.Filtered, res.Errors, got, res.Total)
	}
}

// TestHydrateDeadlineAccountsEveryBatch is the production-shaped case: the
// internal timeout expires long before the rate limiter can drain the queue, so
// nearly every batch is abandoned by the producer rather than by a worker.
func TestHydrateDeadlineAccountsEveryBatch(t *testing.T) {
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			time.Sleep(10 * time.Millisecond)
			return makeViews(uris), nil
		},
	}
	h := New(fetcher, &mockUpdater{}, Config{
		BatchSize:      5,
		MaxConcurrency: 1,
		RateLimit:      1000,
		Timeout:        100 * time.Millisecond, // internal deadline, as in production
	})

	posts := makePosts(500) // 100 batches; the deadline cuts this short
	res, err := h.Hydrate(context.Background(), posts)
	if err == nil {
		t.Fatal("expected deadline error, got nil")
	}
	if res.Total != 500 {
		t.Errorf("Total = %d, want 500", res.Total)
	}
	if res.Errors == 0 {
		t.Error("Errors = 0, want > 0 (abandoned batches must be charged)")
	}
	if got := res.Hydrated + res.Filtered + res.Errors; got != res.Total {
		t.Errorf("accounting mismatch: hydrated(%d) + filtered(%d) + errors(%d) = %d, want Total = %d",
			res.Hydrated, res.Filtered, res.Errors, got, res.Total)
	}
}

func TestHydrateConfigDefaults(t *testing.T) {
	h := New(&mockFetcher{}, &mockUpdater{}, Config{})
	if h.cfg.BatchSize != 25 {
		t.Errorf("BatchSize = %d, want 25", h.cfg.BatchSize)
	}
	if h.cfg.MaxConcurrency != 10 {
		t.Errorf("MaxConcurrency = %d, want 10", h.cfg.MaxConcurrency)
	}
	// ~8.33 rps
	if h.cfg.RateLimit < 8.0 || h.cfg.RateLimit > 9.0 {
		t.Errorf("RateLimit = %f, want ~8.33", h.cfg.RateLimit)
	}
	if h.cfg.Timeout != 12*time.Minute {
		t.Errorf("Timeout = %v, want 12m", h.cfg.Timeout)
	}
}

func TestHydrateMutatesPostsInPlace(t *testing.T) {
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			return makeViews(uris), nil
		},
	}
	h := New(fetcher, &mockUpdater{}, Config{BatchSize: 25, RateLimit: 1000})

	posts := makePosts(60)
	for _, p := range posts {
		if p.AuthorHandle != "" {
			t.Fatalf("precondition: posts start without handles")
		}
	}

	if _, err := h.Hydrate(context.Background(), posts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Every post should now carry the values from makeViews.
	for i, p := range posts {
		if p.AuthorHandle == "" {
			t.Errorf("posts[%d].AuthorHandle not populated", i)
		}
		if p.Likes == 0 && p.Reposts == 0 && p.Replies == 0 {
			t.Errorf("posts[%d] engagement not populated", i)
		}
	}
}

func TestHydrateUpdaterCalled(t *testing.T) {
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			return []*bsky.FeedDefs_PostView{{
				Uri:         uris[0],
				LikeCount:   i64(42),
				RepostCount: i64(7),
				ReplyCount:  i64(3),
				Author:      &bsky.ActorDefs_ProfileViewBasic{Handle: "alice.bsky.social"},
			}}, nil
		},
	}
	updater := &mockUpdater{}
	h := New(fetcher, updater, Config{RateLimit: 1000})

	posts := makePosts(1)
	_, err := h.Hydrate(context.Background(), posts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updater.mu.Lock()
	defer updater.mu.Unlock()
	if len(updater.calls) != 1 {
		t.Fatalf("updater called %d times, want 1", len(updater.calls))
	}
	c := updater.calls[0]
	if c.Likes != 42 {
		t.Errorf("Likes = %d, want 42", c.Likes)
	}
	if c.Reposts != 7 {
		t.Errorf("Reposts = %d, want 7", c.Reposts)
	}
	if c.Replies != 3 {
		t.Errorf("Replies = %d, want 3", c.Replies)
	}
	if c.AuthorHandle != "alice.bsky.social" {
		t.Errorf("AuthorHandle = %q, want alice.bsky.social", c.AuthorHandle)
	}
}

// TestHydrateGoroutineBounded asserts the worker pool is actually bounded: a
// goroutine-per-batch implementation would spike to ~400 live goroutines here,
// whereas the pool must stay within MaxConcurrency plus the feeder, the
// sampler, and a little slack for runtime bookkeeping.
func TestHydrateGoroutineBounded(t *testing.T) {
	const (
		batchSize      = 25
		numBatches     = 400
		maxConcurrency = 10
		slack          = 5 // feeder + sampler + runtime bookkeeping
	)

	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			time.Sleep(2 * time.Millisecond) // slow enough to keep workers busy
			return makeViews(uris), nil
		},
	}
	h := New(fetcher, &mockUpdater{}, Config{
		BatchSize:      batchSize,
		MaxConcurrency: maxConcurrency,
		RateLimit:      1e6, // effectively unlimited
	})

	baseline := runtime.NumGoroutine()
	limit := baseline + maxConcurrency + slack

	var peak atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if n := int64(runtime.NumGoroutine()); n > peak.Load() {
				peak.Store(n)
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()

	posts := makePosts(batchSize * numBatches)
	res, err := h.Hydrate(context.Background(), posts)
	close(stop)
	<-done

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Hydrated != batchSize*numBatches {
		t.Errorf("Hydrated = %d, want %d", res.Hydrated, batchSize*numBatches)
	}

	got := int(peak.Load())
	t.Logf("peak goroutines = %d (baseline %d, limit %d)", got, baseline, limit)
	if got > limit {
		t.Errorf("peak goroutines = %d, want <= %d (baseline %d + %d workers + %d slack)",
			got, limit, baseline, maxConcurrency, slack)
	}
}

// TestHydrateFewerBatchesThanWorkers covers the degenerate case where the pool
// is larger than the amount of work available.
func TestHydrateFewerBatchesThanWorkers(t *testing.T) {
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			return makeViews(uris), nil
		},
	}
	h := New(fetcher, &mockUpdater{}, Config{BatchSize: 25, MaxConcurrency: 32, RateLimit: 1000})

	posts := makePosts(30) // 2 batches, 32 workers configured
	res, err := h.Hydrate(context.Background(), posts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Hydrated != 30 {
		t.Errorf("Hydrated = %d, want 30", res.Hydrated)
	}
	if res.Errors != 0 {
		t.Errorf("Errors = %d, want 0", res.Errors)
	}
}
