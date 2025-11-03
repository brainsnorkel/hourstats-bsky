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

	// Debug: Log credential details (without exposing the password)
	log.Printf("🔐 FETCHER: Retrieved credentials - Handle: %s, Password length: %d", handle, len(password))

	// Create and authenticate Bluesky client
	blueskyClient := bskyclient.New(handle, password)
	if err := blueskyClient.Authenticate(); err != nil {
		log.Printf("Failed to authenticate: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to authenticate: " + err.Error(),
		}, err
	}

	// Calculate time period details (use UTC to match API timestamps)
	now := time.Now().UTC()
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
	currentCursor := "" // Start with empty cursor to get most recent posts
	iteration := 0
	maxIterations := 100 // Increased for sequential pagination (100 pages * 100 posts = 10,000 posts max)

	// Track URIs to detect duplicates per iteration
	seenURIs := make(map[string]bool)

	// Track start time for early-stop logic (stop at 14 minutes to allow 1 min for dispatch)
	startTime := time.Now()
	earlyStopTime := 14 * time.Minute // Stop at 14 minutes to leave 1 minute for dispatch
	minPostsForEarlyStop := 1000      // Minimum posts needed for early stop

	log.Printf("🔄 FETCHER: Starting sequential fetch for posts since %s (sort=latest)", cutoffTime.Format("2006-01-02 15:04:05 UTC"))

	for {
		iteration++
		if iteration > maxIterations {
			log.Printf("⚠️ FETCHER: Reached max iterations (%d), stopping", maxIterations)
			break
		}

		log.Printf("🔄 FETCHER: Starting iteration %d with cursor: '%s'", iteration, currentCursor)

		// Make a single API call with proper cursor-based pagination
		posts, nextCursor, hasMore, err := client.GetTrendingPostsBatch(ctx, currentCursor, cutoffTime)
		if err != nil {
			return totalPosts, fmt.Errorf("failed to fetch batch at iteration %d: %w", iteration, err)
		}

		log.Printf("📊 FETCHER: Iteration %d - API returned %d posts (nextCursor: '%s', hasMore: %v)",
			iteration, len(posts), nextCursor, hasMore)

		// HEURISTIC: If the first call (cursor="") returns 0 posts, something is wrong with API parameters
		if iteration == 1 && currentCursor == "" && len(posts) == 0 {
			log.Printf("🚨 FETCHER: HEURISTIC FAILED - First API call with empty cursor returned 0 posts!")
			log.Printf("🚨 FETCHER: This indicates a problem with API parameters (since/sort) or no posts exist in time window")
			log.Printf("🚨 FETCHER: Cutoff time: %s (UTC)", cutoffTime.Format("2006-01-02 15:04:05 UTC"))
			log.Printf("🚨 FETCHER: Current time: %s (UTC)", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
			log.Printf("🚨 FETCHER: Time window: %d minutes", int(time.Since(cutoffTime).Minutes()))
			// Continue anyway to see if subsequent calls return posts, but log the issue
		}

		// Determine if we should stop based on whether posts are before cutoff time
		shouldStop := false
		if len(posts) == 0 {
			// If we got 0 posts and there are no more pages, stop
			if !hasMore || nextCursor == "" {
				log.Printf("📄 FETCHER: No posts and no more pages, stopping")
				break
			}
			// Otherwise continue to next page
			currentCursor = nextCursor
			continue
		}

		// Check if oldest post is before cutoff time
		if len(posts) > 0 {
			oldestPost := posts[len(posts)-1] // Posts sorted by most recent first
			oldestTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt)
			if err == nil && oldestTime.Before(cutoffTime) {
				shouldStop = true
				log.Printf("⏰ FETCHER: Oldest post (%s) is before cutoff (%s), stopping",
					oldestTime.Format("2006-01-02 15:04:05 UTC"),
					cutoffTime.Format("2006-01-02 15:04:05 UTC"))
			}
		}

		// Count duplicates in this iteration
		iterationDuplicates := 0
		for _, post := range posts {
			if seenURIs[post.URI] {
				iterationDuplicates++
			} else {
				seenURIs[post.URI] = true
			}
		}

		log.Printf("🔄 FETCHER: Iteration %d - Fetched %d posts, %d duplicates (Total unique URIs: %d)",
			iteration, len(posts), iterationDuplicates, len(seenURIs))

		// Convert to state posts and store
		statePosts := h.convertToStatePosts(posts)
		log.Printf("💾 FETCHER: Storing %d posts from iteration %d", len(statePosts), iteration)

		if err := h.stateManager.AddPosts(ctx, runID, statePosts); err != nil {
			return totalPosts, fmt.Errorf("failed to add posts: %w", err)
		}

		totalPosts += len(posts)

		// Debug: Find and log the highest engagement post in this iteration
		if len(posts) > 0 {
			highestEngagementPost := posts[0]
			highestEngagement := posts[0].EngagementScore
			for _, post := range posts {
				if post.EngagementScore > highestEngagement {
					highestEngagement = post.EngagementScore
					highestEngagementPost = post
				}
			}
			textPreview := highestEngagementPost.Text
			if len(textPreview) > 50 {
				textPreview = textPreview[:50] + "..."
			}
			log.Printf("🏆 FETCHER: Highest engagement post in iteration %d: @%s (score: %.1f) - %s",
				iteration, highestEngagementPost.Author, highestEngagement, textPreview)
		}

		log.Printf("✅ FETCHER: Iteration %d complete - Retrieved %d posts (Total: %d)", iteration, len(posts), totalPosts)

		// Early stop check: If we're at 14 minutes and have enough posts, stop to ensure dispatch
		elapsed := time.Since(startTime)
		if elapsed >= earlyStopTime && totalPosts >= minPostsForEarlyStop {
			log.Printf("⏰ FETCHER: Early stop triggered - Elapsed: %s, Posts: %d (≥%d)", elapsed.Round(time.Second), totalPosts, minPostsForEarlyStop)
			log.Printf("⏰ FETCHER: Stopping early to ensure processor dispatch before timeout (leaving 1 minute buffer)")
			break
		}

		// Log time remaining if we're getting close
		if elapsed >= 12*time.Minute && elapsed < earlyStopTime {
			remaining := earlyStopTime - elapsed
			log.Printf("⏱️  FETCHER: Time check - Elapsed: %s, Remaining before early stop: %s, Posts: %d",
				elapsed.Round(time.Second), remaining.Round(time.Second), totalPosts)
		}

		// Check if we've reached posts before our time window or no more pages
		if shouldStop {
			log.Printf("⏰ FETCHER: Found posts before time window, stopping at iteration %d", iteration)
			break
		}

		if !hasMore || nextCursor == "" {
			log.Printf("📄 FETCHER: No more pages available, stopping at iteration %d", iteration)
			break
		}

		// Use the API's returned cursor for the next iteration
		currentCursor = nextCursor
		log.Printf("➡️ FETCHER: Preparing next iteration with API cursor: '%s'", currentCursor)
	}

	log.Printf("🏁 FETCHER: Sequential fetch complete - Total posts: %d across %d iterations", totalPosts, iteration)
	return totalPosts, nil
}

// convertToStatePosts converts client posts to state posts
func (h *FetcherHandler) convertToStatePosts(posts []bskyclient.Post) []state.Post {
	statePosts := make([]state.Post, len(posts))
	for i, post := range posts {
		// Calculate engagement score (same formula as in analyzer)
		engagementScore := float64(post.Replies + post.Likes + post.Reposts)

		statePosts[i] = state.Post{
			URI:             post.URI,
			CID:             post.CID,
			Text:            post.Text,
			Author:          post.Author,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			CreatedAt:       post.CreatedAt,
			Sentiment:       post.Sentiment,
			EngagementScore: engagementScore,
		}
	}
	return statePosts
}

// getBlueskyCredentials retrieves credentials from SSM Parameter Store
func (h *FetcherHandler) getBlueskyCredentials(ctx context.Context) (string, string, error) {
	log.Printf("🔐 FETCHER: Attempting to retrieve credentials from SSM...")

	handleParam, err := h.ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/hourstats/bluesky/handle"),
		WithDecryption: aws.Bool(false),
	})
	if err != nil {
		log.Printf("❌ FETCHER: Failed to get handle parameter: %v", err)
		return "", "", fmt.Errorf("failed to get handle parameter: %w", err)
	}
	log.Printf("✅ FETCHER: Successfully retrieved handle parameter")

	passwordParam, err := h.ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/hourstats/bluesky/password"),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		log.Printf("❌ FETCHER: Failed to get password parameter: %v", err)
		return "", "", fmt.Errorf("failed to get password parameter: %w", err)
	}
	log.Printf("✅ FETCHER: Successfully retrieved password parameter")

	handle := aws.ToString(handleParam.Parameter.Value)
	password := aws.ToString(passwordParam.Parameter.Value)

	log.Printf("🔐 FETCHER: Credentials retrieved - Handle: %s, Password length: %d", handle, len(password))
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
