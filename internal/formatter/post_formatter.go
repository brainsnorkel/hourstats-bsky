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

	// Get descriptive word for sentiment
	moodWord := getMoodWord(netSentiment)

	// Generate the post content with new format (mood word as hashtag)
	content := fmt.Sprintf("Bluesky is #%s\n\n", moodWord)

	for i, post := range topPosts {
		sentimentSymbol := getSentimentSymbol(post.Sentiment)

		// Just show the handle and sentiment - facets will handle the linking
		content += fmt.Sprintf("%d. @%s %s\n", i+1, post.Author, sentimentSymbol)
	}

	return content
}

// formatTimePeriod formats the analysis interval as a human-readable time period
func formatTimePeriod(analysisIntervalMinutes int) string {
	if analysisIntervalMinutes < 60 {
		return fmt.Sprintf("%d minutes", analysisIntervalMinutes)
	} else {
		hours := analysisIntervalMinutes / 60
		minutes := analysisIntervalMinutes % 60
		if minutes == 0 {
			if hours == 1 {
				return "1 hour"
			}
			return fmt.Sprintf("%d hours", hours)
		} else {
			if hours == 1 {
				return fmt.Sprintf("1 hour %d minutes", minutes)
			}
			return fmt.Sprintf("%d hours %d minutes", hours, minutes)
		}
	}
}

// getMoodWord maps sentiment percentage to a descriptive word
func getMoodWord(netSentiment float64) string {
	// Map sentiment percentage to descriptive words
	switch {
	case netSentiment >= 90:
		return "ecstatic"
	case netSentiment >= 80:
		return "thrilled"
	case netSentiment >= 70:
		return "excited"
	case netSentiment >= 60:
		return "optimistic"
	case netSentiment >= 50:
		return "hopeful"
	case netSentiment >= 40:
		return "cheerful"
	case netSentiment >= 30:
		return "content"
	case netSentiment >= 20:
		return "satisfied"
	case netSentiment >= 10:
		return "pleased"
	case netSentiment >= -10:
		return "neutral"
	case netSentiment >= -20:
		return "concerned"
	case netSentiment >= -30:
		return "worried"
	case netSentiment >= -40:
		return "disappointed"
	case netSentiment >= -50:
		return "frustrated"
	case netSentiment >= -60:
		return "upset"
	case netSentiment >= -70:
		return "angry"
	case netSentiment >= -80:
		return "distressed"
	case netSentiment >= -90:
		return "devastated"
	default:
		return "hopeless"
	}
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
