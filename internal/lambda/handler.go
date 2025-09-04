package lambda

import (
	"context"
	"log"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
	"github.com/christophergentle/hourstats-bsky/internal/scheduler"
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

	// Calculate overall sentiment from top posts
	overallSentiment := h.calculateOverallSentiment(topPosts)

	// Convert back to client posts for posting
	clientTopPosts := h.convertToClientPosts(topPosts)

	// Post the results (skip if dry run)
	if !h.config.Settings.DryRun {
		if err := h.client.PostTrendingSummary(clientTopPosts, overallSentiment, h.config.Settings.AnalysisIntervalMinutes); err != nil {
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
func (h *HourStatsAnalyzer) calculateOverallSentiment(posts []analyzer.AnalyzedPost) string {
	if len(posts) == 0 {
		return "neutral"
	}

	// Count sentiment categories
	positiveCount := 0
	negativeCount := 0
	neutralCount := 0

	for _, post := range posts {
		switch post.Sentiment {
		case "positive":
			positiveCount++
		case "negative":
			negativeCount++
		case "neutral":
			neutralCount++
		}
	}

	// Determine dominant sentiment
	total := len(posts)
	if positiveCount > negativeCount && positiveCount > neutralCount {
		return h.getEmotionFromSentiment("positive", positiveCount, total)
	} else if negativeCount > positiveCount && negativeCount > neutralCount {
		return h.getEmotionFromSentiment("negative", negativeCount, total)
	} else {
		return h.getEmotionFromSentiment("neutral", neutralCount, total)
	}
}

// getEmotionFromSentiment selects an appropriate emotion based on sentiment and count
func (h *HourStatsAnalyzer) getEmotionFromSentiment(sentiment string, count, total int) string {
	percentage := float64(count) / float64(total) * 100

	// Define emotions based on sentiment and intensity
	positiveEmotions := []string{"passionate", "excited", "thrilled", "enthusiastic", "optimistic", "hopeful", "cheerful", "upbeat", "vibrant", "energetic"}
	negativeEmotions := []string{"concerned", "worried", "frustrated", "disappointed", "anxious", "troubled", "uneasy", "pessimistic", "gloomy", "melancholy"}
	neutralEmotions := []string{"steady", "calm", "balanced", "composed", "stable", "measured", "collected", "serene", "tranquil", "peaceful"}

	var emotions []string
	switch sentiment {
	case "positive":
		emotions = positiveEmotions
	case "negative":
		emotions = negativeEmotions
	case "neutral":
		emotions = neutralEmotions
	default:
		return "neutral"
	}

	// Select emotion based on percentage dominance
	var selectedEmotion string
	if percentage >= 80 {
		selectedEmotion = emotions[0] // Strong emotion
	} else if percentage >= 60 {
		selectedEmotion = emotions[1] // Moderate emotion
	} else if percentage >= 40 {
		selectedEmotion = emotions[2] // Mild emotion
	} else {
		selectedEmotion = emotions[3] // Very mild emotion
	}

	log.Printf("Selected emotion '%s' for %s sentiment (%d/%d posts, %.1f%%)", selectedEmotion, sentiment, count, total, percentage)
	return selectedEmotion
}
