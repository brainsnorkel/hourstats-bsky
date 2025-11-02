package state

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// SentimentDataPoint represents a single sentiment measurement at a point in time
type SentimentDataPoint struct {
	RunID                string    `json:"runId" dynamodbav:"runId"`
	Timestamp            time.Time `json:"timestamp" dynamodbav:"timestamp"`
	AverageCompoundScore float64   `json:"averageCompoundScore" dynamodbav:"averageCompoundScore"`
	NetSentimentPercent  float64   `json:"netSentimentPercent" dynamodbav:"netSentimentPercent"`
	SentimentCategory    string    `json:"sentimentCategory" dynamodbav:"sentimentCategory"`
	TotalPosts           int       `json:"totalPosts" dynamodbav:"totalPosts"`
	CreatedAt            time.Time `json:"createdAt" dynamodbav:"createdAt"`
	TTL                  int64     `json:"ttl" dynamodbav:"ttl"`
}

// SentimentHistoryManager handles sentiment history operations
type SentimentHistoryManager struct {
	client    *dynamodb.Client
	tableName string
}

// NewSentimentHistoryManager creates a new sentiment history manager
func NewSentimentHistoryManager(ctx context.Context, tableName string) (*SentimentHistoryManager, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := dynamodb.NewFromConfig(cfg)

	return &SentimentHistoryManager{
		client:    client,
		tableName: tableName,
	}, nil
}

// StoreSentimentData stores a sentiment data point
func (shm *SentimentHistoryManager) StoreSentimentData(ctx context.Context, dataPoint SentimentDataPoint) error {
	// Set CreatedAt first, then TTL based on CreatedAt to ensure consistency
	dataPoint.CreatedAt = time.Now()
	// Set TTL to 14 days from creation time
	dataPoint.TTL = dataPoint.CreatedAt.Add(14 * 24 * time.Hour).Unix()

	item, err := attributevalue.MarshalMap(dataPoint)
	if err != nil {
		return fmt.Errorf("failed to marshal sentiment data point: %w", err)
	}

	_, err = shm.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(shm.tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("failed to store sentiment data point: %w", err)
	}

	return nil
}

// GetSentimentHistory retrieves sentiment data for a given time range
// Handles pagination to retrieve all data points across multiple DynamoDB pages
func (shm *SentimentHistoryManager) GetSentimentHistory(ctx context.Context, duration time.Duration) ([]SentimentDataPoint, error) {
	// Calculate the start time for the query
	startTime := time.Now().Add(-duration)

	var allDataPoints []SentimentDataPoint
	var lastEvaluatedKey map[string]types.AttributeValue
	pageCount := 0

	for {
		// Use Scan with filter for time-range queries
		// Note: This is less efficient than Query but necessary for timestamp range filtering
		scanInput := &dynamodb.ScanInput{
			TableName:        aws.String(shm.tableName),
			FilterExpression: aws.String("#timestamp >= :startTime"),
			ExpressionAttributeNames: map[string]string{
				"#timestamp": "timestamp",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":startTime": &types.AttributeValueMemberS{Value: startTime.Format(time.RFC3339)},
			},
		}

		// Add pagination token if we're continuing from a previous page
		if lastEvaluatedKey != nil {
			scanInput.ExclusiveStartKey = lastEvaluatedKey
		}

		result, err := shm.client.Scan(ctx, scanInput)
		if err != nil {
			return nil, fmt.Errorf("failed to query sentiment history: %w", err)
		}

		pageCount++
		log.Printf("GetSentimentHistory: Retrieved page %d with %d items", pageCount, len(result.Items))

		// Process items from this page
		for _, item := range result.Items {
			var dataPoint SentimentDataPoint
			err := attributevalue.UnmarshalMap(item, &dataPoint)
			if err != nil {
				continue // Skip invalid items
			}
			allDataPoints = append(allDataPoints, dataPoint)
		}

		// Check if there are more pages to retrieve
		if len(result.LastEvaluatedKey) == 0 {
			break
		}

		lastEvaluatedKey = result.LastEvaluatedKey
	}

	// Sort by timestamp
	sort.Slice(allDataPoints, func(i, j int) bool {
		return allDataPoints[i].Timestamp.Before(allDataPoints[j].Timestamp)
	})

	log.Printf("GetSentimentHistory: Retrieved %d total data points across %d pages", len(allDataPoints), pageCount)
	return allDataPoints, nil
}

// GetSentimentHistoryForRun retrieves sentiment data for a specific run
// Handles pagination to retrieve all data points across multiple DynamoDB pages
func (shm *SentimentHistoryManager) GetSentimentHistoryForRun(ctx context.Context, runID string, duration time.Duration) ([]SentimentDataPoint, error) {
	startTime := time.Now().Add(-duration)

	var allDataPoints []SentimentDataPoint
	var lastEvaluatedKey map[string]types.AttributeValue
	pageCount := 0

	for {
		queryInput := &dynamodb.QueryInput{
			TableName:              aws.String(shm.tableName),
			KeyConditionExpression: aws.String("runId = :runId AND #timestamp >= :startTime"),
			ExpressionAttributeNames: map[string]string{
				"#timestamp": "timestamp",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":runId":     &types.AttributeValueMemberS{Value: runID},
				":startTime": &types.AttributeValueMemberS{Value: startTime.Format(time.RFC3339)},
			},
			ScanIndexForward: aws.Bool(true),
		}

		// Add pagination token if we're continuing from a previous page
		if lastEvaluatedKey != nil {
			queryInput.ExclusiveStartKey = lastEvaluatedKey
		}

		result, err := shm.client.Query(ctx, queryInput)
		if err != nil {
			return nil, fmt.Errorf("failed to query sentiment history for run: %w", err)
		}

		pageCount++
		log.Printf("GetSentimentHistoryForRun: Retrieved page %d with %d items", pageCount, len(result.Items))

		// Process items from this page
		for _, item := range result.Items {
			var dataPoint SentimentDataPoint
			err := attributevalue.UnmarshalMap(item, &dataPoint)
			if err != nil {
				continue
			}
			allDataPoints = append(allDataPoints, dataPoint)
		}

		// Check if there are more pages to retrieve
		if len(result.LastEvaluatedKey) == 0 {
			break
		}

		lastEvaluatedKey = result.LastEvaluatedKey
	}

	log.Printf("GetSentimentHistoryForRun: Retrieved %d total data points across %d pages", len(allDataPoints), pageCount)
	return allDataPoints, nil
}

// ParseCompositeKey parses a composite key string in the format "runId#timestamp"
// Returns the runId and timestamp strings, or an error if parsing fails
func ParseCompositeKey(key string) (string, string, error) {
	parts := strings.Split(key, "#")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid composite key format: expected 'runId#timestamp', got '%s'", key)
	}
	return parts[0], parts[1], nil
}

// GetSentimentDataByKey retrieves a specific sentiment data point by its composite key
func (shm *SentimentHistoryManager) GetSentimentDataByKey(ctx context.Context, runID string, timestampStr string) (*SentimentDataPoint, error) {
	// Parse the timestamp string to time.Time
	timestamp, err := time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp format: %w", err)
	}

	// Query by runId and find the matching timestamp
	// This approach is more robust as it works regardless of how the timestamp string is formatted in DynamoDB
	result, err := shm.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(shm.tableName),
		KeyConditionExpression: aws.String("runId = :runId"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":runId": &types.AttributeValueMemberS{Value: runID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query sentiment data point: %w", err)
	}

	// Find the item with matching timestamp (compare time.Time values, not strings)
	for _, item := range result.Items {
		var dataPoint SentimentDataPoint
		err := attributevalue.UnmarshalMap(item, &dataPoint)
		if err != nil {
			continue
		}
		// Compare timestamps with a small tolerance (1 second) to account for any precision differences
		if dataPoint.RunID == runID && abs(dataPoint.Timestamp.Sub(timestamp)) < time.Second {
			return &dataPoint, nil
		}
	}

	return nil, fmt.Errorf("sentiment data point not found: runId=%s, timestamp=%s", runID, timestampStr)
}

// abs returns the absolute value of a time.Duration
func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// DeleteSentimentData deletes a specific sentiment data point by its composite key
// Returns the deleted data point for backup/restore purposes
func (shm *SentimentHistoryManager) DeleteSentimentData(ctx context.Context, runID string, timestampStr string) (*SentimentDataPoint, error) {
	// First, retrieve the item to return it (this uses the actual stored timestamp)
	dataPoint, err := shm.GetSentimentDataByKey(ctx, runID, timestampStr)
	if err != nil {
		return nil, err
	}

	// Use the actual timestamp from the retrieved item to ensure exact match
	keyStruct := struct {
		RunID     string    `dynamodbav:"runId"`
		Timestamp time.Time `dynamodbav:"timestamp"`
	}{
		RunID:     dataPoint.RunID,
		Timestamp: dataPoint.Timestamp, // Use the actual stored timestamp
	}

	key, err := attributevalue.MarshalMap(keyStruct)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal key: %w", err)
	}

	_, err = shm.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(shm.tableName),
		Key:       key,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to delete sentiment data point: %w", err)
	}

	return dataPoint, nil
}
