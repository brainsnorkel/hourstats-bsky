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

	// Test with 30-minute window
	cutoffTime := time.Now().UTC().Add(-30 * time.Minute)
	fmt.Printf("🧪 Testing API call with since=%s, sort=latest\n", cutoffTime.Format(time.RFC3339))
	fmt.Printf("📅 Cutoff: %s UTC\n", cutoffTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("📅 Now:    %s UTC\n", time.Now().UTC().Format("2006-01-02 15:04:05"))

	// Make first call with empty cursor
	ctx := context.Background()
	posts, nextCursor, hasMore, err := client.GetTrendingPostsBatch(ctx, "", cutoffTime)
	if err != nil {
		log.Fatalf("API call failed: %v", err)
	}

	fmt.Printf("\n📊 Results:\n")
	fmt.Printf("   Posts returned: %d\n", len(posts))
	fmt.Printf("   Next cursor: '%s'\n", nextCursor)
	fmt.Printf("   Has more: %v\n", hasMore)

	// HEURISTIC CHECK
	if len(posts) == 0 {
		fmt.Printf("\n🚨 HEURISTIC FAILED: First API call returned 0 posts!\n")
		fmt.Printf("🚨 This indicates a problem with API parameters (since/sort)\n")
		fmt.Printf("🚨 The API should return recent posts when called with:\n")
		fmt.Printf("   - cursor: \"\" (empty)\n")
		fmt.Printf("   - since: %s\n", cutoffTime.Format(time.RFC3339))
		fmt.Printf("   - sort: \"latest\"\n")
		os.Exit(1)
	}

	// Show first few posts
	fmt.Printf("\n📝 First 3 posts:\n")
	for i, post := range posts {
		if i >= 3 {
			break
		}
		postTime, _ := time.Parse(time.RFC3339, post.CreatedAt)
		textPreview := post.Text
		if len(textPreview) > 50 {
			textPreview = textPreview[:50] + "..."
		}
		diff := time.Since(postTime)
		fmt.Printf("   %d. @%s - %s\n", i+1, post.Author, textPreview)
		fmt.Printf("      Time: %s (%s ago)\n", postTime.Format("15:04:05 UTC"), diff.Round(time.Second))
	}

	fmt.Printf("\n✅ HEURISTIC PASSED: API returned %d posts\n", len(posts))
}

