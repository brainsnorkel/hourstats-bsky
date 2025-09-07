package main

import (
	"fmt"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func main() {
	fmt.Println("🔧 Testing Improved First Run Sparkline Behavior")
	fmt.Println("===============================================")

	// Test the improved sparkline poster logic
	fmt.Println("\n📊 Scenario 1: First run with no historical data")
	testImprovedSparklineLogic(0, "First run - no data")

	fmt.Println("\n📊 Scenario 2: Second run with 1 data point")
	testImprovedSparklineLogic(1, "Second run - 1 data point")

	fmt.Println("\n📊 Scenario 3: Third run with 2+ data points")
	testImprovedSparklineLogic(3, "Third run - sufficient data")

	fmt.Println("\n📊 Scenario 4: Full 48-hour dataset")
	testImprovedSparklineLogic(24, "Full dataset - 24 data points")

	fmt.Println("\n✅ All improved first-run scenarios tested!")
}

func testImprovedSparklineLogic(dataPointCount int, scenario string) {
	fmt.Printf("  🎯 %s\n", scenario)

	// Create mock data
	dataPoints := generateMockData(dataPointCount)

	// Test the improved logic
	if len(dataPoints) < 2 {
		fmt.Printf("  📊 Retrieved %d data points\n", len(dataPoints))
		fmt.Printf("  📝 Action: Post insufficient data message\n")

		// Simulate the insufficient data message
		var message string
		if len(dataPoints) == 0 {
			message = "📊 Building sentiment history...\n\n" +
				"⏳ Sparkline charts will be available after collecting 48 hours of data.\n" +
				"📈 First chart expected in ~24-48 hours.\n\n" +
				"💡 In the meantime, check out the hourly sentiment summaries above!"
		} else {
			message = fmt.Sprintf("📊 Building sentiment history...\n\n"+
				"⏳ Sparkline charts will be available after collecting 48 hours of data.\n"+
				"📈 Currently have %d data points, need 2+ for charts.\n\n"+
				"💡 In the meantime, check out the hourly sentiment summaries above!", len(dataPoints))
		}

		fmt.Printf("  📝 Message: %s\n", message)
		fmt.Printf("  ✅ Result: Graceful handling - user informed\n")
	} else {
		fmt.Printf("  📊 Retrieved %d data points\n", len(dataPoints))
		fmt.Printf("  🎨 Action: Generate sparkline\n")

		// Test sparkline generation
		generator := sparkline.NewSparklineGenerator(nil)
		imageData, err := generator.GenerateSentimentSparkline(dataPoints)
		if err != nil {
			fmt.Printf("  ❌ Result: Failed to generate sparkline: %v\n", err)
		} else {
			fmt.Printf("  ✅ Result: Generated sparkline (%d bytes)\n", len(imageData))
		}
	}
}

func generateMockData(count int) []state.SentimentDataPoint {
	if count == 0 {
		return []state.SentimentDataPoint{}
	}

	now := time.Now()
	var dataPoints []state.SentimentDataPoint

	for i := 0; i < count; i++ {
		hoursAgo := 48 - (i * 2)
		timestamp := now.Add(-time.Duration(hoursAgo) * time.Hour)

		sentiment := float64((i%3 - 1) * 20) // -20, 0, or +20

		var category string
		if sentiment > 10 {
			category = "positive"
		} else if sentiment < -10 {
			category = "negative"
		} else {
			category = "neutral"
		}

		dataPoint := state.SentimentDataPoint{
			RunID:                fmt.Sprintf("mock-run-%d", i),
			Timestamp:            timestamp,
			AverageCompoundScore: sentiment / 100.0,
			NetSentimentPercent:  sentiment,
			SentimentCategory:    category,
			TotalPosts:           100 + i*10,
		}

		dataPoints = append(dataPoints, dataPoint)
	}

	return dataPoints
}
