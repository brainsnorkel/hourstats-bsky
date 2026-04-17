// Package hydrator batch-hydrates Bluesky posts with engagement data
// (likes, reposts, replies, author handle) via the app.bsky.feed.getPosts API.
package hydrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/lex/util"
	"github.com/christophergentle/hourstats-bsky/internal/store"
	"golang.org/x/time/rate"
)

// PostFetcher abstracts the Bluesky getPosts API call for testability.
type PostFetcher interface {
	GetPosts(ctx context.Context, uris []string) ([]*bsky.FeedDefs_PostView, error)
}

// PostUpdater abstracts the store write for testability.
type PostUpdater interface {
	UpdatePostEngagement(ctx context.Context, uri string, likes, reposts, replies int, authorHandle, sentiment string, engagementScore float64) error
}

// BlueskyFetcher is the real PostFetcher backed by the indigo client.
type BlueskyFetcher struct {
	client util.LexClient
}

// NewBlueskyFetcher wraps an authenticated indigo LexClient.
func NewBlueskyFetcher(client util.LexClient) *BlueskyFetcher {
	return &BlueskyFetcher{client: client}
}

// GetPosts calls app.bsky.feed.getPosts and returns the PostView slice.
func (f *BlueskyFetcher) GetPosts(ctx context.Context, uris []string) ([]*bsky.FeedDefs_PostView, error) {
	out, err := bsky.FeedGetPosts(ctx, f.client, uris)
	if err != nil {
		return nil, err
	}
	return out.Posts, nil
}

// Config controls hydration behaviour.
type Config struct {
	BatchSize      int           // Max URIs per API call (default 25, Bluesky limit)
	MaxConcurrency int           // Concurrent goroutines (default 10)
	RateLimit      float64       // Requests per second (default ~8.33 = 500/min)
	Timeout        time.Duration // Overall timeout (default 12 minutes)
}

func (c Config) withDefaults() Config {
	if c.BatchSize <= 0 {
		c.BatchSize = 25
	}
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = 10
	}
	if c.RateLimit <= 0 {
		c.RateLimit = 500.0 / 60.0 // ~8.33 rps
	}
	if c.Timeout <= 0 {
		c.Timeout = 12 * time.Minute
	}
	return c
}

// HydrateResult summarises one hydration pass.
type HydrateResult struct {
	Total    int // total posts attempted
	Hydrated int // successfully hydrated
	Filtered int // filtered out (adult content)
	Errors   int // API or update errors
}

// Hydrator fetches engagement data from the Bluesky API and writes it to the store.
type Hydrator struct {
	fetcher PostFetcher
	updater PostUpdater
	cfg     Config
}

// New creates a Hydrator with sensible defaults for any zero-value config fields.
func New(fetcher PostFetcher, updater PostUpdater, cfg Config) *Hydrator {
	return &Hydrator{
		fetcher: fetcher,
		updater: updater,
		cfg:     cfg.withDefaults(),
	}
}

// Hydrate fetches engagement metrics for posts and writes them to the store.
// It returns partial results if the context is cancelled or the deadline is exceeded.
func (h *Hydrator) Hydrate(ctx context.Context, posts []store.Post) (*HydrateResult, error) {
	if len(posts) == 0 {
		return &HydrateResult{}, nil
	}

	// Apply timeout if configured.
	if h.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.cfg.Timeout)
		defer cancel()
	}

	// Collect URIs and build a URI→index map so each batch goroutine can
	// mutate its posts in place. URIs are partitioned across disjoint batches,
	// so writes touch disjoint slice elements without additional locking.
	uris := make([]string, len(posts))
	idx := make(map[string]int, len(posts))
	for i, p := range posts {
		uris[i] = p.URI
		idx[p.URI] = i
	}

	// Split into batches.
	batches := splitBatches(uris, h.cfg.BatchSize)

	var (
		hydrated atomic.Int64
		filtered atomic.Int64
		errCount atomic.Int64
		wg       sync.WaitGroup
		sem      = make(chan struct{}, h.cfg.MaxConcurrency)
		limiter  = rate.NewLimiter(rate.Limit(h.cfg.RateLimit), 1)
	)

	for i, batch := range batches {
		// Check context before launching.
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		go func(batchIdx int, batchURIs []string) {
			defer wg.Done()

			// Acquire semaphore.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errCount.Add(int64(len(batchURIs)))
				return
			}

			// Rate limit.
			if err := limiter.Wait(ctx); err != nil {
				errCount.Add(int64(len(batchURIs)))
				return
			}

			views, err := h.fetcher.GetPosts(ctx, batchURIs)
			if err != nil {
				slog.Error("hydrator batch failed", "batch", fmt.Sprintf("%d/%d", batchIdx+1, len(batches)), "error", err)
				errCount.Add(int64(len(batchURIs)))
				return
			}

			for _, v := range views {
				if v == nil {
					continue
				}

				if hasAdultContent(v.Labels) {
					filtered.Add(1)
					continue
				}

				likes, reposts, replies := counters(v)
				handle := ""
				if v.Author != nil {
					handle = v.Author.Handle
				}

				// Sentiment and engagement score are left zero — the analyzer fills them later.
				if err := h.updater.UpdatePostEngagement(ctx, v.Uri, likes, reposts, replies, handle, "", 0); err != nil {
					slog.Error("hydrator update failed", "uri", v.Uri, "error", err)
					errCount.Add(1)
					continue
				}

				// Mirror the DB write to the in-memory slice so callers don't
				// need a second GetPostsSince to see post-hydration state.
				if i, ok := idx[v.Uri]; ok {
					posts[i].AuthorHandle = handle
					posts[i].Likes = likes
					posts[i].Reposts = reposts
					posts[i].Replies = replies
				}
				hydrated.Add(1)
			}

			// Progress log every few batches.
			h2 := hydrated.Load()
			if h2 > 0 && h2%500 < int64(h.cfg.BatchSize) {
				slog.Info("hydrator progress", "hydrated", h2, "total", len(posts))
			}
		}(i, batch)
	}

	wg.Wait()

	result := &HydrateResult{
		Total:    len(posts),
		Hydrated: int(hydrated.Load()),
		Filtered: int(filtered.Load()),
		Errors:   int(errCount.Load()),
	}

	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, nil
}

// splitBatches divides uris into slices of at most size n.
func splitBatches(uris []string, n int) [][]string {
	var batches [][]string
	for i := 0; i < len(uris); i += n {
		end := i + n
		if end > len(uris) {
			end = len(uris)
		}
		batches = append(batches, uris[i:end])
	}
	return batches
}

// adultLabels are Bluesky moderation label values for adult content.
var adultLabels = map[string]bool{
	"porn":          true,
	"sexual":        true,
	"nudity":        true,
	"graphic-media": true,
}

// hasAdultContent returns true if any label matches an adult content filter.
func hasAdultContent(labels []*atproto.LabelDefs_Label) bool {
	for _, l := range labels {
		if l != nil && adultLabels[l.Val] {
			return true
		}
	}
	return false
}

// counters safely extracts engagement counts, treating nil as 0.
func counters(v *bsky.FeedDefs_PostView) (likes, reposts, replies int) {
	if v.LikeCount != nil {
		likes = int(*v.LikeCount)
	}
	if v.RepostCount != nil {
		reposts = int(*v.RepostCount)
	}
	if v.ReplyCount != nil {
		replies = int(*v.ReplyCount)
	}
	return
}
