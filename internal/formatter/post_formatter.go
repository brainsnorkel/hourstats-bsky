package formatter

import (
	"fmt"
)

// Post represents a post for formatting
type Post struct {
	Author          string
	Likes           int
	Reposts         int
	Replies         int
	Sentiment       string
	EngagementScore float64
}

// FormatPostContent generates the post content that will be posted to Bluesky
func FormatPostContent(topPosts []Post, overallSentiment string, analysisIntervalMinutes int, totalPosts int, positivePercent, negativePercent float64) string {
	// Format time period
	timePeriod := formatTimePeriod(analysisIntervalMinutes)

	// Generate the post content with new format
	content := fmt.Sprintf("For the last %s I found %d posts with sentiment +%.0f%% -%.0f%%\n\n", 
		timePeriod, totalPosts, positivePercent, negativePercent)
	
	content += "Top engagement:\n"

	for i, post := range topPosts {
		engagementScore := int(post.Likes + post.Reposts + post.Replies)
		sentimentSymbol := getSentimentSymbol(post.Sentiment)
		content += fmt.Sprintf("%d. @%s (%d) %s\n", i+1, post.Author, engagementScore, sentimentSymbol)
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
