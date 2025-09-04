package scheduler

import (
	"log"
	"time"

	"github.com/christophergentle/trendjournal/internal/analyzer"
	"github.com/christophergentle/trendjournal/internal/client"
	"github.com/christophergentle/trendjournal/internal/config"
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
	for {
		select {
		case <-ticker.C:
			if err := s.runAnalysis(); err != nil {
				log.Printf("Error in scheduled analysis: %v", err)
			}
		}
	}
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

	// Calculate overall sentiment from top posts
	overallSentiment := s.CalculateOverallSentiment(topPosts)

	// Convert back to client posts for posting
	clientTopPosts := s.convertToClientPosts(topPosts)

	// Post the results
	if err := s.client.PostTrendingSummary(clientTopPosts, overallSentiment, s.config.Settings.AnalysisIntervalMinutes); err != nil {
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
		})
	}
	return clientPosts
}

func (s *Scheduler) CalculateOverallSentiment(posts []analyzer.AnalyzedPost) string {
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

	// Determine overall sentiment
	total := len(posts)
	if positiveCount > total/2 {
		return s.getEmotionFromSentiment("positive")
	} else if negativeCount > total/2 {
		return s.getEmotionFromSentiment("negative")
	} else {
		return s.getEmotionFromSentiment("neutral")
	}
}

func (s *Scheduler) getEmotionFromSentiment(sentiment string) string {
	emotions := map[string][]string{
		"positive": {"passionate", "enthusiastic", "optimistic", "confident", "inspired"},
		"negative": {"anxious", "pessimistic", "uncertain", "confused", "overwhelmed"},
		"neutral":  {"contemplative", "analytical", "curious", "observant", "reflective"},
	}

	// For now, return the first emotion in each category
	// In a more sophisticated implementation, we could randomize or use more context
	if emotionList, exists := emotions[sentiment]; exists && len(emotionList) > 0 {
		return emotionList[0]
	}
	return "neutral"
}

func (s *Scheduler) GetTopPosts(posts []analyzer.AnalyzedPost, count int) []analyzer.AnalyzedPost {
	// Sort by engagement score (replies + likes + reposts + sentiment boost)
	// This matches the README specification for ranking posts

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
