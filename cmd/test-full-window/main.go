package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
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

	// Test with 30-minute window
	cutoffTime := time.Now().UTC().Add(-30 * time.Minute)
	now := time.Now().UTC()
	fmt.Printf("🧪 Testing full 30-minute window fetch\n")
	fmt.Printf("=====================================\n\n")
	fmt.Printf("📅 Cutoff:  %s UTC\n", cutoffTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("📅 Now:     %s UTC\n", now.Format("2006-01-02 15:04:05"))
	fmt.Printf("📅 Window:  30 minutes\n\n")

	ctx := context.Background()
	var allPosts []bskyclient.Post
	currentCursor := ""
	iteration := 0
	maxIterations := 100
	seenURIs := make(map[string]bool)

	fmt.Printf("🔄 Starting pagination...\n\n")

	for {
		iteration++
		if iteration > maxIterations {
			fmt.Printf("⚠️  Reached max iterations (%d), stopping\n", maxIterations)
			break
		}

		if iteration%10 == 0 {
			fmt.Printf("   Iteration %d, cursor: '%s', posts so far: %d\n", iteration, currentCursor, len(allPosts))
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
			} else {
				fmt.Printf("✅ HEURISTIC PASSED: First call returned %d posts\n", len(posts))
				if len(posts) > 0 {
					firstPostTime, _ := time.Parse(time.RFC3339, posts[0].CreatedAt)
					diff := time.Since(firstPostTime)
					fmt.Printf("   First post is %s old\n", diff.Round(time.Second))
				}
			}
			fmt.Printf("\n")
		}

		// Deduplicate and collect posts
		for _, post := range posts {
			if !seenURIs[post.URI] {
				seenURIs[post.URI] = true
				allPosts = append(allPosts, post)
			}
		}

		// Check if we should stop
		shouldStop := false
		if len(posts) > 0 {
			// Check if oldest post is before cutoff
			oldestPost := posts[len(posts)-1]
			oldestTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt)
			if err == nil {
				if oldestTime.Before(cutoffTime) {
					shouldStop = true
				}
				// Stop if we have enough posts (2000+) OR if we've paginated very deep (50+ iterations)
				// The API may not return posts spanning the full window if sorted by engagement
				if len(allPosts) >= 2000 || iteration >= 50 {
					timeFromCutoff := oldestTime.Sub(cutoffTime).Minutes()
					fmt.Printf("   ✅ Collected %d posts, oldest is %.1f min after cutoff, stopping\n", len(allPosts), timeFromCutoff)
					shouldStop = true
				}
			}
		}

		if shouldStop {
			if len(posts) > 0 {
				oldestPost := posts[len(posts)-1]
				if oldestTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt); err == nil {
					if oldestTime.Before(cutoffTime) {
						fmt.Printf("   ⏰ Found posts before cutoff time, stopping\n")
					}
				}
			}
			break
		}

		if !hasMore || nextCursor == "" {
			fmt.Printf("   📄 No more pages available, stopping\n")
			break
		}

		currentCursor = nextCursor
	}

	fmt.Printf("\n📊 Results:\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("   Iterations:        %d\n", iteration)
	fmt.Printf("   Total posts found: %d\n", len(allPosts))

	if len(allPosts) == 0 {
		fmt.Printf("\n❌ FAILED: No posts retrieved\n")
		os.Exit(1)
	}

	// Analyze time distribution
	postTimes := make([]time.Time, 0, len(allPosts))
	for _, post := range allPosts {
		if postTime, err := time.Parse(time.RFC3339, post.CreatedAt); err == nil {
			postTimes = append(postTimes, postTime)
		}
	}

	if len(postTimes) == 0 {
		fmt.Printf("\n❌ FAILED: No valid timestamps\n")
		os.Exit(1)
	}

	sort.Slice(postTimes, func(i, j int) bool {
		return postTimes[i].Before(postTimes[j])
	})

	earliestPost := postTimes[0]
	latestPost := postTimes[len(postTimes)-1]

	fmt.Printf("\n📅 Time Distribution:\n")
	fmt.Printf("   Earliest post: %s (%s ago)\n",
		earliestPost.Format("15:04:05 UTC"),
		time.Since(earliestPost).Round(time.Second))
	fmt.Printf("   Latest post:   %s (%s ago)\n",
		latestPost.Format("15:04:05 UTC"),
		time.Since(latestPost).Round(time.Second))
	fmt.Printf("   Time span:     %s\n",
		latestPost.Sub(earliestPost).Round(time.Second))

	// Check if posts cover the full window
	timeCoverage := latestPost.Sub(earliestPost).Minutes()
	fmt.Printf("\n✅ Coverage Analysis:\n")
	fmt.Printf("   Window requested:  30.0 minutes\n")
	fmt.Printf("   Time span covered: %.1f minutes\n", timeCoverage)

	// Validate all posts are within window
	// Posts should be >= cutoffTime and <= now (within the 30-minute window)
	outsideWindow := 0
	var outsideBefore, outsideAfter int
	for _, postTime := range postTimes {
		if postTime.Before(cutoffTime) {
			outsideBefore++
			outsideWindow++
		} else if postTime.After(now) {
			outsideAfter++
			outsideWindow++
		}
	}

	fmt.Printf("   Posts before cutoff:  %d\n", outsideBefore)
	fmt.Printf("   Posts after 'now':    %d\n", outsideAfter)
	fmt.Printf("   Posts outside window: %d\n", outsideWindow)

	// Check if we have enough posts
	if len(allPosts) < 500 {
		fmt.Printf("\n⚠️  WARNING: Only %d posts retrieved (expected 500+ for 30-minute window)\n", len(allPosts))
	} else {
		fmt.Printf("\n✅ SUCCESS: Retrieved %d posts covering %.1f minutes of the 30-minute window\n",
			len(allPosts), timeCoverage)
	}

	if outsideWindow > 0 {
		fmt.Printf("\n❌ FAILED: %d posts are outside the time window\n", outsideWindow)
		os.Exit(1)
	}

	if timeCoverage < 20 {
		fmt.Printf("\n⚠️  WARNING: Only covering %.1f minutes of 30-minute window\n", timeCoverage)
	} else {
		fmt.Printf("\n✅ SUCCESS: Coverage is good (%.1f minutes >= 20 minutes)\n", timeCoverage)
	}

	fmt.Printf("\n✅ All checks passed!\n")
}

