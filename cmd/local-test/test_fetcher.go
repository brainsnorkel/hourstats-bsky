package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	bskyclient "github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// TestFetcherValidation tests the fetcher with strict validation
func TestFetcherValidation() error {
	ctx := context.Background()

	// Get Bluesky credentials
	handle := os.Getenv("BLUESKY_HANDLE")
	password := os.Getenv("BLUESKY_PASSWORD")

	if handle == "" || password == "" {
		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("no Bluesky credentials found: %w", err)
		}
		handle = cfg.Bluesky.Handle
		password = cfg.Bluesky.Password
	}

	// Create Bluesky client
	blueskyClient := bskyclient.New(handle, password)
	if err := blueskyClient.Authenticate(); err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	// Set up 30-minute analysis window
	analysisIntervalMinutes := 30
	cutoffTime := time.Now().UTC().Add(-time.Duration(analysisIntervalMinutes) * time.Minute)
	now := time.Now().UTC()

	fmt.Printf("🧪 Fetcher Validation Test\n")
	fmt.Printf("========================\n\n")
	fmt.Printf("Analysis Window: %s to %s (%d minutes)\n",
		cutoffTime.Format("2006-01-02 15:04:05 UTC"),
		now.Format("2006-01-02 15:04:05 UTC"),
		analysisIntervalMinutes)
	fmt.Printf("\n")

	// Initialize state manager for creating a test run
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return fmt.Errorf("failed to create state manager: %w", err)
	}

	runID := fmt.Sprintf("test-fetcher-%d", time.Now().Unix())
	// Use the cutoffTime already calculated above
	var runState *state.RunState
	runState, err = stateManager.CreateRun(ctx, runID, analysisIntervalMinutes, cutoffTime)
	_ = runState // Use the variable to avoid unused error
	if err != nil {
		return fmt.Errorf("failed to create run: %w", err)
	}

	fmt.Printf("📝 Test Run ID: %s\n\n", runID)

	// Fetch posts using the same logic as the fetcher
	totalPosts := 0
	currentCursor := ""
	iteration := 0
	maxIterations := 20
	seenURIs := make(map[string]bool)
	var allValidPosts []bskyclient.Post

	for {
		iteration++
		if iteration > maxIterations {
			fmt.Printf("⚠️  Reached max iterations (%d), stopping\n", maxIterations)
			break
		}

		// Check cursor limit
		var cursorNum int
		if currentCursor != "" {
			if _, parseErr := fmt.Sscanf(currentCursor, "%d", &cursorNum); parseErr == nil {
				if cursorNum >= 9000 {
					fmt.Printf("🚨 Cursor limit reached at %d, stopping\n", cursorNum)
					break
				}
			}
		}

		fmt.Printf("🔄 Iteration %d (cursor: %s)\n", iteration, currentCursor)

		// Fetch batch
		posts, shouldStop, err := fetchBatchInParallel(ctx, blueskyClient, currentCursor, cutoffTime)
		if err != nil {
			return fmt.Errorf("failed to fetch batch: %w", err)
		}

		fmt.Printf("   Retrieved: %d posts, shouldStop: %t\n", len(posts), shouldStop)

		// Validate posts are within time window
		if len(posts) > 0 {
			validCount := 0
			invalidCount := 0
			for _, post := range posts {
				// Skip duplicates
				if seenURIs[post.URI] {
					continue
				}
				seenURIs[post.URI] = true

				// Validate timestamp
				postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
				if err != nil {
					fmt.Printf("   ⚠️  Invalid timestamp in post %s: %v\n", post.URI, err)
					invalidCount++
					continue
				}

				// Check if post is within window (using IndexedAt which is stored in CreatedAt)
				if postTime.Before(cutoffTime) {
					fmt.Printf("   ❌ Post %s is OUTSIDE window: %s (cutoff: %s, diff: %s)\n",
						post.URI,
						postTime.Format("2006-01-02 15:04:05 UTC"),
						cutoffTime.Format("2006-01-02 15:04:05 UTC"),
						cutoffTime.Sub(postTime).Round(time.Second))
					invalidCount++
					continue
				}

				if postTime.After(now) {
					fmt.Printf("   ❌ Post %s is in FUTURE: %s (now: %s)\n",
						post.URI,
						postTime.Format("2006-01-02 15:04:05 UTC"),
						now.Format("2006-01-02 15:04:05 UTC"))
					invalidCount++
					continue
				}

				validCount++
				allValidPosts = append(allValidPosts, post)
			}
			totalPosts += len(posts)
			fmt.Printf("   ✓ Valid: %d, Invalid: %d (duplicates excluded from count)\n", validCount, invalidCount)
		}

		// Continue searching logic
		if len(posts) == 0 {
			fmt.Printf("   📭 No posts in iteration %d\n", iteration)
			if shouldStop {
				fmt.Printf("   ⏰ Confirmed past time window, stopping\n")
				break
			}
			if iteration >= maxIterations {
				fmt.Printf("   ⚠️  Max iterations reached\n")
				break
			}
			currentCursor = fmt.Sprintf("%d", iteration*1000)
			fmt.Printf("   ➡️  Continuing search...\n")
			continue
		}

		// Check stopping condition
		if shouldStop && totalPosts >= 1000 {
			fmt.Printf("   ✅ Reached end of time period with %d+ posts, stopping\n", totalPosts)
			break
		}

		// Prepare next iteration
		currentCursor = fmt.Sprintf("%d", iteration*1000)
	}

	fmt.Printf("\n📊 Final Results:\n")
	fmt.Printf("   Iterations: %d\n", iteration)
	fmt.Printf("   Total Posts Retrieved: %d\n", totalPosts)
	fmt.Printf("   Unique Valid Posts: %d\n", len(allValidPosts))

	// Validation
	if len(allValidPosts) == 0 {
		return fmt.Errorf("❌ FAILED: No valid posts retrieved. Expected 1000+ posts every 30 minutes")
	}

	if len(allValidPosts) < 100 && totalPosts < 1000 {
		return fmt.Errorf("❌ FAILED: Only %d valid posts retrieved. Expected 1000+ posts. Total retrieved: %d", len(allValidPosts), totalPosts)
	}

	// Validate all posts are within time window
	for i, post := range allValidPosts {
		if i >= 10 {
			break // Only check first 10 for detailed validation
		}
		postTime, _ := time.Parse(time.RFC3339, post.CreatedAt)
		if postTime.Before(cutoffTime) || postTime.After(now) {
			return fmt.Errorf("❌ FAILED: Post %d has invalid timestamp: %s (window: %s to %s)",
				i+1, postTime.Format("2006-01-02 15:04:05 UTC"),
				cutoffTime.Format("2006-01-02 15:04:05 UTC"),
				now.Format("2006-01-02 15:04:05 UTC"))
		}
	}

	fmt.Printf("\n✅ SUCCESS: Retrieved %d valid posts within the 30-minute window\n", len(allValidPosts))
	return nil
}

// fetchBatchInParallel makes parallel API calls (matches fetcher logic)
func fetchBatchInParallel(ctx context.Context, client *bskyclient.BlueskyClient, startCursor string, cutoffTime time.Time) ([]bskyclient.Post, bool, error) {
	// Define cursors for 10 parallel calls (100 posts each = 1000 total)
	cursors := []string{
		startCursor,
		addToCursor(startCursor, 100),
		addToCursor(startCursor, 200),
		addToCursor(startCursor, 300),
		addToCursor(startCursor, 400),
		addToCursor(startCursor, 500),
		addToCursor(startCursor, 600),
		addToCursor(startCursor, 700),
		addToCursor(startCursor, 800),
		addToCursor(startCursor, 900),
	}

	var allPosts []bskyclient.Post
	var oldestPostTime *time.Time
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Launch 10 goroutines for parallel fetching
	for i, cursor := range cursors {
		wg.Add(1)
		go func(cursorIndex int, cursorValue string) {
			defer wg.Done()

			// Add delay to reduce API load
			time.Sleep(time.Duration(cursorIndex) * time.Second)

			posts, _, _, err := client.GetTrendingPostsBatch(ctx, cursorValue, cutoffTime)
			if err != nil {
				fmt.Printf("   ❌ Parallel call %d failed: %v\n", cursorIndex+1, err)
				return
			}

			fmt.Printf("   📊 Call %d: API returned posts (filtered to %d)\n", cursorIndex+1, len(posts))

			// Find oldest post time
			var localOldestTime *time.Time
			if len(posts) > 0 {
				oldestPost := posts[len(posts)-1]
				postTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt)
				if err == nil {
					localOldestTime = &postTime
				}
			}

			// Thread-safe accumulation
			mu.Lock()
			allPosts = append(allPosts, posts...)
			if localOldestTime != nil {
				if oldestPostTime == nil || localOldestTime.Before(*oldestPostTime) {
					oldestPostTime = localOldestTime
				}
			}
			mu.Unlock()
		}(i, cursor)
	}

	wg.Wait()

	// Determine shouldStop - merge conditional into variable declaration
	shouldStop := oldestPostTime != nil && oldestPostTime.Before(cutoffTime)

	return allPosts, shouldStop, nil
}


