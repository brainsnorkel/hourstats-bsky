package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
	"github.com/christophergentle/hourstats-bsky/internal/formatter"
	lambdapkg "github.com/christophergentle/hourstats-bsky/internal/lambda"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// ProcessorEvent represents the event for the processor lambda
type ProcessorEvent struct {
	RunID                   string `json:"runId"`
	AnalysisIntervalMinutes int    `json:"analysisIntervalMinutes"`
	Status                  string `json:"status"`
}

// Response represents the Lambda response
type Response struct {
	StatusCode       int    `json:"statusCode"`
	Body             string `json:"body"`
	PostsAnalyzed    int    `json:"postsAnalyzed,omitempty"`
	TopPostsCount    int    `json:"topPostsCount,omitempty"`
	OverallSentiment string `json:"overallSentiment,omitempty"`
}

// ProcessorHandler handles the combined analysis, aggregation, and posting
type ProcessorHandler struct {
	stateManager      *state.StateManager
	sentimentAnalyzer *analyzer.SentimentAnalyzer
	blueskyClient     *client.BlueskyClient
	config            *config.Config
}

// NewProcessorHandler creates a new processor handler
func NewProcessorHandler(ctx context.Context) (*ProcessorHandler, error) {
	// Load configuration
	configLoader, err := lambdapkg.NewSSMConfigLoader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSM config loader: %w", err)
	}

	cfg, err := configLoader.LoadConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	// Initialize sentiment analyzer
	sentimentAnalyzer := analyzer.New()

	// Initialize Bluesky client
	blueskyClient := client.New(cfg.Bluesky.Handle, cfg.Bluesky.Password)

	return &ProcessorHandler{
		stateManager:      stateManager,
		sentimentAnalyzer: sentimentAnalyzer,
		blueskyClient:     blueskyClient,
		config:            cfg,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *ProcessorHandler) HandleRequest(ctx context.Context, event ProcessorEvent) (Response, error) {
	log.Printf("Processor received event: %+v", event)

	// Get current run state - look for orchestrator step which has the run metadata
	runState, err := h.stateManager.GetRun(ctx, event.RunID, "orchestrator")
	if err != nil {
		log.Printf("Failed to get fetcher run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get run state: " + err.Error(),
		}, err
	}

	// Log the time range being used for processing
	log.Printf("📅 PROCESSOR: Processing posts from time range - From: %s, To: %s (current time: %s)",
		runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"),
		time.Now().Format("2006-01-02 15:04:05 UTC"))

	// Retrieve all posts for this run
	allPosts, err := h.stateManager.GetAllPosts(ctx, event.RunID)
	if err != nil {
		log.Printf("Failed to get all posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get posts: " + err.Error(),
		}, err
	}

	// Fix URI format for posts retrieved from DynamoDB
	allPosts = h.fixPostURIs(allPosts)

	log.Printf("🔍 PROCESSOR DEBUG: Retrieved %d posts from DynamoDB for run %s", len(allPosts), event.RunID)
	log.Printf("🔍 PROCESSOR DEBUG: Using cutoff time from DynamoDB: %s", runState.CutoffTime.Format("2006-01-02 15:04:05 UTC"))

	// Deduplicate posts by URI, keeping the one with highest engagement score
	deduplicatedPosts := h.deduplicatePostsByURI(allPosts)
	log.Printf("🔍 PROCESSOR DEBUG: After deduplication: %d posts (from %d original)", len(deduplicatedPosts), len(allPosts))

	// Filter posts by cutoff time
	filteredPosts := h.filterPostsByCutoffTime(deduplicatedPosts, runState.CutoffTime)
	log.Printf("🔍 PROCESSOR DEBUG: After time filtering: %d posts (from %d deduplicated)", len(filteredPosts), len(deduplicatedPosts))

	if len(filteredPosts) == 0 {
		log.Printf("No posts found for the time period, skipping analysis")
		return Response{
			StatusCode: 200,
			Body:       "No posts to analyze",
		}, nil
	}

	// Step 1: Analyze posts for sentiment and calculate engagement scores
	log.Printf("Analyzing %d posts", len(filteredPosts))
	analyzedPosts, overallSentiment, netSentimentPercentage, err := h.analyzePosts(filteredPosts)
	if err != nil {
		log.Printf("Failed to analyze posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to analyze posts: " + err.Error(),
		}, err
	}

	// Step 2: Get top posts by engagement score
	log.Printf("Aggregating %d posts after analysis", len(analyzedPosts))
	topPosts := h.getTopPosts(analyzedPosts, 5)

	// Debug logging for top posts
	log.Printf("🔍 PROCESSOR DEBUG: Top 5 posts selected:")
	for i, post := range topPosts {
		log.Printf("🔍 PROCESSOR DEBUG: Top %d - Author: %s, Sentiment: %s, EngagementScore: %.2f, Likes: %d, Reposts: %d, Replies: %d",
			i+1, post.Author, post.Sentiment, post.EngagementScore, post.Likes, post.Reposts, post.Replies)
	}

	// Step 3: Update run state with top posts
	log.Printf("Updating run state with top posts")
	err = h.stateManager.SetAnalysisComplete(ctx, event.RunID, overallSentiment, topPosts)
	if err != nil {
		log.Printf("Failed to update run state with top posts: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to update run state: " + err.Error(),
		}, err
	}

	// Step 4: Post summary to Bluesky
	log.Printf("Posting summary to Bluesky")
	log.Printf("🔍 PROCESSOR DEBUG: Sentiment data - Overall: %s, Net sentiment: %.1f%%, Total posts: %d",
		overallSentiment, netSentimentPercentage, len(filteredPosts))

	// Authenticate before posting
	if err := h.blueskyClient.Authenticate(); err != nil {
		log.Printf("Failed to authenticate with Bluesky: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to authenticate with Bluesky: " + err.Error(),
		}, err
	}
	log.Printf("✅ Successfully authenticated with Bluesky")

	err = h.postSummary(runState, topPosts, overallSentiment, len(filteredPosts), netSentimentPercentage)
	if err != nil {
		log.Printf("Failed to post summary: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to post summary: " + err.Error(),
		}, err
	}

	log.Printf("Successfully processed %d posts and posted summary for run: %s", len(analyzedPosts), event.RunID)
	return Response{
		StatusCode:       200,
		Body:             "Posts processed and summary posted successfully",
		PostsAnalyzed:    len(analyzedPosts),
		TopPostsCount:    len(topPosts),
		OverallSentiment: overallSentiment,
	}, nil
}

// analyzePosts analyzes sentiment and calculates engagement scores
func (h *ProcessorHandler) analyzePosts(posts []state.Post) ([]state.Post, string, float64, error) {
	log.Printf("Analyzing %d posts", len(posts))

	// Convert state posts to analyzer posts
	analyzerPosts := make([]analyzer.Post, len(posts))
	for i, post := range posts {
		analyzerPosts[i] = analyzer.Post{
			URI:       post.URI,
			CID:       post.CID,
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
		return nil, "", 0.0, fmt.Errorf("failed to analyze posts: %w", err)
	}

	// Calculate overall sentiment using compound scores
	overallSentiment, netSentimentPercentage := h.calculateOverallSentimentWithCompoundScores(analyzedPosts)

	// Convert back to state posts with analysis results
	statePosts := make([]state.Post, len(analyzedPosts))
	for i, analyzed := range analyzedPosts {
		statePosts[i] = state.Post{
			URI:             analyzed.URI,
			CID:             analyzed.CID,
			Text:            analyzed.Text,
			Author:          analyzed.Author,
			Likes:           analyzed.Likes,
			Reposts:         analyzed.Reposts,
			Replies:         analyzed.Replies,
			Sentiment:       analyzed.Sentiment,
			EngagementScore: analyzed.EngagementScore,
			CreatedAt:       analyzed.CreatedAt,
		}

		// Debug logging for first few posts
		if i < 5 {
			log.Printf("🔍 PROCESSOR DEBUG: Post %d - Author: %s, Likes: %d, Reposts: %d, Replies: %d, Sentiment: %s, EngagementScore: %.2f",
				i+1, analyzed.Author, analyzed.Likes, analyzed.Reposts, analyzed.Replies, analyzed.Sentiment, analyzed.EngagementScore)
		}
	}

	return statePosts, overallSentiment, netSentimentPercentage, nil
}

func (h *ProcessorHandler) calculateOverallSentimentWithCompoundScores(posts []analyzer.AnalyzedPost) (string, float64) {
	if len(posts) == 0 {
		return "neutral", 0.0
	}

	var totalCompoundScore float64
	for _, post := range posts {
		totalCompoundScore += post.SentimentScore // This is already the compound score
	}

	averageCompoundScore := totalCompoundScore / float64(len(posts))

	// Map compound score to category for backward compatibility
	var sentimentCategory string
	if averageCompoundScore >= 0.3 {
		sentimentCategory = "positive"
	} else if averageCompoundScore <= -0.3 {
		sentimentCategory = "negative"
	} else {
		sentimentCategory = "neutral"
	}

	// Scale to percentage range for 100-word system
	netSentimentPercentage := averageCompoundScore * 100.0

	log.Printf("🔍 PROCESSOR DEBUG: Average compound score: %.3f, Net sentiment: %.1f%%, Sentiment: %s",
		averageCompoundScore, netSentimentPercentage, sentimentCategory)

	return sentimentCategory, netSentimentPercentage
}


// getTopPosts gets the top N posts by engagement score
func (h *ProcessorHandler) getTopPosts(posts []state.Post, n int) []state.Post {
	if len(posts) <= n {
		return posts
	}

	// Sort by engagement score (descending)
	for i := 0; i < len(posts)-1; i++ {
		for j := i + 1; j < len(posts); j++ {
			if posts[i].EngagementScore < posts[j].EngagementScore {
				posts[i], posts[j] = posts[j], posts[i]
			}
		}
	}

	return posts[:n]
}

// filterPostsByCutoffTime filters posts to only include those after the cutoff time
func (h *ProcessorHandler) filterPostsByCutoffTime(posts []state.Post, cutoffTime time.Time) []state.Post {
	var filteredPosts []state.Post

	for _, post := range posts {
		postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
		if err != nil {
			continue // Skip posts with invalid timestamps
		}

		if !postTime.Before(cutoffTime) {
			filteredPosts = append(filteredPosts, post)
		}
	}

	return filteredPosts
}

// postSummary posts the summary to Bluesky
func (h *ProcessorHandler) postSummary(runState *state.RunState, topPosts []state.Post, overallSentiment string, totalPosts int, netSentimentPercentage float64) error {
	// Check if we have data to post
	if runState.TotalPostsRetrieved == 0 {
		log.Printf("No posts retrieved, skipping post")
		return nil
	}

	if len(topPosts) == 0 {
		log.Printf("No top posts to display, skipping post")
		return nil
	}

	if overallSentiment == "" {
		log.Printf("No sentiment analysis completed, skipping post")
		return nil
	}

	// Convert state posts to client posts
	clientPosts := make([]client.Post, len(topPosts))
	for i, post := range topPosts {
		clientPosts[i] = client.Post{
			URI:             post.URI,
			CID:             post.CID,
			Text:            post.Text,
			Author:          post.Author,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			CreatedAt:       post.CreatedAt,
			Sentiment:       post.Sentiment,
			EngagementScore: post.EngagementScore,
		}
	}

	// Generate post content to check character count using shared formatter
	formatterPosts := make([]formatter.Post, len(topPosts))
	for i, post := range topPosts {
		formatterPosts[i] = formatter.Post{
			URI:             post.URI,
			CID:             post.CID,
			Author:          post.Author,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			Sentiment:       post.Sentiment,
			EngagementScore: post.EngagementScore,
		}
	}

	postContent := formatter.FormatPostContent(formatterPosts, overallSentiment, runState.AnalysisIntervalMinutes, totalPosts, netSentimentPercentage)
	characterCount := len(postContent)
	blueskyLimit := 300
	remainingChars := blueskyLimit - characterCount

	log.Printf("📊 Post Statistics - Characters: %d/%d, Remaining: %d", characterCount, blueskyLimit, remainingChars)

	if remainingChars < 0 {
		log.Printf("⚠️  WARNING: Post exceeds Bluesky limit by %d characters!", -remainingChars)
	} else if remainingChars < 50 {
		log.Printf("⚠️  WARNING: Post is close to Bluesky limit (%d characters remaining)", remainingChars)
	} else {
		log.Printf("✅ Post is within Bluesky limits")
	}

	// Post the summary
	return h.blueskyClient.PostTrendingSummary(clientPosts, overallSentiment, runState.AnalysisIntervalMinutes, totalPosts, netSentimentPercentage)
}

// deduplicatePostsByURI removes duplicate posts by URI, keeping the one with highest engagement score
func (h *ProcessorHandler) deduplicatePostsByURI(posts []state.Post) []state.Post {
	uriToPost := make(map[string]state.Post)

	for _, post := range posts {
		// Skip posts with empty URIs
		if post.URI == "" {
			continue
		}

		// Calculate engagement score for this post
		currentEngagement := post.Likes + post.Reposts + post.Replies

		// Check if we've seen this URI before
		if existingPost, exists := uriToPost[post.URI]; exists {
			// Calculate engagement score for existing post
			existingEngagement := existingPost.Likes + existingPost.Reposts + existingPost.Replies

			// Keep the post with higher engagement score
			if currentEngagement > existingEngagement {
				uriToPost[post.URI] = post
				log.Printf("🔍 PROCESSOR DEBUG: Replacing post %s (engagement: %d) with better version (engagement: %d)",
					post.URI, existingEngagement, currentEngagement)
			}
		} else {
			// First time seeing this URI, add it
			uriToPost[post.URI] = post
		}
	}

	// Convert map values to slice
	var deduplicatedPosts []state.Post
	for _, post := range uriToPost {
		deduplicatedPosts = append(deduplicatedPosts, post)
	}

	log.Printf("🔍 PROCESSOR DEBUG: Deduplication removed %d duplicate posts", len(posts)-len(deduplicatedPosts))
	return deduplicatedPosts
}

// fixPostURIs fixes the URI format for posts retrieved from DynamoDB
func (h *ProcessorHandler) fixPostURIs(posts []state.Post) []state.Post {
	fixedCount := 0
	for i, post := range posts {
		// If the URI is in the old format (at://post-XXX), we need to construct a proper AT Protocol URI
		// However, we don't have the DID information stored, so we'll skip posts with invalid URIs
		if strings.HasPrefix(post.URI, "at://post-") {
			log.Printf("🔍 URI FIX: Skipping post with invalid URI format: %s", post.URI)
			posts[i].URI = "" // Set to empty to indicate invalid URI
			fixedCount++
		}
	}
	log.Printf("🔍 URI FIX: Fixed %d posts with invalid URI format", fixedCount)
	return posts
}

func main() {
	ctx := context.Background()
	handler, err := NewProcessorHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create processor handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
