package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// This test demonstrates the batch overwrite bug
func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run cmd/test-batch-overwrite/main.go <runId>")
		fmt.Println("This will demonstrate how AddPosts overwrites batches")
		os.Exit(1)
	}

	runID := os.Args[1]
	ctx := context.Background()

	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		log.Fatalf("Failed to create state manager: %v", err)
	}

	// Simulate what happens in the fetcher
	// Each iteration calls AddPosts with batchIndex starting at 0
	fmt.Printf("Simulating fetcher iterations...\n\n")

	for iteration := 1; iteration <= 3; iteration++ {
		// Simulate posts from each iteration
		posts := make([]state.Post, 100)
		for i := 0; i < 100; i++ {
			posts[i] = state.Post{
				URI:       fmt.Sprintf("at://test/post%d-iter%d", i, iteration),
				CID:       fmt.Sprintf("cid%d", i),
				Text:      fmt.Sprintf("Post %d from iteration %d", i, iteration),
				Author:    "test",
				Likes:     i,
				Reposts:   0,
				Replies:   0,
				CreatedAt: "2025-11-02T00:00:00Z",
			}
		}

		fmt.Printf("Iteration %d: Storing 100 posts...\n", iteration)
		err := stateManager.AddPosts(ctx, runID, posts)
		if err != nil {
			log.Fatalf("Failed to add posts: %v", err)
		}

		// Check what's actually stored
		allPosts, err := stateManager.GetAllPosts(ctx, runID)
		if err != nil {
			log.Fatalf("Failed to get posts: %v", err)
		}

		fmt.Printf("  After iteration %d: %d posts stored in DynamoDB\n", iteration, len(allPosts))
		fmt.Printf("  Expected: %d posts (iteration * 100)\n\n", iteration*100)

		// Show first and last post URIs to demonstrate overwrite
		if len(allPosts) > 0 {
			fmt.Printf("  First post URI: %s\n", allPosts[0].URI)
			fmt.Printf("  Last post URI: %s\n\n", allPosts[len(allPosts)-1].URI)
		}
	}

	fmt.Println("🔍 BUG DEMONSTRATED:")
	fmt.Println("   - Each iteration should add 100 posts")
	fmt.Println("   - But batchIndex resets to 0 each time")
	fmt.Println("   - So batch0 gets overwritten repeatedly")
	fmt.Println("   - Only the last batch survives!")
}

