package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

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
	runID := "run-1757470589119553493"
	
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

	for i, item := range result.Items {
		fmt.Printf("\n=== Item %d ===\n", i)
		
		// Print raw DynamoDB item
		itemJSON, _ := json.MarshalIndent(item, "", "  ")
		fmt.Printf("Raw DynamoDB item:\n%s\n", itemJSON)
		
		// Try to unmarshal as PostBatch
		var postBatch PostBatch
		err := attributevalue.UnmarshalMap(item, &postBatch)
		if err != nil {
			fmt.Printf("Failed to unmarshal as PostBatch: %v\n", err)
		} else {
			fmt.Printf("Successfully unmarshaled as PostBatch:\n")
			fmt.Printf("  RunID: %s\n", postBatch.RunID)
			fmt.Printf("  PostID: %s\n", postBatch.PostID)
			fmt.Printf("  Posts count: %d\n", len(postBatch.Posts))
			if len(postBatch.Posts) > 0 {
				fmt.Printf("  First post author: %s\n", postBatch.Posts[0].Author)
				fmt.Printf("  First post text: %s\n", postBatch.Posts[0].Text[:min(50, len(postBatch.Posts[0].Text))])
			}
		}
		
		if i >= 2 { // Only check first 3 items
			break
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
