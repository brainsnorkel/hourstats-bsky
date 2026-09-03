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

// FormatPostContent generates the post content that will be posted to Bluesky.
//
// Layout:
//
//	Bluesky is #<mood> +X.X% sentiment
//
//	Top recent posts
//	1. @handle
//	2. @handle
//	3. @handle
func FormatPostContent(topPosts []Post, overallSentiment string, analysisIntervalMinutes int, totalPosts int, netSentiment float64) string {
	// Get descriptive word for sentiment using 100-word scale with normal curve
	moodWord := getMoodWord100(netSentiment)

	// Show a + sign for positive sentiment; negatives carry their own sign
	var sentimentSign string
	if netSentiment > 0 {
		sentimentSign = "+"
	}
	content := fmt.Sprintf("Bluesky is #%s %s%.1f%% sentiment\n", moodWord, sentimentSign, netSentiment)

	if len(topPosts) == 0 {
		return content
	}

	content += "\nTop recent posts\n"
	for i, post := range topPosts {
		// Just show the handle - facets will handle the linking
		content += fmt.Sprintf("%d. @%s\n", i+1, post.Author)
	}

	return content
}
