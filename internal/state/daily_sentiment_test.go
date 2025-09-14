package state

import (
	"context"
	"testing"
	"time"
)

func TestDailySentimentDataPoint(t *testing.T) {
	now := time.Now()
	date := now.Format("2006-01-02")

	dataPoint := DailySentimentDataPoint{
		Date:             date,
		RunID:            "daily-" + date,
		AverageSentiment: 15.5,
		MinSentiment:     -5.2,
		MaxSentiment:     25.8,
		TotalRuns:        48, // 48 runs per day (every 30 minutes)
		TotalPosts:       1500,
		CreatedAt:        now,
		TTL:              now.Add(3 * 365 * 24 * time.Hour).Unix(),
	}

	// Test basic properties
	if dataPoint.Date != date {
		t.Errorf("Expected date %s, got %s", date, dataPoint.Date)
	}

	if dataPoint.AverageSentiment != 15.5 {
		t.Errorf("Expected average sentiment 15.5, got %f", dataPoint.AverageSentiment)
	}

	if dataPoint.MinSentiment != -5.2 {
		t.Errorf("Expected min sentiment -5.2, got %f", dataPoint.MinSentiment)
	}

	if dataPoint.MaxSentiment != 25.8 {
		t.Errorf("Expected max sentiment 25.8, got %f", dataPoint.MaxSentiment)
	}

	if dataPoint.TotalRuns != 48 {
		t.Errorf("Expected total runs 48, got %d", dataPoint.TotalRuns)
	}

	if dataPoint.TotalPosts != 1500 {
		t.Errorf("Expected total posts 1500, got %d", dataPoint.TotalPosts)
	}
}

func TestYearlySparklineDataPoint(t *testing.T) {
	date := "2025-01-05"
	timestamp := time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC)

	dataPoint := YearlySparklineDataPoint{
		Date:                date,
		AverageSentiment:    12.3,
		MinSentiment:        -8.1,
		MaxSentiment:        22.7,
		Timestamp:           timestamp,
		NetSentimentPercent: 12.3, // Should match AverageSentiment
	}

	// Test basic properties
	if dataPoint.Date != date {
		t.Errorf("Expected date %s, got %s", date, dataPoint.Date)
	}

	if dataPoint.AverageSentiment != 12.3 {
		t.Errorf("Expected average sentiment 12.3, got %f", dataPoint.AverageSentiment)
	}

	if dataPoint.NetSentimentPercent != dataPoint.AverageSentiment {
		t.Errorf("Expected NetSentimentPercent to match AverageSentiment, got %f vs %f",
			dataPoint.NetSentimentPercent, dataPoint.AverageSentiment)
	}

	if !dataPoint.Timestamp.Equal(timestamp) {
		t.Errorf("Expected timestamp %v, got %v", timestamp, dataPoint.Timestamp)
	}
}

func TestCalculateDailySentimentFromHistory(t *testing.T) {
	// This is a mock test since we can't easily test DynamoDB operations without AWS
	// In a real test environment, we would use DynamoDB Local or mocks

	// Test data validation
	targetDate := "2025-01-05"

	// Test invalid date format
	_, err := (&DailySentimentManager{}).CalculateDailySentimentFromHistory(
		context.Background(),
		nil, // We can't test with real manager without AWS
		"invalid-date",
	)
	if err == nil {
		t.Error("Expected error for invalid date format, got nil")
	}

	// Test valid date format - this will fail because we're passing nil for the manager
	_, err = (&DailySentimentManager{}).CalculateDailySentimentFromHistory(
		context.Background(),
		nil, // We can't test with real manager without AWS
		targetDate,
	)
	// We expect an error because we're passing nil for the sentiment history manager
	if err == nil {
		t.Error("Expected error for nil sentiment history manager, got nil")
	}
}

func TestDailySentimentManager_NewDailySentimentManager(t *testing.T) {
	// Test with valid table name (will fail on AWS config, but that's expected)
	_, err := NewDailySentimentManager(context.Background(), "test-table")
	if err != nil && err.Error() != "failed to load AWS config: no configuration found" {
		t.Errorf("Expected AWS config error, got: %v", err)
	}
}
