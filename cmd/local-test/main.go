package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	bskyclient "github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
	"github.com/christophergentle/hourstats-bsky/internal/formatter"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// MockLambdaClient simulates Lambda invocations locally
type MockLambdaClient struct {
	stateManager   *state.StateManager
	superDebugMode bool
}

// MockFetcherEvent represents the event for the fetcher lambda
type MockFetcherEvent struct {
	RunID                   string `json:"runId"`
	AnalysisIntervalMinutes int    `json:"analysisIntervalMinutes"`
	Status                  string `json:"status"`
	MaxIterations           int    `json:"maxIterations"`
	Cursor                  string `json:"cursor,omitempty"`
}

// MockProcessorEvent represents the event for the processor lambda
type MockProcessorEvent struct {
	RunID                   string `json:"runId"`
	AnalysisIntervalMinutes int    `json:"analysisIntervalMinutes"`
	Status                  string `json:"status"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run cmd/local-test/main.go <test-interval-minutes> [live] [super-debug]")
		fmt.Println("Example: go run cmd/local-test/main.go 5")
		fmt.Println("Example: go run cmd/local-test/main.go 60 live")
		fmt.Println("Example: go run cmd/local-test/main.go 2 super-debug")
		fmt.Println("")
		fmt.Println("Options:")
		fmt.Println("  <test-interval-minutes>  Number of minutes to analyze (1-60)")
		fmt.Println("  live                     Run in live mode: process all posts and post to Bluesky")
		fmt.Println("  super-debug              Print detailed info for each post (handle, URI, timestamp, analysis window)")
		os.Exit(1)
	}

	// Parse test interval
	var testIntervalMinutes int
	_, err := fmt.Sscanf(os.Args[1], "%d", &testIntervalMinutes)
	if err != nil {
		log.Fatalf("Invalid interval: %v", err)
	}

	if testIntervalMinutes < 1 || testIntervalMinutes > 60 {
		log.Fatalf("Test interval must be between 1 and 60 minutes")
	}

	// Check for live mode and super debug mode
	liveMode := len(os.Args) > 2 && os.Args[2] == "live"
	superDebugMode := false

	// Check for super-debug in any argument position
	for _, arg := range os.Args[2:] {
		if arg == "super-debug" {
			superDebugMode = true
			break
		}
	}

	if liveMode {
		fmt.Printf("🚀 Starting LIVE test with %d minute interval...\n", testIntervalMinutes)
		fmt.Println("⚠️  WARNING: This will post to Bluesky!")
		fmt.Println("")
	} else {
		fmt.Printf("🧪 Starting local test with %d minute interval...\n\n", testIntervalMinutes)
	}

	if superDebugMode {
		fmt.Println("🔍 SUPER DEBUG MODE: Will print detailed info for each post")
		fmt.Println("")
	}

	ctx := context.Background()

	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		log.Fatalf("Failed to create state manager: %v", err)
	}

	// Create mock lambda client
	mockClient := &MockLambdaClient{
		stateManager:   stateManager,
		superDebugMode: superDebugMode,
	}

	// Generate a test run ID
	runID := fmt.Sprintf("test-run-%d", time.Now().Unix())
	fmt.Printf("📝 Test Run ID: %s\n", runID)

	// Step 1: Create run state (orchestrator step)
	fmt.Println("\n🎯 Step 1: Creating run state (Orchestrator)...")
	err = mockClient.createRunState(ctx, runID, testIntervalMinutes)
	if err != nil {
		log.Fatalf("Failed to create run state: %v", err)
	}
	fmt.Println("✅ Run state created successfully")

	// Step 2: Simulate fetcher chain
	fmt.Println("\n🔄 Step 2: Running fetcher chain...")
	err = mockClient.runFetcherChain(ctx, runID, liveMode)
	if err != nil {
		log.Fatalf("Failed to run fetcher chain: %v", err)
	}
	fmt.Println("✅ Fetcher chain completed")

	// Step 3: Run processor
	fmt.Println("\n⚙️ Step 3: Running processor...")
	err = mockClient.runProcessor(ctx, runID, testIntervalMinutes, liveMode)
	if err != nil {
		log.Fatalf("Failed to run processor: %v", err)
	}
	fmt.Println("✅ Processor completed")

	// Step 4: Show results
	fmt.Println("\n📊 Step 4: Test Results...")
	err = mockClient.showResults(ctx, runID)
	if err != nil {
		log.Fatalf("Failed to show results: %v", err)
	}

	if liveMode {
		fmt.Println("\n🎉 LIVE test completed successfully!")
	} else {
		fmt.Println("\n🎉 Local test completed successfully!")
	}
}

// createRunState simulates the orchestrator creating a run state
func (m *MockLambdaClient) createRunState(ctx context.Context, runID string, analysisIntervalMinutes int) error {
	now := time.Now()

	// Calculate cutoff time for testing (go back the specified minutes)
	// Calculate cutoff time in UTC to match API timestamps
	cutoffTime := now.UTC().Add(-time.Duration(analysisIntervalMinutes) * time.Minute)

	_, err := m.stateManager.CreateRun(ctx, runID, analysisIntervalMinutes)
	if err != nil {
		return fmt.Errorf("failed to create run: %w", err)
	}

	fmt.Printf("  📅 Analysis period: %s to %s (%d minutes)\n",
		cutoffTime.Local().Format("2006-01-02 15:04:05"),
		now.Local().Format("2006-01-02 15:04:05"),
		analysisIntervalMinutes)

	return nil
}

// runFetcherChain simulates the new parallel fetcher execution
func (m *MockLambdaClient) runFetcherChain(ctx context.Context, runID string, liveMode bool) error {
	// Get Bluesky credentials from environment or config
	handle := os.Getenv("BLUESKY_HANDLE")
	password := os.Getenv("BLUESKY_PASSWORD")

	if handle == "" || password == "" {
		// Try to load from config file
		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("no Bluesky credentials found in environment or config: %w", err)
		}
		handle = cfg.Bluesky.Handle
		password = cfg.Bluesky.Password
	}

	// Create Bluesky client
	blueskyClient := bskyclient.New(handle, password)
	if err := blueskyClient.Authenticate(); err != nil {
		return fmt.Errorf("failed to authenticate with Bluesky: %w", err)
	}

	// Get run state
	runState, err := m.stateManager.GetRun(ctx, runID, "orchestrator")
	if err != nil {
		return fmt.Errorf("failed to get run state: %w", err)
	}

	fmt.Println("  🚀 Starting parallel fetch with internal loops...")

	// Run parallel fetch with internal loops (same as new fetcher)
	totalPosts, err := m.fetchAllPostsInParallel(ctx, blueskyClient, runState.CutoffTime, runID, liveMode)
	if err != nil {
		return fmt.Errorf("failed to fetch posts in parallel: %w", err)
	}

	// Update state to indicate fetching is complete
	if err := m.stateManager.UpdateCursor(ctx, runID, "", false); err != nil {
		return fmt.Errorf("failed to update cursor: %w", err)
	}

	fmt.Printf("  ✅ Parallel fetcher completed - Total posts: %d\n", totalPosts)
	return nil
}

// runProcessor simulates the processor execution
func (m *MockLambdaClient) runProcessor(ctx context.Context, runID string, analysisIntervalMinutes int, liveMode bool) error {
	// Get all posts for this run
	allPosts, err := m.stateManager.GetAllPosts(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to get all posts: %w", err)
	}

	// Deduplicate posts by URI, keeping the one with highest engagement score
	deduplicatedPosts := m.deduplicatePostsByURI(allPosts)
	fmt.Printf("  📊 Processing %d posts (deduplicated from %d original)...\n", len(deduplicatedPosts), len(allPosts))

	if len(deduplicatedPosts) == 0 {
		fmt.Println("  ❌ No posts to process")
		return nil
	}

	// Analyze sentiment for all posts
	sentimentAnalyzer := analyzer.New()
	for i := range deduplicatedPosts {
		// Convert to analyzer.Post format
		analyzerPost := analyzer.Post{
			URI:     deduplicatedPosts[i].URI,
			CID:     deduplicatedPosts[i].CID,
			Text:    deduplicatedPosts[i].Text,
			Author:  deduplicatedPosts[i].Author,
			Likes:   deduplicatedPosts[i].Likes,
			Replies: deduplicatedPosts[i].Replies,
			Reposts: deduplicatedPosts[i].Reposts,
			CreatedAt: deduplicatedPosts[i].CreatedAt,
		}

		analyzedPosts, err := sentimentAnalyzer.AnalyzePosts([]analyzer.Post{analyzerPost})
		if err != nil {
			fmt.Printf("    ⚠️ Failed to analyze sentiment for post %d: %v\n", i+1, err)
			deduplicatedPosts[i].Sentiment = "neutral"
		} else if len(analyzedPosts) > 0 {
			deduplicatedPosts[i].Sentiment = analyzedPosts[0].Sentiment
		} else {
			deduplicatedPosts[i].Sentiment = "neutral"
		}
	}

	// Calculate engagement scores
	for i := range deduplicatedPosts {
		deduplicatedPosts[i].EngagementScore = float64(deduplicatedPosts[i].Likes + deduplicatedPosts[i].Reposts + deduplicatedPosts[i].Replies)
	}

	// Sort by engagement score (simple bubble sort for testing)
	for i := 0; i < len(deduplicatedPosts)-1; i++ {
		for j := 0; j < len(deduplicatedPosts)-i-1; j++ {
			if deduplicatedPosts[j].EngagementScore < deduplicatedPosts[j+1].EngagementScore {
				deduplicatedPosts[j], deduplicatedPosts[j+1] = deduplicatedPosts[j+1], deduplicatedPosts[j]
			}
		}
	}

	// Get top 5 posts
	topPosts := deduplicatedPosts
	if len(deduplicatedPosts) > 5 {
		topPosts = deduplicatedPosts[:5]
	}

	// Calculate overall sentiment and percentages
	overallSentiment, positivePercent, negativePercent := m.calculateOverallSentimentWithPercentages(deduplicatedPosts)

	// Convert to formatter posts
	formatterPosts := make([]formatter.Post, len(topPosts))
	for i, post := range topPosts {
		formatterPosts[i] = formatter.Post{
			URI:             post.URI,
			CID:             post.CID,
			Author:          post.Author,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			EngagementScore: post.EngagementScore,
			Sentiment:       post.Sentiment,
		}
	}

	// Generate post content
	postContent := formatter.FormatPostContent(formatterPosts, overallSentiment, analysisIntervalMinutes, len(allPosts), positivePercent, negativePercent)

	// Calculate character count
	charCount := len(postContent)
	blueskyLimit := 300
	remaining := blueskyLimit - charCount

	fmt.Printf("  📝 Generated post content (%d chars, %d remaining):\n", charCount, remaining)
	fmt.Println("  " + strings.Repeat("=", 60))
	fmt.Printf("  %s\n", postContent)
	fmt.Println("  " + strings.Repeat("=", 60))

	if remaining < 0 {
		fmt.Printf("  ⚠️ WARNING: Post exceeds Bluesky limit by %d characters!\n", -remaining)
	} else if remaining < 50 {
		fmt.Printf("  ⚠️ WARNING: Only %d characters remaining\n", remaining)
	}

	// Update run state with top posts
	err = m.stateManager.SetAnalysisComplete(ctx, runID, overallSentiment, topPosts)
	if err != nil {
		return fmt.Errorf("failed to set top posts: %w", err)
	}

	// Post to Bluesky if in live mode
	if liveMode {
		fmt.Println("  🚀 Posting to Bluesky...")

		// Get Bluesky credentials
		handle := os.Getenv("BLUESKY_HANDLE")
		password := os.Getenv("BLUESKY_PASSWORD")

		if handle == "" || password == "" {
			// Try to load from config file
			cfg, err := config.LoadConfig()
			if err != nil {
				return fmt.Errorf("no Bluesky credentials found for live posting: %w", err)
			}
			handle = cfg.Bluesky.Handle
			password = cfg.Bluesky.Password
		}

		// Create Bluesky client
		blueskyClient := bskyclient.New(handle, password)
		if err := blueskyClient.Authenticate(); err != nil {
			return fmt.Errorf("failed to authenticate with Bluesky for posting: %w", err)
		}

		// Convert posts to client format for posting with facets and embed cards
		clientPosts := make([]bskyclient.Post, len(topPosts))
		for i, post := range topPosts {
			clientPosts[i] = bskyclient.Post{
				URI:             post.URI,
				CID:             post.CID,
				Author:          post.Author,
				Likes:           post.Likes,
				Reposts:         post.Reposts,
				Replies:         post.Replies,
				Sentiment:       post.Sentiment,
				EngagementScore: post.EngagementScore,
			}
		}

		// Post to Bluesky with facets and embed cards
		err = blueskyClient.PostTrendingSummary(clientPosts, overallSentiment, analysisIntervalMinutes, len(deduplicatedPosts), positivePercent, negativePercent)
		if err != nil {
			return fmt.Errorf("failed to post to Bluesky: %w", err)
		}

		fmt.Printf("  ✅ Successfully posted to Bluesky: @%s\n", handle)
	} else {
		fmt.Println("  📝 Post content generated (not posted - test mode)")
	}

	// Update run state to processor step
	runState, err := m.stateManager.GetRun(ctx, runID, "orchestrator")
	if err != nil {
		return fmt.Errorf("failed to get run state: %w", err)
	}

	runState.Step = "processor"
	runState.Status = "completed"
	runState.UpdatedAt = time.Now()
	err = m.stateManager.UpdateRun(ctx, runState)
	if err != nil {
		return fmt.Errorf("failed to update run state: %w", err)
	}

	fmt.Println("  ✅ Processor completed successfully")
	return nil
}

// showResults displays the test results
func (m *MockLambdaClient) showResults(ctx context.Context, runID string) error {
	// Get run stats
	stats, err := m.stateManager.GetRunStats(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to get run stats: %w", err)
	}

	fmt.Printf("📊 Final Run Statistics:\n")
	fmt.Printf("  Status: %s\n", stats.Status)
	fmt.Printf("  Step: %s\n", stats.Step)
	fmt.Printf("  Analysis Interval: %d minutes\n", stats.AnalysisIntervalMinutes)
	fmt.Printf("  Time Range: %s to %s\n",
		stats.CutoffTime.Local().Format("2006-01-02 15:04:05"),
		stats.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Printf("  Total Posts Retrieved: %d\n", stats.TotalPostsRetrieved)
	fmt.Printf("  Actual Posts in DB: %d\n", stats.ActualPostsCount)
	fmt.Printf("  Overall Sentiment: %s\n", stats.OverallSentiment)
	fmt.Printf("  Top Posts Count: %d\n", stats.TopPostsCount)

	return nil
}

// Helper methods

func (m *MockLambdaClient) convertToStatePosts(posts []bskyclient.Post) []state.Post {
	statePosts := make([]state.Post, len(posts))
	for i, post := range posts {
		statePosts[i] = state.Post{
			URI:             post.URI, // Use the real URI from the client
			CID:             post.CID,
			Author:          post.Author,
			Text:            post.Text,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			CreatedAt:       post.CreatedAt,
			EngagementScore: float64(post.Likes + post.Reposts + post.Replies),
			Sentiment:       "neutral", // Will be analyzed later
		}
	}
	return statePosts
}

func (m *MockLambdaClient) calculateOverallSentimentWithPercentages(posts []state.Post) (string, float64, float64) {
	if len(posts) == 0 {
		return "neutral", 0, 0
	}

	positiveCount := 0
	negativeCount := 0
	neutralCount := 0

	for _, post := range posts {
		switch post.Sentiment {
		case "positive":
			positiveCount++
		case "negative":
			negativeCount++
		default:
			neutralCount++
		}
	}

	total := len(posts)
	positivePercent := float64(positiveCount) / float64(total) * 100
	negativePercent := float64(negativeCount) / float64(total) * 100

	if positiveCount > total/2 {
		return "positive", positivePercent, negativePercent
	} else if negativeCount > total/2 {
		return "negative", positivePercent, negativePercent
	}
	return "neutral", positivePercent, negativePercent
}

// fetchAllPostsInParallel fetches all posts using parallel API calls and internal loops
func (m *MockLambdaClient) fetchAllPostsInParallel(ctx context.Context, client *bskyclient.BlueskyClient, cutoffTime time.Time, runID string, liveMode bool) (int, error) {
	var totalPosts int
	currentCursor := ""
	iteration := 0
	maxIterations := 3 // Limit iterations for testing
	if liveMode {
		maxIterations = 20 // Allow more iterations in live mode
	}

	maxFetchTime := 5 * time.Minute // Maximum time to spend fetching
	startTime := time.Now()

	for {
		iteration++
		if iteration > maxIterations {
			fmt.Printf("    ⚠️ Reached max iterations (%d), stopping\n", maxIterations)
			break
		}

		// Check if we've exceeded maximum fetch time
		if time.Since(startTime) > maxFetchTime {
			fmt.Printf("    ⏰ Exceeded maximum fetch time (%v), stopping\n", maxFetchTime)
			break
		}

		fmt.Printf("    🔄 Starting iteration %d with cursor: %s\n", iteration, currentCursor)

		// Make 8 parallel API calls for this iteration
		posts, shouldStop, err := m.fetchBatchInParallel(ctx, client, currentCursor, cutoffTime)
		if err != nil {
			return totalPosts, fmt.Errorf("failed to fetch batch: %w", err)
		}

		if len(posts) == 0 {
			fmt.Printf("    📭 No posts retrieved in iteration %d, stopping\n", iteration)
			break
		}

		// Convert to state posts and store
		statePosts := m.convertToStatePosts(posts)
		fmt.Printf("    💾 Storing %d posts from iteration %d\n", len(statePosts), iteration)

		if err := m.stateManager.AddPosts(ctx, runID, statePosts); err != nil {
			return totalPosts, fmt.Errorf("failed to add posts: %w", err)
		}

		totalPosts += len(posts)
		fmt.Printf("    ✅ Iteration %d complete - Retrieved %d posts (Total: %d)\n", iteration, len(posts), totalPosts)

		// Check if we've reached posts before our time window
		if shouldStop {
			fmt.Printf("    ⏰ Found posts before time window, stopping at iteration %d\n", iteration)
			break
		}

		// Prepare for next iteration (800 posts ahead)
		currentCursor = fmt.Sprintf("%d", iteration*800)
		fmt.Printf("    ➡️ Preparing next iteration with cursor: %s\n", currentCursor)
	}

	fmt.Printf("    🏁 Parallel fetch complete - Total posts: %d across %d iterations\n", totalPosts, iteration)
	return totalPosts, nil
}

// fetchBatchInParallel makes 8 parallel API calls and returns combined results
func (m *MockLambdaClient) fetchBatchInParallel(ctx context.Context, client *bskyclient.BlueskyClient, startCursor string, cutoffTime time.Time) ([]bskyclient.Post, bool, error) {
	// Define cursors for 8 parallel calls (100 posts each = 800 total)
	cursors := []string{
		startCursor,
		addToCursor(startCursor, 100),
		addToCursor(startCursor, 200),
		addToCursor(startCursor, 300),
		addToCursor(startCursor, 400),
		addToCursor(startCursor, 500),
		addToCursor(startCursor, 600),
		addToCursor(startCursor, 700),
	}

	fmt.Printf("      🚀 Making 8 parallel API calls with cursors: %v\n", cursors)

	var allPosts []bskyclient.Post
	var oldestPostTime *time.Time
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Launch 8 goroutines for parallel fetching
	for i, cursor := range cursors {
		wg.Add(1)
		go func(cursorIndex int, cursorValue string) {
			defer wg.Done()

			fmt.Printf("        📡 Starting parallel call %d with cursor: %s\n", cursorIndex+1, cursorValue)

			posts, _, _, err := client.GetTrendingPostsBatch(ctx, cursorValue, cutoffTime)
			if err != nil {
				fmt.Printf("        ❌ Parallel call %d failed: %v\n", cursorIndex+1, err)
				return
			}

			fmt.Printf("        ✅ Parallel call %d completed - Retrieved %d posts\n", cursorIndex+1, len(posts))

			// Super debug: Print detailed info for each post
			if m.superDebugMode && len(posts) > 0 {
				fmt.Printf("        🔍 SUPER DEBUG - Parallel call %d posts:\n", cursorIndex+1)
				for i, post := range posts {
					postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
					if err == nil {
						// Calculate seconds before the end of the analysis period
						// Analysis period ends at now, so this shows how many seconds before now the post was created
						secondsBeforeEnd := time.Since(postTime).Seconds()
						isBeforeWindow := postTime.Before(cutoffTime)
						status := "✅ IN WINDOW"
						if isBeforeWindow {
							status = "❌ BEFORE WINDOW"
						}

						fmt.Printf("          %d. @%s | URI: %s | %.1fs before end of analysis period | %s\n",
							i+1, post.Author, post.URI, secondsBeforeEnd, status)
					}
				}
				fmt.Printf("        🔍 End super debug for parallel call %d\n", cursorIndex+1)
			}

			// Debug: Show first and last few timestamps to verify order
			if len(posts) > 0 {
				firstPost, _ := time.Parse(time.RFC3339, posts[0].CreatedAt)
				lastPost, _ := time.Parse(time.RFC3339, posts[len(posts)-1].CreatedAt)
				fmt.Printf("        🔍 Parallel call %d: First post: %d, Last post: %d (diff: %d seconds)\n",
					cursorIndex+1, firstPost.Unix(), lastPost.Unix(), firstPost.Unix()-lastPost.Unix())
			}

			// Find the oldest post in this batch to track the true boundary
			var localOldestTime *time.Time
			if len(posts) > 0 {
				// Find the oldest post in this batch (posts are sorted by most recent first)
				oldestPost := posts[len(posts)-1]
				postTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt)
				if err == nil {
					localOldestTime = &postTime

					// Convert to Unix timestamps for clean comparison
					postUnixTime := postTime.Unix()
					cutoffUnixTime := cutoffTime.Unix()

					fmt.Printf("        📅 Parallel call %d: Oldest post Unix: %d, Cutoff Unix: %d (diff: %d seconds)\n",
						cursorIndex+1, postUnixTime, cutoffUnixTime, postUnixTime-cutoffUnixTime)

					if postUnixTime < cutoffUnixTime {
						fmt.Printf("        ⏰ Parallel call %d: Found posts before cutoff time (oldest: %d < cutoff: %d)\n",
							cursorIndex+1, postUnixTime, cutoffUnixTime)
					}
				}
			}

			// Thread-safe accumulation and boundary tracking
			mu.Lock()
			allPosts = append(allPosts, posts...)

			// Track the oldest post time across all goroutines
			if localOldestTime != nil {
				if oldestPostTime == nil || localOldestTime.Before(*oldestPostTime) {
					oldestPostTime = localOldestTime
				}
			}
			mu.Unlock()
		}(i, cursor)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Determine if we should stop based on the oldest post across all goroutines
	shouldStop := false
	if oldestPostTime != nil && oldestPostTime.Before(cutoffTime) {
		shouldStop = true
		fmt.Printf("      ⏰ Found posts before cutoff time across all goroutines (oldest: %s < cutoff: %s)\n",
			oldestPostTime.Format("2006-01-02 15:04:05"), cutoffTime.Format("2006-01-02 15:04:05"))
	}

	fmt.Printf("      🎯 Parallel batch complete - Total posts: %d, Should stop: %t\n", len(allPosts), shouldStop)
	return allPosts, shouldStop, nil
}

// addToCursor adds a number to a cursor string (handles empty string case)
func addToCursor(cursor string, add int) string {
	if cursor == "" {
		return fmt.Sprintf("%d", add)
	}

	// Parse current cursor as number and add
	var current int
	if _, err := fmt.Sscanf(cursor, "%d", &current); err != nil {
		// If parsing fails, return the addition value
		return fmt.Sprintf("%d", add)
	}

	return fmt.Sprintf("%d", current+add)
}

// deduplicatePostsByURI removes duplicate posts by URI, keeping the one with highest engagement score
func (m *MockLambdaClient) deduplicatePostsByURI(posts []state.Post) []state.Post {
	uriToPost := make(map[string]state.Post)

	for _, post := range posts {
		// Skip posts with empty URIs
		if post.URI == "" {
			continue
		}

		// Calculate engagement score for this post
		currentEngagement := post.Likes + post.Reposts + post.Replies

		// Check if we've seen this URI before
		if existingPost, exists := uriToPost[post.URI]; exists {
			// Calculate engagement score for existing post
			existingEngagement := existingPost.Likes + existingPost.Reposts + existingPost.Replies

			// Keep the post with higher engagement score
			if currentEngagement > existingEngagement {
				uriToPost[post.URI] = post
				fmt.Printf("    🔍 Deduplication: Replacing post %s (engagement: %d) with better version (engagement: %d)\n",
					post.URI, existingEngagement, currentEngagement)
			}
		} else {
			// First time seeing this URI, add it
			uriToPost[post.URI] = post
		}
	}

	// Convert map values to slice
	var deduplicatedPosts []state.Post
	for _, post := range uriToPost {
		deduplicatedPosts = append(deduplicatedPosts, post)
	}

	fmt.Printf("    🔍 Deduplication removed %d duplicate posts\n", len(posts)-len(deduplicatedPosts))
	return deduplicatedPosts
}
