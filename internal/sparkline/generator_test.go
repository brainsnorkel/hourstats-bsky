package sparkline

import (
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func TestSparklineGenerator(t *testing.T) {
	generator := NewSparklineGenerator(nil)

	// Create test data points
	dataPoints := []state.SentimentDataPoint{
		{
			RunID:               "test-run-1",
			Timestamp:           time.Now().Add(-48 * time.Hour),
			NetSentimentPercent: -20.0,
			SentimentCategory:   "negative",
			TotalPosts:          100,
		},
		{
			RunID:               "test-run-2",
			Timestamp:           time.Now().Add(-24 * time.Hour),
			NetSentimentPercent: 10.0,
			SentimentCategory:   "positive",
			TotalPosts:          150,
		},
		{
			RunID:               "test-run-3",
			Timestamp:           time.Now().Add(-12 * time.Hour),
			NetSentimentPercent: 5.0,
			SentimentCategory:   "positive",
			TotalPosts:          120,
		},
		{
			RunID:               "test-run-4",
			Timestamp:           time.Now().Add(-6 * time.Hour),
			NetSentimentPercent: -5.0,
			SentimentCategory:   "negative",
			TotalPosts:          80,
		},
		{
			RunID:               "test-run-5",
			Timestamp:           time.Now(),
			NetSentimentPercent: 15.0,
			SentimentCategory:   "positive",
			TotalPosts:          200,
		},
	}

	// Generate sparkline
	imageData, err := generator.GenerateSentimentSparkline(dataPoints)
	if err != nil {
		t.Fatalf("Failed to generate sparkline: %v", err)
	}

	// Check that we got some image data
	if len(imageData) == 0 {
		t.Fatal("Generated image data is empty")
	}

	// Check that it's a reasonable size (PNG should be at least a few KB)
	if len(imageData) < 1000 {
		t.Fatalf("Generated image data is too small: %d bytes", len(imageData))
	}

	t.Logf("Generated sparkline image: %d bytes", len(imageData))
}

func TestSparklineGeneratorEmptyData(t *testing.T) {
	generator := NewSparklineGenerator(nil)

	// Test with empty data
	_, err := generator.GenerateSentimentSparkline([]state.SentimentDataPoint{})
	if err == nil {
		t.Fatal("Expected error for empty data points")
	}

	if err.Error() != "no data points provided" {
		t.Fatalf("Expected 'no data points provided' error, got: %v", err)
	}
}

func TestSparklineGeneratorSingleDataPoint(t *testing.T) {
	generator := NewSparklineGenerator(nil)

	// Test with single data point
	dataPoints := []state.SentimentDataPoint{
		{
			RunID:               "test-run-1",
			Timestamp:           time.Now(),
			NetSentimentPercent: 50.0,
			SentimentCategory:   "positive",
			TotalPosts:          100,
		},
	}

	// Should not error but might not draw a line
	imageData, err := generator.GenerateSentimentSparkline(dataPoints)
	if err != nil {
		t.Fatalf("Failed to generate sparkline with single data point: %v", err)
	}

	if len(imageData) == 0 {
		t.Fatal("Generated image data is empty")
	}

	t.Logf("Generated sparkline with single data point: %d bytes", len(imageData))
}

func TestSparklineConfig(t *testing.T) {
	// Test default config
	config := DefaultConfig()
	if config.Width == 0 || config.Height == 0 {
		t.Fatal("Default config should have non-zero dimensions")
	}

	// Test custom config
	customConfig := &SparklineConfig{
		Width:  800,
		Height: 400,
	}

	generator := NewSparklineGenerator(customConfig)
	if generator.config.Width != 800 || generator.config.Height != 400 {
		t.Fatal("Custom config not applied correctly")
	}
}
