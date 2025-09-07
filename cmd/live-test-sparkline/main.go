package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func main() {
	fmt.Println("🚀 Live Testing 48-Hour Sentiment Sparkline Feature")
	fmt.Println("==================================================")

	// Test 1: Generate test data and create sparkline
	fmt.Println("\n📊 Test 1: Generating sparkline with test data...")
	testSparklineGeneration()

	// Test 2: Test with realistic historical data
	fmt.Println("\n📈 Test 2: Generating sparkline with realistic data...")
	testRealisticData()

	// Test 3: Test edge cases
	fmt.Println("\n🔍 Test 3: Testing edge cases...")
	testEdgeCases()

	// Test 4: Performance test
	fmt.Println("\n⚡ Test 4: Performance testing...")
	testPerformance()

	fmt.Println("\n✅ All sparkline tests completed successfully!")
}

func testSparklineGeneration() {
	// Create test data points for the last 48 hours
	dataPoints := generateTestData()

	// Create sparkline generator
	generator := sparkline.NewSparklineGenerator(nil)

	// Generate sparkline image
	imageData, err := generator.GenerateSentimentSparkline(dataPoints)
	if err != nil {
		log.Fatalf("Failed to generate sparkline: %v", err)
	}

	// Save to file
	filename := "live-test-sparkline.png"
	err = os.WriteFile(filename, imageData, 0644)
	if err != nil {
		log.Fatalf("Failed to write image file: %v", err)
	}

	fmt.Printf("✅ Generated sparkline: %s (%d bytes)\n", filename, len(imageData))
	fmt.Printf("📊 Data points: %d\n", len(dataPoints))
	fmt.Printf("📈 Time range: %s to %s\n",
		dataPoints[0].Timestamp.Format("2006-01-02 15:04:05"),
		dataPoints[len(dataPoints)-1].Timestamp.Format("2006-01-02 15:04:05"))

	// Show sentiment statistics
	positiveCount := 0
	negativeCount := 0
	neutralCount := 0
	totalSentiment := 0.0

	for _, point := range dataPoints {
		totalSentiment += point.NetSentimentPercent
		switch point.SentimentCategory {
		case "positive":
			positiveCount++
		case "negative":
			negativeCount++
		case "neutral":
			neutralCount++
		}
	}

	avgSentiment := totalSentiment / float64(len(dataPoints))
	fmt.Printf("📊 Sentiment breakdown: +%d, -%d, ~%d (avg: %.1f%%)\n",
		positiveCount, negativeCount, neutralCount, avgSentiment)
}

func testRealisticData() {
	// Generate more realistic data that mimics real sentiment patterns
	dataPoints := generateRealisticData()

	generator := sparkline.NewSparklineGenerator(nil)
	imageData, err := generator.GenerateSentimentSparkline(dataPoints)
	if err != nil {
		log.Fatalf("Failed to generate realistic sparkline: %v", err)
	}

	filename := "realistic-sparkline.png"
	err = os.WriteFile(filename, imageData, 0644)
	if err != nil {
		log.Fatalf("Failed to write realistic image file: %v", err)
	}

	fmt.Printf("✅ Generated realistic sparkline: %s (%d bytes)\n", filename, len(imageData))
	fmt.Printf("📊 Data points: %d\n", len(dataPoints))

	// Analyze the data
	analyzeDataPoints(dataPoints)
}

func testEdgeCases() {
	generator := sparkline.NewSparklineGenerator(nil)

	// Test 1: Empty data
	fmt.Println("  Testing empty data...")
	_, err := generator.GenerateSentimentSparkline([]state.SentimentDataPoint{})
	if err == nil {
		log.Fatal("Expected error for empty data")
	}
	fmt.Printf("  ✅ Empty data handled correctly: %v\n", err)

	// Test 2: Single data point
	fmt.Println("  Testing single data point...")
	singlePoint := []state.SentimentDataPoint{
		{
			RunID:               "single-test",
			Timestamp:           time.Now(),
			NetSentimentPercent: 50.0,
			SentimentCategory:   "positive",
			TotalPosts:          100,
		},
	}
	imageData, err := generator.GenerateSentimentSparkline(singlePoint)
	if err != nil {
		log.Fatalf("Failed to generate single point sparkline: %v", err)
	}
	fmt.Printf("  ✅ Single data point handled: %d bytes\n", len(imageData))

	// Test 3: Extreme values
	fmt.Println("  Testing extreme values...")
	extremeData := generateExtremeData()
	imageData, err = generator.GenerateSentimentSparkline(extremeData)
	if err != nil {
		log.Fatalf("Failed to generate extreme sparkline: %v", err)
	}
	fmt.Printf("  ✅ Extreme values handled: %d bytes\n", len(imageData))

	// Test 4: Custom configuration
	fmt.Println("  Testing custom configuration...")
	customConfig := &sparkline.SparklineConfig{
		Width:  800,
		Height: 400,
	}
	customGenerator := sparkline.NewSparklineGenerator(customConfig)
	imageData, err = customGenerator.GenerateSentimentSparkline(extremeData)
	if err != nil {
		log.Fatalf("Failed to generate custom sparkline: %v", err)
	}
	fmt.Printf("  ✅ Custom config handled: %d bytes\n", len(imageData))
}

func testPerformance() {
	// Generate large dataset
	dataPoints := generateLargeDataset()

	generator := sparkline.NewSparklineGenerator(nil)

	// Time the generation
	start := time.Now()
	imageData, err := generator.GenerateSentimentSparkline(dataPoints)
	duration := time.Since(start)

	if err != nil {
		log.Fatalf("Failed to generate performance sparkline: %v", err)
	}

	fmt.Printf("✅ Generated sparkline with %d data points in %v\n", len(dataPoints), duration)
	fmt.Printf("📊 Image size: %d bytes\n", len(imageData))
	fmt.Printf("⚡ Performance: %.2f data points/second\n", float64(len(dataPoints))/duration.Seconds())
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
			TotalPosts:           100 + i*5,
		}

		dataPoints = append(dataPoints, dataPoint)
	}

	return dataPoints
}

func generateRealisticData() []state.SentimentDataPoint {
	now := time.Now()
	var dataPoints []state.SentimentDataPoint

	// Simulate a realistic 48-hour period with different sentiment patterns
	patterns := []struct {
		startHour int
		endHour   int
		trend     string
		base      float64
	}{
		{0, 8, "negative", -20},   // Night: negative sentiment
		{8, 16, "positive", 15},   // Morning/afternoon: positive
		{16, 24, "mixed", 0},      // Evening: mixed
		{24, 32, "negative", -10}, // Next night: slightly negative
		{32, 40, "positive", 20},  // Next day: very positive
		{40, 48, "declining", 5},  // Late day: declining
	}

	for _, pattern := range patterns {
		for hour := pattern.startHour; hour < pattern.endHour; hour++ {
			timestamp := now.Add(-time.Duration(48-hour) * time.Hour)

			// Calculate sentiment based on pattern
			var sentiment float64
			switch pattern.trend {
			case "negative":
				sentiment = pattern.base - float64(hour%4)*5
			case "positive":
				sentiment = pattern.base + float64(hour%3)*3
			case "mixed":
				sentiment = pattern.base + float64((hour%6-3))*8
			case "declining":
				sentiment = pattern.base - float64(hour-pattern.startHour)*2
			}

			// Add some realistic noise
			noise := float64((hour%5-2) * 3)
			sentiment += noise

			// Clamp to range
			if sentiment > 100 {
				sentiment = 100
			} else if sentiment < -100 {
				sentiment = -100
			}

			// Determine category
			var category string
			if sentiment > 15 {
				category = "positive"
			} else if sentiment < -15 {
				category = "negative"
			} else {
				category = "neutral"
			}

			dataPoint := state.SentimentDataPoint{
				RunID:               fmt.Sprintf("realistic-run-%d", hour),
				Timestamp:           timestamp,
				AverageCompoundScore: sentiment / 100.0,
				NetSentimentPercent:  sentiment,
				SentimentCategory:    category,
				TotalPosts:           150 + hour*3,
			}

			dataPoints = append(dataPoints, dataPoint)
		}
	}

	return dataPoints
}

func generateExtremeData() []state.SentimentDataPoint {
	now := time.Now()
	var dataPoints []state.SentimentDataPoint

	// Generate extreme values
	extremeValues := []float64{-100, -80, -50, -20, 0, 20, 50, 80, 100, 90, 70, 40, 10, -10, -30, -60, -90, -100}

	for i, value := range extremeValues {
		timestamp := now.Add(-time.Duration(48-i*2) * time.Hour)

		var category string
		if value > 15 {
			category = "positive"
		} else if value < -15 {
			category = "negative"
		} else {
			category = "neutral"
		}

		dataPoint := state.SentimentDataPoint{
			RunID:               fmt.Sprintf("extreme-run-%d", i),
			Timestamp:           timestamp,
			AverageCompoundScore: value / 100.0,
			NetSentimentPercent:  value,
			SentimentCategory:    category,
			TotalPosts:           100,
		}

		dataPoints = append(dataPoints, dataPoint)
	}

	return dataPoints
}

func generateLargeDataset() []state.SentimentDataPoint {
	now := time.Now()
	var dataPoints []state.SentimentDataPoint

	// Generate data every 30 minutes for 48 hours (96 data points)
	for i := 0; i < 96; i++ {
		minutesAgo := 48*60 - (i * 30)
		timestamp := now.Add(-time.Duration(minutesAgo) * time.Minute)

		// Create a sine wave pattern with noise
		sineValue := math.Sin(float64(i) * math.Pi / 24) * 50
		noise := float64((i%7-3) * 10)
		sentiment := sineValue + noise

		// Clamp to range
		if sentiment > 100 {
			sentiment = 100
		} else if sentiment < -100 {
			sentiment = -100
		}

		var category string
		if sentiment > 15 {
			category = "positive"
		} else if sentiment < -15 {
			category = "negative"
		} else {
			category = "neutral"
		}

		dataPoint := state.SentimentDataPoint{
			RunID:               fmt.Sprintf("large-run-%d", i),
			Timestamp:           timestamp,
			AverageCompoundScore: sentiment / 100.0,
			NetSentimentPercent:  sentiment,
			SentimentCategory:    category,
			TotalPosts:           200 + i,
		}

		dataPoints = append(dataPoints, dataPoint)
	}

	return dataPoints
}

func analyzeDataPoints(dataPoints []state.SentimentDataPoint) {
	if len(dataPoints) == 0 {
		return
	}

	// Calculate statistics
	minSentiment := dataPoints[0].NetSentimentPercent
	maxSentiment := dataPoints[0].NetSentimentPercent
	totalSentiment := 0.0

	positiveCount := 0
	negativeCount := 0
	neutralCount := 0

	for _, point := range dataPoints {
		if point.NetSentimentPercent < minSentiment {
			minSentiment = point.NetSentimentPercent
		}
		if point.NetSentimentPercent > maxSentiment {
			maxSentiment = point.NetSentimentPercent
		}
		totalSentiment += point.NetSentimentPercent

		switch point.SentimentCategory {
		case "positive":
			positiveCount++
		case "negative":
			negativeCount++
		case "neutral":
			neutralCount++
		}
	}

	avgSentiment := totalSentiment / float64(len(dataPoints))

	fmt.Printf("  📊 Analysis:\n")
	fmt.Printf("    Range: %.1f%% to %.1f%%\n", minSentiment, maxSentiment)
	fmt.Printf("    Average: %.1f%%\n", avgSentiment)
	fmt.Printf("    Distribution: +%d (%.1f%%), -%d (%.1f%%), ~%d (%.1f%%)\n",
		positiveCount, float64(positiveCount)/float64(len(dataPoints))*100,
		negativeCount, float64(negativeCount)/float64(len(dataPoints))*100,
		neutralCount, float64(neutralCount)/float64(len(dataPoints))*100)
}
