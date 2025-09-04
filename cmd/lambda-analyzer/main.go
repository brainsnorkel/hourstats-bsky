package main

import (
	"context"
	"fmt"
	"log"

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
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
	PostsAnalyzed int `json:"postsAnalyzed"`
}

// AnalyzerHandler handles the analyzer Lambda function
type AnalyzerHandler struct {
	stateManager    *state.StateManager
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
		stateManager:    stateManager,
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

	// Analyze posts
	analyzedPosts, overallSentiment, err := h.analyzePosts(runState.Posts)
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
		StatusCode: 200,
		Body:       "Posts analyzed successfully",
		PostsAnalyzed: len(analyzedPosts),
	}, nil
}

// analyzePosts analyzes sentiment and calculates engagement scores
func (h *AnalyzerHandler) analyzePosts(posts []state.Post) ([]state.Post, string, error) {
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
			URI:            analyzed.URI,
			Text:           analyzed.Text,
			Author:         analyzed.Author,
			Likes:          analyzed.Likes,
			Reposts:        analyzed.Reposts,
			Replies:        analyzed.Replies,
			Sentiment:      analyzed.Sentiment,
			EngagementScore: analyzed.EngagementScore,
			CreatedAt:      analyzed.CreatedAt,
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

func main() {
	ctx := context.Background()
	handler, err := NewAnalyzerHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create analyzer handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
