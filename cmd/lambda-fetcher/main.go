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
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/state"
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
	StatusCode     int    `json:"statusCode"`
	Body           string `json:"body"`
	HasMorePosts   bool   `json:"hasMorePosts"`
	PostsRetrieved int    `json:"postsRetrieved"`
	NextCursor     string `json:"nextCursor,omitempty"`
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

	// Get current run state (try fetcher step first, fall back to orchestrator)
	runState, err := h.stateManager.GetRun(ctx, event.RunID, "fetcher")
	if err != nil {
		// If fetcher step doesn't exist, get orchestrator step
		runState, err = h.stateManager.GetRun(ctx, event.RunID, "orchestrator")
		if err != nil {
			log.Printf("Failed to get run state: %v", err)
			return Response{
				StatusCode: 500,
				Body:       "Failed to get run state: " + err.Error(),
			}, err
		}
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

	// Log the time range being used for fetching
	log.Printf("📅 FETCHER: Fetching posts from time range - From: %s, To: %s (current time: %s)",
		runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"))

	// Fetch posts using current cursor and cutoff time from run state
	posts, nextCursor, hasMorePosts, err := h.fetchPostsWithCursor(ctx, blueskyClient, runState.CurrentCursor, runState.CutoffTime)
	if err != nil {
		log.Printf("Failed to fetch posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to fetch posts: " + err.Error(),
		}, err
	}

	// Convert to state posts
	statePosts := h.convertToStatePosts(posts)
	log.Printf("🔍 FETCHER DEBUG: Converting %d posts to state format", len(posts))

	// Add posts to state
	log.Printf("🔍 FETCHER DEBUG: Storing %d posts in DynamoDB for run %s", len(statePosts), event.RunID)
	if err := h.stateManager.AddPosts(ctx, event.RunID, statePosts); err != nil {
		log.Printf("Failed to add posts to state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to add posts: " + err.Error(),
		}, err
	}
	log.Printf("✅ FETCHER DEBUG: Successfully stored %d posts in DynamoDB", len(statePosts))

	// Get updated state to show cumulative count
	updatedState, err := h.stateManager.GetRun(ctx, event.RunID, "fetcher")
	if err != nil {
		// Fall back to orchestrator step if fetcher step doesn't exist yet
		updatedState, err = h.stateManager.GetRun(ctx, event.RunID, "orchestrator")
		if err != nil {
			log.Printf("Warning: Could not get updated state for cumulative count: %v", err)
		}
	}

	if updatedState != nil {
		log.Printf("🔍 FETCHER DEBUG: Total posts now in DynamoDB: %d (run: %s)", updatedState.TotalPostsRetrieved, event.RunID)
		log.Printf("🔍 FETCHER DEBUG: DynamoDB cutoff time: %s", updatedState.CutoffTime.Format("2006-01-02 15:04:05 UTC"))
	}

	// Update cursor and create fetcher step
	if err := h.stateManager.UpdateCursor(ctx, event.RunID, nextCursor, hasMorePosts); err != nil {
		log.Printf("Failed to update cursor: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to update cursor: " + err.Error(),
		}, err
	}

	// Log cumulative post count
	cumulativeCount := 0
	if updatedState != nil {
		cumulativeCount = updatedState.TotalPostsRetrieved
	}
	log.Printf("✅ FETCHER BATCH COMPLETE - Run: %s, Batch: %s, Posts this batch: %d, Cumulative posts: %d, Cursor: %s → %s, HasMore: %v",
		event.RunID, event.BatchID, len(posts), cumulativeCount, runState.CurrentCursor, nextCursor, hasMorePosts)
	return Response{
		StatusCode:     200,
		Body:           "Posts fetched successfully",
		HasMorePosts:   hasMorePosts,
		PostsRetrieved: len(posts),
		NextCursor:     nextCursor,
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
func (h *FetcherHandler) fetchPostsWithCursor(ctx context.Context, client *client.BlueskyClient, cursor string, cutoffTime time.Time) ([]client.Post, string, bool, error) {
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
			URI:             post.URI,
			Text:            post.Text,
			Author:          post.Author,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			Sentiment:       post.Sentiment,
			EngagementScore: 0, // Will be calculated by analyzer
			CreatedAt:       post.CreatedAt,
		}
		
		// Debug logging for first few posts to see what's being stored
		if i < 5 {
			log.Printf("🔍 FETCHER DEBUG: Converting post %d - Author: %s, Likes: %d, Reposts: %d, Replies: %d", 
				i+1, post.Author, post.Likes, post.Reposts, post.Replies)
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
