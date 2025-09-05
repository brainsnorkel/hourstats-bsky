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
	MaxIterations           int    `json:"maxIterations"` // Maximum number of fetch iterations
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

	// Set default max iterations if not specified
	maxIterations := event.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 30 // Default to 30 iterations
	}

	// Log the time range being used for fetching
	log.Printf("📅 FETCHER: Fetching posts from time range - From: %s, To: %s (current time: %s), Max iterations: %d",
		runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"),
		maxIterations)

	var totalPostsRetrieved int
	var finalCursor string
	var finalHasMorePosts bool

	// Loop to fetch multiple batches
	for iteration := 0; iteration < maxIterations; iteration++ {
		log.Printf("🔄 FETCHER ITERATION %d/%d - Current cursor: %s", iteration+1, maxIterations, runState.CurrentCursor)

		// Fetch posts using current cursor and cutoff time from run state
		posts, nextCursor, hasMorePosts, err := h.fetchPostsWithCursor(ctx, blueskyClient, runState.CurrentCursor, runState.CutoffTime)
		if err != nil {
			log.Printf("Failed to fetch posts in iteration %d: %v", iteration+1, err)
			return Response{
				StatusCode: 500,
				Body:       fmt.Sprintf("Failed to fetch posts in iteration %d: %v", iteration+1, err.Error()),
			}, err
		}

		// Convert to state posts
		statePosts := h.convertToStatePosts(posts)
		log.Printf("🔍 FETCHER DEBUG: Converting %d posts to state format in iteration %d", len(posts), iteration+1)

		// Add posts to state
		log.Printf("🔍 FETCHER DEBUG: Storing %d posts in DynamoDB for run %s in iteration %d", len(statePosts), event.RunID, iteration+1)
		if err := h.stateManager.AddPosts(ctx, event.RunID, statePosts); err != nil {
			log.Printf("Failed to add posts to state in iteration %d: %v", iteration+1, err)
			return Response{
				StatusCode: 500,
				Body:       fmt.Sprintf("Failed to add posts in iteration %d: %v", iteration+1, err.Error()),
			}, err
		}
		log.Printf("✅ FETCHER DEBUG: Successfully stored %d posts in DynamoDB in iteration %d", len(statePosts), iteration+1)

		// Update totals
		totalPostsRetrieved += len(posts)
		finalCursor = nextCursor
		finalHasMorePosts = hasMorePosts

		// Update cursor and hasMorePosts status
		if err := h.stateManager.UpdateCursor(ctx, event.RunID, nextCursor, hasMorePosts); err != nil {
			log.Printf("Failed to update cursor in iteration %d: %v", iteration+1, err)
			return Response{
				StatusCode: 500,
				Body:       fmt.Sprintf("Failed to update cursor in iteration %d: %v", iteration+1, err.Error()),
			}, err
		}

		log.Printf("✅ FETCHER ITERATION %d COMPLETE - Posts this iteration: %d, Cumulative posts: %d, Cursor: %s → %s, HasMore: %t",
			iteration+1, len(posts), totalPostsRetrieved, runState.CurrentCursor, nextCursor, hasMorePosts)

		// If no more posts, break the loop
		if !hasMorePosts {
			log.Printf("🛑 FETCHER: No more posts available, stopping after %d iterations", iteration+1)
			break
		}

		// Update runState for next iteration
		runState.CurrentCursor = nextCursor
	}

	log.Printf("✅ FETCHER BATCH COMPLETE - Run: %s, Batch: %s, Total iterations: %d, Total posts retrieved: %d, Final cursor: %s, HasMore: %t",
		event.RunID, event.BatchID, maxIterations, totalPostsRetrieved, finalCursor, finalHasMorePosts)

	return Response{
		StatusCode:     200,
		Body:           "Posts fetched successfully",
		PostsRetrieved: totalPostsRetrieved,
		NextCursor:     finalCursor,
		HasMorePosts:   finalHasMorePosts,
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
			log.Printf("🔍 FETCHER DEBUG: Sample post %d - Author: %s, Likes: %d, Reposts: %d, Replies: %d",
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
