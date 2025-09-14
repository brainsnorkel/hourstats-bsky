package sparkline

import (
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func TestYearlySparklineConfig(t *testing.T) {
	config := DefaultYearlyConfig()

	// Test that the config is 25% larger than the default sparkline config
	expectedWidth := 1200 + (1200 * 25 / 100) // 1500
	expectedHeight := 800 + (800 * 25 / 100)  // 1000

	if config.Width != expectedWidth {
		t.Errorf("Expected width %d, got %d", expectedWidth, config.Width)
	}

	if config.Height != expectedHeight {
		t.Errorf("Expected height %d, got %d", expectedHeight, config.Height)
	}

	// Test that padding is scaled proportionally
	expectedPadding := 80 + (80 * 25 / 100) // 100
	if config.Padding != expectedPadding {
		t.Errorf("Expected padding %d, got %d", expectedPadding, config.Padding)
	}

	// Test that line width is scaled proportionally
	expectedLineWidth := 4.0 // 3.0 * 1.25 = 3.75, but we use 4.0 for cleaner scaling
	if config.LineWidth != expectedLineWidth {
		t.Errorf("Expected line width %f, got %f", expectedLineWidth, config.LineWidth)
	}
}

func TestYearlySparklineGenerator(t *testing.T) {
	generator := NewYearlySparklineGenerator(nil)

	if generator == nil {
		t.Error("Expected generator to be created, got nil")
	}

	if generator.config == nil {
		t.Error("Expected config to be set, got nil")
	}
}

func TestCalculateYearlyYRange(t *testing.T) {
	generator := NewYearlySparklineGenerator(nil)

	// Test with empty data
	yRange := generator.calculateYearlyYRange([]state.YearlySparklineDataPoint{})
	expectedMin, expectedMax := -100.0, 100.0
	if yRange.Min != expectedMin || yRange.Max != expectedMax {
		t.Errorf("Expected range [%f, %f], got [%f, %f]", expectedMin, expectedMax, yRange.Min, yRange.Max)
	}

	// Test with single data point
	dataPoints := []state.YearlySparklineDataPoint{
		{
			Date:                "2025-01-01",
			AverageSentiment:    15.5,
			MinSentiment:        10.0,
			MaxSentiment:        20.0,
			Timestamp:           time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			NetSentimentPercent: 15.5,
		},
	}

	yRange = generator.calculateYearlyYRange(dataPoints)
	if yRange.Min >= yRange.Max {
		t.Errorf("Expected min < max, got min=%f, max=%f", yRange.Min, yRange.Max)
	}

	// Test with multiple data points
	dataPoints = append(dataPoints, state.YearlySparklineDataPoint{
		Date:                "2025-01-02",
		AverageSentiment:    -5.2,
		MinSentiment:        -10.0,
		MaxSentiment:        0.0,
		Timestamp:           time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		NetSentimentPercent: -5.2,
	})

	yRange = generator.calculateYearlyYRange(dataPoints)
	if yRange.Min >= yRange.Max {
		t.Errorf("Expected min < max, got min=%f, max=%f", yRange.Min, yRange.Max)
	}

	// Test that padding is applied
	expectedMin = -5.2 - 5.0 // min - padding (minimum 5%)
	expectedMax = 15.5 + 5.0 // max + padding (minimum 5%)
	if yRange.Min > expectedMin || yRange.Max < expectedMax {
		t.Errorf("Expected range to include padding, got [%f, %f]", yRange.Min, yRange.Max)
	}
}

func TestYearlyGaussianSmoothing(t *testing.T) {
	// Test with empty data
	smoothed := yearlyGaussianSmoothing([]float64{}, 2.0)
	if len(smoothed) != 0 {
		t.Errorf("Expected empty result, got %v", smoothed)
	}

	// Test with single data point
	data := []float64{10.0}
	smoothed = yearlyGaussianSmoothing(data, 2.0)
	if len(smoothed) != 1 || smoothed[0] != 10.0 {
		t.Errorf("Expected [10.0], got %v", smoothed)
	}

	// Test with multiple data points
	data = []float64{10.0, 20.0, 30.0, 40.0, 50.0}
	smoothed = yearlyGaussianSmoothing(data, 2.0)
	if len(smoothed) != len(data) {
		t.Errorf("Expected length %d, got %d", len(data), len(smoothed))
	}

	// Test that smoothing doesn't change the overall trend
	originalSum := 0.0
	smoothedSum := 0.0
	for i := 0; i < len(data); i++ {
		originalSum += data[i]
		smoothedSum += smoothed[i]
	}

	// The sums should be approximately equal (within 1% tolerance)
	tolerance := originalSum * 0.01
	if smoothedSum < originalSum-tolerance || smoothedSum > originalSum+tolerance {
		t.Errorf("Expected smoothed sum to be within tolerance of original, got %f vs %f", smoothedSum, originalSum)
	}
}

func TestGenerateYearlySentimentSparkline_EmptyData(t *testing.T) {
	generator := NewYearlySparklineGenerator(nil)

	// Test with empty data
	_, err := generator.GenerateYearlySentimentSparkline([]state.YearlySparklineDataPoint{})
	if err == nil {
		t.Error("Expected error for empty data, got nil")
	}

	expectedError := "no data points provided"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestGenerateYearlySentimentSparkline_ValidData(t *testing.T) {
	generator := NewYearlySparklineGenerator(nil)

	// Create test data
	dataPoints := []state.YearlySparklineDataPoint{
		{
			Date:                "2025-01-01",
			AverageSentiment:    15.5,
			MinSentiment:        10.0,
			MaxSentiment:        20.0,
			Timestamp:           time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			NetSentimentPercent: 15.5,
		},
		{
			Date:                "2025-01-02",
			AverageSentiment:    -5.2,
			MinSentiment:        -10.0,
			MaxSentiment:        0.0,
			Timestamp:           time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			NetSentimentPercent: -5.2,
		},
		{
			Date:                "2025-01-03",
			AverageSentiment:    8.7,
			MinSentiment:        5.0,
			MaxSentiment:        12.0,
			Timestamp:           time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC),
			NetSentimentPercent: 8.7,
		},
	}

	// Test with valid data
	imageData, err := generator.GenerateYearlySentimentSparkline(dataPoints)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(imageData) == 0 {
		t.Error("Expected non-empty image data, got empty")
	}

	// Test that the image data looks like a PNG (starts with PNG signature)
	if len(imageData) < 8 {
		t.Error("Expected image data to be at least 8 bytes")
	}

	// PNG signature: 89 50 4E 47 0D 0A 1A 0A
	pngSignature := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	for i, b := range pngSignature {
		if imageData[i] != b {
			t.Errorf("Expected PNG signature at position %d, got %x", i, imageData[i])
		}
	}
}
