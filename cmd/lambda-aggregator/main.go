package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

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
	StatusCode    int    `json:"statusCode"`
	Body          string `json:"body"`
	TopPostsCount int    `json:"topPostsCount"`
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

	// Log the time range being used for aggregation
	log.Printf("📅 AGGREGATOR: Aggregating posts from time range - From: %s, To: %s (current time: %s)", 
		runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"), 
		time.Now().Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"))
	
	// Filter posts by cutoff time and aggregate
	log.Printf("🔍 AGGREGATOR DEBUG: Retrieved %d posts from DynamoDB for run %s", len(runState.Posts), event.RunID)
	log.Printf("🔍 AGGREGATOR DEBUG: Using cutoff time from DynamoDB: %s", runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"))
	filteredPosts := h.filterPostsByCutoffTime(runState.Posts, runState.CutoffTime)
	log.Printf("🔍 AGGREGATOR DEBUG: After time filtering: %d posts (from %d original)", len(filteredPosts), len(runState.Posts))
	log.Printf("Aggregating %d posts after cutoff filtering", len(filteredPosts))
	topPosts := h.getTopPosts(filteredPosts, 5)

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
		StatusCode:    200,
		Body:          "Posts aggregated successfully",
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

// filterPostsByCutoffTime filters posts to only include those after the cutoff time
func (h *AggregatorHandler) filterPostsByCutoffTime(posts []state.Post, cutoffTime time.Time) []state.Post {
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
	handler, err := NewAggregatorHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create aggregator handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
