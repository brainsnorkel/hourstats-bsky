package state

import (
	"context"
	"fmt"
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
	// Set TTL to 7 days for historical data
	dataPoint.TTL = time.Now().Add(7 * 24 * time.Hour).Unix()
	dataPoint.CreatedAt = time.Now()

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
func (shm *SentimentHistoryManager) GetSentimentHistory(ctx context.Context, duration time.Duration) ([]SentimentDataPoint, error) {
	// Calculate the start time for the query
	startTime := time.Now().Add(-duration)

	// Query using the timestamp-index GSI
	result, err := shm.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(shm.tableName),
		IndexName:              aws.String("timestamp-index"),
		KeyConditionExpression: aws.String("#timestamp >= :startTime"),
		ExpressionAttributeNames: map[string]string{
			"#timestamp": "timestamp",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":startTime": &types.AttributeValueMemberS{Value: startTime.Format(time.RFC3339)},
		},
		ScanIndexForward: aws.Bool(true), // Sort by timestamp ascending
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query sentiment history: %w", err)
	}

	var dataPoints []SentimentDataPoint
	for _, item := range result.Items {
		var dataPoint SentimentDataPoint
		err := attributevalue.UnmarshalMap(item, &dataPoint)
		if err != nil {
			continue // Skip invalid items
		}
		dataPoints = append(dataPoints, dataPoint)
	}

	return dataPoints, nil
}

// GetSentimentHistoryForRun retrieves sentiment data for a specific run
func (shm *SentimentHistoryManager) GetSentimentHistoryForRun(ctx context.Context, runID string, duration time.Duration) ([]SentimentDataPoint, error) {
	startTime := time.Now().Add(-duration)

	result, err := shm.client.Query(ctx, &dynamodb.QueryInput{
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
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query sentiment history for run: %w", err)
	}

	var dataPoints []SentimentDataPoint
	for _, item := range result.Items {
		var dataPoint SentimentDataPoint
		err := attributevalue.UnmarshalMap(item, &dataPoint)
		if err != nil {
			continue
		}
		dataPoints = append(dataPoints, dataPoint)
	}

	return dataPoints, nil
}
