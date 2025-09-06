package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	bskyclient "github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// FetcherEvent represents the event for the fetcher lambda
type FetcherEvent struct {
	RunID                   string `json:"runId"`
	AnalysisIntervalMinutes int    `json:"analysisIntervalMinutes"`
	Status                  string `json:"status"`
}

// Response represents the Lambda response
type Response struct {
	StatusCode     int    `json:"statusCode"`
	Body           string `json:"body"`
	PostsRetrieved int    `json:"postsRetrieved"`
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

	// Initialize AWS SDK
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Initialize SSM client
	ssmClient := ssm.NewFromConfig(cfg)

	// Initialize Lambda client
	lambdaClient := awslambda.NewFromConfig(cfg)

	return &FetcherHandler{
		stateManager: stateManager,
		ssmClient:    ssmClient,
		lambdaClient: lambdaClient,
	}, nil
}

// Handle handles the Lambda function invocation
func (h *FetcherHandler) Handle(ctx context.Context, event FetcherEvent) (Response, error) {
	log.Printf("🚀 FETCHER: Starting fetcher for run: %s", event.RunID)

	// Get run state
	runState, err := h.stateManager.GetRun(ctx, event.RunID, "orchestrator")
	if err != nil {
		log.Printf("Failed to get run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get run state: " + err.Error(),
		}, err
	}

	// Get Bluesky credentials
	handle, password, err := h.getBlueskyCredentials(ctx)
	if err != nil {
		log.Printf("Failed to get credentials: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get credentials: " + err.Error(),
		}, err
	}

	// Create and authenticate Bluesky client
	blueskyClient := bskyclient.New(handle, password)
	if err := blueskyClient.Authenticate(); err != nil {
		log.Printf("Failed to authenticate: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to authenticate: " + err.Error(),
		}, err
	}

	// Calculate time period details
	now := time.Now()
	timeWindow := now.Sub(runState.CutoffTime)

	// Log detailed time range information
	log.Printf("📅 FETCHER: Starting parallel fetch for posts in time window:")
	log.Printf("   ⏰ Start Time: %s (%s ago)",
		runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"),
		now.Sub(runState.CutoffTime).Round(time.Second))
	log.Printf("   ⏰ End Time: %s (now)", now.Format("2006-01-02 15:04:05 UTC"))
	log.Printf("   ⏱️  Time Window: %s", timeWindow.Round(time.Second))
	log.Printf("   📊 Analysis Interval: %d minutes", runState.AnalysisIntervalMinutes)

	// Run parallel fetch with internal loops
	totalPosts, err := h.fetchAllPostsInParallel(ctx, blueskyClient, runState.CutoffTime, event.RunID)
	if err != nil {
		log.Printf("Failed to fetch posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to fetch posts: " + err.Error(),
		}, err
	}

	// Update state to indicate fetching is complete
	if err := h.stateManager.UpdateCursor(ctx, event.RunID, "", false); err != nil {
		log.Printf("Failed to update cursor: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to update cursor: " + err.Error(),
		}, err
	}

	log.Printf("✅ FETCHER: All fetching complete - Run: %s, Total posts retrieved: %d", event.RunID, totalPosts)

	// Dispatch processor
	log.Printf("🏁 FETCHER: Fetching complete, dispatching processor")
	err = h.dispatchProcessor(ctx, event.RunID)
	if err != nil {
		log.Printf("Failed to dispatch processor: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to dispatch processor: " + err.Error(),
		}, err
	}
	log.Printf("✅ FETCHER: Processor dispatched successfully")

	return Response{
		StatusCode:     200,
		Body:           "Posts fetched successfully and processor dispatched",
		PostsRetrieved: totalPosts,
	}, nil
}

// fetchAllPostsInParallel fetches all posts using parallel API calls and internal loops
func (h *FetcherHandler) fetchAllPostsInParallel(ctx context.Context, client *bskyclient.BlueskyClient, cutoffTime time.Time, runID string) (int, error) {
	var totalPosts int
	currentCursor := ""
	iteration := 0
	maxIterations := 20 // Safety limit to prevent infinite loops

	for {
		iteration++
		if iteration > maxIterations {
			log.Printf("⚠️ FETCHER: Reached max iterations (%d), stopping", maxIterations)
			break
		}

		log.Printf("🔄 FETCHER: Starting iteration %d with cursor: %s", iteration, currentCursor)

		// Make 5 parallel API calls for this iteration
		posts, hasOldPosts, err := h.fetchBatchInParallel(ctx, client, currentCursor, cutoffTime)
		if err != nil {
			return totalPosts, fmt.Errorf("failed to fetch batch: %w", err)
		}

		if len(posts) == 0 {
			log.Printf("📭 FETCHER: No posts retrieved in iteration %d, stopping", iteration)
			break
		}

		// Convert to state posts and store
		statePosts := h.convertToStatePosts(posts)
		log.Printf("💾 FETCHER: Storing %d posts from iteration %d", len(statePosts), iteration)

		if err := h.stateManager.AddPosts(ctx, runID, statePosts); err != nil {
			return totalPosts, fmt.Errorf("failed to add posts: %w", err)
		}

		totalPosts += len(posts)
		log.Printf("✅ FETCHER: Iteration %d complete - Retrieved %d posts (Total: %d)", iteration, len(posts), totalPosts)

		// Check if we've reached posts before our time window
		if hasOldPosts {
			log.Printf("⏰ FETCHER: Found posts before time window, stopping at iteration %d", iteration)
			break
		}

		// Prepare for next iteration (1000 posts ahead)
		currentCursor = fmt.Sprintf("%d", iteration*1000)
		log.Printf("➡️ FETCHER: Preparing next iteration with cursor: %s", currentCursor)
	}

	log.Printf("🏁 FETCHER: Parallel fetch complete - Total posts: %d across %d iterations", totalPosts, iteration)
	return totalPosts, nil
}

// fetchBatchInParallel makes 10 parallel API calls and returns combined results
func (h *FetcherHandler) fetchBatchInParallel(ctx context.Context, client *bskyclient.BlueskyClient, startCursor string, cutoffTime time.Time) ([]bskyclient.Post, bool, error) {
	// Define cursors for 10 parallel calls (100 posts each = 1000 total)
	cursors := []string{
		startCursor,
		addToCursor(startCursor, 100),
		addToCursor(startCursor, 200),
		addToCursor(startCursor, 300),
		addToCursor(startCursor, 400),
		addToCursor(startCursor, 500),
		addToCursor(startCursor, 600),
		addToCursor(startCursor, 700),
		addToCursor(startCursor, 800),
		addToCursor(startCursor, 900),
	}

	log.Printf("🚀 FETCHER: Making 10 parallel API calls with cursors: %v", cursors)

	var allPosts []bskyclient.Post
	var hasOldPosts bool
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Launch 10 goroutines for parallel fetching
	for i, cursor := range cursors {
		wg.Add(1)
		go func(cursorIndex int, cursorValue string) {
			defer wg.Done()

			log.Printf("📡 FETCHER: Starting parallel call %d with cursor: %s", cursorIndex+1, cursorValue)

			posts, _, _, err := client.GetTrendingPostsBatch(ctx, cursorValue, cutoffTime)
			if err != nil {
				log.Printf("❌ FETCHER: Parallel call %d failed: %v", cursorIndex+1, err)
				return
			}

			log.Printf("✅ FETCHER: Parallel call %d completed - Retrieved %d posts", cursorIndex+1, len(posts))

			// Check if any posts are before cutoff time
			localHasOldPosts := false
			for _, post := range posts {
				postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
				if err == nil && postTime.Before(cutoffTime) {
					localHasOldPosts = true
					break
				}
			}

			// Thread-safe accumulation
			mu.Lock()
			allPosts = append(allPosts, posts...)
			if localHasOldPosts {
				hasOldPosts = true
			}
			mu.Unlock()
		}(i, cursor)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	log.Printf("🎯 FETCHER: Parallel batch complete - Total posts: %d, Has old posts: %t", len(allPosts), hasOldPosts)
	return allPosts, hasOldPosts, nil
}

// addToCursor adds a number to a cursor string (handles empty string case)
func addToCursor(cursor string, add int) string {
	if cursor == "" {
		return fmt.Sprintf("%d", add)
	}

	// Parse current cursor as number and add
	var current int
	if _, err := fmt.Sscanf(cursor, "%d", &current); err != nil {
		// If parsing fails, return the addition value
		return fmt.Sprintf("%d", add)
	}

	return fmt.Sprintf("%d", current+add)
}

// convertToStatePosts converts client posts to state posts
func (h *FetcherHandler) convertToStatePosts(posts []bskyclient.Post) []state.Post {
	statePosts := make([]state.Post, len(posts))
	for i, post := range posts {
		statePosts[i] = state.Post{
			URI:       post.URI,
			Text:      post.Text,
			Author:    post.Author,
			Likes:     post.Likes,
			Reposts:   post.Reposts,
			Replies:   post.Replies,
			CreatedAt: post.CreatedAt,
			Sentiment: post.Sentiment,
		}
	}
	return statePosts
}

// getBlueskyCredentials retrieves credentials from SSM Parameter Store
func (h *FetcherHandler) getBlueskyCredentials(ctx context.Context) (string, string, error) {
	handleParam, err := h.ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/hourstats/bluesky/handle"),
		WithDecryption: aws.Bool(false),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to get handle parameter: %w", err)
	}

	passwordParam, err := h.ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/hourstats/bluesky/password"),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to get password parameter: %w", err)
	}

	handle := aws.ToString(handleParam.Parameter.Value)
	password := aws.ToString(passwordParam.Parameter.Value)

	return handle, password, nil
}

// dispatchProcessor invokes the processor lambda
func (h *FetcherHandler) dispatchProcessor(ctx context.Context, runID string) error {
	processorPayload := map[string]interface{}{
		"runId": runID,
	}

	payloadBytes, err := json.Marshal(processorPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal processor payload: %w", err)
	}

	_, err = h.lambdaClient.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName:   aws.String("hourstats-processor"),
		Payload:        payloadBytes,
		InvocationType: "Event",
	})
	if err != nil {
		return fmt.Errorf("failed to invoke processor: %w", err)
	}

	return nil
}

func main() {
	ctx := context.Background()
	handler, err := NewFetcherHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create fetcher handler: %v", err)
	}

	lambda.Start(handler.Handle)
}
