package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
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
	StatusCode    int    `json:"statusCode"`
	Body          string `json:"body"`
	PostsAnalyzed int    `json:"postsAnalyzed"`
}

// AnalyzerHandler handles the analyzer Lambda function
type AnalyzerHandler struct {
	stateManager      *state.StateManager
	sentimentAnalyzer *analyzer.SentimentAnalyzer
}

// NewAnalyzerHandler creates a new analyzer handler
func NewAnalyzerHandler(ctx context.Context) (*AnalyzerHandler, error) {
	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	// Initialize sentiment analyzer
	sentimentAnalyzer := analyzer.New()

	return &AnalyzerHandler{
		stateManager:      stateManager,
		sentimentAnalyzer: sentimentAnalyzer,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *AnalyzerHandler) HandleRequest(ctx context.Context, event StepFunctionsEvent) (Response, error) {
	log.Printf("Analyzer received event: %+v", event)

	// Get current run state
	runState, err := h.stateManager.GetLatestRun(ctx, event.RunID)
	if err != nil {
		log.Printf("Failed to get run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get run state: " + err.Error(),
		}, err
	}

	// Log the time range being used for analysis
	log.Printf("📅 ANALYZER: Analyzing posts from time range - From: %s, To: %s (current time: %s)", 
		runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"), 
		time.Now().Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"))
	
	// Filter posts by cutoff time and analyze
	log.Printf("🔍 ANALYZER DEBUG: Retrieved %d posts from DynamoDB for run %s", len(runState.Posts), event.RunID)
	log.Printf("🔍 ANALYZER DEBUG: Using cutoff time from DynamoDB: %s", runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"))
	filteredPosts := h.filterPostsByCutoffTime(runState.Posts, runState.CutoffTime)
	log.Printf("🔍 ANALYZER DEBUG: After time filtering: %d posts (from %d original)", len(filteredPosts), len(runState.Posts))
	analyzedPosts, overallSentiment, err := h.analyzePosts(filteredPosts)
	if err != nil {
		log.Printf("Failed to analyze posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to analyze posts: " + err.Error(),
		}, err
	}

	// Update state with analyzed posts
	runState.Posts = analyzedPosts
	runState.OverallSentiment = overallSentiment
	runState.Step = "analyzer"
	runState.Status = "analyzed"

	if err := h.stateManager.UpdateRun(ctx, runState); err != nil {
		log.Printf("Failed to update run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to update state: " + err.Error(),
		}, err
	}

	log.Printf("Successfully analyzed %d posts for run: %s", len(analyzedPosts), event.RunID)
	return Response{
		StatusCode:    200,
		Body:          "Posts analyzed successfully",
		PostsAnalyzed: len(analyzedPosts),
	}, nil
}

// analyzePosts analyzes sentiment and calculates engagement scores
func (h *AnalyzerHandler) analyzePosts(posts []state.Post) ([]state.Post, string, error) {
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
	}

	return statePosts, overallSentiment, nil
}

// calculateOverallSentiment calculates the overall sentiment from analyzed posts
func (h *AnalyzerHandler) calculateOverallSentiment(posts []analyzer.AnalyzedPost) string {
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

// filterPostsByCutoffTime filters posts to only include those after the cutoff time
func (h *AnalyzerHandler) filterPostsByCutoffTime(posts []state.Post, cutoffTime time.Time) []state.Post {
	var filteredPosts []state.Post
	for _, post := range posts {
		postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
		if err != nil {
			log.Printf("Warning: Skipping post with invalid timestamp: %s", post.URI)
			continue
		}

		// Only include posts after the cutoff time
		if postTime.After(cutoffTime) {
			filteredPosts = append(filteredPosts, post)
		}
	}

	log.Printf("Filtered posts by cutoff time: %d original -> %d after cutoff", len(posts), len(filteredPosts))
	return filteredPosts
}

func main() {
	ctx := context.Background()
	handler, err := NewAnalyzerHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create analyzer handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
