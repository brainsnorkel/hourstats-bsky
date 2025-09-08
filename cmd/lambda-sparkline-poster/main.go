package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// StepFunctionsEvent represents the event from Step Functions
type StepFunctionsEvent struct {
	RunID                   string `json:"runId"`
	AnalysisIntervalMinutes int    `json:"analysisIntervalMinutes"`
	Status                  string `json:"status"`
}

// Response represents the Lambda response
type Response struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
	Posted     bool   `json:"posted"`
}

// SparklinePosterHandler handles the sparkline poster Lambda function
type SparklinePosterHandler struct {
	sentimentHistoryManager *state.SentimentHistoryManager
	sparklineGenerator      *sparkline.SparklineGenerator
	ssmClient               *ssm.Client
}

// NewSparklinePosterHandler creates a new sparkline poster handler
func NewSparklinePosterHandler(ctx context.Context) (*SparklinePosterHandler, error) {
	// Initialize sentiment history manager
	sentimentHistoryManager, err := state.NewSentimentHistoryManager(ctx, "hourstats-sentiment-history")
	if err != nil {
		return nil, fmt.Errorf("failed to create sentiment history manager: %w", err)
	}

	// Initialize sparkline generator
	sparklineGenerator := sparkline.NewSparklineGenerator(nil) // Use default config

	// Initialize AWS clients
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ssmClient := ssm.NewFromConfig(cfg)

	return &SparklinePosterHandler{
		sentimentHistoryManager: sentimentHistoryManager,
		sparklineGenerator:      sparklineGenerator,
		ssmClient:               ssmClient,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *SparklinePosterHandler) HandleRequest(ctx context.Context, event StepFunctionsEvent) (Response, error) {
	log.Printf("Sparkline poster received event: %+v", event)

	// Check if dry run mode is enabled
	dryRun, err := h.isDryRunMode(ctx)
	if err != nil {
		log.Printf("Failed to check dry run mode: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to check dry run mode: " + err.Error(),
		}, err
	}

	if dryRun {
		log.Printf("Dry run mode enabled, skipping sparkline post for run: %s", event.RunID)
		return Response{
			StatusCode: 200,
			Body:       "Dry run mode - sparkline post skipped",
			Posted:     false,
		}, nil
	}

	// Get 7 days of sentiment data
	dataPoints, err := h.sentimentHistoryManager.GetSentimentHistory(ctx, 7*24*time.Hour)
	if err != nil {
		log.Printf("Failed to get sentiment history: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get sentiment history: " + err.Error(),
		}, err
	}

	if len(dataPoints) < 2 {
		log.Printf("Insufficient sentiment data for sparkline (got %d points, need at least 2)", len(dataPoints))

		// Post a message about insufficient data instead of failing silently
		return h.postInsufficientDataMessage(ctx, len(dataPoints))
	}

	// Generate sparkline image
	imageData, err := h.sparklineGenerator.GenerateSentimentSparkline(dataPoints)
	if err != nil {
		log.Printf("Failed to generate sparkline: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to generate sparkline: " + err.Error(),
		}, err
	}

	// Get Bluesky credentials
	handle, password, err := h.getBlueskyCredentials(ctx)
	if err != nil {
		log.Printf("Failed to get Bluesky credentials: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get credentials: " + err.Error(),
		}, err
	}

	// Create Bluesky client
	blueskyClient := client.New(handle, password)
	if err := blueskyClient.Authenticate(); err != nil {
		log.Printf("Failed to authenticate with Bluesky: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to authenticate: " + err.Error(),
		}, err
	}

	// Analyze sentiment extremes
	extremeMessage := h.analyzeSentimentExtremes(dataPoints)
	
	// Post sparkline with embedded image to Bluesky
	postText := "📊 Seven day Bluesky sentiment"
	if extremeMessage != "" {
		postText += "\n\n" + extremeMessage
	}
	altText := "Seven day sentiment trend chart showing community mood over time"
	if err := blueskyClient.PostWithImage(ctx, postText, imageData, altText); err != nil {
		log.Printf("Failed to post sparkline with embedded image: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to post sparkline: " + err.Error(),
		}, err
	}

	log.Printf("Successfully posted sparkline for run: %s", event.RunID)
	return Response{
		StatusCode: 200,
		Body:       "Sparkline posted successfully",
		Posted:     true,
	}, nil
}

// isDryRunMode checks if dry run mode is enabled
func (h *SparklinePosterHandler) isDryRunMode(ctx context.Context) (bool, error) {
	result, err := h.ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/hourstats/settings/dry_run"),
		WithDecryption: aws.Bool(false),
	})
	if err != nil {
		return false, fmt.Errorf("failed to get dry run parameter: %w", err)
	}

	return *result.Parameter.Value == "true", nil
}

// getBlueskyCredentials retrieves credentials from SSM
func (h *SparklinePosterHandler) getBlueskyCredentials(ctx context.Context) (string, string, error) {
	parameterNames := []string{
		"/hourstats/bluesky/handle",
		"/hourstats/bluesky/password",
	}

	result, err := h.ssmClient.GetParameters(ctx, &ssm.GetParametersInput{
		Names:          parameterNames,
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to get parameters: %w", err)
	}

	params := make(map[string]string)
	for _, p := range result.Parameters {
		params[*p.Name] = *p.Value
	}

	handle, ok := params["/hourstats/bluesky/handle"]
	if !ok {
		return "", "", fmt.Errorf("handle parameter not found")
	}

	password, ok := params["/hourstats/bluesky/password"]
	if !ok {
		return "", "", fmt.Errorf("password parameter not found")
	}

	return handle, password, nil
}

// analyzeSentimentExtremes checks if the latest sentiment is the highest or lowest for the week
func (h *SparklinePosterHandler) analyzeSentimentExtremes(dataPoints []state.SentimentDataPoint) string {
	if len(dataPoints) < 2 {
		return ""
	}
	
	// Get the latest sentiment (last data point)
	latestSentiment := dataPoints[len(dataPoints)-1].NetSentimentPercent
	
	// Find min and max sentiment values
	minSentiment := dataPoints[0].NetSentimentPercent
	maxSentiment := dataPoints[0].NetSentimentPercent
	
	for _, point := range dataPoints {
		if point.NetSentimentPercent < minSentiment {
			minSentiment = point.NetSentimentPercent
		}
		if point.NetSentimentPercent > maxSentiment {
			maxSentiment = point.NetSentimentPercent
		}
	}
	
	// Check if latest sentiment is the lowest (with small tolerance for floating point comparison)
	if latestSentiment <= minSentiment+0.01 {
		return "* Lowest sentiment for the charted period"
	}
	
	// Check if latest sentiment is the highest (with small tolerance for floating point comparison)
	if latestSentiment >= maxSentiment-0.01 {
		return "* Highest sentiment for the charted period"
	}
	
	return ""
}

// postInsufficientDataMessage posts a message about insufficient data
func (h *SparklinePosterHandler) postInsufficientDataMessage(ctx context.Context, dataPointCount int) (Response, error) {
	// Get Bluesky credentials
	handle, password, err := h.getBlueskyCredentials(ctx)
	if err != nil {
		log.Printf("Failed to get Bluesky credentials: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get credentials: " + err.Error(),
		}, err
	}

	// Create Bluesky client
	blueskyClient := client.New(handle, password)
	if err := blueskyClient.Authenticate(); err != nil {
		log.Printf("Failed to authenticate with Bluesky: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to authenticate: " + err.Error(),
		}, err
	}

	// Create appropriate message based on data availability
	var message string
	if dataPointCount == 0 {
		message = "📊 Building sentiment history...\n\n" +
			"⏳ Sparkline charts will be available after collecting 7 days of data.\n" +
			"📈 First chart expected in ~7 days.\n\n" +
			"💡 In the meantime, check out the hourly sentiment summaries above!"
	} else {
		message = fmt.Sprintf("📊 Building sentiment history...\n\n"+
			"⏳ Sparkline charts will be available after collecting 7 days of data.\n"+
			"📈 Currently have %d data points, need 2+ for charts.\n\n"+
			"💡 In the meantime, check out the hourly sentiment summaries above!", dataPointCount)
	}

	// Post the message
	if err := blueskyClient.PostWithFacets(ctx, message, nil); err != nil {
		log.Printf("Failed to post insufficient data message: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to post message: " + err.Error(),
		}, err
	}

	log.Printf("Posted insufficient data message (data points: %d)", dataPointCount)
	return Response{
		StatusCode: 200,
		Body:       "Insufficient data message posted",
		Posted:     true,
	}, nil
}

func main() {
	ctx := context.Background()
	handler, err := NewSparklinePosterHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create sparkline poster handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
