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

	// Calculate overall sentiment from all analyzed posts
	overallSentiment := s.CalculateOverallSentiment(analyzedPosts)

	// Calculate sentiment percentages from all analyzed posts
	positiveCount := 0
	negativeCount := 0
	for _, post := range analyzedPosts {
		switch post.Sentiment {
		case "positive":
			positiveCount++
		case "negative":
			negativeCount++
		}
	}
	totalPosts := len(analyzedPosts)
	positivePercent := float64(positiveCount) / float64(totalPosts) * 100
	negativePercent := float64(negativeCount) / float64(totalPosts) * 100

	// Convert back to client posts for posting
	clientTopPosts := s.convertToClientPosts(topPosts)

	// Post the results
	if err := s.client.PostTrendingSummary(clientTopPosts, overallSentiment, s.config.Settings.AnalysisIntervalMinutes, totalPosts, positivePercent, negativePercent); err != nil {
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

	// Log the sentiment distribution for debugging
	log.Printf("Sentiment distribution: %d positive, %d negative, %d neutral", positiveCount, negativeCount, neutralCount)

	// Determine overall sentiment based on which category has the most posts
	total := len(posts)
	if positiveCount > negativeCount && positiveCount > neutralCount {
		return s.getEmotionFromSentiment("positive", positiveCount, total)
	} else if negativeCount > positiveCount && negativeCount > neutralCount {
		return s.getEmotionFromSentiment("negative", negativeCount, total)
	} else {
		return s.getEmotionFromSentiment("neutral", neutralCount, total)
	}
}

func (s *Scheduler) getEmotionFromSentiment(sentiment string, count int, total int) string {
	emotions := map[string][]string{
		"positive": {"passionate", "enthusiastic", "optimistic", "confident", "inspired", "excited", "hopeful", "energetic", "upbeat", "cheerful"},
		"negative": {"anxious", "pessimistic", "uncertain", "confused", "overwhelmed", "worried", "frustrated", "disappointed", "concerned", "troubled"},
		"neutral":  {"contemplative", "analytical", "curious", "observant", "reflective", "thoughtful", "measured", "balanced", "calm", "steady"},
	}

	// Calculate the percentage of posts with this sentiment
	percentage := float64(count) / float64(total) * 100

	// Select emotion based on intensity (percentage of posts)
	emotionList, exists := emotions[sentiment]
	if !exists || len(emotionList) == 0 {
		return "neutral"
	}

	// Choose emotion based on how dominant the sentiment is
	var selectedEmotion string
	if percentage >= 80 {
		// Very dominant sentiment - use strong emotions
		selectedEmotion = emotionList[0]
	} else if percentage >= 60 {
		// Moderately dominant - use middle emotions
		selectedEmotion = emotionList[len(emotionList)/2]
	} else {
		// Less dominant - use milder emotions
		selectedEmotion = emotionList[len(emotionList)-1]
	}

	log.Printf("Selected emotion '%s' for %s sentiment (%d/%d posts, %.1f%%)", selectedEmotion, sentiment, count, total, percentage)
	return selectedEmotion
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
