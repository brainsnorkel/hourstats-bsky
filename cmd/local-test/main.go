package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
	"github.com/christophergentle/hourstats-bsky/internal/formatter"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// MockLambdaClient simulates Lambda invocations locally
type MockLambdaClient struct {
	stateManager *state.StateManager
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
		fmt.Println("Usage: go run cmd/local-test/main.go <test-interval-minutes> [live]")
		fmt.Println("Example: go run cmd/local-test/main.go 5")
		fmt.Println("Example: go run cmd/local-test/main.go 60 live")
		fmt.Println("")
		fmt.Println("Options:")
		fmt.Println("  <test-interval-minutes>  Number of minutes to analyze (1-60)")
		fmt.Println("  live                     Run in live mode: process all posts and post to Bluesky")
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

	// Check for live mode
	liveMode := len(os.Args) > 2 && os.Args[2] == "live"

	if liveMode {
		fmt.Printf("🚀 Starting LIVE test with %d minute interval...\n", testIntervalMinutes)
		fmt.Println("⚠️  WARNING: This will post to Bluesky!")
		fmt.Println("")
	} else {
		fmt.Printf("🧪 Starting local test with %d minute interval...\n\n", testIntervalMinutes)
	}

	ctx := context.Background()

	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		log.Fatalf("Failed to create state manager: %v", err)
	}

	// Create mock lambda client
	mockClient := &MockLambdaClient{stateManager: stateManager}

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
	err = mockClient.runFetcherChain(ctx, runID, testIntervalMinutes, liveMode)
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
	cutoffTime := now.Add(-time.Duration(analysisIntervalMinutes) * time.Minute)

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

// runFetcherChain simulates the fetcher chain execution
func (m *MockLambdaClient) runFetcherChain(ctx context.Context, runID string, analysisIntervalMinutes int, liveMode bool) error {
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
	blueskyClient := client.New(handle, password)
	if err := blueskyClient.Authenticate(); err != nil {
		return fmt.Errorf("failed to authenticate with Bluesky: %w", err)
	}

	// Get run state
	runState, err := m.stateManager.GetRun(ctx, runID, "orchestrator")
	if err != nil {
		return fmt.Errorf("failed to get run state: %w", err)
	}

	cursor := ""
	fetchCount := 0
	maxFetches := 3 // Limit fetches for testing
	if liveMode {
		maxFetches = 100 // Allow more fetches in live mode
	}

	for fetchCount < maxFetches {
		fetchCount++
		fmt.Printf("  🔄 Fetch batch %d (cursor: %s)...\n", fetchCount, cursor)

		// Fetch posts
		posts, nextCursor, hasMorePosts, err := blueskyClient.GetTrendingPostsBatch(ctx, cursor, runState.CutoffTime)
		if err != nil {
			return fmt.Errorf("failed to fetch posts: %w", err)
		}

		fmt.Printf("    📥 Retrieved %d posts\n", len(posts))

		if len(posts) == 0 {
			fmt.Println("    🛑 No posts retrieved, stopping fetch")
			break
		}

		// Convert to state posts
		statePosts := m.convertToStatePosts(posts)

		// Add posts to state
		if err := m.stateManager.AddPosts(ctx, runID, statePosts); err != nil {
			return fmt.Errorf("failed to add posts: %w", err)
		}

		// Update cursor
		if err := m.stateManager.UpdateCursor(ctx, runID, nextCursor, hasMorePosts); err != nil {
			return fmt.Errorf("failed to update cursor: %w", err)
		}

		// Check if we should continue (simulate the completion logic)
		shouldContinue := m.shouldContinueFetching(posts, runState.CutoffTime)
		if !shouldContinue || !hasMorePosts {
			fmt.Printf("    🛑 Stopping fetch (shouldContinue: %t, hasMore: %t)\n", shouldContinue, hasMorePosts)
			break
		}

		cursor = nextCursor
	}

	// Update run state to completed
	runState.Status = "completed"
	runState.UpdatedAt = time.Now()
	err = m.stateManager.UpdateRun(ctx, runState)
	if err != nil {
		return fmt.Errorf("failed to update run state: %w", err)
	}

	fmt.Printf("  ✅ Fetcher chain completed, %d batches fetched\n", fetchCount)
	return nil
}

// runProcessor simulates the processor execution
func (m *MockLambdaClient) runProcessor(ctx context.Context, runID string, analysisIntervalMinutes int, liveMode bool) error {
	// Get all posts for this run
	allPosts, err := m.stateManager.GetAllPosts(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to get all posts: %w", err)
	}

	fmt.Printf("  📊 Processing %d posts...\n", len(allPosts))

	if len(allPosts) == 0 {
		fmt.Println("  ❌ No posts to process")
		return nil
	}

	// Analyze sentiment for all posts
	sentimentAnalyzer := analyzer.New()
	for i := range allPosts {
		// Convert to analyzer.Post format
		analyzerPost := analyzer.Post{
			Text:    allPosts[i].Text,
			Likes:   allPosts[i].Likes,
			Replies: allPosts[i].Replies,
			Reposts: allPosts[i].Reposts,
		}

		analyzedPosts, err := sentimentAnalyzer.AnalyzePosts([]analyzer.Post{analyzerPost})
		if err != nil {
			fmt.Printf("    ⚠️ Failed to analyze sentiment for post %d: %v\n", i+1, err)
			allPosts[i].Sentiment = "neutral"
		} else if len(analyzedPosts) > 0 {
			allPosts[i].Sentiment = analyzedPosts[0].Sentiment
		} else {
			allPosts[i].Sentiment = "neutral"
		}
	}

	// Calculate engagement scores
	for i := range allPosts {
		allPosts[i].EngagementScore = float64(allPosts[i].Likes + allPosts[i].Reposts + allPosts[i].Replies)
	}

	// Sort by engagement score (simple bubble sort for testing)
	for i := 0; i < len(allPosts)-1; i++ {
		for j := 0; j < len(allPosts)-i-1; j++ {
			if allPosts[j].EngagementScore < allPosts[j+1].EngagementScore {
				allPosts[j], allPosts[j+1] = allPosts[j+1], allPosts[j]
			}
		}
	}

	// Get top 5 posts
	topPosts := allPosts
	if len(allPosts) > 5 {
		topPosts = allPosts[:5]
	}

	// Calculate overall sentiment and percentages
	overallSentiment, positivePercent, negativePercent := m.calculateOverallSentimentWithPercentages(allPosts)

	// Convert to formatter posts
	formatterPosts := make([]formatter.Post, len(topPosts))
	for i, post := range topPosts {
		formatterPosts[i] = formatter.Post{
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
		blueskyClient := client.New(handle, password)
		if err := blueskyClient.Authenticate(); err != nil {
			return fmt.Errorf("failed to authenticate with Bluesky for posting: %w", err)
		}

		// Post to Bluesky
		err = blueskyClient.PostText(ctx, postContent)
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

func (m *MockLambdaClient) convertToStatePosts(posts []client.Post) []state.Post {
	statePosts := make([]state.Post, len(posts))
	for i, post := range posts {
		statePosts[i] = state.Post{
			URI:             fmt.Sprintf("at://post-%d", i), // Generate a simple URI
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

func (m *MockLambdaClient) shouldContinueFetching(posts []client.Post, cutoffTime time.Time) bool {
	if len(posts) == 0 {
		return false
	}

	// Check if any post is older than cutoff time
	for _, post := range posts {
		postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
		if err != nil {
			continue
		}
		if postTime.Before(cutoffTime) {
			return false
		}
	}

	return true
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
