package client

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/atproto/client"
)

type Post struct {
	URI      string
	Text     string
	Author   string
	Likes    int
	Reposts  int
	Replies  int
	CreatedAt string
}

type BlueskyClient struct {
	client *client.APIClient
	handle string
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

func (c *BlueskyClient) GetTrendingPosts() ([]Post, error) {
	ctx := context.Background()
	
	// For now, we'll fetch recent posts from the timeline
	// In a real implementation, we'd need to implement trending logic
	// based on engagement metrics over time
	
	// Get the timeline
	timeline, err := bsky.FeedGetTimeline(ctx, c.client, "reverse-chronological", "", 100)
	if err != nil {
		return nil, fmt.Errorf("failed to get timeline: %w", err)
	}

	var posts []Post
	for _, feedItem := range timeline.Feed {
		if feedItem.Post != nil {
			// Handle pointer fields safely
			var likes, reposts, replies int
			if feedItem.Post.LikeCount != nil {
				likes = int(*feedItem.Post.LikeCount)
			}
			if feedItem.Post.RepostCount != nil {
				reposts = int(*feedItem.Post.RepostCount)
			}
			if feedItem.Post.ReplyCount != nil {
				replies = int(*feedItem.Post.ReplyCount)
			}
			
			// For now, use a placeholder for text since we need to decode the record
			// In a real implementation, we'd decode the record to get the actual text
			text := "Post content (text extraction not yet implemented)"
			
			post := Post{
				URI:       feedItem.Post.Uri,
				Text:      text,
				Author:    feedItem.Post.Author.Handle,
				Likes:     likes,
				Reposts:   reposts,
				Replies:   replies,
				CreatedAt: feedItem.Post.IndexedAt,
			}
			posts = append(posts, post)
		}
	}

	return posts, nil
}

func (c *BlueskyClient) PostTrendingSummary(posts []Post, overallSentiment string) error {
	// Get current local time
	now := time.Now()
	timeStr := now.Format("2006-01-02 15:04")
	
	// Create the summary post in the specified format
	summaryText := fmt.Sprintf("Top five this hour %s\n\n", timeStr)
	
	// Add links to the top 5 posts (ranked by likes + reposts)
	for i, post := range posts {
		summaryText += fmt.Sprintf("%d. %s\n", i+1, post.URI)
		summaryText += fmt.Sprintf("   @%s | 💙 %d likes | 🔄 %d reposts\n\n", post.Author, post.Likes, post.Reposts)
	}
	
	// Add sentiment summary
	summaryText += fmt.Sprintf("Bluesky is %s", overallSentiment)

	// For now, we'll just log the post content
	// In a real implementation, we'd use the AT Protocol to create the post
	log.Printf("Would post: %s", summaryText)
	
	return nil
}

func truncateText(text string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}
	return text[:maxLength-3] + "..."
}
