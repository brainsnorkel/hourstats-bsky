package analyzer

import (
	"fmt"
	"strings"

	"github.com/jonreiter/govader"
)

type AnalyzedPost struct {
	Post
	Sentiment       string
	SentimentScore  float64
	Topics          []string
	EngagementScore float64
}

// Post represents a social media post for analysis
type Post struct {
	URI       string
	CID       string
	Text      string
	Author    string
	Likes     int
	Reposts   int
	Replies   int
	CreatedAt string
	IsReply   bool
}

type SentimentAnalyzer struct {
	analyzer *govader.SentimentIntensityAnalyzer
}

func New() *SentimentAnalyzer {
	return &SentimentAnalyzer{
		analyzer: govader.NewSentimentIntensityAnalyzer(),
	}
}

func (sa *SentimentAnalyzer) AnalyzePosts(posts []Post) ([]AnalyzedPost, error) {
	var analyzedPosts []AnalyzedPost

	for _, post := range posts {
		analyzedPost, err := sa.analyzePost(post)
		if err != nil {
			return nil, fmt.Errorf("failed to analyze post %s: %w", post.URI, err)
		}
		analyzedPosts = append(analyzedPosts, analyzedPost)
	}

	return analyzedPosts, nil
}

func (sa *SentimentAnalyzer) analyzePost(post Post) (AnalyzedPost, error) {
	sentiment := sa.analyzer.PolarityScores(post.Text)
	sentimentCategory := sa.categorizeSentiment(sentiment)
	topics := sa.extractTopics(post.Text)
	engagementScore := sa.calculateEngagementScore(post, sentiment.Compound)

	return AnalyzedPost{
		Post:            post,
		Sentiment:       sentimentCategory,
		SentimentScore:  sentiment.Compound,
		Topics:          topics,
		EngagementScore: engagementScore,
	}, nil
}

func (sa *SentimentAnalyzer) categorizeSentiment(sentiment govader.Sentiment) string {
	compound := sentiment.Compound

	// Use more nuanced thresholds for better emotion detection
	// Adjusted thresholds to better handle neutral language like "okay"
	if compound >= 0.3 {
		return "positive"
	} else if compound <= -0.3 {
		return "negative"
	}
	return "neutral"
}

func (sa *SentimentAnalyzer) extractTopics(text string) []string {
	// Simple topic extraction based on hashtags and common keywords
	// In a more sophisticated implementation, we'd use NLP libraries
	// or machine learning models for better topic extraction

	// Clean the text and split into words
	cleaned := strings.ToLower(text)
	words := strings.Fields(cleaned)
	var topics []string

	// Extract common topic keywords (simplified)
	topicKeywords := map[string]string{
		"tech":     "technology",
		"ai":       "artificial intelligence",
		"crypto":   "cryptocurrency",
		"climate":  "climate change",
		"politics": "politics",
		"news":     "news",
		"music":    "music",
		"art":      "art",
		"science":  "science",
		"health":   "health",
	}

	// Extract hashtags and their keyword equivalents
	for _, word := range words {
		if strings.HasPrefix(word, "#") {
			topics = append(topics, word)
			// Also check if the hashtag content matches a keyword
			hashtagContent := strings.TrimLeft(word, "#")
			cleanHashtag := strings.TrimRight(hashtagContent, ".,!?;:")
			if topic, exists := topicKeywords[cleanHashtag]; exists {
				topics = append(topics, topic)
			}
		}
	}

	for _, word := range words {
		// Remove punctuation from the end of words
		cleanWord := strings.TrimRight(word, ".,!?;:")
		if topic, exists := topicKeywords[cleanWord]; exists {
			topics = append(topics, topic)
		}
	}

	// Remove duplicates
	seen := make(map[string]bool)
	var uniqueTopics []string
	for _, topic := range topics {
		if !seen[topic] {
			seen[topic] = true
			uniqueTopics = append(uniqueTopics, topic)
		}
	}
	topics = uniqueTopics

	return topics
}

func (sa *SentimentAnalyzer) calculateEngagementScore(post Post, sentimentScore float64) float64 {
	// Engagement score calculation based on replies + likes + reposts
	// This matches the README specification for ranking posts

	return float64(post.Replies + post.Likes + post.Reposts)
}
