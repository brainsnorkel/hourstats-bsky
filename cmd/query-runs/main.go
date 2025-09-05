package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/formatter"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func main() {
	var (
		listRuns    = flag.Bool("list", false, "List all run IDs")
		runID       = flag.String("run", "", "Run ID to analyze")
		limit       = flag.Int("limit", 10, "Limit number of runs to list")
		showDetails = flag.Bool("details", false, "Show detailed run information")
	)
	flag.Parse()

	ctx := context.Background()

	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		log.Fatalf("Failed to create state manager: %v", err)
	}

	if *listRuns {
		listAllRuns(ctx, stateManager, *limit, *showDetails)
		return
	}

	if *runID == "" {
		fmt.Println("Usage:")
		fmt.Println("  List runs:    go run cmd/query-runs/main.go -list [-limit=10] [-details]")
		fmt.Println("  Analyze run:  go run cmd/query-runs/main.go -run <runID>")
		os.Exit(1)
	}

	analyzeRun(ctx, stateManager, *runID)
}

func listAllRuns(ctx context.Context, stateManager *state.StateManager, limit int, showDetails bool) {
	fmt.Printf("📋 Listing last %d runs:\n\n", limit)

	runIDs, err := stateManager.ListRuns(ctx, int32(limit))
	if err != nil {
		log.Fatalf("Failed to list runs: %v", err)
	}

	if len(runIDs) == 0 {
		fmt.Println("No runs found.")
		return
	}

	for i, runID := range runIDs {
		fmt.Printf("%d. %s", i+1, runID)

		if showDetails {
			stats, err := stateManager.GetRunStats(ctx, runID)
			if err != nil {
				fmt.Printf(" (error getting stats: %v)", err)
			} else {
				// Calculate duration and format times
				duration := stats.UpdatedAt.Sub(stats.CutoffTime)
				minutes := int(duration.Minutes())

				// Convert to local time
				startTime := stats.CutoffTime.Local()
				endTime := stats.UpdatedAt.Local()

				fmt.Printf(" - %s/%s, %d posts, %d min (%s to %s)",
					stats.Status, stats.Step, stats.ActualPostsCount, minutes,
					startTime.Format("2006-01-02 15:04:05"),
					endTime.Format("2006-01-02 15:04:05"))
			}
		}
		fmt.Println()
	}
}

func analyzeRun(ctx context.Context, stateManager *state.StateManager, runID string) {
	fmt.Printf("🔍 Analyzing run: %s\n\n", runID)

	// Get run stats
	stats, err := stateManager.GetRunStats(ctx, runID)
	if err != nil {
		log.Fatalf("Failed to get run stats: %v", err)
	}

	// Calculate duration and format times
	duration := stats.UpdatedAt.Sub(stats.CutoffTime)
	minutes := int(duration.Minutes())

	// Convert to local time
	startTime := stats.CutoffTime.Local()
	endTime := stats.UpdatedAt.Local()
	createdTime := stats.CreatedAt.Local()
	updatedTime := stats.UpdatedAt.Local()

	fmt.Printf("📊 Run Statistics:\n")
	fmt.Printf("  Status: %s\n", stats.Status)
	fmt.Printf("  Step: %s\n", stats.Step)
	fmt.Printf("  Analysis Interval: %d minutes\n", stats.AnalysisIntervalMinutes)
	fmt.Printf("  Time Range: %s to %s (%d minutes)\n",
		startTime.Format("2006-01-02 15:04:05"),
		endTime.Format("2006-01-02 15:04:05"),
		minutes)
	fmt.Printf("  Cutoff Time (UTC): %s\n", stats.CutoffTime.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("  Total Posts Retrieved: %d\n", stats.TotalPostsRetrieved)
	fmt.Printf("  Actual Posts in DB: %d\n", stats.ActualPostsCount)
	fmt.Printf("  Created: %s\n", createdTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Updated: %s\n", updatedTime.Format("2006-01-02 15:04:05"))
	if stats.OverallSentiment != "" {
		fmt.Printf("  Overall Sentiment: %s\n", stats.OverallSentiment)
	}
	fmt.Printf("  Top Posts Count: %d\n", stats.TopPostsCount)
	fmt.Println()

	// Get all posts for this run
	posts, err := stateManager.GetAllPosts(ctx, runID)
	if err != nil {
		log.Fatalf("Failed to get posts: %v", err)
	}

	if len(posts) == 0 {
		fmt.Println("❌ No posts found for this run.")
		return
	}

	fmt.Printf("📝 Found %d posts in DynamoDB\n\n", len(posts))

	// Filter posts by cutoff time (same logic as processor)
	filteredPosts := filterPostsByCutoffTime(posts, stats.CutoffTime)
	fmt.Printf("⏰ After time filtering: %d posts (from %d original)\n\n", len(filteredPosts), len(posts))

	if len(filteredPosts) == 0 {
		fmt.Println("❌ No posts found within the analysis time period.")
		return
	}

	// Analyze posts (same logic as processor)
	fmt.Println("🧠 Analyzing posts...")
	analyzedPosts, overallSentiment, positivePercent, negativePercent, err := analyzePosts(filteredPosts)
	if err != nil {
		log.Fatalf("Failed to analyze posts: %v", err)
	}

	// Get top posts
	topPosts := getTopPosts(analyzedPosts, 5)

	fmt.Printf("📈 Analysis Results:\n")
	fmt.Printf("  Overall Sentiment: %s\n", overallSentiment)
	fmt.Printf("  Posts Analyzed: %d\n", len(analyzedPosts))
	fmt.Printf("  Top Posts Selected: %d\n\n", len(topPosts))

	// Generate and display the post that would be created
	fmt.Println("📄 Generated Post (what would be posted to Bluesky):")
	fmt.Println(strings.Repeat("=", 60))

	// Convert state posts to formatter posts
	formatterPosts := make([]formatter.Post, len(topPosts))
	for i, post := range topPosts {
		formatterPosts[i] = formatter.Post{
			Author:          post.Author,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			Sentiment:       post.Sentiment,
			EngagementScore: post.EngagementScore,
		}
	}

	postContent := formatter.FormatPostContent(formatterPosts, overallSentiment, stats.AnalysisIntervalMinutes, len(filteredPosts), positivePercent, negativePercent)
	fmt.Println(postContent)
	fmt.Println(strings.Repeat("=", 60))

	// Display character count information
	characterCount := len(postContent)
	blueskyLimit := 300
	remainingChars := blueskyLimit - characterCount

	fmt.Printf("\n📊 Post Statistics:\n")
	fmt.Printf("  Character Count: %d characters\n", characterCount)
	fmt.Printf("  Bluesky Limit: %d characters\n", blueskyLimit)
	fmt.Printf("  Remaining: %d characters\n", remainingChars)

	if remainingChars < 0 {
		fmt.Printf("  ⚠️  WARNING: Post exceeds Bluesky limit by %d characters!\n", -remainingChars)
	} else if remainingChars < 50 {
		fmt.Printf("  ⚠️  WARNING: Post is close to Bluesky limit (%d characters remaining)\n", remainingChars)
	} else {
		fmt.Printf("  ✅ Post is within Bluesky limits\n")
	}
}

func filterPostsByCutoffTime(posts []state.Post, cutoffTime time.Time) []state.Post {
	var filteredPosts []state.Post

	for _, post := range posts {
		postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
		if err != nil {
			continue // Skip posts with invalid timestamps
		}

		if !postTime.Before(cutoffTime) {
			filteredPosts = append(filteredPosts, post)
		}
	}

	return filteredPosts
}

func analyzePosts(posts []state.Post) ([]state.Post, string, float64, float64, error) {
	// Convert state posts to analyzer posts
	analyzerPosts := make([]analyzer.Post, len(posts))
	for i, post := range posts {
		analyzerPosts[i] = analyzer.Post{
			URI:       post.URI,
			Text:      post.Text,
			Author:    post.Author,
			Likes:     post.Likes,
			Reposts:   post.Reposts,
			Replies:   post.Replies,
			CreatedAt: post.CreatedAt,
		}
	}

	// Analyze posts
	sentimentAnalyzer := analyzer.New()
	analyzedPosts, err := sentimentAnalyzer.AnalyzePosts(analyzerPosts)
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("failed to analyze posts: %w", err)
	}

	// Calculate overall sentiment and percentages
	overallSentiment, positivePercent, negativePercent := calculateOverallSentimentWithPercentages(analyzedPosts)

	// Convert back to state posts with analysis results
	statePosts := make([]state.Post, len(analyzedPosts))
	for i, analyzed := range analyzedPosts {
		statePosts[i] = state.Post{
			URI:             analyzed.URI,
			Text:            analyzed.Text,
			Author:          analyzed.Author,
			Likes:           analyzed.Likes,
			Reposts:         analyzed.Reposts,
			Replies:         analyzed.Replies,
			Sentiment:       analyzed.Sentiment,
			EngagementScore: analyzed.EngagementScore,
			CreatedAt:       analyzed.CreatedAt,
		}
	}

	return statePosts, overallSentiment, positivePercent, negativePercent, nil
}

func calculateOverallSentiment(posts []analyzer.AnalyzedPost) string {
	positiveCount := 0
	negativeCount := 0
	neutralCount := 0

	for _, post := range posts {
		switch post.Sentiment {
		case "positive":
			positiveCount++
		case "negative":
			negativeCount++
		case "neutral":
			neutralCount++
		}
	}

	total := len(posts)
	if total == 0 {
		return "neutral"
	}

	positivePercent := float64(positiveCount) / float64(total)
	negativePercent := float64(negativeCount) / float64(total)
	neutralPercent := float64(neutralCount) / float64(total)

	// Determine dominant sentiment
	if positivePercent > negativePercent && positivePercent > neutralPercent {
		return "positive"
	} else if negativePercent > positivePercent && negativePercent > neutralPercent {
		return "negative"
	}
	return "neutral"
}

func calculateOverallSentimentWithPercentages(posts []analyzer.AnalyzedPost) (string, float64, float64) {
	positiveCount := 0
	negativeCount := 0
	neutralCount := 0

	for _, post := range posts {
		switch post.Sentiment {
		case "positive":
			positiveCount++
		case "negative":
			negativeCount++
		case "neutral":
			neutralCount++
		}
	}

	total := len(posts)
	if total == 0 {
		return "neutral", 0, 0
	}

	positivePercent := float64(positiveCount) / float64(total) * 100
	negativePercent := float64(negativeCount) / float64(total) * 100
	neutralPercent := float64(neutralCount) / float64(total) * 100

	// Determine dominant sentiment
	if positivePercent > negativePercent && positivePercent > neutralPercent {
		return "positive", positivePercent, negativePercent
	} else if negativePercent > positivePercent && negativePercent > neutralPercent {
		return "negative", positivePercent, negativePercent
	}
	return "neutral", positivePercent, negativePercent
}

func getTopPosts(posts []state.Post, n int) []state.Post {
	if len(posts) <= n {
		return posts
	}

	// Sort by engagement score (descending)
	for i := 0; i < len(posts)-1; i++ {
		for j := i + 1; j < len(posts); j++ {
			if posts[i].EngagementScore < posts[j].EngagementScore {
				posts[i], posts[j] = posts[j], posts[i]
			}
		}
	}

	return posts[:n]
}
