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
	useTimeBasedSearch := false
	timeBasedSearchStart := cutoffTime
	
	// Track URIs to detect duplicates per iteration
	seenURIs := make(map[string]bool)

	for {
		iteration++
		if iteration > maxIterations {
			log.Printf("⚠️ FETCHER: Reached max iterations (%d), stopping", maxIterations)
			break
		}

		log.Printf("🔄 FETCHER: Starting iteration %d with cursor: %s (time-based: %t)", iteration, currentCursor, useTimeBasedSearch)

		// Make 8 parallel API calls for this iteration
		posts, shouldStop, err := h.fetchBatchInParallel(ctx, client, currentCursor, cutoffTime, useTimeBasedSearch, timeBasedSearchStart)
		if err != nil {
			return totalPosts, fmt.Errorf("failed to fetch batch: %w", err)
		}

		if len(posts) == 0 {
			log.Printf("📭 FETCHER: No posts retrieved in iteration %d, stopping", iteration)
			break
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

		// Check if we've reached posts before our time window
		if shouldStop {
			log.Printf("⏰ FETCHER: Found posts before time window, stopping at iteration %d", iteration)
			break
		}

		// Check if we need to switch to time-based search (cursor limit avoidance)
		if !useTimeBasedSearch {
			// Parse current cursor to check if we're approaching the limit
			var cursorNum int
			if currentCursor != "" {
				if _, parseErr := fmt.Sscanf(currentCursor, "%d", &cursorNum); parseErr == nil {
					if cursorNum >= 9000 {
						log.Printf("🚨 FETCHER: Cursor limit avoidance triggered! Switching to time-based search at cursor %d", cursorNum)
						
						// Find the timestamp of the last post retrieved to use as new search boundary
						if len(posts) > 0 {
							// Find the oldest post in this batch (posts are sorted by most recent first)
							oldestPost := posts[len(posts)-1]
							lastPostTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt)
							if err == nil {
								timeBasedSearchStart = lastPostTime
								log.Printf("🕐 FETCHER: Switching to time-based search from timestamp: %s", 
									timeBasedSearchStart.Format("2006-01-02 15:04:05 UTC"))
								useTimeBasedSearch = true
								currentCursor = "" // Reset cursor for time-based search
								continue
							}
						}
					}
				}
			}
		}

		// Prepare for next iteration
		if useTimeBasedSearch {
			// For time-based search, we don't use cursors - the API handles time filtering
			log.Printf("🕐 FETCHER: Time-based search - no cursor advancement needed")
		} else {
			// For cursor-based search, advance by 800 posts
			currentCursor = fmt.Sprintf("%d", iteration*800)
			log.Printf("➡️ FETCHER: Preparing next iteration with cursor: %s", currentCursor)
		}
	}

	log.Printf("🏁 FETCHER: Parallel fetch complete - Total posts: %d across %d iterations (time-based: %t)", totalPosts, iteration, useTimeBasedSearch)
	return totalPosts, nil
}

// fetchBatchInParallel makes 8 parallel API calls and returns combined results
func (h *FetcherHandler) fetchBatchInParallel(ctx context.Context, client *bskyclient.BlueskyClient, startCursor string, cutoffTime time.Time, useTimeBasedSearch bool, timeBasedSearchStart time.Time) ([]bskyclient.Post, bool, error) {
	var cursors []string
	
	if useTimeBasedSearch {
		// For time-based search, we don't use cursors - make 8 calls with empty cursors
		// The API will handle time filtering based on the search parameters
		cursors = []string{"", "", "", "", "", "", "", ""}
		log.Printf("🕐 FETCHER: Making 8 parallel time-based API calls (no cursors)")
	} else {
		// Define cursors for 8 parallel calls (100 posts each = 800 total)
		cursors = []string{
			startCursor,
			addToCursor(startCursor, 100),
			addToCursor(startCursor, 200),
			addToCursor(startCursor, 300),
			addToCursor(startCursor, 400),
			addToCursor(startCursor, 500),
			addToCursor(startCursor, 600),
			addToCursor(startCursor, 700),
		}
		log.Printf("🚀 FETCHER: Making 8 parallel API calls with cursors: %v", cursors)
	}

	var allPosts []bskyclient.Post
	var oldestPostTime *time.Time
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Launch 8 goroutines for parallel fetching with 1-second delays
	for i, cursor := range cursors {
		wg.Add(1)
		go func(cursorIndex int, cursorValue string) {
			defer wg.Done()

			// Add 1-second delay between goroutine starts to reduce API load
			time.Sleep(time.Duration(cursorIndex) * time.Second)
			
			if useTimeBasedSearch {
				log.Printf("📡 FETCHER: Starting time-based parallel call %d", cursorIndex+1)
			} else {
				log.Printf("📡 FETCHER: Starting parallel call %d with cursor: %s", cursorIndex+1, cursorValue)
			}

			// Use the appropriate search method based on the mode
			var posts []bskyclient.Post
			var err error
			
			if useTimeBasedSearch {
				// For time-based search, we need to modify the client to support time-based queries
				// For now, we'll use the regular batch method but with time filtering
				posts, _, _, err = client.GetTrendingPostsBatch(ctx, cursorValue, timeBasedSearchStart)
			} else {
				posts, _, _, err = client.GetTrendingPostsBatch(ctx, cursorValue, cutoffTime)
			}
			
			if err != nil {
				log.Printf("❌ FETCHER: Parallel call %d failed: %v", cursorIndex+1, err)
				return
			}

			log.Printf("✅ FETCHER: Parallel call %d completed - Retrieved %d posts", cursorIndex+1, len(posts))

			// Find the oldest post in this batch to track the true boundary
			var localOldestTime *time.Time
			if len(posts) > 0 {
				// Find the oldest post in this batch (posts are sorted by most recent first)
				oldestPost := posts[len(posts)-1]
				postTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt)
				if err == nil {
					localOldestTime = &postTime

					// Convert to Unix timestamps for clean comparison
					postUnixTime := postTime.Unix()
					cutoffUnixTime := cutoffTime.Unix()

					log.Printf("🎯 FETCHER: Parallel call %d - Oldest post Unix: %d, Cutoff Unix: %d (diff: %d seconds)",
						cursorIndex+1, postUnixTime, cutoffUnixTime, postUnixTime-cutoffUnixTime)

					if postUnixTime < cutoffUnixTime {
						log.Printf("🎯 FETCHER: Parallel call %d found posts before cutoff time (oldest: %d < cutoff: %d)",
							cursorIndex+1, postUnixTime, cutoffUnixTime)
					}
				}
			}

			// Thread-safe accumulation and boundary tracking
			mu.Lock()
			allPosts = append(allPosts, posts...)

			// Track the oldest post time across all goroutines
			if localOldestTime != nil {
				if oldestPostTime == nil || localOldestTime.Before(*oldestPostTime) {
					oldestPostTime = localOldestTime
				}
			}
			mu.Unlock()
		}(i, cursor)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Determine if we should stop based on the oldest post across all goroutines
	shouldStop := false
	if oldestPostTime != nil && oldestPostTime.Before(cutoffTime) {
		shouldStop = true
		log.Printf("⏰ FETCHER: Found posts before cutoff time across all goroutines (oldest: %s < cutoff: %s)",
			oldestPostTime.Format("2006-01-02 15:04:05"), cutoffTime.Format("2006-01-02 15:04:05"))
	}

	log.Printf("🎯 FETCHER: Parallel batch complete - Total posts: %d, Should stop: %t (time-based: %t)", len(allPosts), shouldStop, useTimeBasedSearch)
	return allPosts, shouldStop, nil
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
