package main

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/aws/aws-lambda-go/lambda"
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
	TopPostsCount int `json:"topPostsCount"`
}

// AggregatorHandler handles the aggregator Lambda function
type AggregatorHandler struct {
	stateManager *state.StateManager
}

// NewAggregatorHandler creates a new aggregator handler
func NewAggregatorHandler(ctx context.Context) (*AggregatorHandler, error) {
	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	return &AggregatorHandler{
		stateManager: stateManager,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *AggregatorHandler) HandleRequest(ctx context.Context, event StepFunctionsEvent) (Response, error) {
	log.Printf("Aggregator received event: %+v", event)

	// Get current run state
	runState, err := h.stateManager.GetLatestRun(ctx, event.RunID)
	if err != nil {
		log.Printf("Failed to get run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get run state: " + err.Error(),
		}, err
	}

	// Get top 5 posts by engagement score
	topPosts := h.getTopPosts(runState.Posts, 5)

	// Update state with top posts
	runState.TopPosts = topPosts
	runState.Step = "aggregator"
	runState.Status = "aggregated"

	if err := h.stateManager.UpdateRun(ctx, runState); err != nil {
		log.Printf("Failed to update run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to update state: " + err.Error(),
		}, err
	}

	log.Printf("Successfully aggregated top %d posts for run: %s", len(topPosts), event.RunID)
	return Response{
		StatusCode: 200,
		Body:       "Posts aggregated successfully",
		TopPostsCount: len(topPosts),
	}, nil
}

// getTopPosts returns the top N posts by engagement score
func (h *AggregatorHandler) getTopPosts(posts []state.Post, count int) []state.Post {
	// Sort posts by engagement score (descending)
	sort.Slice(posts, func(i, j int) bool {
		return posts[i].EngagementScore > posts[j].EngagementScore
	})

	// Return top N posts
	if len(posts) < count {
		count = len(posts)
	}

	return posts[:count]
}

func main() {
	ctx := context.Background()
	handler, err := NewAggregatorHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create aggregator handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
