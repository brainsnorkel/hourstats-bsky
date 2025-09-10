package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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

type PostItem struct {
	RunID     string    `json:"runId" dynamodbav:"runId"`
	Step      string    `json:"step" dynamodbav:"step"`
	PostID    string    `json:"postId" dynamodbav:"postId"`
	Post      Post      `json:"post" dynamodbav:"post"`
	CreatedAt string    `json:"createdAt" dynamodbav:"createdAt"`
	TTL       int64     `json:"ttl" dynamodbav:"ttl"`
}

func main() {
	ctx := context.Background()
	
	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	// Create DynamoDB client
	client := dynamodb.NewFromConfig(cfg)
	tableName := "hourstats-state"

	// Query for the latest run's posts
	runID := "run-1757471737499127873"
	
	fmt.Printf("Querying for run: %s\n", runID)
	
	result, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(tableName),
		IndexName:              aws.String("posts-index"),
		KeyConditionExpression: aws.String("runId = :runId AND begins_with(postId, :postIdPrefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":runId":        &types.AttributeValueMemberS{Value: runID},
			":postIdPrefix": &types.AttributeValueMemberS{Value: runID + "#"},
		},
	})
	if err != nil {
		log.Fatalf("Failed to query posts: %v", err)
	}

	fmt.Printf("Found %d items\n", len(result.Items))

	var allPosts []Post
	for i, item := range result.Items {
		fmt.Printf("\n=== Item %d ===\n", i)
		
		// Try to unmarshal as PostBatch first (new format)
		var postBatch PostBatch
		err := attributevalue.UnmarshalMap(item, &postBatch)
		if err == nil && strings.Contains(postBatch.PostID, "#batch") {
			// This is a batched post item
			fmt.Printf("Successfully unmarshaled as PostBatch:\n")
			fmt.Printf("  RunID: %s\n", postBatch.RunID)
			fmt.Printf("  PostID: %s\n", postBatch.PostID)
			fmt.Printf("  Posts count: %d\n", len(postBatch.Posts))
			if len(postBatch.Posts) > 0 {
				fmt.Printf("  First post author: %s\n", postBatch.Posts[0].Author)
				fmt.Printf("  First post text: %s\n", postBatch.Posts[0].Text[:min(50, len(postBatch.Posts[0].Text))])
			}
			allPosts = append(allPosts, postBatch.Posts...)
			continue
		}

		// Fallback to individual PostItem (legacy format)
		var postItem PostItem
		err = attributevalue.UnmarshalMap(item, &postItem)
		if err != nil {
			fmt.Printf("Failed to unmarshal as PostItem: %v\n", err)
			continue
		}
		// Only include posts that have a postId with # (filter out run state items)
		if strings.Contains(postItem.PostID, "#") && !strings.Contains(postItem.PostID, "#batch") {
			fmt.Printf("Successfully unmarshaled as PostItem:\n")
			fmt.Printf("  RunID: %s\n", postItem.RunID)
			fmt.Printf("  PostID: %s\n", postItem.PostID)
			fmt.Printf("  Post author: %s\n", postItem.Post.Author)
			allPosts = append(allPosts, postItem.Post)
		}
	}
	
	fmt.Printf("\n=== SUMMARY ===\n")
	fmt.Printf("Total posts found: %d\n", len(allPosts))
	if len(allPosts) > 0 {
		fmt.Printf("First post author: %s\n", allPosts[0].Author)
		fmt.Printf("First post text: %s\n", allPosts[0].Text[:min(50, len(allPosts[0].Text))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
