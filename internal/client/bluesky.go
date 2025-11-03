package client

import (
	"bytes"
	"context"
	"fmt"
	"log"
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
	CID             string
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
		// Search for all public posts - matching original working code (no sort, no since)
		// The API will return posts sorted by engagement (default), and we'll filter by time client-side
		log.Printf("Making API request with cursor: '%s' (default sort, no time filter)", cursor)
		searchResult, err = bsky.FeedSearchPosts(ctx, c.client, "", cursor, "", "en", 100, "", "*", "", "", nil, "", "")
		if err == nil {
			break
		}

		// If it's a rate limit error, wait and retry
		if strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), "rate") {
			log.Printf("API rate limit hit, waiting 5 seconds before retry %d/3", retries+1)
			time.Sleep(5 * time.Second)
			continue
		}

		// Check for timeout errors - these are retriable
		if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "timeout") {
			log.Printf("⚠️ API timeout detected (attempt %d/3): %v", retries+1, err)
			if retries < 2 {
				// Wait longer before retrying timeout errors
				waitTime := time.Duration(retries+1) * 10 * time.Second
				log.Printf("⏳ Waiting %v before retry...", waitTime)
				time.Sleep(waitTime)
				continue
			}
			// After 3 retries, if it's a timeout at a high cursor, skip this cursor
			if cursor != "" {
				var cursorNum int
				if _, parseErr := fmt.Sscanf(cursor, "%d", &cursorNum); parseErr == nil {
					if cursorNum > 8000 {
						log.Printf("⚠️ Timeout at high cursor %d, skipping this cursor and continuing", cursorNum)
						// Return empty result but indicate we should continue with next cursor
						return nil, "", true, nil
					}
				}
			}
			// For other timeouts, return error but fetcher can handle it
			return nil, "", false, fmt.Errorf("API request timed out after 3 retries: %w", err)
		}

		// Check for cursor pagination limits (HTTP 400 InvalidRequest)
		if strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "InvalidRequest") {
			log.Printf("HTTP 400 InvalidRequest error details: %+v", err)
			// Try to extract more details from the error
			if httpErr, ok := err.(interface{ Response() interface{} }); ok {
				log.Printf("HTTP Response details: %+v", httpErr.Response())
			}

			// Check if this might be a cursor pagination limit
			if cursor != "" {
				// Try to parse cursor as number to detect if we've hit a limit
				var cursorNum int
				if _, parseErr := fmt.Sscanf(cursor, "%d", &cursorNum); parseErr == nil {
					// If cursor is very high (>10000), likely hit pagination limit
					if cursorNum > 10000 {
						log.Printf("Likely hit cursor pagination limit at cursor %d, stopping gracefully", cursorNum)
						return nil, "", false, fmt.Errorf("cursor pagination limit reached at %d", cursorNum)
					}
				}
			}

			// For HTTP 400 errors that aren't timeouts, don't retry - likely a permanent issue
			return nil, "", false, fmt.Errorf("API request failed with HTTP 400: %w", err)
		}

		// Log detailed error information for debugging
		log.Printf("API request failed (attempt %d/3): %v", retries+1, err)

		// For other errors, fail immediately
		return nil, "", false, fmt.Errorf("failed to search public posts: %w", err)
	}

	if err != nil {
		return nil, "", false, fmt.Errorf("failed to search public posts after 3 retries: %w", err)
	}

	// DEBUG: Log API response details
	log.Printf("📊 API Response: Received %d posts from API (cursor: %s)", len(searchResult.Posts), cursor)
	if len(searchResult.Posts) > 0 {
		firstPost := searchResult.Posts[0]
		lastPost := searchResult.Posts[len(searchResult.Posts)-1]
		log.Printf("📊 First post IndexedAt: %s", firstPost.IndexedAt)
		log.Printf("📊 Last post IndexedAt: %s", lastPost.IndexedAt)
		log.Printf("📊 Cutoff time: %s", cutoffTime.Format(time.RFC3339))
		
		// Parse and compare timestamps
		if firstPost.IndexedAt != "" {
			firstTime, err := time.Parse(time.RFC3339, firstPost.IndexedAt)
			if err == nil {
				diff := firstTime.Sub(cutoffTime)
				log.Printf("📊 First post is %s %s the cutoff", 
					diff.Abs().Round(time.Second),
					map[bool]string{true: "after", false: "before"}[diff >= 0])
			}
		}
		if lastPost.IndexedAt != "" {
			lastTime, err := time.Parse(time.RFC3339, lastPost.IndexedAt)
			if err == nil {
				diff := lastTime.Sub(cutoffTime)
				log.Printf("📊 Last post is %s %s the cutoff", 
					diff.Abs().Round(time.Second),
					map[bool]string{true: "after", false: "before"}[diff >= 0])
			}
		}
	}

	// Convert to our Post format and filter by time
	var posts []Post
	var filteredCount int

	for _, postView := range searchResult.Posts {
		// Filter posts by creation time
		postTime, err := time.Parse(time.RFC3339, postView.IndexedAt)
		if err != nil {
			continue // Skip posts with invalid timestamps
		}

		// Only include posts from the analysis interval
		if postTime.Before(cutoffTime) {
			filteredCount++
			continue
		}

		// Filter out adult content based on moderation labels
		if c.hasAdultContentLabel(postView.Labels) {
			log.Printf("Filtering out post with adult content: %s", postView.Uri)
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

		// Construct proper AT Protocol URI
		uri := postView.Uri
		if !strings.HasPrefix(postView.Uri, "at://") && postView.Author != nil {
			// Try to construct AT Protocol URI from available data
			// Format: at://did:plc:abc123/app.bsky.feed.post/xyz789
			if postView.Author.Did != "" {
				// Use the original URI as the record ID if it's not already an AT Protocol URI
				// The API might return something like "post-123" or just "123"
				recordID := strings.TrimPrefix(postView.Uri, "post-")
				uri = fmt.Sprintf("at://%s/app.bsky.feed.post/%s", postView.Author.Did, recordID)
			}
		}

		// Extract CID from the postView
		cid := postView.Cid

		post := Post{
			URI:       uri,
			CID:       cid,
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
			// Search for all public posts - matching original working code (no sort, no since)
			log.Printf("Making API request with cursor: '%s' (default sort, no time filter)", cursor)
			searchResult, err = bsky.FeedSearchPosts(ctx, c.client, "", cursor, "", "en", 100, "", "*", "", "", nil, "", "")
			if err == nil {
				break
			}

			// If it's a rate limit error, wait and retry
			if strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), "rate") {
				log.Printf("API rate limit hit, waiting 5 seconds before retry %d/3", retries+1)
				time.Sleep(5 * time.Second)
				continue
			}

			// Check for cursor pagination limits (HTTP 400 InvalidRequest)
			if strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "InvalidRequest") {
				log.Printf("HTTP 400 InvalidRequest error details: %+v", err)
				// Try to extract more details from the error
				if httpErr, ok := err.(interface{ Response() interface{} }); ok {
					log.Printf("HTTP Response details: %+v", httpErr.Response())
				}

				// Check if this might be a cursor pagination limit
				if cursor != "" {
					// Try to parse cursor as number to detect if we've hit a limit
					var cursorNum int
					if _, parseErr := fmt.Sscanf(cursor, "%d", &cursorNum); parseErr == nil {
						// If cursor is very high (>10000), likely hit pagination limit
						if cursorNum > 10000 {
							log.Printf("Likely hit cursor pagination limit at cursor %d, stopping gracefully", cursorNum)
							return nil, fmt.Errorf("cursor pagination limit reached at %d", cursorNum)
						}
					}
				}

				// For HTTP 400 errors, don't retry - likely a permanent issue
				return nil, fmt.Errorf("API request failed with HTTP 400: %w", err)
			}

			// Log detailed error information for debugging
			log.Printf("API request failed (attempt %d/3): %v", retries+1, err)

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

	// Deduplicate posts by URI, keeping the one with higher engagement score
	uriToPost := make(map[string]Post)
	for _, postView := range allPosts {

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

		// Construct proper AT Protocol URI
		uri := postView.Uri
		if !strings.HasPrefix(postView.Uri, "at://") && postView.Author != nil {
			// Try to construct AT Protocol URI from available data
			// Format: at://did:plc:abc123/app.bsky.feed.post/xyz789
			if postView.Author.Did != "" {
				// Use the original URI as the record ID if it's not already an AT Protocol URI
				// The API might return something like "post-123" or just "123"
				recordID := strings.TrimPrefix(postView.Uri, "post-")
				uri = fmt.Sprintf("at://%s/app.bsky.feed.post/%s", postView.Author.Did, recordID)
			}
		}

		// Extract CID from the postView
		cid := postView.Cid

		post := Post{
			URI:       uri,
			CID:       cid,
			Text:      text,
			Author:    postView.Author.Handle,
			Likes:     likes,
			Reposts:   reposts,
			Replies:   replies,
			CreatedAt: postView.IndexedAt,
		}

		// Debug: Log URI format to understand what we're getting
		if !strings.HasPrefix(uri, "at://") {
			log.Printf("DEBUG: Non-standard URI format: %s for post by @%s (original: %s)", uri, postView.Author.Handle, postView.Uri)
		}

		// Check if we've seen this URI before (use the properly formatted URI)
		if existingPost, exists := uriToPost[uri]; exists {
			// Calculate engagement scores for comparison
			currentEngagement := likes + reposts + replies
			existingEngagement := existingPost.Likes + existingPost.Reposts + existingPost.Replies

			// Keep the post with higher engagement score
			if currentEngagement > existingEngagement {
				uriToPost[uri] = post
			}
		} else {
			// First time seeing this URI, add it
			uriToPost[uri] = post
		}
	}

	// Convert map values to slice
	var posts []Post
	for _, post := range uriToPost {
		posts = append(posts, post)
	}

	log.Printf("Found %d public posts from the last %d minutes", len(posts), analysisIntervalMinutes)
	return posts, nil
}

func (c *BlueskyClient) PostTrendingSummary(posts []Post, overallSentiment string, analysisIntervalMinutes int, totalPosts int, netSentimentPercentage float64) (string, string, error) {
	ctx := context.Background()

	// Convert client posts to formatter posts
	formatterPosts := make([]formatter.Post, len(posts))
	for i, post := range posts {
		formatterPosts[i] = formatter.Post{
			URI:             post.URI,
			CID:             post.CID,
			Author:          post.Author,
			Likes:           post.Likes,
			Reposts:         post.Reposts,
			Replies:         post.Replies,
			Sentiment:       post.Sentiment,
			EngagementScore: post.EngagementScore,
		}
	}

	// Use the pre-calculated sentiment data from all posts, not just the top 5

	// Use shared formatter to generate the post content
	summaryText := formatter.FormatPostContent(formatterPosts, overallSentiment, analysisIntervalMinutes, totalPosts, netSentimentPercentage)

	// Check if we need to truncate, but try to keep all 5 posts
	if len([]rune(summaryText)) > 300 {
		// If still too long, truncate but preserve the structure
		summaryText = truncateText(summaryText, 300)
	}

	// Post to Bluesky
	log.Printf("Posting to Bluesky: %s", summaryText)

	// Create facets for clickable links (user handles to posts)
	facets := createUserHandleFacets(summaryText, posts)

	// Create embed card for the first post if available (skip posts with invalid URIs)
	var embed *bsky.FeedPost_Embed
	if len(posts) > 0 {
		for _, post := range posts {
			if post.URI != "" && post.CID != "" && !strings.HasPrefix(post.URI, "at://post-") {
				log.Printf("Creating embed card for post: %s", post.URI)
				embed = c.createEmbedCard(ctx, post)
				if embed != nil {
					break
				}
			}
		}
	}

	// Create the post using the AT Protocol
	postRecord := &bsky.FeedPost{
		Text:      summaryText,
		CreatedAt: time.Now().Format(time.RFC3339),
		Facets:    facets,
		Embed:     embed,
	}

	// Post the record
	result, err := atproto.RepoCreateRecord(ctx, c.client, &atproto.RepoCreateRecord_Input{
		Repo:       c.handle, // Use the handle from the client
		Collection: "app.bsky.feed.post",
		Record:     &util.LexiconTypeDecoder{Val: postRecord},
	})

	if err != nil {
		return "", "", fmt.Errorf("failed to post to Bluesky: %w", err)
	}

	// Extract the posted URI and CID from the result
	postedURI := result.Uri
	postedCID := result.Cid

	log.Printf("Successfully posted to Bluesky! URI: %s, CID: %s", postedURI, postedCID)
	return postedURI, postedCID, nil
}

// createEmbedCard creates an embed card for a post
func (c *BlueskyClient) createEmbedCard(ctx context.Context, post Post) *bsky.FeedPost_Embed {
	if post.URI == "" || post.CID == "" {
		log.Printf("Cannot create embed card: missing URI (%s) or CID (%s)", post.URI, post.CID)
		return nil
	}

	log.Printf("Creating embed card for post: URI=%s, CID=%s", post.URI, post.CID)

	return &bsky.FeedPost_Embed{
		EmbedRecord: &bsky.EmbedRecord{
			Record: &atproto.RepoStrongRef{
				Uri: post.URI,
				Cid: post.CID,
			},
		},
	}
}

// createUserHandleFacets creates facets to link user handles to their posts and mood hashtag
func createUserHandleFacets(text string, posts []Post) []*bsky.RichtextFacet {
	var facets []*bsky.RichtextFacet

	// Create hashtag facet for mood word (e.g., #satisfied)
	if strings.HasPrefix(text, "Bluesky is #") {
		// Find the hashtag in the text
		hashtagStart := strings.Index(text, "#")
		if hashtagStart != -1 {
			// Find the end of the hashtag (end of line or space)
			hashtagEnd := strings.Index(text[hashtagStart:], "\n")
			if hashtagEnd == -1 {
				hashtagEnd = len(text)
			} else {
				hashtagEnd = hashtagStart + hashtagEnd
			}

			// Extract the hashtag text (without #)
			hashtagText := text[hashtagStart+1 : hashtagEnd]

			// Create hashtag facet
			hashtagFacet := &bsky.RichtextFacet{
				Index: &bsky.RichtextFacet_ByteSlice{
					ByteStart: int64(hashtagStart),
					ByteEnd:   int64(hashtagEnd),
				},
				Features: []*bsky.RichtextFacet_Features_Elem{
					{
						RichtextFacet_Tag: &bsky.RichtextFacet_Tag{
							Tag: hashtagText,
						},
					},
				},
			}

			facets = append(facets, hashtagFacet)
		}
	}

	// Create facets for each user handle linking to their post
	for _, post := range posts {
		if post.URI == "" {
			continue
		}

		// Find the handle in the text and create a facet
		handle := "@" + post.Author
		startIndex := strings.Index(text, handle)
		if startIndex == -1 {
			continue
		}

		endIndex := startIndex + len(handle)

		// Convert AT Protocol URI to web URL for clickable links
		webURL := convertATURItoWebURL(post.URI)

		facet := &bsky.RichtextFacet{
			Index: &bsky.RichtextFacet_ByteSlice{
				ByteStart: int64(startIndex),
				ByteEnd:   int64(endIndex),
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

	return facets
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
	return c.PostWithFacets(ctx, text, nil)
}

func (c *BlueskyClient) PostWithFacets(ctx context.Context, text string, facets []*bsky.RichtextFacet) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	// Create a text post with optional facets
	postRecord := &bsky.FeedPost{
		Text:      text,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	// Add facets if provided
	if facets != nil {
		postRecord.Facets = facets
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

// UploadImage uploads an image to Bluesky's blob service and returns the blob reference
func (c *BlueskyClient) UploadImage(ctx context.Context, imageData []byte, altText string) (*bsky.EmbedImages_Image, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	// Determine content type from image data
	contentType := "image/png" // Default for our sparkline images
	if len(imageData) > 4 {
		// Check PNG signature
		if imageData[0] == 0x89 && imageData[1] == 0x50 && imageData[2] == 0x4E && imageData[3] == 0x47 {
			contentType = "image/png"
		} else if imageData[0] == 0xFF && imageData[1] == 0xD8 {
			contentType = "image/jpeg"
		}
	}

	// Upload the blob
	blob, err := atproto.RepoUploadBlob(ctx, c.client, bytes.NewReader(imageData))
	if err != nil {
		return nil, fmt.Errorf("failed to upload image blob: %w", err)
	}

	// Create image reference for embedding
	imageRef := &bsky.EmbedImages_Image{
		Image: &util.LexBlob{
			Ref:      blob.Blob.Ref,
			MimeType: contentType,
			Size:     int64(len(imageData)),
		},
		Alt: altText,
	}

	log.Printf("Successfully uploaded image blob: %s (%d bytes, %s)", blob.Blob.Ref, len(imageData), contentType)
	return imageRef, nil
}

// PostWithImage posts a text with an embedded image and returns the post URI and CID
// Optional facets can be provided to make URLs or other text clickable
func (c *BlueskyClient) PostWithImage(ctx context.Context, text string, imageData []byte, altText string, facets ...[]*bsky.RichtextFacet) (string, string, error) {
	if c.client == nil {
		return "", "", fmt.Errorf("client not authenticated")
	}

	// Upload the image first
	imageRef, err := c.UploadImage(ctx, imageData, altText)
	if err != nil {
		return "", "", fmt.Errorf("failed to upload image: %w", err)
	}

	// Create the post with image embed
	postRecord := &bsky.FeedPost{
		Text:      text,
		CreatedAt: time.Now().Format(time.RFC3339),
		Embed: &bsky.FeedPost_Embed{
			EmbedImages: &bsky.EmbedImages{
				Images: []*bsky.EmbedImages_Image{imageRef},
			},
		},
	}

	// Add facets if provided
	if len(facets) > 0 && len(facets[0]) > 0 {
		postRecord.Facets = facets[0]
	}

	// Post the record
	result, err := atproto.RepoCreateRecord(ctx, c.client, &atproto.RepoCreateRecord_Input{
		Repo:       c.handle,
		Collection: "app.bsky.feed.post",
		Record:     &util.LexiconTypeDecoder{Val: postRecord},
	})

	if err != nil {
		return "", "", fmt.Errorf("failed to post with image: %w", err)
	}

	postedURI := result.Uri
	postedCID := result.Cid
	log.Printf("Successfully posted with embedded image: %s (URI: %s, CID: %s)", text[:min(50, len(text))], postedURI, postedCID)
	return postedURI, postedCID, nil
}

// PostWithImageAsReply posts a text with an embedded image as a reply to another post
func (c *BlueskyClient) PostWithImageAsReply(ctx context.Context, text string, imageData []byte, altText string, replyToURI, replyToCID string) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	// Upload the image first
	imageRef, err := c.UploadImage(ctx, imageData, altText)
	if err != nil {
		return fmt.Errorf("failed to upload image: %w", err)
	}

	// Create the post with image embed and reply structure
	postRecord := &bsky.FeedPost{
		Text:      text,
		CreatedAt: time.Now().Format(time.RFC3339),
		Embed: &bsky.FeedPost_Embed{
			EmbedImages: &bsky.EmbedImages{
				Images: []*bsky.EmbedImages_Image{imageRef},
			},
		},
		Reply: &bsky.FeedPost_ReplyRef{
			Root: &atproto.RepoStrongRef{
				Uri: replyToURI,
				Cid: replyToCID,
			},
			Parent: &atproto.RepoStrongRef{
				Uri: replyToURI,
				Cid: replyToCID,
			},
		},
	}

	// Post the record
	_, err = atproto.RepoCreateRecord(ctx, c.client, &atproto.RepoCreateRecord_Input{
		Repo:       c.handle,
		Collection: "app.bsky.feed.post",
		Record:     &util.LexiconTypeDecoder{Val: postRecord},
	})

	if err != nil {
		return fmt.Errorf("failed to post reply with image: %w", err)
	}

	log.Printf("Successfully posted reply with embedded image: %s (replying to: %s)", text[:min(50, len(text))], replyToURI)
	return nil
}

// PinPost pins a post to the account's profile
func (c *BlueskyClient) PinPost(ctx context.Context, postURI string, postCID string) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	// Get the DID from the authenticated client
	// The authenticated APIClient has an AccountDID field that may be populated after login
	handle := strings.Trim(c.handle, `"`)
	
	var did string
	
	// Check if authenticated client has AccountDID (set after login)
	if c.client != nil && c.client.AccountDID != nil {
		did = c.client.AccountDID.String()
		log.Printf("Using AccountDID from authenticated client: %s", did)
	} else {
		// Fallback: resolve handle to DID
		log.Printf("AccountDID not available, resolving handle %s to DID...", handle)
		resolution, err := atproto.IdentityResolveHandle(ctx, c.client, handle)
		if err != nil {
			return fmt.Errorf("failed to resolve handle to DID: %w", err)
		}
		did = resolution.Did
		log.Printf("Resolved handle %s to DID: %s", handle, did)
	}

	// Use DID for RepoGetRecord (DID is more reliable than handle for repo operations)
	// Function signature: RepoGetRecord(ctx, client, cid, collection, repo, rkey)
	// Parameters: ctx, client, "" (cid - empty for latest), collection, repo (DID/handle), rkey ("self")
	log.Printf("Attempting RepoGetRecord with DID: %s", did)
	profile, err := atproto.RepoGetRecord(ctx, c.client, "", "app.bsky.actor.profile", did, "self")
	if err != nil {
		// Log the full error for debugging
		errMsg := err.Error()
		log.Printf("RepoGetRecord with DID failed: %s", errMsg)
		log.Printf("Full error details: %+v", err)
		
		// Try with handle as fallback
		log.Printf("Attempting RepoGetRecord with handle as fallback: %s", handle)
		profile, err = atproto.RepoGetRecord(ctx, c.client, "", "app.bsky.actor.profile", handle, "self")
		if err != nil {
			log.Printf("RepoGetRecord with handle also failed: %s", err.Error())
			log.Printf("Full error details: %+v", err)
			return fmt.Errorf("failed to get current profile (tried DID %s and handle %s): %w", did, handle, err)
		}
		log.Printf("Successfully retrieved profile using handle (fallback)")
	} else {
		log.Printf("Successfully retrieved profile using DID")
	}

	// Parse the existing profile record
	// The profile record should already contain all existing fields
	recordVal := profile.Value.Val
	profileRecord, ok := recordVal.(*bsky.ActorProfile)
	if !ok {
		return fmt.Errorf("failed to parse profile record as ActorProfile")
	}

	// Create pinned post reference
	pinnedPost := &atproto.RepoStrongRef{
		Uri: postURI,
		Cid: postCID,
	}

	// Update profile with pinned post (preserves all other fields)
	profileRecord.PinnedPost = pinnedPost

	// Update the profile record - use DID as the repo identifier
	_, err = atproto.RepoPutRecord(ctx, c.client, &atproto.RepoPutRecord_Input{
		Repo:       did,
		Collection: "app.bsky.actor.profile",
		Rkey:       "self",
		Record:     &util.LexiconTypeDecoder{Val: profileRecord},
		SwapRecord: profile.Cid,
	})

	if err != nil {
		return fmt.Errorf("failed to pin post: %w", err)
	}

	log.Printf("Successfully pinned post: %s", postURI)
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
