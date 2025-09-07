package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func main() {
	// Create test sentiment data points for the last 48 hours
	dataPoints := generateTestData()

	// Create sparkline generator
	generator := sparkline.NewSparklineGenerator(nil)

	// Generate sparkline image
	imageData, err := generator.GenerateSentimentSparkline(dataPoints)
	if err != nil {
		log.Fatalf("Failed to generate sparkline: %v", err)
	}

	// Save to file
	filename := "test-sparkline.png"
	err = os.WriteFile(filename, imageData, 0644)
	if err != nil {
		log.Fatalf("Failed to write image file: %v", err)
	}

	fmt.Printf("✅ Generated sparkline image: %s (%d bytes)\n", filename, len(imageData))
	fmt.Printf("📊 Data points: %d\n", len(dataPoints))
	fmt.Printf("📈 Time range: %s to %s\n", 
		dataPoints[0].Timestamp.Format("2006-01-02 15:04:05"),
		dataPoints[len(dataPoints)-1].Timestamp.Format("2006-01-02 15:04:05"))
}

func generateTestData() []state.SentimentDataPoint {
	now := time.Now()
	var dataPoints []state.SentimentDataPoint

	// Generate data points every 2 hours for the last 48 hours
	for i := 0; i < 24; i++ {
		hoursAgo := 48 - (i * 2)
		timestamp := now.Add(-time.Duration(hoursAgo) * time.Hour)

		// Create realistic sentiment variation
		baseSentiment := float64(0)
		if i < 8 { // First 16 hours: mostly negative
			baseSentiment = -30 + float64(i)*2
		} else if i < 16 { // Middle 16 hours: mixed
			baseSentiment = -10 + float64(i-8)*2.5
		} else { // Last 16 hours: mostly positive
			baseSentiment = 10 + float64(i-16)*1.5
		}

		// Add some random variation
		variation := float64((i%3-1) * 5) // -5, 0, or +5
		sentiment := baseSentiment + variation

		// Clamp to reasonable range
		if sentiment > 100 {
			sentiment = 100
		} else if sentiment < -100 {
			sentiment = -100
		}

		// Determine category
		var category string
		if sentiment > 10 {
			category = "positive"
		} else if sentiment < -10 {
			category = "negative"
		} else {
			category = "neutral"
		}

		dataPoint := state.SentimentDataPoint{
			RunID:               fmt.Sprintf("test-run-%d", i),
			Timestamp:           timestamp,
			AverageCompoundScore: sentiment / 100.0,
			NetSentimentPercent:  sentiment,
			SentimentCategory:    category,
			TotalPosts:           100 + i*5, // Increasing post count over time
		}

		dataPoints = append(dataPoints, dataPoint)
	}

	return dataPoints
}
