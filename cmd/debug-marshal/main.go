package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
)

type Post struct {
	Author          string  `json:"author" dynamodbav:"author"`
	Text            string  `json:"text" dynamodbav:"text"`
	URI             string  `json:"uri" dynamodbav:"uri"`
	CID             string  `json:"cid" dynamodbav:"cid"`
	CreatedAt       string  `json:"createdAt" dynamodbav:"createdAt"`
	Likes           int     `json:"likes" dynamodbav:"likes"`
	Reposts         int     `json:"reposts" dynamodbav:"reposts"`
	Replies         int     `json:"replies" dynamodbav:"replies"`
	Sentiment       string  `json:"sentiment" dynamodbav:"sentiment"`
	EngagementScore float64 `json:"engagementScore" dynamodbav:"engagementScore"`
}

type PostBatch struct {
	RunID     string    `json:"runId" dynamodbav:"runId"`
	Step      string    `json:"step" dynamodbav:"step"`
	PostID    string    `json:"postId" dynamodbav:"postId"`
	Posts     []Post    `json:"posts" dynamodbav:"posts"`
	CreatedAt string    `json:"createdAt" dynamodbav:"createdAt"`
	TTL       int64     `json:"ttl" dynamodbav:"ttl"`
}

func main() {
	// Create a test PostBatch
	posts := []Post{
		{
			Author:          "test.author.bsky.social",
			Text:            "This is a test post",
			URI:             "at://did:test/app.bsky.feed.post/123",
			CID:             "bafyretest",
			CreatedAt:       "2025-09-10T02:00:00Z",
			Likes:           5,
			Reposts:         2,
			Replies:         1,
			Sentiment:       "positive",
			EngagementScore: 8.0,
		},
		{
			Author:          "test2.author.bsky.social",
			Text:            "This is another test post",
			URI:             "at://did:test/app.bsky.feed.post/456",
			CID:             "bafyretest2",
			CreatedAt:       "2025-09-10T02:01:00Z",
			Likes:           3,
			Reposts:         1,
			Replies:         0,
			Sentiment:       "neutral",
			EngagementScore: 4.0,
		},
	}

	postBatch := PostBatch{
		RunID:     "run-test123",
		Step:      "fetcher",
		PostID:    "run-test123#batch0",
		Posts:     posts,
		CreatedAt: time.Now().Format(time.RFC3339),
		TTL:       time.Now().Add(2 * 24 * time.Hour).Unix(),
	}

	// Marshal to DynamoDB format
	item, err := attributevalue.MarshalMap(postBatch)
	if err != nil {
		log.Fatalf("Failed to marshal PostBatch: %v", err)
	}

	// Print the marshaled item
	fmt.Println("Marshaled DynamoDB item:")
	itemJSON, _ := json.MarshalIndent(item, "", "  ")
	fmt.Printf("%s\n", itemJSON)

	// Try to unmarshal back
	var unmarshaled PostBatch
	err = attributevalue.UnmarshalMap(item, &unmarshaled)
	if err != nil {
		log.Fatalf("Failed to unmarshal PostBatch: %v", err)
	}

	fmt.Printf("\nUnmarshaled PostBatch:\n")
	fmt.Printf("  RunID: %s\n", unmarshaled.RunID)
	fmt.Printf("  PostID: %s\n", unmarshaled.PostID)
	fmt.Printf("  Posts count: %d\n", len(unmarshaled.Posts))
	if len(unmarshaled.Posts) > 0 {
		fmt.Printf("  First post author: %s\n", unmarshaled.Posts[0].Author)
		fmt.Printf("  First post text: %s\n", unmarshaled.Posts[0].Text)
	}
}
