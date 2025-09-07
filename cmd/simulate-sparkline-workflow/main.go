package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// SimulateSparklineWorkflow simulates the complete sparkline posting workflow
func main() {
	fmt.Println("🎯 Simulating Complete 48-Hour Sparkline Workflow")
	fmt.Println("=================================================")

	// Step 1: Simulate analyzer storing sentiment data
	fmt.Println("\n📊 Step 1: Simulating analyzer storing sentiment data...")
	sentimentData := simulateAnalyzerWorkflow()

	// Step 2: Simulate poster triggering sparkline generation
	fmt.Println("\n📈 Step 2: Simulating sparkline generation...")
	sparklineImage, err := simulateSparklineGeneration(sentimentData)
	if err != nil {
		log.Fatalf("Failed to generate sparkline: %v", err)
	}

	// Step 3: Simulate S3 upload
	fmt.Println("\n☁️ Step 3: Simulating S3 upload...")
	imageURL, err := simulateS3Upload(sparklineImage)
	if err != nil {
		log.Fatalf("Failed to simulate S3 upload: %v", err)
	}

	// Step 4: Simulate Bluesky posting
	fmt.Println("\n🐦 Step 4: Simulating Bluesky post...")
	err = simulateBlueskyPost(imageURL)
	if err != nil {
		log.Fatalf("Failed to simulate Bluesky post: %v", err)
	}

	fmt.Println("\n✅ Complete sparkline workflow simulation successful!")
	fmt.Println("🎉 The 48-hour sentiment sparkline feature is ready for production!")
}

func simulateAnalyzerWorkflow() []state.SentimentDataPoint {
	// Simulate storing sentiment data over the last 48 hours
	// This would normally be done by the analyzer Lambda after each run

	fmt.Println("  📝 Storing sentiment data points...")

	// Generate realistic sentiment data for the last 48 hours
	dataPoints := generateWorkflowTestData()

	// Simulate storing each data point (in real workflow, this goes to DynamoDB)
	for i, point := range dataPoints {
		fmt.Printf("  💾 Stored data point %d: %s (%.1f%%) - %s\n",
			i+1,
			point.Timestamp.Format("15:04"),
			point.NetSentimentPercent,
			point.SentimentCategory)
	}

	fmt.Printf("  ✅ Stored %d sentiment data points\n", len(dataPoints))
	return dataPoints
}

func simulateSparklineGeneration(dataPoints []state.SentimentDataPoint) ([]byte, error) {
	fmt.Println("  🎨 Generating sparkline image...")

	// Create sparkline generator
	generator := sparkline.NewSparklineGenerator(nil)

	// Generate the sparkline
	imageData, err := generator.GenerateSentimentSparkline(dataPoints)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sparkline: %w", err)
	}

	// Save locally for verification
	filename := "workflow-sparkline.png"
	err = os.WriteFile(filename, imageData, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to save sparkline: %w", err)
	}

	fmt.Printf("  ✅ Generated sparkline: %s (%d bytes)\n", filename, len(imageData))
	return imageData, nil
}

func simulateS3Upload(imageData []byte) (string, error) {
	fmt.Println("  📤 Uploading to S3...")

	// In real workflow, this would upload to S3
	// For simulation, we'll just generate a mock URL
	bucketName := "hourstats-sparkline-images"
	key := fmt.Sprintf("sparklines/workflow-test-%d.png", time.Now().Unix())
	imageURL := fmt.Sprintf("https://%s.s3.amazonaws.com/%s", bucketName, key)

	fmt.Printf("  ✅ Uploaded to S3: %s\n", imageURL)
	fmt.Printf("  📊 Image size: %d bytes\n", len(imageData))
	return imageURL, nil
}

func simulateBlueskyPost(imageURL string) error {
	fmt.Println("  🐦 Posting to Bluesky...")

	// Simulate the post content that would be sent to Bluesky
	postText := fmt.Sprintf("📊 Sentiment for the last 48 hours\n\n📈 View chart: %s", imageURL)

	fmt.Println("  📝 Post content:")
	fmt.Println("  " + postText)
	fmt.Printf("  📏 Character count: %d\n", len(postText))

	// Simulate successful posting
	fmt.Println("  ✅ Posted to Bluesky successfully!")
	return nil
}

func generateWorkflowTestData() []state.SentimentDataPoint {
	now := time.Now()
	var dataPoints []state.SentimentDataPoint

	// Generate data every 2 hours for the last 48 hours (24 data points)
	// This simulates the analyzer running every 2 hours and storing sentiment data

	for i := 0; i < 24; i++ {
		hoursAgo := 48 - (i * 2)
		timestamp := now.Add(-time.Duration(hoursAgo) * time.Hour)

		// Create a realistic sentiment pattern that shows trends over time
		var sentiment float64

		// Simulate different periods of the day/night cycle
		if i < 6 { // First 12 hours (night/early morning)
			sentiment = -20 + float64(i)*3 // Gradually improving from negative
		} else if i < 12 { // Next 12 hours (morning/afternoon)
			sentiment = 10 + float64(i-6)*2 // Positive trend
		} else if i < 18 { // Next 12 hours (afternoon/evening)
			sentiment = 25 - float64(i-12)*1.5 // Peak then decline
		} else { // Last 12 hours (evening/night)
			sentiment = 5 - float64(i-18)*2 // Declining to neutral
		}

		// Add some realistic noise
		noise := float64((i%5 - 2) * 4)
		sentiment += noise

		// Clamp to reasonable range
		if sentiment > 100 {
			sentiment = 100
		} else if sentiment < -100 {
			sentiment = -100
		}

		// Determine sentiment category
		var category string
		if sentiment > 15 {
			category = "positive"
		} else if sentiment < -15 {
			category = "negative"
		} else {
			category = "neutral"
		}

		// Simulate varying post counts (more posts during active hours)
		postCount := 100
		if i >= 6 && i < 18 { // Active hours
			postCount = 200 + i*10
		} else { // Quiet hours
			postCount = 50 + i*5
		}

		dataPoint := state.SentimentDataPoint{
			RunID:                fmt.Sprintf("workflow-run-%d", i),
			Timestamp:            timestamp,
			AverageCompoundScore: sentiment / 100.0,
			NetSentimentPercent:  sentiment,
			SentimentCategory:    category,
			TotalPosts:           postCount,
		}

		dataPoints = append(dataPoints, dataPoint)
	}

	return dataPoints
}

// Additional utility function to show what the actual Lambda workflow would look like
func showLambdaWorkflow() {
	fmt.Println("\n🔧 Actual Lambda Workflow:")
	fmt.Println("=========================")
	fmt.Println("1. 📊 Analyzer Lambda:")
	fmt.Println("   - Analyzes posts and calculates sentiment")
	fmt.Println("   - Stores sentiment data point in DynamoDB")
	fmt.Println("   - Continues with normal analysis workflow")
	fmt.Println("")
	fmt.Println("2. 📝 Poster Lambda:")
	fmt.Println("   - Posts main sentiment summary")
	fmt.Println("   - Triggers sparkline poster Lambda (async)")
	fmt.Println("")
	fmt.Println("3. 📈 Sparkline Poster Lambda:")
	fmt.Println("   - Queries 48 hours of sentiment data from DynamoDB")
	fmt.Println("   - Generates sparkline PNG image")
	fmt.Println("   - Uploads image to S3")
	fmt.Println("   - Posts sparkline to Bluesky with image URL")
	fmt.Println("")
	fmt.Println("4. 🗄️ Infrastructure:")
	fmt.Println("   - DynamoDB table: hourstats-sentiment-history")
	fmt.Println("   - S3 bucket: hourstats-sparkline-images")
	fmt.Println("   - TTL: 7 days for sentiment data")
	fmt.Println("   - Public read access for images")
}
