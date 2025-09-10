package lambda

import (
	"context"
	"log"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
)

// AnalysisResult represents the result of a trend analysis
type AnalysisResult struct {
	PostsAnalyzed int    `json:"posts_analyzed"`
	TopPosts      int    `json:"top_posts"`
	Sentiment     string `json:"sentiment"`
	Success       bool   `json:"success"`
	ErrorMessage  string `json:"error_message,omitempty"`
}

// HourStatsAnalyzer handles the main analysis logic for Lambda
type HourStatsAnalyzer struct {
	client   *client.BlueskyClient
	analyzer *analyzer.SentimentAnalyzer
	config   *config.Config
}

// NewHourStatsAnalyzer creates a new analyzer instance
func NewHourStatsAnalyzer(cfg *config.Config) *HourStatsAnalyzer {
	blueskyClient := client.New(cfg.Bluesky.Handle, cfg.Bluesky.Password)
	sentimentAnalyzer := analyzer.New()

	return &HourStatsAnalyzer{
		client:   blueskyClient,
		analyzer: sentimentAnalyzer,
		config:   cfg,
	}
}

// RunAnalysis executes the complete trend analysis process
func (h *HourStatsAnalyzer) RunAnalysis(ctx context.Context) (*AnalysisResult, error) {
	log.Println("Starting trend analysis...")

	// Authenticate with Bluesky
	if err := h.client.Authenticate(); err != nil {
		return &AnalysisResult{
			Success:      false,
			ErrorMessage: "Failed to authenticate with Bluesky: " + err.Error(),
		}, err
	}

	log.Println("Successfully authenticated with Bluesky")

	// Fetch trending posts
	clientPosts, err := h.client.GetTrendingPosts(h.config.Settings.AnalysisIntervalMinutes)
	if err != nil {
		return &AnalysisResult{
			Success:      false,
			ErrorMessage: "Failed to fetch trending posts: " + err.Error(),
		}, err
	}

	log.Printf("Retrieved %d posts from Bluesky", len(clientPosts))

	// Convert client posts to analyzer posts
	analyzerPosts := h.convertToAnalyzerPosts(clientPosts)

	// Analyze sentiment and extract topics
	analyzedPosts, err := h.analyzer.AnalyzePosts(analyzerPosts)
	if err != nil {
		return &AnalysisResult{
			Success:      false,
			ErrorMessage: "Failed to analyze posts: " + err.Error(),
		}, err
	}

	log.Printf("Analyzed %d posts for sentiment", len(analyzedPosts))

	// Get top 5 posts
	topPosts := h.getTopPosts(analyzedPosts, h.config.Settings.TopPostsCount)

	// Calculate overall sentiment from all analyzed posts using compound scores
	overallSentiment, netSentimentPercentage := h.calculateOverallSentiment(analyzedPosts)
	totalPosts := len(analyzedPosts)

	// Convert back to client posts for posting
	clientTopPosts := h.convertToClientPosts(topPosts)

	// Post the results (skip if dry run)
	if !h.config.Settings.DryRun {
		_, _, err := h.client.PostTrendingSummary(clientTopPosts, overallSentiment, h.config.Settings.AnalysisIntervalMinutes, totalPosts, netSentimentPercentage)
		if err != nil {
			return &AnalysisResult{
				Success:      false,
				ErrorMessage: "Failed to post trending summary: " + err.Error(),
			}, err
		}
		log.Printf("Successfully posted trending summary with %d posts", len(clientTopPosts))
	} else {
		log.Println("Dry run mode: Skipping post to Bluesky")
	}

	return &AnalysisResult{
		PostsAnalyzed: len(analyzedPosts),
		TopPosts:      len(topPosts),
		Sentiment:     overallSentiment,
		Success:       true,
	}, nil
}

// convertToAnalyzerPosts converts client posts to analyzer posts
func (h *HourStatsAnalyzer) convertToAnalyzerPosts(clientPosts []client.Post) []analyzer.Post {
	var analyzerPosts []analyzer.Post
	for _, post := range clientPosts {
		analyzerPosts = append(analyzerPosts, analyzer.Post{
			URI:       post.URI,
			Text:      post.Text,
			Author:    post.Author,
			Likes:     post.Likes,
			Reposts:   post.Reposts,
			Replies:   post.Replies,
			CreatedAt: post.CreatedAt,
		})
	}
	return analyzerPosts
}

// convertToClientPosts converts analyzer posts to client posts
func (h *HourStatsAnalyzer) convertToClientPosts(analyzedPosts []analyzer.AnalyzedPost) []client.Post {
	var clientPosts []client.Post
	for _, post := range analyzedPosts {
		clientPosts = append(clientPosts, client.Post{
			URI:       post.URI,
			Text:      post.Text,
			Author:    post.Author,
			Likes:     post.Likes,
			Reposts:   post.Reposts,
			Replies:   post.Replies,
			CreatedAt: post.CreatedAt,
			Sentiment: post.Sentiment,
		})
	}
	return clientPosts
}

// getTopPosts returns the top N posts by engagement score
func (h *HourStatsAnalyzer) getTopPosts(posts []analyzer.AnalyzedPost, count int) []analyzer.AnalyzedPost {
	// Sort posts by engagement score in descending order
	for i := 0; i < len(posts)-1; i++ {
		for j := i + 1; j < len(posts); j++ {
			if posts[i].EngagementScore < posts[j].EngagementScore {
				posts[i], posts[j] = posts[j], posts[i]
			}
		}
	}

	// Return the top N posts
	if len(posts) < count {
		return posts
	}
	return posts[:count]
}

// calculateOverallSentiment calculates the overall sentiment from top posts
func (h *HourStatsAnalyzer) calculateOverallSentiment(posts []analyzer.AnalyzedPost) (string, float64) {
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

	log.Printf("Sentiment analysis: Average compound score: %.3f, Net sentiment: %.1f%%, Category: %s",
		averageCompoundScore, netSentimentPercentage, sentimentCategory)

	return sentimentCategory, netSentimentPercentage
}
