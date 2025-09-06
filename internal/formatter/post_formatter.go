package formatter

import (
	"fmt"
	"strings"
)

// Post represents a post for formatting
type Post struct {
	URI             string
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

	// Format time period
	timePeriod := formatTimePeriod(analysisIntervalMinutes)

	// Generate the post content with new format
	content := fmt.Sprintf("Bluesky mood %+.0f%% from %d posts in %s\n\n",
		netSentiment, totalPosts, timePeriod)

	for i, post := range topPosts {
		engagementScore := int(post.Likes + post.Reposts + post.Replies)
		sentimentSymbol := getSentimentSymbol(post.Sentiment)

		// Just show the handle, engagement, and sentiment - facets will handle the linking
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

// convertATURItoWebURL converts an AT Protocol URI to a web-friendly URL
// Example: at://did:plc:abc123/app.bsky.feed.post/xyz789 -> https://bsky.app/profile/did:plc:abc123/post/xyz789
func convertATURItoWebURL(uri string) string {
	// Handle AT Protocol URIs
	if strings.HasPrefix(uri, "at://") {
		// Remove the at:// prefix
		uri = strings.TrimPrefix(uri, "at://")

		// Split by / to get the components
		parts := strings.Split(uri, "/")
		if len(parts) >= 3 {
			did := parts[0]
			postID := parts[2]
			return fmt.Sprintf("https://bsky.app/profile/%s/post/%s", did, postID)
		}
	}

	// If it's not a valid AT Protocol URI, return as-is
	return uri
}
