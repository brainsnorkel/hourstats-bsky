package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/client"
)

// StepFunctionsEvent represents the event from Step Functions
type StepFunctionsEvent struct {
	RunID                   string `json:"runId"`
	AnalysisIntervalMinutes int    `json:"analysisIntervalMinutes"`
	Status                  string `json:"status"`
	BatchID                 string `json:"batchId,omitempty"`
}

// Response represents the Lambda response
type Response struct {
	StatusCode    int    `json:"statusCode"`
	Body          string `json:"body"`
	HasMorePosts  bool   `json:"hasMorePosts"`
	PostsRetrieved int   `json:"postsRetrieved"`
	NextCursor    string `json:"nextCursor,omitempty"`
}

// FetcherHandler handles the fetcher Lambda function
type FetcherHandler struct {
	stateManager *state.StateManager
	ssmClient    *ssm.Client
}

// NewFetcherHandler creates a new fetcher handler
func NewFetcherHandler(ctx context.Context) (*FetcherHandler, error) {
	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	// Initialize SSM client
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ssmClient := ssm.NewFromConfig(cfg)

	return &FetcherHandler{
		stateManager: stateManager,
		ssmClient:    ssmClient,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *FetcherHandler) HandleRequest(ctx context.Context, event StepFunctionsEvent) (Response, error) {
	log.Printf("Fetcher received event: %+v", event)

	// Get current run state
	runState, err := h.stateManager.GetLatestRun(ctx, event.RunID)
	if err != nil {
		log.Printf("Failed to get run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get run state: " + err.Error(),
		}, err
	}

	// Get Bluesky credentials from SSM
	handle, password, err := h.getBlueskyCredentials(ctx)
	if err != nil {
		log.Printf("Failed to get Bluesky credentials: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get credentials: " + err.Error(),
		}, err
	}

	// Create Bluesky client
	blueskyClient := client.New(handle, password)
	if err := blueskyClient.Authenticate(); err != nil {
		log.Printf("Failed to authenticate with Bluesky: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to authenticate: " + err.Error(),
		}, err
	}

	// Fetch posts using current cursor
	posts, nextCursor, hasMorePosts, err := h.fetchPostsWithCursor(ctx, blueskyClient, runState.CurrentCursor, event.AnalysisIntervalMinutes)
	if err != nil {
		log.Printf("Failed to fetch posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to fetch posts: " + err.Error(),
		}, err
	}

	// Convert to state posts
	statePosts := h.convertToStatePosts(posts)

	// Add posts to state
	if err := h.stateManager.AddPosts(ctx, event.RunID, statePosts); err != nil {
		log.Printf("Failed to add posts to state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to add posts: " + err.Error(),
		}, err
	}

	// Update cursor
	if err := h.stateManager.UpdateCursor(ctx, event.RunID, nextCursor, hasMorePosts); err != nil {
		log.Printf("Failed to update cursor: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to update cursor: " + err.Error(),
		}, err
	}

	log.Printf("Successfully fetched %d posts for run: %s", len(posts), event.RunID)
	return Response{
		StatusCode:    200,
		Body:          "Posts fetched successfully",
		HasMorePosts:  hasMorePosts,
		PostsRetrieved: len(posts),
		NextCursor:    nextCursor,
	}, nil
}

// getBlueskyCredentials retrieves credentials from SSM
func (h *FetcherHandler) getBlueskyCredentials(ctx context.Context) (string, string, error) {
	parameterNames := []string{
		"/hourstats/bluesky/handle",
		"/hourstats/bluesky/password",
	}

	result, err := h.ssmClient.GetParameters(ctx, &ssm.GetParametersInput{
		Names:          parameterNames,
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to get parameters: %w", err)
	}

	params := make(map[string]string)
	for _, p := range result.Parameters {
		params[*p.Name] = *p.Value
	}

	handle, ok := params["/hourstats/bluesky/handle"]
	if !ok {
		return "", "", fmt.Errorf("handle parameter not found")
	}

	password, ok := params["/hourstats/bluesky/password"]
	if !ok {
		return "", "", fmt.Errorf("password parameter not found")
	}

	return handle, password, nil
}

// fetchPostsWithCursor fetches posts using the current cursor
func (h *FetcherHandler) fetchPostsWithCursor(ctx context.Context, client *client.BlueskyClient, cursor string, analysisIntervalMinutes int) ([]client.Post, string, bool, error) {
	// Calculate cutoff time
	cutoffTime := time.Now().Add(-time.Duration(analysisIntervalMinutes) * time.Minute)
	
	// Use the existing GetTrendingPosts method but with cursor support
	// For now, we'll fetch one batch of 100 posts
	posts, nextCursor, hasMorePosts, err := client.GetTrendingPostsBatch(ctx, cursor, cutoffTime)
	if err != nil {
		return nil, "", false, fmt.Errorf("failed to fetch posts batch: %w", err)
	}
	
	return posts, nextCursor, hasMorePosts, nil
}

// convertToStatePosts converts client posts to state posts
func (h *FetcherHandler) convertToStatePosts(posts []client.Post) []state.Post {
	statePosts := make([]state.Post, len(posts))
	for i, post := range posts {
		statePosts[i] = state.Post{
			URI:            post.URI,
			Text:           post.Text,
			Author:         post.Author,
			Likes:          post.Likes,
			Reposts:        post.Reposts,
			Replies:        post.Replies,
			Sentiment:      post.Sentiment,
			EngagementScore: 0, // Will be calculated by analyzer
			CreatedAt:      post.CreatedAt,
		}
	}
	return statePosts
}

func main() {
	ctx := context.Background()
	handler, err := NewFetcherHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create fetcher handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
