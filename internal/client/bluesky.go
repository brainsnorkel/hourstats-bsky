package client

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/atproto/client"
	"github.com/bluesky-social/indigo/lex/util"
	"github.com/christophergentle/hourstats-bsky/internal/formatter"
)

type Post struct {
	URI             string
	Text            string
	Author          string
	Likes           int
	Reposts         int
	Replies         int
	CreatedAt       string
	Sentiment       string // "positive", "negative", or "neutral"
	EngagementScore float64
}

type BlueskyClient struct {
	client   *client.APIClient
	handle   string
	password string
}

func New(handle, password string) *BlueskyClient {
	return &BlueskyClient{
		client:   client.NewAPIClient("https://bsky.social"),
		handle:   handle,
		password: password,
	}
}

func (c *BlueskyClient) Authenticate() error {
	ctx := context.Background()

	// Create an authenticated client
	authClient, err := client.LoginWithPasswordHost(ctx, "https://bsky.social", c.handle, c.password, "", nil)
	if err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	// Replace the client with the authenticated one
	c.client = authClient

	return nil
}

// GetTrendingPostsBatch fetches a single batch of posts using cursor-based pagination
func (c *BlueskyClient) GetTrendingPostsBatch(ctx context.Context, cursor string, cutoffTime time.Time) ([]Post, string, bool, error) {
	log.Printf("Fetching posts batch with cursor: %s", cursor)

	// Make the API request with retry logic
	var searchResult *bsky.FeedSearchPosts_Output
	var err error

	for retries := 0; retries < 3; retries++ {
		// Use Bluesky's official moderation labeler to get labels
		subscribedLabelers := []string{"did:plc:ar7c4by46qjd4h4ww4t5xvwa"}
		searchResult, err = bsky.FeedSearchPosts(ctx, c.client, "", cursor, "", "en", 100, "", "*", "", "", subscribedLabelers, "", "")
		if err == nil {
			break
		}

		// If it's a rate limit error, wait and retry
		if strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), "rate") {
			log.Printf("API rate limit hit, waiting 5 seconds before retry %d/3", retries+1)
			time.Sleep(5 * time.Second)
			continue
		}

		// For other errors, fail immediately
		return nil, "", false, fmt.Errorf("failed to search public posts: %w", err)
	}

	if err != nil {
		return nil, "", false, fmt.Errorf("failed to search public posts after 3 retries: %w", err)
	}

	// Convert to our Post format and filter by time
	var posts []Post
	var filteredCount int
	log.Printf("🔍 FETCHER DEBUG: Processing %d posts from API, cutoff time: %s", len(searchResult.Posts), cutoffTime.Format("2006-01-02 15:04:05 UTC"))

	for _, postView := range searchResult.Posts {
		// Filter posts by creation time
		postTime, err := time.Parse(time.RFC3339, postView.IndexedAt)
		if err != nil {
			log.Printf("⚠️ FETCHER DEBUG: Skipping post with invalid timestamp: %s", postView.IndexedAt)
			continue // Skip posts with invalid timestamps
		}

		// Only include posts from the analysis interval
		if postTime.Before(cutoffTime) {
			filteredCount++
			continue
		}

		// Handle pointer fields safely
		var author string
		if postView.Author != nil {
			author = postView.Author.Handle
		}

		var text string
		if postView.Record != nil {
			if feedPost, ok := postView.Record.Val.(*bsky.FeedPost); ok {
				text = feedPost.Text
			}
		}

		// Count engagement metrics - using correct lowercase field names
		likes := 0
		if postView.LikeCount != nil {
			likes = int(*postView.LikeCount)
		}

		reposts := 0
		if postView.RepostCount != nil {
			reposts = int(*postView.RepostCount)
		}

		replies := 0
		if postView.ReplyCount != nil {
			replies = int(*postView.ReplyCount)
		}

		// Debug logging for first few posts to see actual engagement data
		if len(posts) < 5 {
			log.Printf("🔍 BLUESKY DEBUG: Post %d - Author: %s, Likes: %d, Reposts: %d, Replies: %d",
				len(posts)+1, author, likes, reposts, replies)
			log.Printf("🔍 BLUESKY DEBUG: Raw pointers - LikeCount: %v (%T), RepostCount: %v (%T), ReplyCount: %v (%T)",
				postView.LikeCount, postView.LikeCount, postView.RepostCount, postView.RepostCount, postView.ReplyCount, postView.ReplyCount)
			if postView.LikeCount != nil {
				log.Printf("🔍 BLUESKY DEBUG: LikeCount value: %d", *postView.LikeCount)
			}
			if postView.RepostCount != nil {
				log.Printf("🔍 BLUESKY DEBUG: RepostCount value: %d", *postView.RepostCount)
			}
			if postView.ReplyCount != nil {
				log.Printf("🔍 BLUESKY DEBUG: ReplyCount value: %d", *postView.ReplyCount)
			}

			// Debug: Check if there are other engagement fields available
			log.Printf("🔍 BLUESKY DEBUG: PostView fields - IndexedAt: %s, Uri: %s", postView.IndexedAt, postView.Uri)
			if postView.Author != nil {
				log.Printf("🔍 BLUESKY DEBUG: Author: %s", postView.Author.Handle)
			}
			// Check if there are other count fields we're missing
			log.Printf("🔍 BLUESKY DEBUG: PostView struct type: %T", postView)
		}

		post := Post{
			URI:       postView.Uri,
			Text:      text,
			Author:    author,
			Likes:     likes,
			Reposts:   reposts,
			Replies:   replies,
			CreatedAt: postTime.Format(time.RFC3339),
		}

		posts = append(posts, post)
	}

	// Extract next cursor and determine if there are more posts
	nextCursor := ""
	hasMorePosts := false
	if searchResult.Cursor != nil && *searchResult.Cursor != "" {
		nextCursor = *searchResult.Cursor
		hasMorePosts = true
	}

	// Check if we've reached the time period boundary
	// If we have posts and the oldest post is before the cutoff time, we should stop
	if len(posts) > 0 {
		// Find the oldest post in this batch (posts are sorted by most recent first)
		oldestPost := posts[len(posts)-1]
		oldestPostTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt)
		if err == nil {
			// If the oldest post is before our cutoff time, we've gone past the time period
			if oldestPostTime.Before(cutoffTime) {
				log.Printf("Reached time period boundary - oldest post (%s) is before cutoff (%s), stopping pagination",
					oldestPostTime.Format("2006-01-02 15:04:05"), cutoffTime.Format("2006-01-02 15:04:05"))
				hasMorePosts = false
			}
		}
	}

	log.Printf("🔍 FETCHER DEBUG: Filtered out %d posts (too old), kept %d posts", filteredCount, len(posts))
	log.Printf("Retrieved %d posts from batch (cursor: %s, nextCursor: %s, hasMore: %v)", len(posts), cursor, nextCursor, hasMorePosts)
	return posts, nextCursor, hasMorePosts, nil
}

func (c *BlueskyClient) GetTrendingPosts(analysisIntervalMinutes int) ([]Post, error) {
	ctx := context.Background()

	// Calculate the cutoff time for posts to consider
	cutoffTime := time.Now().Add(-time.Duration(analysisIntervalMinutes) * time.Minute)
	sinceTime := cutoffTime.UTC().Format(time.RFC3339)
	log.Printf("Searching all public posts from the last %d minutes (since %s, UTC: %s)", analysisIntervalMinutes, cutoffTime.Format("2006-01-02 15:04:05"), sinceTime)

	// Search for all public posts - we'll do client-side time filtering
	// Using search API to get all public posts, not just followed accounts
	// Use pagination to get more posts
	var allPosts []*bsky.FeedDefs_PostView
	var cursor string
	totalRetrieved := 0

	// Paginate through results, stopping when we hit posts older than our analysis window
	for {
		// Make the API request with retry logic
		var searchResult *bsky.FeedSearchPosts_Output
		var err error

		for retries := 0; retries < 3; retries++ {
			// Use Bluesky's official moderation labeler to get labels
			subscribedLabelers := []string{"did:plc:ar7c4by46qjd4h4ww4t5xvwa"}
			searchResult, err = bsky.FeedSearchPosts(ctx, c.client, "", cursor, "", "en", 100, "", "*", "", "", subscribedLabelers, "", "")
			if err == nil {
				break
			}

			// If it's a rate limit error, wait and retry
			if strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), "rate") {
				log.Printf("API rate limit hit, waiting 5 seconds before retry %d/3", retries+1)
				time.Sleep(5 * time.Second)
				continue
			}

			// For other errors, fail immediately
			return nil, fmt.Errorf("failed to search public posts: %w", err)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to search public posts after 3 retries: %w", err)
		}

		// Check if the oldest post in this batch is still within our time window
		// Since posts are sorted by most recent first, we check the last post in the batch
		hasRecentPosts := false
		if len(searchResult.Posts) > 0 {
			// Check the last (oldest) post in this batch
			lastPost := searchResult.Posts[len(searchResult.Posts)-1]
			postTime, err := time.Parse(time.RFC3339, lastPost.IndexedAt)
			if err == nil {
				// Convert both times to UTC for comparison
				postTimeUTC := postTime.UTC()
				cutoffTimeUTC := cutoffTime.UTC()
				// Log the timestamp of the oldest post in this batch
				log.Printf("Oldest post in batch: %s UTC (cutoff: %s UTC)", postTimeUTC.Format("2006-01-02 15:04:05"), cutoffTimeUTC.Format("2006-01-02 15:04:05"))
				if !postTimeUTC.Before(cutoffTimeUTC) {
					hasRecentPosts = true
				}
			}
		}

		allPosts = append(allPosts, searchResult.Posts...)
		totalRetrieved += len(searchResult.Posts)

		// Log progress
		if searchResult.HitsTotal != nil {
			log.Printf("Retrieved %d/%d posts from Bluesky search", totalRetrieved, *searchResult.HitsTotal)
		} else {
			log.Printf("Retrieved %d posts from Bluesky search", totalRetrieved)
		}

		// Stop if no recent posts in this batch (posts are getting too old)
		if !hasRecentPosts {
			log.Printf("No recent posts found in this batch, stopping pagination")
			break
		}

		// Check if we have more pages
		if searchResult.Cursor == nil || *searchResult.Cursor == "" {
			break
		}
		cursor = *searchResult.Cursor

		// Continue pagination until we've collected all posts from the time period
		// The time-based cutoff and cursor pagination will naturally limit collection
	}

	log.Printf("Retrieved %d total public posts from Bluesky search", len(allPosts))

	// Deduplicate posts by URI to prevent same posts appearing multiple times
	seenURIs := make(map[string]bool)
	var posts []Post
	for _, postView := range allPosts {
		// Skip if we've already seen this post
		if seenURIs[postView.Uri] {
			continue
		}
		seenURIs[postView.Uri] = true

		// Filter posts by creation time (client-side filtering)
		postTime, err := time.Parse(time.RFC3339, postView.IndexedAt)
		if err != nil {
			continue // Skip posts with invalid timestamps
		}

		// Only include posts from the analysis interval
		if postTime.Before(cutoffTime) {
			continue
		}

		// Handle pointer fields safely
		var likes, reposts, replies int
		if postView.LikeCount != nil {
			likes = int(*postView.LikeCount)
		}
		if postView.RepostCount != nil {
			reposts = int(*postView.RepostCount)
		}
		if postView.ReplyCount != nil {
			replies = int(*postView.ReplyCount)
		}

		// Extract the actual post text from the record
		text := "No text available"
		if postView.Record != nil {
			// Try to cast the record to FeedPost type
			if feedPost, ok := postView.Record.Val.(*bsky.FeedPost); ok {
				text = feedPost.Text
			}
		}

		// Fallback to author if no text found
		if text == "No text available" {
			text = fmt.Sprintf("Post by @%s", postView.Author.Handle)
		}

		// Check for adult content labels
		hasAdultLabel := c.hasAdultContentLabel(postView.Labels)
		if hasAdultLabel {
			log.Printf("Filtering out adult content post by @%s (labels: %v)", postView.Author.Handle, postView.Labels)
			continue // Skip this post
		}

		post := Post{
			URI:       postView.Uri,
			Text:      text,
			Author:    postView.Author.Handle,
			Likes:     likes,
			Reposts:   reposts,
			Replies:   replies,
			CreatedAt: postView.IndexedAt,
		}
		posts = append(posts, post)
	}

	log.Printf("Found %d public posts from the last %d minutes", len(posts), analysisIntervalMinutes)
	return posts, nil
}

func (c *BlueskyClient) PostTrendingSummary(posts []Post, overallSentiment string, analysisIntervalMinutes int) error {
	ctx := context.Background()

	// Convert client posts to formatter posts
	formatterPosts := make([]formatter.Post, len(posts))
	for i, post := range posts {
		formatterPosts[i] = formatter.Post{
			Author:          post.Author,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			Sentiment:       post.Sentiment,
			EngagementScore: post.EngagementScore,
		}
	}

	// Calculate sentiment percentages
	positiveCount := 0
	negativeCount := 0
	for _, post := range posts {
		switch post.Sentiment {
		case "positive":
			positiveCount++
		case "negative":
			negativeCount++
		}
	}
	totalPosts := len(posts)
	positivePercent := float64(positiveCount) / float64(totalPosts) * 100
	negativePercent := float64(negativeCount) / float64(totalPosts) * 100

	// Use shared formatter to generate the post content
	summaryText := formatter.FormatPostContent(formatterPosts, overallSentiment, analysisIntervalMinutes, totalPosts, positivePercent, negativePercent)

	// Check if we need to truncate, but try to keep all 5 posts
	if len([]rune(summaryText)) > 300 {
		// If still too long, truncate but preserve the structure
		summaryText = truncateText(summaryText, 300)
	}

	// Post to Bluesky
	log.Printf("Posting to Bluesky: %s", summaryText)

	// Create facets for clickable links
	facets := createLinkFacets(summaryText, posts)

	// Create the post using the AT Protocol
	postRecord := &bsky.FeedPost{
		Text:      summaryText,
		CreatedAt: time.Now().Format(time.RFC3339),
		Facets:    facets,
	}

	// Post the record
	_, err := atproto.RepoCreateRecord(ctx, c.client, &atproto.RepoCreateRecord_Input{
		Repo:       c.handle, // Use the handle from the client
		Collection: "app.bsky.feed.post",
		Record:     &util.LexiconTypeDecoder{Val: postRecord},
	})

	if err != nil {
		return fmt.Errorf("failed to post to Bluesky: %w", err)
	}

	log.Printf("Successfully posted to Bluesky!")
	return nil
}

// convertATURItoWebURL converts an AT Protocol URI to a web-friendly URL
// Example: at://did:plc:abc123/app.bsky.feed.post/xyz789 -> https://bsky.app/profile/did:plc:abc123/post/xyz789
func convertATURItoWebURL(uri string) string {
	// Handle AT Protocol URIs
	if strings.HasPrefix(uri, "at://") {
		// Remove the at:// prefix
		uri = strings.TrimPrefix(uri, "at://")

		// Split by / to get parts: [did, app.bsky.feed.post, recordId]
		parts := strings.Split(uri, "/")
		if len(parts) >= 3 {
			did := parts[0]
			recordType := parts[1]
			recordId := parts[2]

			// Convert to web URL format
			if recordType == "app.bsky.feed.post" {
				return fmt.Sprintf("https://bsky.app/profile/%s/post/%s", did, recordId)
			}
		}
	}

	// If it's already a web URL or we can't parse it, return as-is
	return uri
}

func truncateText(text string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}
	return text[:maxLength-3] + "..."
}

// createLinkFacets creates rich text facets for URLs in the text
// Based on Bluesky rich text documentation: https://docs.bsky.app/docs/advanced-guides/post-richtext
func createLinkFacets(text string, posts []Post) []*bsky.RichtextFacet {
	var facets []*bsky.RichtextFacet

	// Find @handle patterns and make them clickable links to the posts
	// Match any handle format (bsky.social, custom domains, etc.)
	handleRegex := regexp.MustCompile(`@([a-zA-Z0-9._-]+\.[a-zA-Z0-9._-]+)`)
	matches := handleRegex.FindAllStringSubmatchIndex(text, -1)

	for i, match := range matches {
		if i >= len(posts) || i >= 5 { // Safety check
			break
		}

		start, end := match[0], match[1]

		// Get the corresponding post URL
		postIndex := i
		if postIndex < len(posts) {
			webURL := convertATURItoWebURL(posts[postIndex].URI)

			facet := &bsky.RichtextFacet{
				Index: &bsky.RichtextFacet_ByteSlice{
					ByteStart: int64(start),
					ByteEnd:   int64(end),
				},
				Features: []*bsky.RichtextFacet_Features_Elem{
					{
						RichtextFacet_Link: &bsky.RichtextFacet_Link{
							Uri: webURL,
						},
					},
				},
			}
			facets = append(facets, facet)
		}
	}

	return facets
}

// hasAdultContentLabel checks if a post has adult content labels
func (c *BlueskyClient) hasAdultContentLabel(labels []*atproto.LabelDefs_Label) bool {
	if labels == nil {
		return false
	}

	// Adult content label values from Bluesky moderation
	adultLabels := []string{"porn", "sexual", "nudity", "graphic-media"}

	for _, label := range labels {
		for _, adultLabel := range adultLabels {
			if label.Val == adultLabel {
				return true
			}
		}
	}

	return false
}

// PostText posts a simple text message to Bluesky
func (c *BlueskyClient) PostText(ctx context.Context, text string) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	// Create a simple text post
	postRecord := &bsky.FeedPost{
		Text:      text,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	// Post the record using the AT Protocol
	_, err := atproto.RepoCreateRecord(ctx, c.client, &atproto.RepoCreateRecord_Input{
		Repo:       c.handle, // Use the handle from the client
		Collection: "app.bsky.feed.post",
		Record:     &util.LexiconTypeDecoder{Val: postRecord},
	})

	if err != nil {
		return fmt.Errorf("failed to post to Bluesky: %w", err)
	}

	log.Printf("Successfully posted to Bluesky: %s", text[:min(50, len(text))])
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
