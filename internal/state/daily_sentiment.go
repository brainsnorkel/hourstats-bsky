package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DailySentimentDataPoint represents a single daily sentiment measurement
type DailySentimentDataPoint struct {
	Date             string    `json:"date" dynamodbav:"date"`   // "2025-01-05"
	RunID            string    `json:"runId" dynamodbav:"runId"` // "daily-2025-01-05"
	AverageSentiment float64   `json:"averageSentiment" dynamodbav:"averageSentiment"`
	MinSentiment     float64   `json:"minSentiment" dynamodbav:"minSentiment"`
	MaxSentiment     float64   `json:"maxSentiment" dynamodbav:"maxSentiment"`
	TotalRuns        int       `json:"totalRuns" dynamodbav:"totalRuns"`
	TotalPosts       int       `json:"totalPosts" dynamodbav:"totalPosts"`
	CreatedAt        time.Time `json:"createdAt" dynamodbav:"createdAt"`
	TTL              int64     `json:"ttl" dynamodbav:"ttl"`
}

// YearlySparklineDataPoint represents a data point for yearly sparkline visualization
type YearlySparklineDataPoint struct {
	Date                string    `json:"date"`
	AverageSentiment    float64   `json:"averageSentiment"`
	MinSentiment        float64   `json:"minSentiment"`
	MaxSentiment        float64   `json:"maxSentiment"`
	Timestamp           time.Time `json:"timestamp"`
	NetSentimentPercent float64   `json:"netSentimentPercent"` // Alias for AverageSentiment
}

// DailySentimentManager handles daily sentiment operations
type DailySentimentManager struct {
	client    *dynamodb.Client
	tableName string
}

// NewDailySentimentManager creates a new daily sentiment manager
func NewDailySentimentManager(ctx context.Context, tableName string) (*DailySentimentManager, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := dynamodb.NewFromConfig(cfg)

	return &DailySentimentManager{
		client:    client,
		tableName: tableName,
	}, nil
}

// StoreDailySentiment stores a daily sentiment data point
func (dsm *DailySentimentManager) StoreDailySentiment(ctx context.Context, dataPoint DailySentimentDataPoint) error {
	// Set CreatedAt and TTL
	dataPoint.CreatedAt = time.Now()
	// Set TTL to 3 years from creation time
	dataPoint.TTL = dataPoint.CreatedAt.Add(3 * 365 * 24 * time.Hour).Unix()

	item, err := attributevalue.MarshalMap(dataPoint)
	if err != nil {
		return fmt.Errorf("failed to marshal daily sentiment data point: %w", err)
	}

	_, err = dsm.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(dsm.tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("failed to store daily sentiment data point: %w", err)
	}

	return nil
}

// GetDailySentimentHistory retrieves daily sentiment data for a given time range
func (dsm *DailySentimentManager) GetDailySentimentHistory(ctx context.Context, days int) ([]DailySentimentDataPoint, error) {
	// Calculate the start date for the query
	startDate := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")

	// Use Scan with filter for date range queries
	result, err := dsm.client.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(dsm.tableName),
		FilterExpression: aws.String("#date BETWEEN :startDate AND :endDate"),
		ExpressionAttributeNames: map[string]string{
			"#date": "date",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":startDate": &types.AttributeValueMemberS{Value: startDate},
			":endDate":   &types.AttributeValueMemberS{Value: endDate},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query daily sentiment history: %w", err)
	}

	var dataPoints []DailySentimentDataPoint
	for _, item := range result.Items {
		var dataPoint DailySentimentDataPoint
		err := attributevalue.UnmarshalMap(item, &dataPoint)
		if err != nil {
			continue // Skip invalid items
		}
		dataPoints = append(dataPoints, dataPoint)
	}

	return dataPoints, nil
}

// GetYearlySentimentData retrieves 365 days of daily sentiment data for yearly sparkline
func (dsm *DailySentimentManager) GetYearlySentimentData(ctx context.Context) ([]YearlySparklineDataPoint, error) {
	// Get 365 days of data
	dailyData, err := dsm.GetDailySentimentHistory(ctx, 365)
	if err != nil {
		return nil, fmt.Errorf("failed to get daily sentiment history: %w", err)
	}

	// Convert to yearly sparkline data points
	var yearlyData []YearlySparklineDataPoint
	for _, daily := range dailyData {
		// Parse the date string to create a timestamp
		date, err := time.Parse("2006-01-02", daily.Date)
		if err != nil {
			continue // Skip invalid dates
		}

		yearlyData = append(yearlyData, YearlySparklineDataPoint{
			Date:                daily.Date,
			AverageSentiment:    daily.AverageSentiment,
			MinSentiment:        daily.MinSentiment,
			MaxSentiment:        daily.MaxSentiment,
			Timestamp:           date,
			NetSentimentPercent: daily.AverageSentiment, // Alias for compatibility
		})
	}

	// Sort by timestamp to ensure chronological order
	// This is critical for proper graph rendering
	sort.Slice(yearlyData, func(i, j int) bool {
		return yearlyData[i].Timestamp.Before(yearlyData[j].Timestamp)
	})

	return yearlyData, nil
}

// GetDailySentimentForDate retrieves daily sentiment for a specific date
func (dsm *DailySentimentManager) GetDailySentimentForDate(ctx context.Context, date string) (*DailySentimentDataPoint, error) {
	result, err := dsm.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(dsm.tableName),
		Key: map[string]types.AttributeValue{
			"date":  &types.AttributeValueMemberS{Value: date},
			"runId": &types.AttributeValueMemberS{Value: "daily-" + date},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get daily sentiment for date: %w", err)
	}

	if result.Item == nil {
		return nil, fmt.Errorf("daily sentiment not found for date: %s", date)
	}

	var dataPoint DailySentimentDataPoint
	err = attributevalue.UnmarshalMap(result.Item, &dataPoint)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal daily sentiment data point: %w", err)
	}

	return &dataPoint, nil
}

// CalculateDailySentimentFromHistory calculates daily sentiment from 24 hours of sentiment history
func (dsm *DailySentimentManager) CalculateDailySentimentFromHistory(ctx context.Context, sentimentHistoryManager *SentimentHistoryManager, targetDate string) (*DailySentimentDataPoint, error) {
	// Parse target date
	date, err := time.Parse("2006-01-02", targetDate)
	if err != nil {
		return nil, fmt.Errorf("invalid date format: %w", err)
	}

	// Check if sentiment history manager is provided
	if sentimentHistoryManager == nil {
		return nil, fmt.Errorf("sentiment history manager is required")
	}

	// Calculate 24-hour window (from midnight to midnight UTC)
	startTime := date
	endTime := date.Add(24 * time.Hour)

	// Get sentiment history for the 24-hour period
	// We'll use a longer duration to ensure we capture all data
	allData, err := sentimentHistoryManager.GetSentimentHistory(ctx, 48*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("failed to get sentiment history: %w", err)
	}

	// Filter data to the specific 24-hour window
	var dayData []SentimentDataPoint
	for _, dp := range allData {
		if dp.Timestamp.After(startTime) && dp.Timestamp.Before(endTime) {
			dayData = append(dayData, dp)
		}
	}

	if len(dayData) == 0 {
		return nil, fmt.Errorf("no sentiment data found for date: %s", targetDate)
	}

	// Calculate statistics
	var sum, min, max float64
	var totalPosts int
	min = dayData[0].NetSentimentPercent
	max = dayData[0].NetSentimentPercent

	for _, dp := range dayData {
		sentiment := dp.NetSentimentPercent
		sum += sentiment
		totalPosts += dp.TotalPosts

		if sentiment < min {
			min = sentiment
		}
		if sentiment > max {
			max = sentiment
		}
	}

	average := sum / float64(len(dayData))

	return &DailySentimentDataPoint{
		Date:             targetDate,
		RunID:            "daily-" + targetDate,
		AverageSentiment: average,
		MinSentiment:     min,
		MaxSentiment:     max,
		TotalRuns:        len(dayData),
		TotalPosts:       totalPosts,
		CreatedAt:        time.Now(),
		TTL:              time.Now().Add(3 * 365 * 24 * time.Hour).Unix(),
	}, nil
}
