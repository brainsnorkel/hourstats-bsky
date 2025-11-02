package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	bskyclient "github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
)

func main() {
	// Get credentials
	handle := os.Getenv("BLUESKY_HANDLE")
	password := os.Getenv("BLUESKY_PASSWORD")
	if handle == "" || password == "" {
		cfg, err := config.LoadConfig()
		if err != nil {
			log.Fatalf("No credentials: %v", err)
		}
		handle = cfg.Bluesky.Handle
		password = cfg.Bluesky.Password
	}

	// Create client
	client := bskyclient.New(handle, password)
	if err := client.Authenticate(); err != nil {
		log.Fatalf("Auth failed: %v", err)
	}

	// Test with 10-minute window
	// Note: cutoffTime is fixed at start, but we'll use current time for validation
	windowStart := time.Now().UTC()
	cutoffTime := windowStart.Add(-10 * time.Minute)
	fmt.Printf("🧪 Testing 10-minute window fetch\n")
	fmt.Printf("================================\n\n")
	fmt.Printf("📅 Window Start: %s UTC\n", windowStart.Format("2006-01-02 15:04:05"))
	fmt.Printf("📅 Cutoff Time:  %s UTC (10 min before start)\n", cutoffTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("📅 Window:       10 minutes\n\n")

	ctx := context.Background()
	var allPosts []bskyclient.Post
	currentCursor := ""
	iteration := 0
	maxIterations := 50
	seenURIs := make(map[string]bool)
	postsInWindow := 0
	postsOutsideWindow := 0

	fmt.Printf("🔄 Starting pagination...\n\n")

	for {
		iteration++
		if iteration > maxIterations {
			fmt.Printf("⚠️  Reached max iterations (%d), stopping\n", maxIterations)
			break
		}

		if iteration == 1 || iteration%5 == 0 {
			fmt.Printf("   Iteration %d, posts in window: %d, total: %d\n", iteration, postsInWindow, len(allPosts))
		}

		// Make API call
		posts, nextCursor, hasMore, err := client.GetTrendingPostsBatch(ctx, currentCursor, cutoffTime)
		if err != nil {
			log.Fatalf("API call failed at iteration %d: %v", iteration, err)
		}

		// Check heuristic on first call
		if iteration == 1 && currentCursor == "" {
			if len(posts) == 0 {
				fmt.Printf("🚨 HEURISTIC FAILED: First call returned 0 posts!\n")
				os.Exit(1)
			} else {
				fmt.Printf("✅ HEURISTIC PASSED: First call returned %d posts\n", len(posts))
				if len(posts) > 0 {
					firstPostTime, _ := time.Parse(time.RFC3339, posts[0].CreatedAt)
					fmt.Printf("   First post is %s old\n\n", time.Since(firstPostTime).Round(time.Second))
				}
			}
		}

		// Count posts in/out of window
		// Use current time for validation (posts can be up to current moment)
		now := time.Now().UTC()
		for _, post := range posts {
			if seenURIs[post.URI] {
				continue
			}
			seenURIs[post.URI] = true

			postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
			if err != nil {
				continue
			}

			// Check if post is within window: >= cutoffTime and <= now
			// Allow small buffer (30 seconds) for posts slightly after "now" due to time passing
			windowEnd := now.Add(30 * time.Second)
			if !postTime.Before(cutoffTime) && !postTime.After(windowEnd) {
				postsInWindow++
				allPosts = append(allPosts, post)
			} else {
				postsOutsideWindow++
			}
		}

		// Stop when we have enough posts in window
		if postsInWindow >= 500 {
			fmt.Printf("\n✅ Collected 500+ posts within the 10-minute window\n")
			break
		}

		// Check if we should stop
		shouldStop := false
		if len(posts) > 0 {
			oldestPost := posts[len(posts)-1]
			oldestTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt)
			if err == nil && oldestTime.Before(cutoffTime) {
				shouldStop = true
			}
		}

		if shouldStop {
			fmt.Printf("   ⏰ Found posts before cutoff time, stopping\n")
			break
		}

		if !hasMore || nextCursor == "" {
			fmt.Printf("   📄 No more pages available, stopping\n")
			break
		}

		currentCursor = nextCursor
	}

	fmt.Printf("\n📊 Final Results:\n")
	fmt.Printf("================================\n")
	fmt.Printf("   Iterations:            %d\n", iteration)
	fmt.Printf("   Posts in window:       %d\n", postsInWindow)
	fmt.Printf("   Posts outside window:  %d\n", postsOutsideWindow)
	fmt.Printf("   Total unique posts:    %d\n", len(allPosts))

	if postsInWindow >= 500 {
		fmt.Printf("\n✅ SUCCESS: Retrieved %d posts within the 10-minute window\n", postsInWindow)
	} else if postsInWindow >= 200 {
		fmt.Printf("\n✅ SUCCESS: Retrieved %d posts within the 10-minute window (acceptable for 10min window)\n", postsInWindow)
	} else {
		fmt.Printf("\n⚠️  WARNING: Only retrieved %d posts within window\n", postsInWindow)
	}

	// Check minimum requirement
	if postsInWindow >= 200 {
		fmt.Printf("\n✅ Minimum requirement met: %d posts (need 200+ for analysis)\n", postsInWindow)
	} else {
		fmt.Printf("\n❌ FAILED: Only %d posts in window (need 200+ minimum)\n", postsInWindow)
		os.Exit(1)
	}

	fmt.Printf("\n✅ Test completed successfully!\n")
}

