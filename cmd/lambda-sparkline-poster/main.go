package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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
	s3Client                *s3.Client
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

	s3Client := s3.NewFromConfig(cfg)
	ssmClient := ssm.NewFromConfig(cfg)

	return &SparklinePosterHandler{
		sentimentHistoryManager: sentimentHistoryManager,
		sparklineGenerator:      sparklineGenerator,
		s3Client:                s3Client,
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

	// Get 48 hours of sentiment data
	dataPoints, err := h.sentimentHistoryManager.GetSentimentHistory(ctx, 48*time.Hour)
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

	// Upload image to S3
	imageURL, err := h.uploadImageToS3(ctx, imageData, event.RunID)
	if err != nil {
		log.Printf("Failed to upload image to S3: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to upload image: " + err.Error(),
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

	// Post sparkline to Bluesky
	postText := "📊 Sentiment for the last 48 hours"
	if err := h.postSparklineToBluesky(ctx, blueskyClient, postText, imageURL); err != nil {
		log.Printf("Failed to post sparkline to Bluesky: %v", err)
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

// uploadImageToS3 uploads the sparkline image to S3
func (h *SparklinePosterHandler) uploadImageToS3(ctx context.Context, imageData []byte, runID string) (string, error) {
	bucketName := "hourstats-sparkline-images"
	key := fmt.Sprintf("sparklines/%s-%d.png", runID, time.Now().Unix())

	_, err := h.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        bytes.NewReader(imageData),
		ContentType: aws.String("image/png"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload to S3: %w", err)
	}

	// Return public URL
	imageURL := fmt.Sprintf("https://%s.s3.amazonaws.com/%s", bucketName, key)
	return imageURL, nil
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
			"⏳ Sparkline charts will be available after collecting 48 hours of data.\n" +
			"📈 First chart expected in ~24-48 hours.\n\n" +
			"💡 In the meantime, check out the hourly sentiment summaries above!"
	} else {
		message = fmt.Sprintf("📊 Building sentiment history...\n\n"+
			"⏳ Sparkline charts will be available after collecting 48 hours of data.\n"+
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

// postSparklineToBluesky posts the sparkline to Bluesky
func (h *SparklinePosterHandler) postSparklineToBluesky(ctx context.Context, client *client.BlueskyClient, text, imageURL string) error {
	// For now, we'll post a text-only version with the image URL
	// In a full implementation, we'd need to implement image embedding in the Bluesky client
	postText := fmt.Sprintf("%s\n\n📈 View chart: %s", text, imageURL)
	
	return client.PostWithFacets(ctx, postText, nil)
}

func main() {
	ctx := context.Background()
	handler, err := NewSparklinePosterHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create sparkline poster handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
