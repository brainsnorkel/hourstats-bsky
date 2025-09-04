package main

import (
	"fmt"
	"time"

	"github.com/christophergentle/trendjournal/internal/analyzer"
	"github.com/christophergentle/trendjournal/internal/client"
	"github.com/christophergentle/trendjournal/internal/scheduler"
)

func main() {
	// Create sample posts for testing
	samplePosts := []client.Post{
		{
			URI:       "https://bsky.app/profile/user1.bsky.social/post/123",
			Text:      "I love this new feature! It's amazing!",
			Author:    "user1.bsky.social",
			Likes:     150,
			Reposts:   25,
			Replies:   10,
			CreatedAt: "2024-01-01T12:00:00Z",
		},
		{
			URI:       "https://bsky.app/profile/user2.bsky.social/post/456",
			Text:      "This is terrible. I hate it so much.",
			Author:    "user2.bsky.social",
			Likes:     5,
			Reposts:   2,
			Replies:   15,
			CreatedAt: "2024-01-01T12:05:00Z",
		},
		{
			URI:       "https://bsky.app/profile/user3.bsky.social/post/789",
			Text:      "The weather is okay today.",
			Author:    "user3.bsky.social",
			Likes:     30,
			Reposts:   8,
			Replies:   5,
			CreatedAt: "2024-01-01T12:10:00Z",
		},
		{
			URI:       "https://bsky.app/profile/user4.bsky.social/post/101",
			Text:      "Great #tech news about #ai development!",
			Author:    "user4.bsky.social",
			Likes:     200,
			Reposts:   50,
			Replies:   20,
			CreatedAt: "2024-01-01T12:15:00Z",
		},
		{
			URI:       "https://bsky.app/profile/user5.bsky.social/post/202",
			Text:      "Check out this #music #art piece!",
			Author:    "user5.bsky.social",
			Likes:     80,
			Reposts:   15,
			Replies:   12,
			CreatedAt: "2024-01-01T12:20:00Z",
		},
	}

	// Create analyzer and scheduler
	analyzer := analyzer.New()
	scheduler := &scheduler.Scheduler{}

	// Convert to analyzer posts
	analyzerPosts := make([]analyzer.Post, len(samplePosts))
	for i, post := range samplePosts {
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
	analyzedPosts, err := analyzer.AnalyzePosts(analyzerPosts)
	if err != nil {
		fmt.Printf("Error analyzing posts: %v\n", err)
		return
	}

	// Get top 5 posts
	topPosts := scheduler.GetTopPosts(analyzedPosts, 5)

	// Calculate overall sentiment
	overallSentiment := scheduler.CalculateOverallSentiment(topPosts)

	// Generate the post content
	now := time.Now()
	timeStr := now.Format("2006-01-02 15:04")

	summaryText := fmt.Sprintf("Top five this hour %s\n\n", timeStr)

	for i, post := range topPosts {
		summaryText += fmt.Sprintf("%d. %s\n", i+1, post.URI)
		summaryText += fmt.Sprintf("   @%s | 💙 %d likes | 🔄 %d reposts\n\n", post.Author, post.Likes, post.Reposts)
	}

	summaryText += fmt.Sprintf("Bluesky is %s", overallSentiment)

	fmt.Println("Generated Post Format:")
	fmt.Println("====================")
	fmt.Println(summaryText)
	fmt.Println("\nAnalysis Details:")
	fmt.Println("=================")
	for i, post := range topPosts {
		fmt.Printf("%d. @%s - Sentiment: %s (%.2f), Topics: %v\n",
			i+1, post.Author, post.Sentiment, post.SentimentScore, post.Topics)
	}
}
