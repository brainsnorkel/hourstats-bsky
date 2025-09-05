package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
	lambdapkg "github.com/christophergentle/hourstats-bsky/internal/lambda"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// StepFunctionsEvent represents the event from Step Functions
type StepFunctionsEvent struct {
	RunID                   string `json:"runId"`
	AnalysisIntervalMinutes int    `json:"analysisIntervalMinutes"`
	Status                  string `json:"status"`
}

// Response represents the Lambda response
type Response struct {
	StatusCode     int    `json:"statusCode"`
	Body           string `json:"body"`
	PostsAnalyzed  int    `json:"postsAnalyzed,omitempty"`
	TopPostsCount  int    `json:"topPostsCount,omitempty"`
	OverallSentiment string `json:"overallSentiment,omitempty"`
}

// ProcessorHandler handles the combined analysis, aggregation, and posting
type ProcessorHandler struct {
	stateManager    *state.StateManager
	sentimentAnalyzer *analyzer.SentimentAnalyzer
	blueskyClient   *client.BlueskyClient
	config          *config.Config
}

// NewProcessorHandler creates a new processor handler
func NewProcessorHandler(ctx context.Context) (*ProcessorHandler, error) {
	// Load configuration
	configLoader, err := lambdapkg.NewSSMConfigLoader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSM config loader: %w", err)
	}

	cfg, err := configLoader.LoadConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	// Initialize sentiment analyzer
	sentimentAnalyzer := analyzer.New()

	// Initialize Bluesky client
	blueskyClient := client.New(cfg.Bluesky.Handle, cfg.Bluesky.Password)

	return &ProcessorHandler{
		stateManager:      stateManager,
		sentimentAnalyzer: sentimentAnalyzer,
		blueskyClient:     blueskyClient,
		config:            cfg,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *ProcessorHandler) HandleRequest(ctx context.Context, event StepFunctionsEvent) (Response, error) {
	log.Printf("Processor received event: %+v", event)

	// Get current run state - look for fetcher step which has the collected posts
	runState, err := h.stateManager.GetRun(ctx, event.RunID, "fetcher")
	if err != nil {
		log.Printf("Failed to get fetcher run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get run state: " + err.Error(),
		}, err
	}

	// Log the time range being used for processing
	log.Printf("📅 PROCESSOR: Processing posts from time range - From: %s, To: %s (current time: %s)",
		runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"))

	// Retrieve all posts for this run
	allPosts, err := h.stateManager.GetAllPosts(ctx, event.RunID)
	if err != nil {
		log.Printf("Failed to get all posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get posts: " + err.Error(),
		}, err
	}

	log.Printf("🔍 PROCESSOR DEBUG: Retrieved %d posts from DynamoDB for run %s", len(allPosts), event.RunID)
	log.Printf("🔍 PROCESSOR DEBUG: Using cutoff time from DynamoDB: %s", runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"))

	// Filter posts by cutoff time
	filteredPosts := h.filterPostsByCutoffTime(allPosts, runState.CutoffTime)
	log.Printf("🔍 PROCESSOR DEBUG: After time filtering: %d posts (from %d original)", len(filteredPosts), len(allPosts))

	if len(filteredPosts) == 0 {
		log.Printf("No posts found for the time period, skipping analysis")
		return Response{
			StatusCode: 200,
			Body:       "No posts to analyze",
		}, nil
	}

	// Step 1: Analyze posts for sentiment and calculate engagement scores
	log.Printf("Analyzing %d posts", len(filteredPosts))
	analyzedPosts, overallSentiment, err := h.analyzePosts(filteredPosts)
	if err != nil {
		log.Printf("Failed to analyze posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to analyze posts: " + err.Error(),
		}, err
	}

	// Step 2: Get top posts by engagement score
	log.Printf("Aggregating %d posts after analysis", len(analyzedPosts))
	topPosts := h.getTopPosts(analyzedPosts, 5)

	// Debug logging for top posts
	log.Printf("🔍 PROCESSOR DEBUG: Top 5 posts selected:")
	for i, post := range topPosts {
		log.Printf("🔍 PROCESSOR DEBUG: Top %d - Author: %s, Sentiment: %s, EngagementScore: %.2f, Likes: %d, Reposts: %d, Replies: %d",
			i+1, post.Author, post.Sentiment, post.EngagementScore, post.Likes, post.Reposts, post.Replies)
	}

	// Step 3: Post summary to Bluesky
	log.Printf("Posting summary to Bluesky")
	err = h.postSummary(ctx, runState, topPosts, overallSentiment)
	if err != nil {
		log.Printf("Failed to post summary: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to post summary: " + err.Error(),
		}, err
	}

	log.Printf("Successfully processed %d posts and posted summary for run: %s", len(analyzedPosts), event.RunID)
	return Response{
		StatusCode:     200,
		Body:           "Posts processed and summary posted successfully",
		PostsAnalyzed:  len(analyzedPosts),
		TopPostsCount:  len(topPosts),
		OverallSentiment: overallSentiment,
	}, nil
}

// analyzePosts analyzes sentiment and calculates engagement scores
func (h *ProcessorHandler) analyzePosts(posts []state.Post) ([]state.Post, string, error) {
	log.Printf("Analyzing %d posts", len(posts))

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
	analyzedPosts, err := h.sentimentAnalyzer.AnalyzePosts(analyzerPosts)
	if err != nil {
		return nil, "", fmt.Errorf("failed to analyze posts: %w", err)
	}

	// Calculate overall sentiment
	overallSentiment := h.calculateOverallSentiment(analyzedPosts)

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
		
		// Debug logging for first few posts
		if i < 5 {
			log.Printf("🔍 PROCESSOR DEBUG: Post %d - Author: %s, Likes: %d, Reposts: %d, Replies: %d, Sentiment: %s, EngagementScore: %.2f",
				i+1, analyzed.Author, analyzed.Likes, analyzed.Reposts, analyzed.Replies, analyzed.Sentiment, analyzed.EngagementScore)
		}
	}

	return statePosts, overallSentiment, nil
}

// calculateOverallSentiment calculates the overall sentiment from analyzed posts
func (h *ProcessorHandler) calculateOverallSentiment(posts []analyzer.AnalyzedPost) string {
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

	log.Printf("🔍 PROCESSOR DEBUG: Sentiment counts - Positive: %d (%.1f%%), Negative: %d (%.1f%%), Neutral: %d (%.1f%%)",
		positiveCount, positivePercent*100, negativeCount, negativePercent*100, neutralCount, neutralPercent*100)

	// Determine dominant sentiment
	if positivePercent > negativePercent && positivePercent > neutralPercent {
		log.Printf("🔍 PROCESSOR DEBUG: Overall sentiment determined as: positive")
		return "positive"
	} else if negativePercent > positivePercent && negativePercent > neutralPercent {
		log.Printf("🔍 PROCESSOR DEBUG: Overall sentiment determined as: negative")
		return "negative"
	}
	log.Printf("🔍 PROCESSOR DEBUG: Overall sentiment determined as: neutral")
	return "neutral"
}

// getTopPosts gets the top N posts by engagement score
func (h *ProcessorHandler) getTopPosts(posts []state.Post, n int) []state.Post {
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

// filterPostsByCutoffTime filters posts to only include those after the cutoff time
func (h *ProcessorHandler) filterPostsByCutoffTime(posts []state.Post, cutoffTime time.Time) []state.Post {
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

// postSummary posts the summary to Bluesky
func (h *ProcessorHandler) postSummary(ctx context.Context, runState *state.RunState, topPosts []state.Post, overallSentiment string) error {
	// Check if we have data to post
	if runState.TotalPostsRetrieved == 0 {
		log.Printf("No posts retrieved, skipping post")
		return nil
	}

	if len(topPosts) == 0 {
		log.Printf("No top posts to display, skipping post")
		return nil
	}

	if overallSentiment == "" {
		log.Printf("No sentiment analysis completed, skipping post")
		return nil
	}

	// Convert state posts to client posts
	clientPosts := make([]client.Post, len(topPosts))
	for i, post := range topPosts {
		clientPosts[i] = client.Post{
			URI:             post.URI,
			Text:            post.Text,
			Author:          post.Author,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			CreatedAt:       post.CreatedAt,
			Sentiment:       post.Sentiment,
			EngagementScore: post.EngagementScore,
		}
	}

	// Post the summary
	return h.blueskyClient.PostTrendingSummary(clientPosts, overallSentiment, runState.AnalysisIntervalMinutes)
}

func main() {
	ctx := context.Background()
	handler, err := NewProcessorHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create processor handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
