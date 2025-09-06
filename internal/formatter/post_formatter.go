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
func FormatPostContent(topPosts []Post, overallSentiment string, analysisIntervalMinutes int, totalPosts int, positivePercent, negativePercent float64) string {
	// Calculate net sentiment (positive - negative)
	netSentiment := positivePercent - negativePercent

	// Get descriptive word for sentiment using 100-word scale
	moodWord := getMoodWord100(netSentiment)

	// Generate the post content with new format (mood word as hashtag)
	content := fmt.Sprintf("Bluesky is #%s\n\n", moodWord)

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
