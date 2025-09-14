package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/fogleman/gg"
)

// YearlySparklineConfig holds configuration for yearly sparkline generation
type YearlySparklineConfig struct {
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

// YearlyYRange represents the Y-axis range for the yearly sparkline
type YearlyYRange struct {
	Min    float64
	Max    float64
	Center float64
	Scale  float64
}

// calculateYearlyYRange calculates the Y-axis range based on actual yearly data
func (yg *YearlySparklineGenerator) calculateYearlyYRange(dataPoints []state.YearlySparklineDataPoint) YearlyYRange {
	if len(dataPoints) == 0 {
		return YearlyYRange{Min: -100, Max: 100, Center: 0, Scale: 1.0}
	}

	// Find min and max values
	min := dataPoints[0].AverageSentiment
	max := dataPoints[0].AverageSentiment

	for _, dp := range dataPoints {
		if dp.AverageSentiment < min {
			min = dp.AverageSentiment
		}
		if dp.AverageSentiment > max {
			max = dp.AverageSentiment
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

	return YearlyYRange{
		Min:    finalMin,
		Max:    finalMax,
		Center: center,
		Scale:  scale,
	}
}

// DefaultYearlyConfig returns a default yearly sparkline configuration (25% larger)
func DefaultYearlyConfig() *YearlySparklineConfig {
	return &YearlySparklineConfig{
		Width:        1500,                           // 25% larger than 1200
		Height:       1000,                           // 25% larger than 800
		Padding:      100,                            // Scaled proportionally
		LineWidth:    4.0,                            // Scaled proportionally
		PointRadius:  1.0,                            // Scaled proportionally
		Background:   color.RGBA{248, 249, 250, 255}, // Light gray
		PositiveLine: color.RGBA{40, 167, 69, 255},   // Green
		NegativeLine: color.RGBA{220, 53, 69, 255},   // Red
		NeutralLine:  color.RGBA{108, 117, 125, 255}, // Gray
		GridColor:    color.RGBA{200, 200, 200, 255}, // Light gray
		TextColor:    color.RGBA{33, 37, 41, 255},    // Dark gray
	}
}

// YearlySparklineGenerator generates yearly sentiment sparkline images
type YearlySparklineGenerator struct {
	config *YearlySparklineConfig
}

// NewYearlySparklineGenerator creates a new yearly sparkline generator
func NewYearlySparklineGenerator(config *YearlySparklineConfig) *YearlySparklineGenerator {
	if config == nil {
		config = DefaultYearlyConfig()
	}
	return &YearlySparklineGenerator{config: config}
}

// GenerateYearlySentimentSparkline creates a PNG image of yearly sentiment data
func (yg *YearlySparklineGenerator) GenerateYearlySentimentSparkline(dataPoints []state.YearlySparklineDataPoint) ([]byte, error) {
	if len(dataPoints) == 0 {
		return nil, fmt.Errorf("no data points provided")
	}

	// Create image context
	dc := gg.NewContext(yg.config.Width, yg.config.Height)

	// Fill background
	dc.SetColor(yg.config.Background)
	dc.Clear()

	// Calculate drawing area with extra space for Y-axis labels
	leftPadding := yg.config.Padding + 50 // Extra 50px for Y-axis labels
	rightPadding := yg.config.Padding
	topPadding := yg.config.Padding
	bottomPadding := yg.config.Padding + 30 // Extra 30px for month labels

	drawWidth := float64(yg.config.Width - leftPadding - rightPadding)
	drawHeight := float64(yg.config.Height - topPadding - bottomPadding)
	drawX := float64(leftPadding)
	drawY := float64(topPadding)

	// Calculate Y-axis range based on actual data
	yRange := yg.calculateYearlyYRange(dataPoints)

	// Draw grid lines
	yg.drawYearlyGrid(dc, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw neutral zone background
	yg.drawYearlyNeutralZone(dc, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw sentiment line
	yg.drawYearlySentimentLine(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw Gaussian smoothed trend line
	yg.drawYearlyGaussianTrendLine(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw average line
	yg.drawYearlyAverageLine(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw labels
	yg.drawYearlyLabels(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw sentiment zone watermarks
	yg.drawYearlySentimentWatermarks(dc, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw branding watermark
	yg.drawYearlyBrandingWatermark(dc, drawX, drawY, drawWidth, drawHeight)

	// Encode as PNG
	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// drawYearlyGrid draws grid lines and axes for yearly view
func (yg *YearlySparklineGenerator) drawYearlyGrid(dc *gg.Context, x, y, width, height float64, yRange YearlyYRange) {
	dc.SetColor(yg.config.GridColor)
	dc.SetLineWidth(0.5)

	// Horizontal grid lines (sentiment levels)
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

	// Vertical center line (neutral)
	centerY := y + height/2
	dc.SetLineWidth(1.0)
	dc.DrawLine(x, centerY, x+width, centerY)
	dc.Stroke()
}

// drawYearlyNeutralZone draws a light gray background area for the neutral sentiment zone
func (yg *YearlySparklineGenerator) drawYearlyNeutralZone(dc *gg.Context, x, y, width, height float64, yRange YearlyYRange) {
	// Define neutral zone boundaries
	neutralMin := -10.0
	neutralMax := 10.0

	// Convert neutral zone boundaries to Y positions
	normalizedMin := (neutralMin - yRange.Center) * yRange.Scale / 100.0
	normalizedMax := (neutralMax - yRange.Center) * yRange.Scale / 100.0

	yMin := y + height/2 - normalizedMax*(height/2)
	yMax := y + height/2 - normalizedMin*(height/2)

	// Only draw if the neutral zone is visible
	if yMin < y+height && yMax > y {
		// Clamp to visible area
		if yMin < y {
			yMin = y
		}
		if yMax > y+height {
			yMax = y + height
		}

		// Draw the neutral zone rectangle
		dc.SetColor(color.RGBA{252, 252, 252, 20})
		dc.DrawRectangle(x, yMin, width, yMax-yMin)
		dc.Fill()

		// Draw "Neutral" watermark
		yg.drawYearlyNeutralWatermark(dc, x, yMin, width, yMax-yMin)
	}
}

// drawYearlyNeutralWatermark draws a "Neutral" watermark
func (yg *YearlySparklineGenerator) drawYearlyNeutralWatermark(dc *gg.Context, x, y, width, height float64) {
	if height < 50 || width < 200 {
		return
	}

	fontSize := height * 0.3
	if fontSize > 60 {
		fontSize = 60
	}
	if fontSize < 20 {
		fontSize = 20
	}

	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", fontSize); err != nil {
		if fallbackErr := dc.LoadFontFace("", fontSize); fallbackErr != nil {
			_ = fallbackErr
		}
	}

	centerX := x + width/2
	centerY := y + height/2

	dc.SetColor(color.RGBA{200, 200, 200, 30})
	dc.DrawStringAnchored("Neutral", centerX, centerY, 0.5, 0.5)
}

// drawYearlySentimentWatermarks draws "Positive" and "Negative" watermarks
func (yg *YearlySparklineGenerator) drawYearlySentimentWatermarks(dc *gg.Context, x, y, width, height float64, yRange YearlyYRange) {
	fontSize := height * 0.15
	if fontSize > 40 {
		fontSize = 40
	}
	if fontSize < 16 {
		fontSize = 16
	}

	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", fontSize); err != nil {
		if fallbackErr := dc.LoadFontFace("", fontSize); fallbackErr != nil {
			_ = fallbackErr
		}
	}

	// Draw "Positive" watermark
	positiveThreshold := 10.0
	if yRange.Max > positiveThreshold {
		normalizedPositive := (positiveThreshold - yRange.Center) * yRange.Scale / 100.0
		positiveY := y + height/2 - normalizedPositive*(height/2)

		if positiveY < y+height-50 {
			positiveCenterY := (positiveY + y) / 2
			dc.SetColor(color.RGBA{40, 167, 69, 60})
			dc.DrawStringAnchored("Positive", x+width/2, positiveCenterY, 0.5, 0.5)
		}
	}

	// Draw "Negative" watermark
	negativeThreshold := -10.0
	if yRange.Min < negativeThreshold {
		normalizedNegative := (negativeThreshold - yRange.Center) * yRange.Scale / 100.0
		negativeY := y + height/2 - normalizedNegative*(height/2)

		if negativeY > y+50 {
			negativeCenterY := (negativeY + y + height) / 2
			dc.SetColor(color.RGBA{220, 53, 69, 60})
			dc.DrawStringAnchored("Negative", x+width/2, negativeCenterY, 0.5, 0.5)
		}
	}
}

// drawYearlyBrandingWatermark draws "@hourstats.bsky.social" branding
func (yg *YearlySparklineGenerator) drawYearlyBrandingWatermark(dc *gg.Context, x, y, width, height float64) {
	fontSize := 12.0

	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", fontSize); err != nil {
		if fallbackErr := dc.LoadFontFace("", fontSize); fallbackErr != nil {
			_ = fallbackErr
		}
	}

	brandX := x + 10
	brandY := y + height - 10

	dc.SetColor(color.RGBA{100, 100, 100, 150})
	dc.DrawStringAnchored("@hourstats.bsky.social", brandX, brandY, 0, 1)
}

// drawYearlySentimentLine draws the sentiment line with appropriate colors
func (yg *YearlySparklineGenerator) drawYearlySentimentLine(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64, yRange YearlyYRange) {
	if len(dataPoints) < 2 {
		return
	}

	// Calculate time range (365 days)
	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	// Draw line segments with appropriate colors
	for i := 0; i < len(dataPoints)-1; i++ {
		current := dataPoints[i]
		next := dataPoints[i+1]

		// Calculate positions
		x1 := x + (current.Timestamp.Sub(startTime).Seconds()/timeRange)*width
		normalizedY1 := (current.AverageSentiment - yRange.Center) * yRange.Scale / 100.0
		y1 := y + height/2 - normalizedY1*(height/2)
		x2 := x + (next.Timestamp.Sub(startTime).Seconds()/timeRange)*width
		normalizedY2 := (next.AverageSentiment - yRange.Center) * yRange.Scale / 100.0
		y2 := y + height/2 - normalizedY2*(height/2)

		// Determine color based on sentiment
		var lineColor color.RGBA
		if current.AverageSentiment > 10 {
			lineColor = yg.config.PositiveLine
		} else if current.AverageSentiment < -10 {
			lineColor = yg.config.NegativeLine
		} else {
			lineColor = yg.config.NeutralLine
		}

		// Draw line segment
		dc.SetColor(lineColor)
		dc.SetLineWidth(yg.config.LineWidth)
		dc.DrawLine(x1, y1, x2, y2)
		dc.Stroke()

		// Draw point
		dc.SetColor(lineColor)
		dc.DrawCircle(x1, y1, yg.config.PointRadius)
		dc.Fill()
	}

	// Draw final point
	lastPoint := dataPoints[len(dataPoints)-1]
	xFinal := x + (lastPoint.Timestamp.Sub(startTime).Seconds()/timeRange)*width
	normalizedYFinal := (lastPoint.AverageSentiment - yRange.Center) * yRange.Scale / 100.0
	yFinal := y + height/2 - normalizedYFinal*(height/2)

	var pointColor color.RGBA
	if lastPoint.AverageSentiment > 10 {
		pointColor = yg.config.PositiveLine
	} else if lastPoint.AverageSentiment < -10 {
		pointColor = yg.config.NegativeLine
	} else {
		pointColor = yg.config.NeutralLine
	}

	dc.SetColor(pointColor)
	dc.DrawCircle(xFinal, yFinal, yg.config.PointRadius)
	dc.Fill()
}

// drawYearlyAverageLine draws a dark grey dotted horizontal line showing the average sentiment
func (yg *YearlySparklineGenerator) drawYearlyAverageLine(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64, yRange YearlyYRange) {
	if len(dataPoints) == 0 {
		return
	}

	// Calculate the average sentiment
	var sum float64
	for _, dp := range dataPoints {
		sum += dp.AverageSentiment
	}
	average := sum / float64(len(dataPoints))

	// Convert average to Y position
	normalizedAverage := (average - yRange.Center) * yRange.Scale / 100.0
	yPos := y + height/2 - normalizedAverage*(height/2)

	// Only draw if the average line is within the visible range
	if yPos >= y && yPos <= y+height {
		dc.SetColor(color.RGBA{80, 80, 80, 255})
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

// drawYearlyLabels draws time and sentiment labels for yearly view
func (yg *YearlySparklineGenerator) drawYearlyLabels(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64, yRange YearlyYRange) {
	dc.SetColor(yg.config.TextColor)

	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		if fallbackErr := dc.LoadFontFace("/System/Library/Fonts/Symbol.ttf", 12); fallbackErr != nil {
			_ = fallbackErr
		}
	}

	// Draw sentiment level labels
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

	// Draw month markers
	yg.drawYearlyMonthMarkers(dc, dataPoints, x, y, width, height)

	// Draw title
	if err := dc.LoadFontFace("", 16); err != nil {
		_ = err
	}
	dc.DrawStringAnchored("Yearly Sentiment (UTC)", x+width/2, y-10, 0.5, 0)

	// Draw average line label
	yg.drawYearlyAverageLabel(dc, dataPoints, x, y, width, height, yRange)
}

// drawYearlyMonthMarkers draws month markers for yearly view
func (yg *YearlySparklineGenerator) drawYearlyMonthMarkers(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64) {
	if len(dataPoints) == 0 {
		return
	}

	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	// Find all month boundaries within the data range
	monthPositions := yg.findYearlyMonthPositions(startTime, endTime)

	// Draw vertical lines and labels for each month
	for _, monthTime := range monthPositions {
		// Calculate x position for this month
		xPos := x + (monthTime.Sub(startTime).Seconds()/timeRange)*width

		// Only draw if within the visible range
		if xPos >= x && xPos <= x+width {
			// Draw vertical line for month boundary
			dc.SetColor(yg.config.GridColor)
			dc.SetLineWidth(0.5)
			dc.DrawLine(xPos, y, xPos, y+height)
			dc.Stroke()

			// Draw month label below the chart
			monthLabel := monthTime.Format("Jan")
			dc.SetColor(yg.config.TextColor)
			dc.DrawStringAnchored(monthLabel, xPos, y+height+15, 0.5, 0)
		}
	}
}

// findYearlyMonthPositions finds all month boundary positions within the given time range
func (yg *YearlySparklineGenerator) findYearlyMonthPositions(startTime, endTime time.Time) []time.Time {
	var months []time.Time

	// Start from the first day of the month containing startTime
	firstMonth := time.Date(startTime.Year(), startTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	if firstMonth.Before(startTime) {
		firstMonth = firstMonth.AddDate(0, 1, 0)
	}

	// Add all month boundaries within the range
	for current := firstMonth; !current.After(endTime); current = current.AddDate(0, 1, 0) {
		months = append(months, current)
	}

	return months
}

// drawYearlyAverageLabel draws a label for the average line
func (yg *YearlySparklineGenerator) drawYearlyAverageLabel(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64, yRange YearlyYRange) {
	if len(dataPoints) == 0 {
		return
	}

	// Calculate the average sentiment
	var sum float64
	for _, dp := range dataPoints {
		sum += dp.AverageSentiment
	}
	average := sum / float64(len(dataPoints))

	// Convert average to Y position
	normalizedAverage := (average - yRange.Center) * yRange.Scale / 100.0
	yPos := y + height/2 - normalizedAverage*(height/2)

	// Only draw if the average line is within the visible range
	if yPos >= y && yPos <= y+height {
		label := fmt.Sprintf("Avg: %.1f%%", average)
		dc.SetColor(yg.config.TextColor)
		dc.DrawStringAnchored(label, x+width/2, yPos-15, 0.5, 1)
	}
}

// yearlyGaussianSmoothing applies Gaussian smoothing to yearly sentiment data
func yearlyGaussianSmoothing(data []float64, sigma float64) []float64 {
	if len(data) == 0 {
		return data
	}

	smoothed := make([]float64, len(data))

	for i := 0; i < len(data); i++ {
		sum := 0.0
		weightSum := 0.0

		for j := 0; j < len(data); j++ {
			distance := math.Abs(float64(j - i))
			weight := math.Exp(-(distance * distance) / (2 * sigma * sigma))

			sum += data[j] * weight
			weightSum += weight
		}

		smoothed[i] = sum / weightSum
	}

	return smoothed
}

// drawYearlyGaussianTrendLine draws a thin dashed blue Gaussian smoothed trend line
func (yg *YearlySparklineGenerator) drawYearlyGaussianTrendLine(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64, yRange YearlyYRange) {
	if len(dataPoints) < 2 {
		return
	}

	// Extract sentiment values for smoothing
	sentimentValues := make([]float64, len(dataPoints))
	for i, dp := range dataPoints {
		sentimentValues[i] = dp.AverageSentiment
	}

	// Apply Gaussian smoothing
	smoothedData := yearlyGaussianSmoothing(sentimentValues, 2.0)

	// Calculate time range
	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	// Draw as thin dashed blue line
	dc.SetColor(color.RGBA{0, 123, 255, 255})
	dc.SetLineWidth(1.5)
	dc.SetDash(4, 3)

	for i := 0; i < len(smoothedData)-1; i++ {
		x1 := x + (dataPoints[i].Timestamp.Sub(startTime).Seconds()/timeRange)*width
		normalizedY1 := (smoothedData[i] - yRange.Center) * yRange.Scale / 100.0
		y1 := y + height/2 - normalizedY1*(height/2)

		x2 := x + (dataPoints[i+1].Timestamp.Sub(startTime).Seconds()/timeRange)*width
		normalizedY2 := (smoothedData[i+1] - yRange.Center) * yRange.Scale / 100.0
		y2 := y + height/2 - normalizedY2*(height/2)

		dc.DrawLine(x1, y1, x2, y2)
		dc.Stroke()
	}

	dc.SetDash() // Reset dash pattern
}
