package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func main() {
	ctx := context.Background()

	// Initialize daily sentiment manager
	dailySentimentManager, err := state.NewDailySentimentManager(ctx, "hourstats-daily-sentiment")
	if err != nil {
		log.Fatalf("Failed to create daily sentiment manager: %v", err)
	}

	// Get 365 days of daily sentiment data
	yearlyData, err := dailySentimentManager.GetYearlySentimentData(ctx)
	if err != nil {
		log.Fatalf("Failed to get yearly sentiment data: %v", err)
	}

	if len(yearlyData) < 30 {
		log.Fatalf("Insufficient yearly sentiment data for chart (got %d days, need at least 30)", len(yearlyData))
	}

	log.Printf("Retrieved %d days of sentiment data", len(yearlyData))

	// Find min and max for display
	minSentiment := yearlyData[0].AverageSentiment
	maxSentiment := yearlyData[0].AverageSentiment
	var minDate, maxDate string

	for _, point := range yearlyData {
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
	for _, point := range yearlyData {
		sum += point.AverageSentiment
	}
	yearlyAverage := sum / float64(len(yearlyData))

	log.Printf("Yearly stats: min=%.2f%% (%s), max=%.2f%% (%s), avg=%.2f%%", 
		minSentiment, minDate, maxSentiment, maxDate, yearlyAverage)

	// Initialize yearly sparkline generator
	yearlySparklineGenerator := sparkline.NewYearlySparklineGenerator(nil)

	// Generate yearly sparkline image
	imageData, err := yearlySparklineGenerator.GenerateYearlySentimentSparkline(yearlyData)
	if err != nil {
		log.Fatalf("Failed to generate yearly sparkline: %v", err)
	}

	// Generate post text (simulate the yearly poster logic)
	// Format: "Bluesky Sentiment {start date} - {end date}"
	var postText string
	if len(yearlyData) > 0 {
		startDate := yearlyData[0].Timestamp.Format("2006-01-02")
		endDate := yearlyData[len(yearlyData)-1].Timestamp.Format("2006-01-02")
		postText = fmt.Sprintf("📊 Bluesky Sentiment %s - %s", startDate, endDate)
	} else {
		postText = "📊 Bluesky Sentiment"
	}
	
	// Generate Wikipedia links
	generateWikipediaLink := func(dateStr string) string {
		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return ""
		}
		monthName := date.Format("January")
		year := date.Year()
		day := date.Day()
		return fmt.Sprintf("https://en.wikipedia.org/wiki/Portal:Current_events/%s_%d#%d_%s_%d",
			monthName, year, year, monthName, day)
	}

	// Add extreme sentiment information
	var extremeMessages []string
	if minDate != "" {
		if date, err := time.Parse("2006-01-02", minDate); err == nil {
			minDateDisplay := date.Format("Jan 2")
			minWikiLink := generateWikipediaLink(minDate)
			if minWikiLink != "" {
				extremeMessages = append(extremeMessages, fmt.Sprintf("Lowest: %.1f%% %s events %s", minSentiment, minDateDisplay, minWikiLink))
			} else {
				extremeMessages = append(extremeMessages, fmt.Sprintf("Lowest: %.1f%% %s", minSentiment, minDateDisplay))
			}
		} else {
			extremeMessages = append(extremeMessages, fmt.Sprintf("Lowest: %.1f%%", minSentiment))
		}
	}
	if maxDate != "" {
		if date, err := time.Parse("2006-01-02", maxDate); err == nil {
			maxDateDisplay := date.Format("Jan 2")
			maxWikiLink := generateWikipediaLink(maxDate)
			if maxWikiLink != "" {
				extremeMessages = append(extremeMessages, fmt.Sprintf("Highest: %.1f%% %s events %s", maxSentiment, maxDateDisplay, maxWikiLink))
			} else {
				extremeMessages = append(extremeMessages, fmt.Sprintf("Highest: %.1f%% %s", maxSentiment, maxDateDisplay))
			}
		} else {
			extremeMessages = append(extremeMessages, fmt.Sprintf("Highest: %.1f%%", maxSentiment))
		}
	}
	
	if len(extremeMessages) > 0 {
		postText += "\n\n" + strings.Join(extremeMessages, "\n")
	}

	log.Printf("Generated post text:\n%s", postText)

	// Ensure test-results directory exists
	testResultsDir := filepath.Join("..", "..", "test-results")
	if err := os.MkdirAll(testResultsDir, 0755); err != nil {
		log.Fatalf("Failed to create test-results directory: %v", err)
	}

	// Save the image
	imagePath := filepath.Join(testResultsDir, "yearly-sentiment-chart.png")
	if err := os.WriteFile(imagePath, imageData, 0644); err != nil {
		log.Fatalf("Failed to save image: %v", err)
	}
	log.Printf("Saved chart image to: %s", imagePath)

	// Save the post text
	textPath := filepath.Join(testResultsDir, "yearly-post-text.txt")
	if err := os.WriteFile(textPath, []byte(postText), 0644); err != nil {
		log.Fatalf("Failed to save post text: %v", err)
	}
	log.Printf("Saved post text to: %s", textPath)

	// Generate alt text
	formatDate := func(dateStr string) string {
		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return dateStr
		}
		return date.Format("Jan 2, 2006")
	}

	altText := fmt.Sprintf("Yearly Bluesky sentiment trend chart showing daily averages over the past year. "+
		"Current sentiment: %.1f%% (%s). "+
		"Highest sentiment: %.1f%% (%s). "+
		"Lowest sentiment: %.1f%% (%s). "+
		"Yearly average sentiment: %.1f%%.",
		yearlyData[len(yearlyData)-1].AverageSentiment, formatDate(yearlyData[len(yearlyData)-1].Date),
		maxSentiment, formatDate(maxDate),
		minSentiment, formatDate(minDate),
		yearlyAverage)

	altPath := filepath.Join(testResultsDir, "yearly-post-alt-text.txt")
	if err := os.WriteFile(altPath, []byte(altText), 0644); err != nil {
		log.Fatalf("Failed to save alt text: %v", err)
	}
	log.Printf("Saved alt text to: %s", altPath)

	fmt.Println("\n✅ Successfully generated yearly post test results!")
	fmt.Printf("📊 Chart: %s\n", imagePath)
	fmt.Printf("📝 Post text: %s\n", textPath)
	fmt.Printf("🎨 Alt text: %s\n", altPath)
}

