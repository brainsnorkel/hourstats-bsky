package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// FetcherEvent represents the event for the fetcher lambda
type FetcherEvent struct {
	RunID                   string `json:"runId"`
	AnalysisIntervalMinutes int    `json:"analysisIntervalMinutes"`
	Status                  string `json:"status"`
	MaxIterations           int    `json:"maxIterations"`    // Maximum number of fetch iterations
	Cursor                  string `json:"cursor,omitempty"` // Cursor to continue from
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
	lambdaClient *awslambda.Client
}

// NewFetcherHandler creates a new fetcher handler
func NewFetcherHandler(ctx context.Context) (*FetcherHandler, error) {
	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	// Initialize AWS clients
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ssmClient := ssm.NewFromConfig(cfg)
	lambdaClient := awslambda.NewFromConfig(cfg)

	return &FetcherHandler{
		stateManager: stateManager,
		ssmClient:    ssmClient,
		lambdaClient: lambdaClient,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *FetcherHandler) HandleRequest(ctx context.Context, event FetcherEvent) (Response, error) {
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

	// Use cursor from event if provided, otherwise use the one from state
	currentCursor := runState.CurrentCursor
	if event.Cursor != "" {
		currentCursor = event.Cursor
		log.Printf("🔄 FETCHER: Using cursor from event: %s", currentCursor)
	} else {
		log.Printf("🔄 FETCHER: Using cursor from state: %s", currentCursor)
	}

	// Calculate time period details
	now := time.Now()
	timeWindow := now.Sub(runState.CutoffTime)
	
	// Log detailed time range and cursor information
	log.Printf("📅 FETCHER: Querying Bluesky for posts in time window:")
	log.Printf("   📍 Cursor: %s", currentCursor)
	log.Printf("   ⏰ Start Time: %s (%s ago)", 
		runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"),
		now.Sub(runState.CutoffTime).Round(time.Second))
	log.Printf("   ⏰ End Time: %s (now)", now.Format("2006-01-02 15:04:05 UTC"))
	log.Printf("   ⏱️  Time Window: %s", timeWindow.Round(time.Second))
	log.Printf("   📊 Analysis Interval: %d minutes", runState.AnalysisIntervalMinutes)

	// Fetch one batch of posts
	log.Printf("🔄 FETCHER: Fetching batch with cursor: %s", currentCursor)

	posts, nextCursor, hasMorePosts, err := h.fetchPostsWithCursor(ctx, blueskyClient, currentCursor, runState.CutoffTime)
	if err != nil {
		log.Printf("Failed to fetch posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to fetch posts: " + err.Error(),
		}, err
	}

	// Log fetch results
	log.Printf("✅ FETCHER: Fetch completed successfully:")
	log.Printf("   📊 Posts Retrieved: %d", len(posts))
	log.Printf("   🔄 Next Cursor: %s", nextCursor)
	log.Printf("   ➡️  Has More Posts: %t", hasMorePosts)
	log.Printf("   📍 Cursor Progression: %s → %s", currentCursor, nextCursor)

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

	// Update cursor and hasMorePosts status
	if err := h.stateManager.UpdateCursor(ctx, event.RunID, nextCursor, hasMorePosts); err != nil {
		log.Printf("Failed to update cursor: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to update cursor: " + err.Error(),
		}, err
	}

	log.Printf("✅ FETCHER BATCH COMPLETE - Run: %s, Posts retrieved: %d, Cursor: %s → %s, HasMore: %t",
		event.RunID, len(posts), runState.CurrentCursor, nextCursor, hasMorePosts)

	// Determine next action based on completion logic
	shouldContinue, err := h.shouldContinueFetching(ctx, event.RunID, posts, runState.CutoffTime)
	if err != nil {
		log.Printf("Failed to determine if should continue: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to determine completion: " + err.Error(),
		}, err
	}

	// Log decision logic
	log.Printf("🤔 FETCHER: Decision analysis:")
	log.Printf("   📊 Posts in this batch: %d", len(posts))
	log.Printf("   ⏰ Cutoff time: %s", runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"))
	log.Printf("   ➡️  Has more posts from API: %t", hasMorePosts)
	log.Printf("   🔄 Should continue fetching: %t", shouldContinue)
	
	// Dispatch next action
	if shouldContinue && hasMorePosts {
		log.Printf("🚀 FETCHER: Dispatching next fetcher with cursor: %s", nextCursor)
		// Dispatch next fetcher
		err = h.dispatchNextFetcher(ctx, event.RunID, event.AnalysisIntervalMinutes, nextCursor)
		if err != nil {
			log.Printf("Failed to dispatch next fetcher: %v", err)
			return Response{
				StatusCode: 500,
				Body:       "Failed to dispatch next fetcher: " + err.Error(),
			}, err
		}
		log.Printf("✅ FETCHER: Dispatched next fetcher for run: %s", event.RunID)
	} else {
		log.Printf("🏁 FETCHER: Fetching complete, dispatching processor")
		log.Printf("   📊 Total posts collected: %d", runState.TotalPostsRetrieved+len(posts))
		log.Printf("   ⏰ Time window covered: %s to %s", 
			runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"),
			time.Now().Format("2006-01-02 15:04:05 UTC"))
		// Dispatch processor
		err = h.dispatchProcessor(ctx, event.RunID, event.AnalysisIntervalMinutes)
		if err != nil {
			log.Printf("Failed to dispatch processor: %v", err)
			return Response{
				StatusCode: 500,
				Body:       "Failed to dispatch processor: " + err.Error(),
			}, err
		}
		log.Printf("✅ FETCHER: Dispatched processor for run: %s", event.RunID)
	}

	return Response{
		StatusCode:     200,
		Body:           "Posts fetched successfully and next action dispatched",
		PostsRetrieved: len(posts),
		NextCursor:     nextCursor,
		HasMorePosts:   hasMorePosts,
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

// shouldContinueFetching determines if we should continue fetching based on the posts we just retrieved
func (h *FetcherHandler) shouldContinueFetching(ctx context.Context, runID string, posts []client.Post, cutoffTime time.Time) (bool, error) {
	if len(posts) == 0 {
		log.Printf("🛑 FETCHER: No posts retrieved, stopping")
		return false, nil
	}

	// Check if any of the posts are before the cutoff time
	// If so, we've reached the analysis period and should stop
	for _, post := range posts {
		postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
		if err != nil {
			log.Printf("⚠️ FETCHER: Skipping post with invalid timestamp: %s", post.CreatedAt)
			continue
		}

		if postTime.Before(cutoffTime) {
			log.Printf("🛑 FETCHER: Found post before cutoff time (%s < %s), stopping fetch",
				postTime.Format("2006-01-02 15:04:05 UTC"),
				cutoffTime.Format("2006-01-02 15:04:05 UTC"))
			return false, nil
		}
	}

	log.Printf("✅ FETCHER: All posts are within analysis period, continuing fetch")
	return true, nil
}

// dispatchNextFetcher invokes the next fetcher lambda
func (h *FetcherHandler) dispatchNextFetcher(ctx context.Context, runID string, analysisIntervalMinutes int, cursor string) error {
	fetcherPayload := map[string]interface{}{
		"runId":                   runID,
		"analysisIntervalMinutes": analysisIntervalMinutes,
		"status":                  "fetching",
		"maxIterations":           1,      // Each fetcher only does one batch
		"cursor":                  cursor, // Pass the cursor to continue from where we left off
	}

	payloadBytes, err := json.Marshal(fetcherPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal fetcher payload: %w", err)
	}

	_, err = h.lambdaClient.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("hourstats-fetcher"),
		Payload:      payloadBytes,
	})

	if err != nil {
		return fmt.Errorf("failed to invoke next fetcher lambda: %w", err)
	}

	log.Printf("Successfully dispatched next fetcher for run: %s", runID)
	return nil
}

// dispatchProcessor invokes the processor lambda
func (h *FetcherHandler) dispatchProcessor(ctx context.Context, runID string, analysisIntervalMinutes int) error {
	processorPayload := map[string]interface{}{
		"runId":                   runID,
		"analysisIntervalMinutes": analysisIntervalMinutes,
		"status":                  "processing",
	}

	payloadBytes, err := json.Marshal(processorPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal processor payload: %w", err)
	}

	_, err = h.lambdaClient.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("hourstats-processor"),
		Payload:      payloadBytes,
	})

	if err != nil {
		return fmt.Errorf("failed to invoke processor lambda: %w", err)
	}

	log.Printf("Successfully dispatched processor for run: %s", runID)
	return nil
}

func main() {
	ctx := context.Background()
	handler, err := NewFetcherHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create fetcher handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
