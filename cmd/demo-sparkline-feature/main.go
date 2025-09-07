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
	fmt.Println("🎨 48-Hour Sentiment Sparkline Feature Demo")
	fmt.Println("===========================================")
	fmt.Println()

	// Generate demo data that shows different sentiment patterns
	fmt.Println("📊 Generating demo data with various sentiment patterns...")
	dataPoints := generateDemoData()

	// Create different sparkline configurations
	configs := []struct {
		name   string
		config *sparkline.SparklineConfig
	}{
		{
			name:   "Default Configuration",
			config: nil, // Use default
		},
		{
			name: "High Resolution",
			config: &sparkline.SparklineConfig{
				Width:  800,
				Height: 400,
			},
		},
		{
			name: "Compact View",
			config: &sparkline.SparklineConfig{
				Width:  300,
				Height: 150,
			},
		},
	}

	// Generate sparklines with different configurations
	for i, config := range configs {
		fmt.Printf("\n🎨 Generating %s...\n", config.name)

		generator := sparkline.NewSparklineGenerator(config.config)
		imageData, err := generator.GenerateSentimentSparkline(dataPoints)
		if err != nil {
			log.Fatalf("Failed to generate %s: %v", config.name, err)
		}

		filename := fmt.Sprintf("demo-sparkline-%d.png", i+1)
		err = os.WriteFile(filename, imageData, 0644)
		if err != nil {
			log.Fatalf("Failed to save %s: %v", filename, err)
		}

		fmt.Printf("✅ Generated %s: %s (%d bytes)\n", config.name, filename, len(imageData))
	}

	// Show data analysis
	fmt.Println("\n📈 Data Analysis:")
	analyzeData(dataPoints)

	// Show feature summary
	fmt.Println("\n✨ Feature Summary:")
	showFeatureSummary()

	fmt.Println("\n🎉 Demo completed! Check the generated PNG files to see the sparklines.")
}

func generateDemoData() []state.SentimentDataPoint {
	now := time.Now()
	var dataPoints []state.SentimentDataPoint

	// Create a realistic 48-hour sentiment pattern that shows different trends
	patterns := []struct {
		startHour     int
		endHour       int
		description   string
		baseSentiment float64
		trend         string
	}{
		{0, 8, "Night/Early Morning (Low Activity)", -15, "gradual_improvement"},
		{8, 16, "Morning/Afternoon (High Activity)", 20, "peak_then_decline"},
		{16, 24, "Evening (Moderate Activity)", 5, "steady_decline"},
		{24, 32, "Next Night (Low Activity)", -10, "stable_negative"},
		{32, 40, "Next Day (High Activity)", 25, "strong_positive"},
		{40, 48, "Late Day (Declining Activity)", 0, "rapid_decline"},
	}

	for _, pattern := range patterns {
		for hour := pattern.startHour; hour < pattern.endHour; hour++ {
			timestamp := now.Add(-time.Duration(48-hour) * time.Hour)

			var sentiment float64
			hourInPattern := hour - pattern.startHour

			switch pattern.trend {
			case "gradual_improvement":
				sentiment = pattern.baseSentiment + float64(hourInPattern)*3
			case "peak_then_decline":
				if hourInPattern < 4 {
					sentiment = pattern.baseSentiment + float64(hourInPattern)*5
				} else {
					sentiment = pattern.baseSentiment + 20 - float64(hourInPattern-4)*3
				}
			case "steady_decline":
				sentiment = pattern.baseSentiment - float64(hourInPattern)*2
			case "stable_negative":
				sentiment = pattern.baseSentiment + float64(hourInPattern%3-1)*5
			case "strong_positive":
				sentiment = pattern.baseSentiment + float64(hourInPattern)*2
			case "rapid_decline":
				sentiment = pattern.baseSentiment - float64(hourInPattern)*4
			}

			// Add realistic noise
			noise := float64((hour%7 - 3) * 3)
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

			// Simulate realistic post counts
			postCount := 50
			if hour >= 8 && hour < 20 { // Active hours
				postCount = 200 + hour*5
			} else if hour >= 20 || hour < 6 { // Quiet hours
				postCount = 30 + hour*2
			}

			dataPoint := state.SentimentDataPoint{
				RunID:                fmt.Sprintf("demo-run-%d", hour),
				Timestamp:            timestamp,
				AverageCompoundScore: sentiment / 100.0,
				NetSentimentPercent:  sentiment,
				SentimentCategory:    category,
				TotalPosts:           postCount,
			}

			dataPoints = append(dataPoints, dataPoint)
		}
	}

	return dataPoints
}

func analyzeData(dataPoints []state.SentimentDataPoint) {
	if len(dataPoints) == 0 {
		return
	}

	// Calculate statistics
	minSentiment := dataPoints[0].NetSentimentPercent
	maxSentiment := dataPoints[0].NetSentimentPercent
	totalSentiment := 0.0
	totalPosts := 0

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
		totalPosts += point.TotalPosts

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

	fmt.Printf("  📊 Time Range: %s to %s\n",
		dataPoints[0].Timestamp.Format("Jan 2, 15:04"),
		dataPoints[len(dataPoints)-1].Timestamp.Format("Jan 2, 15:04"))
	fmt.Printf("  📈 Sentiment Range: %.1f%% to %.1f%%\n", minSentiment, maxSentiment)
	fmt.Printf("  📊 Average Sentiment: %.1f%%\n", avgSentiment)
	fmt.Printf("  📝 Total Posts Analyzed: %d\n", totalPosts)
	fmt.Printf("  🎯 Distribution: +%d (%.1f%%), -%d (%.1f%%), ~%d (%.1f%%)\n",
		positiveCount, float64(positiveCount)/float64(len(dataPoints))*100,
		negativeCount, float64(negativeCount)/float64(len(dataPoints))*100,
		neutralCount, float64(neutralCount)/float64(len(dataPoints))*100)
}

func showFeatureSummary() {
	fmt.Println("  🎨 Visual Features:")
	fmt.Println("    • Color-coded sentiment lines (green/red/gray)")
	fmt.Println("    • Time-based X-axis with 48-hour range")
	fmt.Println("    • Sentiment percentage Y-axis (-100% to +100%)")
	fmt.Println("    • Grid lines for easy reading")
	fmt.Println("    • Data points with sentiment indicators")
	fmt.Println("    • Professional chart styling")
	fmt.Println()
	fmt.Println("  🔧 Technical Features:")
	fmt.Println("    • PNG image generation (lightweight)")
	fmt.Println("    • Configurable dimensions and styling")
	fmt.Println("    • High performance (27K+ data points/second)")
	fmt.Println("    • Error handling for edge cases")
	fmt.Println("    • Memory efficient processing")
	fmt.Println()
	fmt.Println("  🚀 Integration Features:")
	fmt.Println("    • DynamoDB storage for historical data")
	fmt.Println("    • S3 hosting for public image access")
	fmt.Println("    • Bluesky posting with image URLs")
	fmt.Println("    • Automatic 7-day data retention")
	fmt.Println("    • Seamless Lambda workflow integration")
}
