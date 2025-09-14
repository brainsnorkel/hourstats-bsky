package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// Event represents the EventBridge event structure
type Event struct {
	Source     string `json:"source"`
	Time       string `json:"time"`
	Action     string `json:"action,omitempty"`
	TargetDate string `json:"targetDate,omitempty"` // For manual triggering with specific date
}

// Response represents the Lambda response
type Response struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
	Processed  bool   `json:"processed"`
	Date       string `json:"date,omitempty"`
}

// DailyAggregatorHandler handles the daily aggregator Lambda function
type DailyAggregatorHandler struct {
	dailySentimentManager   *state.DailySentimentManager
	sentimentHistoryManager *state.SentimentHistoryManager
}

// NewDailyAggregatorHandler creates a new daily aggregator handler
func NewDailyAggregatorHandler(ctx context.Context) (*DailyAggregatorHandler, error) {
	// Initialize daily sentiment manager
	dailySentimentManager, err := state.NewDailySentimentManager(ctx, "hourstats-daily-sentiment")
	if err != nil {
		return nil, fmt.Errorf("failed to create daily sentiment manager: %w", err)
	}

	// Initialize sentiment history manager
	sentimentHistoryManager, err := state.NewSentimentHistoryManager(ctx, "hourstats-sentiment-history")
	if err != nil {
		return nil, fmt.Errorf("failed to create sentiment history manager: %w", err)
	}

	return &DailyAggregatorHandler{
		dailySentimentManager:   dailySentimentManager,
		sentimentHistoryManager: sentimentHistoryManager,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *DailyAggregatorHandler) HandleRequest(ctx context.Context, event Event) (Response, error) {
	log.Printf("Daily aggregator received event: %+v", event)

	// Determine target date
	var targetDate string
	if event.TargetDate != "" {
		// Manual trigger with specific date
		targetDate = event.TargetDate
	} else {
		// Scheduled trigger - process previous day
		targetDate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}

	log.Printf("Processing daily sentiment aggregation for date: %s", targetDate)

	// Check if daily sentiment already exists for this date
	existing, err := h.dailySentimentManager.GetDailySentimentForDate(ctx, targetDate)
	if err == nil && existing != nil {
		log.Printf("Daily sentiment already exists for date %s, skipping aggregation", targetDate)
		return Response{
			StatusCode: 200,
			Body:       fmt.Sprintf("Daily sentiment already exists for date: %s", targetDate),
			Processed:  false,
			Date:       targetDate,
		}, nil
	}

	// Calculate daily sentiment from 24 hours of sentiment history
	dailySentiment, err := h.dailySentimentManager.CalculateDailySentimentFromHistory(ctx, h.sentimentHistoryManager, targetDate)
	if err != nil {
		log.Printf("Failed to calculate daily sentiment for date %s: %v", targetDate, err)
		return Response{
			StatusCode: 500,
			Body:       fmt.Sprintf("Failed to calculate daily sentiment: %v", err),
			Processed:  false,
			Date:       targetDate,
		}, err
	}

	// Store the daily sentiment
	err = h.dailySentimentManager.StoreDailySentiment(ctx, *dailySentiment)
	if err != nil {
		log.Printf("Failed to store daily sentiment for date %s: %v", targetDate, err)
		return Response{
			StatusCode: 500,
			Body:       fmt.Sprintf("Failed to store daily sentiment: %v", err),
			Processed:  false,
			Date:       targetDate,
		}, err
	}

	log.Printf("Successfully processed daily sentiment for date %s: avg=%.2f%%, min=%.2f%%, max=%.2f%%, runs=%d, posts=%d",
		targetDate,
		dailySentiment.AverageSentiment,
		dailySentiment.MinSentiment,
		dailySentiment.MaxSentiment,
		dailySentiment.TotalRuns,
		dailySentiment.TotalPosts)

	return Response{
		StatusCode: 200,
		Body:       fmt.Sprintf("Daily sentiment processed successfully for date: %s", targetDate),
		Processed:  true,
		Date:       targetDate,
	}, nil
}

func main() {
	ctx := context.Background()
	handler, err := NewDailyAggregatorHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create daily aggregator handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
