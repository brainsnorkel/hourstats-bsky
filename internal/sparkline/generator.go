package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"strings"
	"time"

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
		Width:        1200,                           // Canvas 1200x800 (3:2 aspect ratio)
		Height:       800,                            // Canvas 1200x800 (3:2 aspect ratio)
		Padding:      80,                             // Adjusted padding for 1200x800 canvas
		LineWidth:    3.0,                            // 50% of original 6.0
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

	// Draw neutral zone background (this will cover the center line in the neutral zone)
	sg.drawNeutralZone(dc, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw sentiment line
	sg.drawSentimentLine(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw average line
	sg.drawAverageLine(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw labels
	sg.drawLabels(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw sentiment zone watermarks
	sg.drawSentimentWatermarks(dc, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw branding watermark
	sg.drawBrandingWatermark(dc, drawX, drawY, drawWidth, drawHeight)

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
		// Skip drawing grid lines within the neutral zone (-10% to +10%) except for 0%
		if level >= -10.0 && level <= 10.0 && level != 0.0 {
			continue
		}

		// Convert to Y position using compressed range
		normalizedLevel := (level - yRange.Center) * yRange.Scale / 100.0
		yPos := y + height/2 - normalizedLevel*(height/2)
		dc.DrawLine(x, yPos, x+width, yPos)
		dc.Stroke()
	}

	// Always draw the 0% line if it's within the visible range
	if yRange.Min <= 0.0 && yRange.Max >= 0.0 {
		normalizedZero := (0.0 - yRange.Center) * yRange.Scale / 100.0
		yZero := y + height/2 - normalizedZero*(height/2)
		dc.DrawLine(x, yZero, x+width, yZero)
		dc.Stroke()
	}

	// Vertical center line (neutral) - only draw if not in neutral zone
	centerY := y + height/2
	dc.SetLineWidth(1.0)
	dc.DrawLine(x, centerY, x+width, centerY)
	dc.Stroke()
}

// drawNeutralZone draws a light gray background area for the neutral sentiment zone (-10% to +10%)
func (sg *SparklineGenerator) drawNeutralZone(dc *gg.Context, x, y, width, height float64, yRange YRange) {
	// Define neutral zone boundaries
	neutralMin := -10.0
	neutralMax := 10.0

	// Convert neutral zone boundaries to Y positions using the same scaling as the data
	normalizedMin := (neutralMin - yRange.Center) * yRange.Scale / 100.0
	normalizedMax := (neutralMax - yRange.Center) * yRange.Scale / 100.0

	yMin := y + height/2 - normalizedMax*(height/2)
	yMax := y + height/2 - normalizedMin*(height/2)

	// Only draw if the neutral zone is visible within the current Y range
	if yMin < y+height && yMax > y {
		// Clamp to visible area
		if yMin < y {
			yMin = y
		}
		if yMax > y+height {
			yMax = y + height
		}

		// Draw the neutral zone rectangle
		dc.SetColor(color.RGBA{252, 252, 252, 20}) // Extremely light gray with very low transparency
		dc.DrawRectangle(x, yMin, width, yMax-yMin)
		dc.Fill()

		// Draw "Neutral" watermark in the center of the neutral zone
		sg.drawNeutralWatermark(dc, x, yMin, width, yMax-yMin)
	}
}

// drawNeutralWatermark draws a "Neutral" watermark in the neutral zone
func (sg *SparklineGenerator) drawNeutralWatermark(dc *gg.Context, x, y, width, height float64) {
	// Only draw watermark if the neutral zone is large enough
	if height < 50 || width < 200 {
		return
	}

	// Calculate font size based on neutral zone size
	fontSize := height * 0.3 // 30% of neutral zone height
	if fontSize > 60 {
		fontSize = 60 // Cap at 60px
	}
	if fontSize < 20 {
		fontSize = 20 // Minimum 20px
	}

	// Load a larger font for the watermark
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", fontSize); err != nil {
		// Fallback to default font if Geneva is not available
		if fallbackErr := dc.LoadFontFace("", fontSize); fallbackErr != nil {
			_ = fallbackErr
			return
		}
	}

	// Calculate center position
	centerX := x + width/2
	centerY := y + height/2

	// Set watermark color - very light gray with low opacity
	dc.SetColor(color.RGBA{200, 200, 200, 30}) // Light gray with low transparency

	// Draw "Neutral" text centered in the neutral zone
	dc.DrawStringAnchored("Neutral", centerX, centerY, 0.5, 0.5)
}

// drawSentimentWatermarks draws "Positive" and "Negative" watermarks in their respective zones
func (sg *SparklineGenerator) drawSentimentWatermarks(dc *gg.Context, x, y, width, height float64, yRange YRange) {
	// Calculate font size based on chart height
	fontSize := height * 0.15 // 15% of chart height
	if fontSize > 40 {
		fontSize = 40 // Cap at 40px
	}
	if fontSize < 16 {
		fontSize = 16 // Minimum 16px
	}

	// Load font for watermarks
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", fontSize); err != nil {
		// Fallback to default font if Geneva is not available
		if fallbackErr := dc.LoadFontFace("", fontSize); fallbackErr != nil {
			_ = fallbackErr
			return
		}
	}

	// Draw "Positive" watermark in the positive zone (above +10%)
	positiveThreshold := 10.0
	if yRange.Max > positiveThreshold {
		// Calculate Y position for +10% line
		normalizedPositive := (positiveThreshold - yRange.Center) * yRange.Scale / 100.0
		positiveY := y + height/2 - normalizedPositive*(height/2)

		// Only draw if positive zone is large enough
		if positiveY < y+height-50 {
			positiveCenterY := (positiveY + y) / 2
			dc.SetColor(color.RGBA{40, 167, 69, 60}) // Green with higher opacity
			dc.DrawStringAnchored("Positive", x+width/2, positiveCenterY, 0.5, 0.5)
		}
	}

	// Draw "Negative" watermark in the negative zone (below -10%)
	negativeThreshold := -10.0
	if yRange.Min < negativeThreshold {
		// Calculate Y position for -10% line
		normalizedNegative := (negativeThreshold - yRange.Center) * yRange.Scale / 100.0
		negativeY := y + height/2 - normalizedNegative*(height/2)

		// Only draw if negative zone is large enough
		if negativeY > y+50 {
			negativeCenterY := (negativeY + y + height) / 2
			dc.SetColor(color.RGBA{220, 53, 69, 60}) // Red with higher opacity
			dc.DrawStringAnchored("Negative", x+width/2, negativeCenterY, 0.5, 0.5)
		}
	}
}

// drawBrandingWatermark draws "@hourstats.bsky.social" in the bottom left corner
func (sg *SparklineGenerator) drawBrandingWatermark(dc *gg.Context, x, y, width, height float64) {
	// Calculate font size for branding
	fontSize := 12.0

	// Load font for branding
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", fontSize); err != nil {
		// Fallback to default font if Geneva is not available
		if fallbackErr := dc.LoadFontFace("", fontSize); fallbackErr != nil {
			_ = fallbackErr
			return
		}
	}

	// Position in bottom left corner with small margin
	brandX := x + 10
	brandY := y + height - 10

	// Set branding color - dark gray with medium opacity
	dc.SetColor(color.RGBA{100, 100, 100, 150}) // Dark gray with medium opacity

	// Draw branding text
	dc.DrawStringAnchored("@hourstats.bsky.social", brandX, brandY, 0, 1)
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

// drawAverageLine draws a dark grey dotted horizontal line showing the average sentiment
func (sg *SparklineGenerator) drawAverageLine(dc *gg.Context, dataPoints []state.SentimentDataPoint, x, y, width, height float64, yRange YRange) {
	if len(dataPoints) == 0 {
		return
	}

	// Calculate the average sentiment
	var sum float64
	for _, dp := range dataPoints {
		sum += dp.NetSentimentPercent
	}
	average := sum / float64(len(dataPoints))

	// Convert average to Y position using the same scaling as the data
	normalizedAverage := (average - yRange.Center) * yRange.Scale / 100.0
	yPos := y + height/2 - normalizedAverage*(height/2)

	// Only draw if the average line is within the visible range
	if yPos >= y && yPos <= y+height {
		// Set up dotted line style
		dc.SetColor(color.RGBA{80, 80, 80, 255}) // Dark grey
		dc.SetLineWidth(2.0)

		// Create dotted line pattern
		dashLength := 8.0
		gapLength := 4.0
		currentX := x

		for currentX < x+width {
			endX := currentX + dashLength
			if endX > x+width {
				endX = x + width
			}

			dc.DrawLine(currentX, yPos, endX, yPos)
			dc.Stroke()

			currentX = endX + gapLength
		}
	}
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

	// Always draw the 0% label if it's within the visible range and different from center
	if yRange.Min <= 0.0 && yRange.Max >= 0.0 && yRange.Center != 0.0 {
		normalizedZero := (0.0 - yRange.Center) * yRange.Scale / 100.0
		yZero := y + height/2 - normalizedZero*(height/2)
		dc.DrawStringAnchored("0.0%", x-15, yZero, 1, 0.5)
	}

	// Draw improved time labels with day markers for midnight UTC
	if len(dataPoints) > 0 {
		startTime := dataPoints[0].Timestamp
		endTime := dataPoints[len(dataPoints)-1].Timestamp

		// Calculate time range in days
		timeRange := endTime.Sub(startTime).Hours() / 24

		if timeRange >= 1 {
			// For multi-day data, show day labels at midnight UTC positions
			sg.drawDayMarkers(dc, dataPoints, x, y, width, height)
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
	dc.DrawStringAnchored("Compound Bluesky Sentiment (UTC)", x+width/2, y-10, 0.5, 0)

	// Draw average line label
	sg.drawAverageLabel(dc, dataPoints, x, y, width, height, yRange)

	// Draw most recent observation label
	sg.drawMostRecentLabel(dc, dataPoints, x, y, width, height, yRange)

	// Draw lowest and highest observation labels
	sg.drawExtremeLabels(dc, dataPoints, x, y, width, height, yRange)
}

// drawDayMarkers draws day markers for midnight UTC positions
func (sg *SparklineGenerator) drawDayMarkers(dc *gg.Context, dataPoints []state.SentimentDataPoint, x, y, width, height float64) {
	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	// Find all midnight UTC positions within the data range
	midnightPositions := sg.findMidnightPositions(startTime, endTime)

	// Draw vertical lines and labels for each midnight
	for _, midnight := range midnightPositions {
		// Calculate x position for this midnight
		xPos := x + (midnight.Sub(startTime).Seconds()/timeRange)*width

		// Only draw if within the visible range
		if xPos >= x && xPos <= x+width {
			// Draw vertical line for midnight
			dc.SetColor(sg.config.GridColor)
			dc.SetLineWidth(0.5)
			dc.DrawLine(xPos, y, xPos, y+height)
			dc.Stroke()

			// Draw day label below the chart
			dayLabel := midnight.Format("Mon")
			dc.SetColor(sg.config.TextColor)
			dc.DrawStringAnchored(dayLabel, xPos, y+height+15, 0.5, 0)
		}
	}
}

// findMidnightPositions finds all midnight UTC positions within the given time range
func (sg *SparklineGenerator) findMidnightPositions(startTime, endTime time.Time) []time.Time {
	var midnights []time.Time

	// Start from the first midnight after or at startTime
	firstMidnight := startTime.Truncate(24 * time.Hour)
	if firstMidnight.Before(startTime) {
		firstMidnight = firstMidnight.Add(24 * time.Hour)
	}

	// Add all midnights within the range
	for current := firstMidnight; !current.After(endTime); current = current.Add(24 * time.Hour) {
		midnights = append(midnights, current)
	}

	return midnights
}

// drawAverageLabel draws a label for the average line
func (sg *SparklineGenerator) drawAverageLabel(dc *gg.Context, dataPoints []state.SentimentDataPoint, x, y, width, height float64, yRange YRange) {
	if len(dataPoints) == 0 {
		return
	}

	// Calculate the average sentiment
	var sum float64
	for _, dp := range dataPoints {
		sum += dp.NetSentimentPercent
	}
	average := sum / float64(len(dataPoints))

	// Convert average to Y position using the same scaling as the data
	normalizedAverage := (average - yRange.Center) * yRange.Scale / 100.0
	yPos := y + height/2 - normalizedAverage*(height/2)

	// Only draw if the average line is within the visible range
	if yPos >= y && yPos <= y+height {
		// Draw label on the right side of the chart
		label := fmt.Sprintf("Avg: %.1f%%", average)
		dc.SetColor(sg.config.TextColor)
		dc.DrawStringAnchored(label, x+width+10, yPos, 0, 0.5)
	}
}

// drawMostRecentLabel draws a label for the most recent observation
func (sg *SparklineGenerator) drawMostRecentLabel(dc *gg.Context, dataPoints []state.SentimentDataPoint, x, y, width, height float64, yRange YRange) {
	if len(dataPoints) == 0 {
		return
	}

	// Get the most recent data point
	lastPoint := dataPoints[len(dataPoints)-1]

	// Calculate position for the most recent point
	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()
	xPos := x + (lastPoint.Timestamp.Sub(startTime).Seconds()/timeRange)*width
	normalizedY := (lastPoint.NetSentimentPercent - yRange.Center) * yRange.Scale / 100.0
	yPos := y + height/2 - normalizedY*(height/2)

	// Draw label above the most recent point
	label := fmt.Sprintf("Latest: %.1f%%", lastPoint.NetSentimentPercent)
	dc.SetColor(sg.config.TextColor)
	dc.DrawStringAnchored(label, xPos, yPos-15, 0.5, 1)
}

// drawExtremeLabels draws labels for the lowest and highest observations
func (sg *SparklineGenerator) drawExtremeLabels(dc *gg.Context, dataPoints []state.SentimentDataPoint, x, y, width, height float64, yRange YRange) {
	if len(dataPoints) == 0 {
		return
	}

	// Find the lowest and highest observations
	var lowest, highest state.SentimentDataPoint
	lowest = dataPoints[0]
	highest = dataPoints[0]

	for _, dp := range dataPoints {
		if dp.NetSentimentPercent < lowest.NetSentimentPercent {
			lowest = dp
		}
		if dp.NetSentimentPercent > highest.NetSentimentPercent {
			highest = dp
		}
	}

	// Get the latest observation to check for duplicates
	latestPoint := dataPoints[len(dataPoints)-1]

	// Calculate time range for positioning
	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	// Draw lowest observation label (only if not the latest observation)
	if !(lowest.Timestamp.Equal(latestPoint.Timestamp) && lowest.NetSentimentPercent == latestPoint.NetSentimentPercent) {
		lowestXPos := x + (lowest.Timestamp.Sub(startTime).Seconds()/timeRange)*width
		normalizedLowestY := (lowest.NetSentimentPercent - yRange.Center) * yRange.Scale / 100.0
		lowestYPos := y + height/2 - normalizedLowestY*(height/2)

		// Position label below the point with timestamp on separate line
		lowestLabel := fmt.Sprintf("Low: %.1f%%\n%s", lowest.NetSentimentPercent, lowest.Timestamp.Format("Mon 15:04"))
		dc.SetColor(sg.config.TextColor)
		sg.drawMultilineStringAnchored(dc, lowestLabel, lowestXPos, lowestYPos+15, 0.5, 0)
	}

	// Draw highest observation label (only if different from lowest and not the latest observation)
	if highest.NetSentimentPercent != lowest.NetSentimentPercent &&
		!(highest.Timestamp.Equal(latestPoint.Timestamp) && highest.NetSentimentPercent == latestPoint.NetSentimentPercent) {
		highestXPos := x + (highest.Timestamp.Sub(startTime).Seconds()/timeRange)*width
		normalizedHighestY := (highest.NetSentimentPercent - yRange.Center) * yRange.Scale / 100.0
		highestYPos := y + height/2 - normalizedHighestY*(height/2)

		// Position label above the point with timestamp on separate line
		highestLabel := fmt.Sprintf("High: %.1f%%\n%s", highest.NetSentimentPercent, highest.Timestamp.Format("Mon 15:04"))
		dc.SetColor(sg.config.TextColor)
		sg.drawMultilineStringAnchored(dc, highestLabel, highestXPos, highestYPos-15, 0.5, 1)
	}
}

// drawMultilineStringAnchored draws multi-line text with proper anchoring
func (sg *SparklineGenerator) drawMultilineStringAnchored(dc *gg.Context, text string, x, y, anchorX, anchorY float64) {
	lines := strings.Split(text, "\n")
	lineHeight := 14.0 // Font height for 12pt font

	// Calculate total height of all lines
	totalHeight := float64(len(lines)-1) * lineHeight

	// Calculate starting Y position based on anchor
	startY := y
	if anchorY == 0.5 { // Center anchor
		startY = y - totalHeight/2
	} else if anchorY == 1.0 { // Top anchor
		startY = y - totalHeight
	}
	// For bottom anchor (anchorY == 0.0), startY remains as y

	// Draw each line
	for i, line := range lines {
		lineY := startY + float64(i)*lineHeight
		dc.DrawStringAnchored(line, x, lineY, anchorX, 0.5)
	}
}
