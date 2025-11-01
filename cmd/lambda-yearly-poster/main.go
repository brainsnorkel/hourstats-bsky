package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// Event represents the EventBridge event structure
type Event struct {
	Source string `json:"source"`
	Time   string `json:"time"`
	Action string `json:"action,omitempty"`
}

// Response represents the Lambda response
type Response struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
	Posted     bool   `json:"posted"`
}

// YearlyPosterHandler handles the yearly poster Lambda function
type YearlyPosterHandler struct {
	dailySentimentManager    *state.DailySentimentManager
	yearlySparklineGenerator *sparkline.YearlySparklineGenerator
	ssmClient                *ssm.Client
}

// NewYearlyPosterHandler creates a new yearly poster handler
func NewYearlyPosterHandler(ctx context.Context) (*YearlyPosterHandler, error) {
	// Initialize daily sentiment manager
	dailySentimentManager, err := state.NewDailySentimentManager(ctx, "hourstats-daily-sentiment")
	if err != nil {
		return nil, fmt.Errorf("failed to create daily sentiment manager: %w", err)
	}

	// Initialize yearly sparkline generator
	yearlySparklineGenerator := sparkline.NewYearlySparklineGenerator(nil) // Use default config

	// Initialize AWS clients
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ssmClient := ssm.NewFromConfig(cfg)

	return &YearlyPosterHandler{
		dailySentimentManager:    dailySentimentManager,
		yearlySparklineGenerator: yearlySparklineGenerator,
		ssmClient:                ssmClient,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *YearlyPosterHandler) HandleRequest(ctx context.Context, event Event) (Response, error) {
	log.Printf("Yearly poster received event: %+v", event)

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
		log.Printf("Dry run mode enabled, skipping yearly post")
		return Response{
			StatusCode: 200,
			Body:       "Dry run mode - yearly post skipped",
			Posted:     false,
		}, nil
	}

	// Get 365 days of daily sentiment data
	yearlyData, err := h.dailySentimentManager.GetYearlySentimentData(ctx)
	if err != nil {
		log.Printf("Failed to get yearly sentiment data: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get yearly sentiment data: " + err.Error(),
		}, err
	}

	if len(yearlyData) < 30 {
		log.Printf("Insufficient yearly sentiment data for chart (got %d days, need at least 30)", len(yearlyData))
		return h.postInsufficientDataMessage(ctx, len(yearlyData))
	}

	// Generate yearly sparkline image
	imageData, err := h.yearlySparklineGenerator.GenerateYearlySentimentSparkline(yearlyData)
	if err != nil {
		log.Printf("Failed to generate yearly sparkline: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to generate yearly sparkline: " + err.Error(),
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

	// Analyze yearly sentiment extremes with Wikipedia links
	extremeMessage := h.analyzeYearlySentimentExtremes(yearlyData)

	// Generate comprehensive alt text
	altText := h.generateYearlyAltText(yearlyData)

	// Post yearly sparkline with embedded image to Bluesky
	// Format: "Bluesky Sentiment {start date} - {end date}"
	var postText string
	if len(yearlyData) > 0 {
		startDate := yearlyData[0].Timestamp.Format("2006-01-02")
		endDate := yearlyData[len(yearlyData)-1].Timestamp.Format("2006-01-02")
		postText = fmt.Sprintf("Bluesky Sentiment %s - %s", startDate, endDate)
	} else {
		postText = "Bluesky Sentiment"
	}
	if extremeMessage != "" {
		postText += "\n\n" + extremeMessage
	}

	// Truncate post text to 300 graphemes (Bluesky limit)
	maxGraphemes := 300
	truncatedPostText := postText
	if len([]rune(postText)) > maxGraphemes {
		runes := []rune(postText)
		if len(runes) > maxGraphemes {
			truncated := string(runes[:maxGraphemes])
			lastNewline := strings.LastIndex(truncated, "\n")
			if lastNewline > maxGraphemes/2 {
				truncatedPostText = truncated[:lastNewline]
			} else {
				truncatedPostText = truncated
			}
			log.Printf("Post text truncated from %d to %d graphemes", len(runes), len([]rune(truncatedPostText)))
		}
	}

	// Create facets for Wikipedia URLs to make them clickable (based on truncated text)
	wikipediaFacets := client.CreateWikipediaLinkFacets(truncatedPostText)

	// Post the yearly chart and get post URI/CID
	var postURI, postCID string
	if len(wikipediaFacets) > 0 {
		postURI, postCID, err = blueskyClient.PostWithImage(ctx, truncatedPostText, imageData, altText, wikipediaFacets)
	} else {
		postURI, postCID, err = blueskyClient.PostWithImage(ctx, truncatedPostText, imageData, altText)
	}
	if err != nil {
		log.Printf("Failed to post yearly sparkline: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to post yearly sparkline: " + err.Error(),
		}, err
	}

	// Pin the post to the account profile
	err = blueskyClient.PinPost(ctx, postURI, postCID)
	if err != nil {
		log.Printf("Failed to pin yearly post: %v (post was successful)", err)
		// Don't fail the entire operation if pinning fails
	} else {
		log.Printf("Yearly sentiment chart posted and pinned successfully")
	}

	log.Printf("Successfully posted yearly sentiment chart with %d days of data", len(yearlyData))
	return Response{
		StatusCode: 200,
		Body:       "Yearly sentiment chart posted successfully",
		Posted:     true,
	}, nil
}

// isDryRunMode checks if dry run mode is enabled
func (h *YearlyPosterHandler) isDryRunMode(ctx context.Context) (bool, error) {
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
func (h *YearlyPosterHandler) getBlueskyCredentials(ctx context.Context) (string, string, error) {
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

// generateWikipediaLink generates a Wikipedia link for a given date
func (h *YearlyPosterHandler) generateWikipediaLink(dateStr string) string {
	// Parse the date
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return ""
	}

	// Format date as Month_Day for Wikipedia URL
	// Example: 2025-09-18 -> September_2025#2025_September_18
	monthName := date.Format("January")
	year := date.Year()
	day := date.Day()

	// Wikipedia URL format: https://en.wikipedia.org/wiki/Portal:Current_events/September_2025#2025_September_18
	url := fmt.Sprintf("https://en.wikipedia.org/wiki/Portal:Current_events/%s_%d#%d_%s_%d",
		monthName, year, year, monthName, day)

	return url
}

// analyzeYearlySentimentExtremes checks for notable sentiment patterns in the yearly data
func (h *YearlyPosterHandler) analyzeYearlySentimentExtremes(dataPoints []state.YearlySparklineDataPoint) string {
	if len(dataPoints) < 30 {
		return ""
	}

	// Get the latest sentiment (most recent data point)
	latestSentiment := dataPoints[len(dataPoints)-1].AverageSentiment

	// Find min and max sentiment values for the year
	minSentiment := dataPoints[0].AverageSentiment
	maxSentiment := dataPoints[0].AverageSentiment
	var minDate, maxDate string

	for _, point := range dataPoints {
		if point.AverageSentiment < minSentiment {
			minSentiment = point.AverageSentiment
			minDate = point.Date
		}
		if point.AverageSentiment > maxSentiment {
			maxSentiment = point.AverageSentiment
			maxDate = point.Date
		}
	}

	// Calculate yearly average
	var sum float64
	for _, point := range dataPoints {
		sum += point.AverageSentiment
	}
	yearlyAverage := sum / float64(len(dataPoints))

	// Generate insights
	var insights []string

	// Check if latest sentiment is notably different from yearly average
	if latestSentiment > yearlyAverage+5 {
		insights = append(insights, "Currently above yearly average")
	} else if latestSentiment < yearlyAverage-5 {
		insights = append(insights, "Currently below yearly average")
	}

	// Always include highest and lowest sentiment with Wikipedia links
	// Format dates for display as "Jan 2" format
	// The date + "events" text will be linked via facets
	if minDate != "" {
		if date, err := time.Parse("2006-01-02", minDate); err == nil {
			minDateDisplay := date.Format("Jan 2")
			// Format as "Sep 18 events" which will be linked via facets
			linkText := fmt.Sprintf("%s events", minDateDisplay)
			insights = append(insights, fmt.Sprintf("Lowest: %.1f%% %s", minSentiment, linkText))
		} else {
			insights = append(insights, fmt.Sprintf("Lowest: %.1f%%", minSentiment))
		}
	}

	if maxDate != "" {
		if date, err := time.Parse("2006-01-02", maxDate); err == nil {
			maxDateDisplay := date.Format("Jan 2")
			// Format as "Oct 10 events" which will be linked via facets
			linkText := fmt.Sprintf("%s events", maxDateDisplay)
			insights = append(insights, fmt.Sprintf("Highest: %.1f%% %s", maxSentiment, linkText))
		} else {
			insights = append(insights, fmt.Sprintf("Highest: %.1f%%", maxSentiment))
		}
	}

	if len(insights) == 0 {
		return ""
	}

	return strings.Join(insights, "\n")
}

// generateYearlyAltText creates comprehensive alt text for the yearly sparkline chart
func (h *YearlyPosterHandler) generateYearlyAltText(dataPoints []state.YearlySparklineDataPoint) string {
	if len(dataPoints) < 2 {
		return "Yearly sentiment trend chart showing community mood over the past year"
	}

	// Calculate statistics
	stats := h.calculateYearlySentimentStats(dataPoints)

	// Format dates for readability
	formatDate := func(dateStr string) string {
		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return dateStr
		}
		return date.Format("Jan 2, 2006")
	}

	// Build comprehensive alt text
	altText := "Yearly Bluesky sentiment trend chart showing daily averages over the past year. "

	// Add current sentiment
	altText += fmt.Sprintf("Current sentiment: %.1f%% (%s). ",
		stats.Current, formatDate(stats.CurrentDate))

	// Add highest sentiment
	altText += fmt.Sprintf("Highest sentiment: %.1f%% (%s). ",
		stats.Highest, formatDate(stats.HighestDate))

	// Add lowest sentiment
	altText += fmt.Sprintf("Lowest sentiment: %.1f%% (%s). ",
		stats.Lowest, formatDate(stats.LowestDate))

	// Add yearly average sentiment
	altText += fmt.Sprintf("Yearly average sentiment: %.1f%%. ", stats.Average)

	// Add trend information
	if stats.Trend > 0 {
		altText += "Trending positive over the year."
	} else if stats.Trend < 0 {
		altText += "Trending negative over the year."
	} else {
		altText += "Stable sentiment over the year."
	}

	return altText
}

// YearlySentimentStats holds calculated yearly sentiment statistics
type YearlySentimentStats struct {
	Current     float64
	CurrentDate string
	Highest     float64
	HighestDate string
	Lowest      float64
	LowestDate  string
	Average     float64
	Trend       float64
}

// calculateYearlySentimentStats calculates comprehensive yearly sentiment statistics
func (h *YearlyPosterHandler) calculateYearlySentimentStats(dataPoints []state.YearlySparklineDataPoint) YearlySentimentStats {
	if len(dataPoints) == 0 {
		return YearlySentimentStats{}
	}

	// Initialize with first data point
	stats := YearlySentimentStats{
		Current:     dataPoints[0].AverageSentiment,
		CurrentDate: dataPoints[0].Date,
		Highest:     dataPoints[0].AverageSentiment,
		HighestDate: dataPoints[0].Date,
		Lowest:      dataPoints[0].AverageSentiment,
		LowestDate:  dataPoints[0].Date,
	}

	// Calculate sum for average
	sum := 0.0
	for _, point := range dataPoints {
		sentiment := point.AverageSentiment
		sum += sentiment

		// Track highest
		if sentiment > stats.Highest {
			stats.Highest = sentiment
			stats.HighestDate = point.Date
		}

		// Track lowest
		if sentiment < stats.Lowest {
			stats.Lowest = sentiment
			stats.LowestDate = point.Date
		}
	}

	// Calculate average
	stats.Average = sum / float64(len(dataPoints))

	// Get current (most recent) sentiment
	latest := dataPoints[len(dataPoints)-1]
	stats.Current = latest.AverageSentiment
	stats.CurrentDate = latest.Date

	// Calculate trend (simple linear trend: first vs last)
	if len(dataPoints) > 1 {
		first := dataPoints[0].AverageSentiment
		last := dataPoints[len(dataPoints)-1].AverageSentiment
		stats.Trend = last - first
	}

	return stats
}

// postInsufficientDataMessage posts a message about insufficient yearly data
func (h *YearlyPosterHandler) postInsufficientDataMessage(ctx context.Context, dataPointCount int) (Response, error) {
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
		message = "📊 Building yearly sentiment history...\n\n" +
			"⏳ Yearly sentiment charts will be available after collecting 30+ days of daily data.\n" +
			"📈 First yearly chart expected in ~30 days.\n\n" +
			"💡 In the meantime, check out the daily sentiment summaries and weekly sparklines!"
	} else {
		message = fmt.Sprintf("📊 Building yearly sentiment history...\n\n"+
			"⏳ Yearly sentiment charts will be available after collecting 30+ days of daily data.\n"+
			"📈 Currently have %d days, need 30+ for yearly charts.\n\n"+
			"💡 In the meantime, check out the daily sentiment summaries and weekly sparklines!", dataPointCount)
	}

	// Post the message
	if err := blueskyClient.PostWithFacets(ctx, message, nil); err != nil {
		log.Printf("Failed to post insufficient data message: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to post message: " + err.Error(),
		}, err
	}

	log.Printf("Posted insufficient yearly data message (data points: %d)", dataPointCount)
	return Response{
		StatusCode: 200,
		Body:       "Insufficient yearly data message posted",
		Posted:     true,
	}, nil
}

func main() {
	ctx := context.Background()
	handler, err := NewYearlyPosterHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create yearly poster handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
