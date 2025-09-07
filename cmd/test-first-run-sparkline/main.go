package main

import (
	"context"
	"fmt"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func main() {
	fmt.Println("🔍 Testing First Run Sparkline Behavior")
	fmt.Println("======================================")

	// Simulate the first run scenario
	fmt.Println("\n📊 Scenario: First run with no historical data")
	
	// Create a mock sentiment history manager that returns empty data
	mockHistoryManager := &MockSentimentHistoryManager{}
	
	// Create sparkline generator
	generator := sparkline.NewSparklineGenerator(nil)
	
	// Test the sparkline poster logic
	result := testSparklinePosterLogic(mockHistoryManager, generator)
	
	fmt.Printf("\n🎯 Result: %s\n", result)
	
	// Test with some historical data
	fmt.Println("\n📊 Scenario: Second run with some historical data")
	
	mockHistoryManagerWithData := &MockSentimentHistoryManager{
		hasData: true,
	}
	
	result2 := testSparklinePosterLogic(mockHistoryManagerWithData, generator)
	
	fmt.Printf("\n🎯 Result: %s\n", result2)
	
	// Show the recommended solution
	fmt.Println("\n💡 Recommended Solution:")
	showRecommendedSolution()
}

func testSparklinePosterLogic(historyManager *MockSentimentHistoryManager, generator *sparkline.SparklineGenerator) string {
	ctx := context.Background()
	
	// Simulate the sparkline poster logic
	fmt.Println("  📈 Getting 48 hours of sentiment data...")
	dataPoints, err := historyManager.GetSentimentHistory(ctx, 48*time.Hour)
	if err != nil {
		return fmt.Sprintf("❌ Failed to get sentiment history: %v", err)
	}
	
	fmt.Printf("  📊 Retrieved %d data points\n", len(dataPoints))
	
	if len(dataPoints) < 2 {
		return fmt.Sprintf("⚠️  Insufficient sentiment data for sparkline (got %d points, need at least 2)", len(dataPoints))
	}
	
	// Generate sparkline image
	fmt.Println("  🎨 Generating sparkline image...")
	imageData, err := generator.GenerateSentimentSparkline(dataPoints)
	if err != nil {
		return fmt.Sprintf("❌ Failed to generate sparkline: %v", err)
	}
	
	return fmt.Sprintf("✅ Generated sparkline successfully (%d bytes)", len(imageData))
}

// Mock sentiment history manager for testing
type MockSentimentHistoryManager struct {
	hasData bool
}

func (m *MockSentimentHistoryManager) GetSentimentHistory(ctx context.Context, duration time.Duration) ([]state.SentimentDataPoint, error) {
	if !m.hasData {
		// Simulate empty data on first run
		return []state.SentimentDataPoint{}, nil
	}
	
	// Simulate some historical data
	now := time.Now()
	var dataPoints []state.SentimentDataPoint
	
	for i := 0; i < 5; i++ {
		hoursAgo := 48 - (i * 12)
		timestamp := now.Add(-time.Duration(hoursAgo) * time.Hour)
		
		dataPoint := state.SentimentDataPoint{
			RunID:               fmt.Sprintf("mock-run-%d", i),
			Timestamp:           timestamp,
			AverageCompoundScore: float64(i-2) * 0.2,
			NetSentimentPercent:  float64(i-2) * 20,
			SentimentCategory:    "neutral",
			TotalPosts:           100 + i*10,
		}
		
		dataPoints = append(dataPoints, dataPoint)
	}
	
	return dataPoints, nil
}

func showRecommendedSolution() {
	fmt.Println("  🔧 Current Issues:")
	fmt.Println("    1. Sparkline poster triggered immediately on first run")
	fmt.Println("    2. No historical data available on first run")
	fmt.Println("    3. Requires at least 2 data points to generate sparkline")
	fmt.Println("    4. Async trigger doesn't actually invoke Lambda function")
	fmt.Println("")
	fmt.Println("  ✅ Recommended Solutions:")
	fmt.Println("    1. Wait for sufficient historical data (24-48 hours)")
	fmt.Println("    2. Add minimum data point requirement check")
	fmt.Println("    3. Implement proper Lambda invocation")
	fmt.Println("    4. Add graceful handling for insufficient data")
	fmt.Println("    5. Consider showing 'insufficient data' message instead of failing")
	fmt.Println("")
	fmt.Println("  🎯 Implementation Options:")
	fmt.Println("    Option A: Skip sparkline posting until sufficient data")
	fmt.Println("    Option B: Show 'insufficient data' message with timeline")
	fmt.Println("    Option C: Generate sparkline with available data (even if < 2 points)")
	fmt.Println("    Option D: Wait 24-48 hours before enabling sparkline feature")
}
