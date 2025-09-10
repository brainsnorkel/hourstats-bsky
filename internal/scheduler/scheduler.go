package scheduler

import (
	"log"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
)

type Scheduler struct {
	client   *client.BlueskyClient
	analyzer *analyzer.SentimentAnalyzer
	config   *config.Config
}

func New(handle, password string, cfg *config.Config) *Scheduler {
	blueskyClient := client.New(handle, password)
	sentimentAnalyzer := analyzer.New()

	return &Scheduler{
		client:   blueskyClient,
		analyzer: sentimentAnalyzer,
		config:   cfg,
	}
}

func (s *Scheduler) Start() error {
	// Authenticate with Bluesky
	if err := s.client.Authenticate(); err != nil {
		return err
	}

	log.Println("Successfully authenticated with Bluesky")

	// Start the hourly ticker
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run immediately on startup
	if err := s.runAnalysis(); err != nil {
		log.Printf("Error in initial analysis: %v", err)
	}

	// Run every hour
	for range ticker.C {
		if err := s.runAnalysis(); err != nil {
			log.Printf("Error in scheduled analysis: %v", err)
		}
	}

	// This return will never be reached due to the infinite loop above
	return nil
}

func (s *Scheduler) runAnalysis() error {
	log.Println("Starting trend analysis...")

	// Fetch trending posts
	clientPosts, err := s.client.GetTrendingPosts(s.config.Settings.AnalysisIntervalMinutes)
	if err != nil {
		return err
	}

	// Convert client posts to analyzer posts
	analyzerPosts := s.convertToAnalyzerPosts(clientPosts)

	// Analyze sentiment and extract topics
	analyzedPosts, err := s.analyzer.AnalyzePosts(analyzerPosts)
	if err != nil {
		return err
	}

	// Get top 5 posts
	topPosts := s.GetTopPosts(analyzedPosts, 5)

	// Calculate overall sentiment from all analyzed posts using compound scores
	overallSentiment, netSentimentPercentage := s.CalculateOverallSentiment(analyzedPosts)
	totalPosts := len(analyzedPosts)

	// Convert back to client posts for posting
	clientTopPosts := s.convertToClientPosts(topPosts)

	// Post the results
	_, _, err = s.client.PostTrendingSummary(clientTopPosts, overallSentiment, s.config.Settings.AnalysisIntervalMinutes, totalPosts, netSentimentPercentage)
	if err != nil {
		return err
	}

	log.Printf("Successfully posted trending summary with %d posts", len(clientTopPosts))

	return nil
}

func (s *Scheduler) convertToAnalyzerPosts(clientPosts []client.Post) []analyzer.Post {
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

func (s *Scheduler) convertToClientPosts(analyzedPosts []analyzer.AnalyzedPost) []client.Post {
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

func (s *Scheduler) CalculateOverallSentiment(posts []analyzer.AnalyzedPost) (string, float64) {
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

func (s *Scheduler) GetTopPosts(posts []analyzer.AnalyzedPost, count int) []analyzer.AnalyzedPost {
	// Sort by engagement score (replies + likes + reposts + sentiment boost)
	// This matches the README specification for ranking posts

	// Log engagement scores for debugging
	log.Printf("Engagement scores before sorting:")
	for i, post := range posts {
		if i < 10 { // Log first 10 posts
			log.Printf("  @%s: likes=%d, reposts=%d, replies=%d, engagement_score=%.2f",
				post.Author, post.Likes, post.Reposts, post.Replies, post.EngagementScore)
		}
	}

	// Sort posts by engagement score in descending order
	for i := 0; i < len(posts)-1; i++ {
		for j := i + 1; j < len(posts); j++ {
			if posts[i].EngagementScore < posts[j].EngagementScore {
				posts[i], posts[j] = posts[j], posts[i]
			}
		}
	}

	// Log top posts after sorting
	log.Printf("Top %d posts after sorting by engagement score:", count)
	for i, post := range posts {
		if i < count {
			log.Printf("  %d. @%s: likes=%d, reposts=%d, replies=%d, engagement_score=%.2f",
				i+1, post.Author, post.Likes, post.Reposts, post.Replies, post.EngagementScore)
		}
	}

	// Return the top N posts
	if len(posts) < count {
		return posts
	}
	return posts[:count]
}
