package sparkline

import (
	"bytes"
	"fmt"
	"image/color"

	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/fogleman/gg"
)

// SparklineConfig holds configuration for sparkline generation
type SparklineConfig struct {
	Width        int
	Height       int
	Padding      int
	LineWidth    float64
	PointRadius  float64
	Background   color.RGBA
	PositiveLine color.RGBA
	NegativeLine color.RGBA
	NeutralLine  color.RGBA
	GridColor    color.RGBA
	TextColor    color.RGBA
}

// YRange represents the Y-axis range for the sparkline
type YRange struct {
	Min    float64
	Max    float64
	Center float64
	Scale  float64
}

// calculateYRange calculates the Y-axis range based on actual data
func (sg *SparklineGenerator) calculateYRange(dataPoints []state.SentimentDataPoint) YRange {
	if len(dataPoints) == 0 {
		return YRange{Min: -100, Max: 100, Center: 0, Scale: 1.0}
	}

	// Find min and max values
	min := dataPoints[0].NetSentimentPercent
	max := dataPoints[0].NetSentimentPercent

	for _, dp := range dataPoints {
		if dp.NetSentimentPercent < min {
			min = dp.NetSentimentPercent
		}
		if dp.NetSentimentPercent > max {
			max = dp.NetSentimentPercent
		}
	}

	// Add padding (10% of the range, minimum 5% on each side)
	dataRange := max - min
	padding := dataRange * 0.1
	if padding < 5.0 {
		padding = 5.0
	}

	// Calculate final range
	finalMin := min - padding
	finalMax := max + padding
	center := (finalMin + finalMax) / 2.0
	scale := 200.0 / (finalMax - finalMin) // Scale to fit in -100 to +100 range

	return YRange{
		Min:    finalMin,
		Max:    finalMax,
		Center: center,
		Scale:  scale,
	}
}

// DefaultConfig returns a default sparkline configuration
func DefaultConfig() *SparklineConfig {
	return &SparklineConfig{
		Width:        1200,                           // Square canvas 1200x1200
		Height:       1200,                           // Square canvas 1200x1200
		Padding:      100,                            // Adjusted padding for square canvas
		LineWidth:    6.0,                            // 75% of 8.0 (8.0 * 0.75)
		PointRadius:  0.8,                            // Reduced to 0.8 for very small dots
		Background:   color.RGBA{248, 249, 250, 255}, // Light gray
		PositiveLine: color.RGBA{40, 167, 69, 255},   // Green
		NegativeLine: color.RGBA{220, 53, 69, 255},   // Red
		NeutralLine:  color.RGBA{108, 117, 125, 255}, // Gray
		GridColor:    color.RGBA{200, 200, 200, 255}, // Light gray
		TextColor:    color.RGBA{33, 37, 41, 255},    // Dark gray
	}
}

// SparklineGenerator generates sentiment sparkline images
type SparklineGenerator struct {
	config *SparklineConfig
}

// NewSparklineGenerator creates a new sparkline generator
func NewSparklineGenerator(config *SparklineConfig) *SparklineGenerator {
	if config == nil {
		config = DefaultConfig()
	}
	return &SparklineGenerator{config: config}
}

// GenerateSentimentSparkline creates a PNG image of sentiment data over time
func (sg *SparklineGenerator) GenerateSentimentSparkline(dataPoints []state.SentimentDataPoint) ([]byte, error) {
	if len(dataPoints) == 0 {
		return nil, fmt.Errorf("no data points provided")
	}

	// Create image context
	dc := gg.NewContext(sg.config.Width, sg.config.Height)

	// Fill background
	dc.SetColor(sg.config.Background)
	dc.Clear()

	// Calculate drawing area with extra space for Y-axis labels
	leftPadding := sg.config.Padding + 50 // Extra 50px for Y-axis labels
	rightPadding := sg.config.Padding
	topPadding := sg.config.Padding
	bottomPadding := sg.config.Padding + 20 // Extra 20px for time labels

	drawWidth := float64(sg.config.Width - leftPadding - rightPadding)
	drawHeight := float64(sg.config.Height - topPadding - bottomPadding)
	drawX := float64(leftPadding)
	drawY := float64(topPadding)

	// Calculate Y-axis range based on actual data
	yRange := sg.calculateYRange(dataPoints)

	// Draw grid lines
	sg.drawGrid(dc, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw sentiment line
	sg.drawSentimentLine(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw labels
	sg.drawLabels(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

	// Encode as PNG
	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// drawGrid draws grid lines and axes
func (sg *SparklineGenerator) drawGrid(dc *gg.Context, x, y, width, height float64, yRange YRange) {
	dc.SetColor(sg.config.GridColor)
	dc.SetLineWidth(0.5)

	// Horizontal grid lines (sentiment levels) - use compressed range
	levels := []float64{yRange.Min, yRange.Center, yRange.Max}
	for _, level := range levels {
		// Convert to Y position using compressed range
		normalizedLevel := (level - yRange.Center) * yRange.Scale / 100.0
		yPos := y + height/2 - normalizedLevel*(height/2)
		dc.DrawLine(x, yPos, x+width, yPos)
		dc.Stroke()
	}

	// Vertical center line (neutral)
	centerY := y + height/2
	dc.SetLineWidth(1.0)
	dc.DrawLine(x, centerY, x+width, centerY)
	dc.Stroke()
}

// drawSentimentLine draws the sentiment line with appropriate colors
func (sg *SparklineGenerator) drawSentimentLine(dc *gg.Context, dataPoints []state.SentimentDataPoint, x, y, width, height float64, yRange YRange) {
	if len(dataPoints) < 2 {
		return
	}

	// Calculate time range
	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	// Draw line segments with appropriate colors
	for i := 0; i < len(dataPoints)-1; i++ {
		current := dataPoints[i]
		next := dataPoints[i+1]

		// Calculate positions
		x1 := x + (current.Timestamp.Sub(startTime).Seconds()/timeRange)*width
		normalizedY1 := (current.NetSentimentPercent - yRange.Center) * yRange.Scale / 100.0
		y1 := y + height/2 - normalizedY1*(height/2)
		x2 := x + (next.Timestamp.Sub(startTime).Seconds()/timeRange)*width
		normalizedY2 := (next.NetSentimentPercent - yRange.Center) * yRange.Scale / 100.0
		y2 := y + height/2 - normalizedY2*(height/2)

		// Determine color based on sentiment
		var lineColor color.RGBA
		if current.NetSentimentPercent > 10 {
			lineColor = sg.config.PositiveLine
		} else if current.NetSentimentPercent < -10 {
			lineColor = sg.config.NegativeLine
		} else {
			lineColor = sg.config.NeutralLine
		}

		// Draw line segment
		dc.SetColor(lineColor)
		dc.SetLineWidth(sg.config.LineWidth)
		dc.DrawLine(x1, y1, x2, y2)
		dc.Stroke()

		// Draw point
		dc.SetColor(lineColor)
		dc.DrawCircle(x1, y1, sg.config.PointRadius)
		dc.Fill()
	}

	// Draw final point
	lastPoint := dataPoints[len(dataPoints)-1]
	xFinal := x + (lastPoint.Timestamp.Sub(startTime).Seconds()/timeRange)*width
	normalizedYFinal := (lastPoint.NetSentimentPercent - yRange.Center) * yRange.Scale / 100.0
	yFinal := y + height/2 - normalizedYFinal*(height/2)

	var pointColor color.RGBA
	if lastPoint.NetSentimentPercent > 10 {
		pointColor = sg.config.PositiveLine
	} else if lastPoint.NetSentimentPercent < -10 {
		pointColor = sg.config.NegativeLine
	} else {
		pointColor = sg.config.NeutralLine
	}

	dc.SetColor(pointColor)
	dc.DrawCircle(xFinal, yFinal, sg.config.PointRadius)
	dc.Fill()
}

// drawLabels draws time and sentiment labels
func (sg *SparklineGenerator) drawLabels(dc *gg.Context, dataPoints []state.SentimentDataPoint, x, y, width, height float64, yRange YRange) {
	dc.SetColor(sg.config.TextColor)

	// Load a system font for text rendering
	// Try Geneva first, then fall back to Symbol if needed
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		// Fallback to Symbol font
		if fallbackErr := dc.LoadFontFace("/System/Library/Fonts/Symbol.ttf", 12); fallbackErr != nil {
			// If both fail, we'll continue without custom font
			_ = fallbackErr
		}
	}

	// Draw sentiment level labels - use compressed range
	levels := []struct {
		value float64
		label string
	}{
		{yRange.Max, fmt.Sprintf("%.1f%%", yRange.Max)},
		{yRange.Center, fmt.Sprintf("%.1f%%", yRange.Center)},
		{yRange.Min, fmt.Sprintf("%.1f%%", yRange.Min)},
	}

	for _, level := range levels {
		normalizedLevel := (level.value - yRange.Center) * yRange.Scale / 100.0
		yPos := y + height/2 - normalizedLevel*(height/2)
		dc.DrawStringAnchored(level.label, x-15, yPos, 1, 0.5)
	}

	// Draw time labels - show days of the week for 7-day view
	if len(dataPoints) > 0 {
		startTime := dataPoints[0].Timestamp
		endTime := dataPoints[len(dataPoints)-1].Timestamp

		// Calculate time range in days
		timeRange := endTime.Sub(startTime).Hours() / 24

		if timeRange >= 1 {
			// For multi-day data, show day labels
			startLabel := startTime.Format("Mon")
			endLabel := endTime.Format("Mon")

			dc.DrawStringAnchored(startLabel, x, y+height+15, 0, 0)
			dc.DrawStringAnchored(endLabel, x+width, y+height+15, 1, 0)

			// Add middle day label if we have enough data
			if timeRange >= 3 {
				middleTime := startTime.Add(endTime.Sub(startTime) / 2)
				middleLabel := middleTime.Format("Mon")
				dc.DrawStringAnchored(middleLabel, x+width/2, y+height+15, 0.5, 0)
			}
		} else {
			// For same-day data, show time labels
			startLabel := startTime.Format("15:04")
			endLabel := endTime.Format("15:04")

			dc.DrawStringAnchored(startLabel, x, y+height+15, 0, 0)
			dc.DrawStringAnchored(endLabel, x+width, y+height+15, 1, 0)
		}
	}

	// Draw title
	if err := dc.LoadFontFace("", 14); err != nil {
		// If font loading fails, continue with default font
		_ = err
	}
	dc.DrawStringAnchored("Sentiment Trend", x+width/2, y-10, 0.5, 0)
}
