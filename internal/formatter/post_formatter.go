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
	// QuoteControlled marks a post whose author disabled quoting. Only the
	// first listed post is checked, and only it carries quoteControlNote.
	QuoteControlled bool
}

// quoteControlNote is appended to the first listed post when its author
// disabled quoting, explaining why the summary carries no quote embed.
const quoteControlNote = " · no embed, post is quote controlled"

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
//
// When the first post is QuoteControlled its line gains quoteControlNote,
// because the summary cannot quote-embed it.
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
		content += fmt.Sprintf("%d. @%s", i+1, post.Author)
		if i == 0 && post.QuoteControlled {
			content += quoteControlNote
		}
		content += "\n"
	}

	return content
}
