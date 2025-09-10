package formatter

import (
	"fmt"
)

// Post represents a post for formatting
type Post struct {
	URI             string
	CID             string
	Author          string
	Likes           int
	Reposts         int
	Replies         int
	Sentiment       string
	EngagementScore float64
}

// FormatPostContent generates the post content that will be posted to Bluesky
func FormatPostContent(topPosts []Post, overallSentiment string, analysisIntervalMinutes int, totalPosts int, averageCompoundScore float64) string {
	// Scale compound score to percentage range for 100-word system
	// Vader compound score: -1.0 to +1.0
	// Scale to percentage: -100% to +100%
	netSentiment := averageCompoundScore * 100.0

	// Get descriptive word for sentiment using 100-word scale with normal curve
	moodWord := getMoodWord100(netSentiment)

	// Generate the post content with new format (mood word as hashtag + debug info)
	// Always show + or - sign for sentiment percentage
	var sentimentSign string
	if netSentiment >= 0 {
		sentimentSign = "+"
	} else {
		sentimentSign = "-"
	}
	content := fmt.Sprintf("Bluesky is #%s\n%s%.1f%% sentiment\n\n", moodWord, sentimentSign, netSentiment)

	for i, post := range topPosts {
		sentimentSymbol := getSentimentSymbol(post.Sentiment)

		// Just show the handle and sentiment - facets will handle the linking
		content += fmt.Sprintf("%d. @%s %s\n", i+1, post.Author, sentimentSymbol)
	}

	return content
}

// getSentimentSymbol returns the symbol for sentiment (+ for positive, - for negative, x for neutral)
func getSentimentSymbol(sentiment string) string {
	switch sentiment {
	case "positive":
		return "+"
	case "negative":
		return "-"
	case "neutral":
		return "x"
	default:
		return "x" // fallback to neutral
	}
}
