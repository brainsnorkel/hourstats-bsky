package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"strings"
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

	// Draw sentiment zone watermarks (before extreme labels to avoid covering them)
	yg.drawYearlySentimentWatermarks(dc, drawX, drawY, drawWidth, drawHeight, yRange)

	// Draw extreme labels (highest and lowest sentiment) - draw last so they're on top
	yg.drawYearlyExtremeLabels(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yRange)

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

	// Draw month markers, biweekly ticks, and weekly ticks
	yg.drawYearlyMonthMarkers(dc, dataPoints, x, y, width, height)
	yg.drawYearlyBiweeklyTicks(dc, dataPoints, x, y, width, height)
	yg.drawYearlyWeeklyTicks(dc, dataPoints, x, y, width, height)
	
	// Draw start and end date labels
	yg.drawYearlyStartEndLabels(dc, dataPoints, x, y, width, height)

	// Draw title with date range - use large font (32pt, doubled from original 16)
	// Explicitly load font before drawing to ensure it's applied
	titleFontSize := 32.0
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", titleFontSize); err != nil {
		// Try fallback fonts if Geneva fails
		if fallbackErr := dc.LoadFontFace("/System/Library/Fonts/Helvetica.ttc", titleFontSize); fallbackErr != nil {
			if fallbackErr2 := dc.LoadFontFace("", titleFontSize); fallbackErr2 != nil {
				_ = fallbackErr2
			}
		}
	}
	// Set text color for title
	dc.SetColor(yg.config.TextColor)
	if len(dataPoints) > 0 {
		startDate := dataPoints[0].Timestamp.Format("2006-01-02")
		endDate := dataPoints[len(dataPoints)-1].Timestamp.Format("2006-01-02")
		title := fmt.Sprintf("Bluesky Sentiment %s - %s", startDate, endDate)
		// Position title higher to accommodate larger font
		dc.DrawStringAnchored(title, x+width/2, y-15, 0.5, 0)
	} else {
		dc.DrawStringAnchored("Bluesky Sentiment", x+width/2, y-15, 0.5, 0)
	}

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
	// Always include this month marker for chart readability
	firstMonth := time.Date(startTime.Year(), startTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	
	// End at the first day of the month after endTime (to include the endTime's month)
	endMonth := time.Date(endTime.Year(), endTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	// Include the month after endTime as well for better context
	endMonth = endMonth.AddDate(0, 1, 0)

	// Add all month boundaries from firstMonth to endMonth (inclusive)
	for current := firstMonth; !current.After(endMonth); current = current.AddDate(0, 1, 0) {
		months = append(months, current)
	}

	return months
}

// drawYearlyBiweeklyTicks draws biweekly (every 2 weeks) date ticks for yearly view
func (yg *YearlySparklineGenerator) drawYearlyBiweeklyTicks(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64) {
	if len(dataPoints) == 0 {
		return
	}

	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	// Find all biweekly positions (every 14 days) within the data range
	biweeklyPositions := yg.findYearlyBiweeklyPositions(startTime, endTime)
	monthPositions := yg.findYearlyMonthPositions(startTime, endTime)

	// Create a set of month positions to avoid duplicating ticks
	monthPosSet := make(map[string]bool)
	for _, monthTime := range monthPositions {
		monthPosSet[monthTime.Format("2006-01-02")] = true
	}

	// Load smaller font for date labels
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 10); err != nil {
		if fallbackErr := dc.LoadFontFace("/System/Library/Fonts/Symbol.ttf", 10); fallbackErr != nil {
			_ = fallbackErr
		}
	}

	// Draw vertical lines and date labels for each biweekly position
	for _, biweeklyTime := range biweeklyPositions {
		// Skip if this is already a month marker
		if monthPosSet[biweeklyTime.Format("2006-01-02")] {
			continue
		}

		// Calculate x position for this biweekly tick
		xPos := x + (biweeklyTime.Sub(startTime).Seconds()/timeRange)*width

		// Only draw if within the visible range
		if xPos >= x && xPos <= x+width {
			// Draw a shorter vertical line for biweekly tick (lighter than month markers)
			dc.SetColor(color.RGBA{220, 220, 220, 255}) // Lighter gray
			dc.SetLineWidth(0.3)
			tickHeight := height * 0.15 // Shorter tick line (15% of chart height)
			dc.DrawLine(xPos, y+height-tickHeight, xPos, y+height)
			dc.Stroke()

			// Draw date label below the chart, rotated 90 degrees clockwise
			// Format: "15 Oct" (day month)
			dateLabel := biweeklyTime.Format("2 Jan") // Format: "15 Oct"
			dc.SetColor(color.RGBA{120, 120, 120, 255}) // Darker gray for text
			
			// Position for the label (below the chart)
			labelX := xPos
			labelY := y + height + 25
			
			// Rotate text 90 degrees clockwise using Push/Translate/Rotate
			// In gg, transformations are applied in order: Translate then Rotate
			dc.Push()
			// Translate to where we want the text (this becomes the rotation center)
			dc.Translate(labelX, labelY)
			// Rotate 90 degrees clockwise around the translated origin
			dc.Rotate(math.Pi / 2)
			// Draw text at (0,0) in the transformed coordinate system
			// The anchor (0.5, 0.5) centers the text at the rotation point
			dc.DrawStringAnchored(dateLabel, 0, 0, 0.5, 0.5)
			dc.Pop()
		}
	}
}

// findYearlyBiweeklyPositions finds all biweekly (every 14 days) positions within the given time range
func (yg *YearlySparklineGenerator) findYearlyBiweeklyPositions(startTime, endTime time.Time) []time.Time {
	var biweeklyPositions []time.Time

	// Start from startTime, then add 14 days for each biweekly tick
	// We want ticks approximately every 2 weeks from the start
	firstBiweekly := startTime.Truncate(24 * time.Hour) // Round to midnight
	
	// Find the first biweekly position (could be startTime or up to 14 days later)
	// We'll align to approximate 14-day intervals
	for current := firstBiweekly; !current.After(endTime); current = current.AddDate(0, 0, 14) {
		// Only add if it's after or equal to startTime
		if !current.Before(startTime) {
			biweeklyPositions = append(biweeklyPositions, current)
		}
	}

	return biweeklyPositions
}

// drawYearlyWeeklyTicks draws weekly (every 7 days) date ticks for yearly view
func (yg *YearlySparklineGenerator) drawYearlyWeeklyTicks(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64) {
	if len(dataPoints) == 0 {
		return
	}

	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	// Find all weekly positions (every 7 days) and existing markers to avoid duplicates
	weeklyPositions := yg.findYearlyWeeklyPositions(startTime, endTime)
	monthPositions := yg.findYearlyMonthPositions(startTime, endTime)
	biweeklyPositions := yg.findYearlyBiweeklyPositions(startTime, endTime)

	// Create a set of existing positions to avoid duplicating ticks
	existingPosSet := make(map[string]bool)
	for _, monthTime := range monthPositions {
		existingPosSet[monthTime.Format("2006-01-02")] = true
	}
	for _, biweeklyTime := range biweeklyPositions {
		existingPosSet[biweeklyTime.Format("2006-01-02")] = true
	}

	// Draw vertical lines for each weekly position
	for _, weeklyTime := range weeklyPositions {
		// Skip if this is already a month or biweekly marker
		if existingPosSet[weeklyTime.Format("2006-01-02")] {
			continue
		}

		// Calculate x position for this weekly tick
		xPos := x + (weeklyTime.Sub(startTime).Seconds()/timeRange)*width

		// Only draw if within the visible range
		if xPos >= x && xPos <= x+width {
			// Draw a very short vertical line for weekly tick (even shorter than biweekly)
			dc.SetColor(color.RGBA{240, 240, 240, 255}) // Very light gray
			dc.SetLineWidth(0.2)
			tickHeight := height * 0.08 // Very short tick line (8% of chart height)
			dc.DrawLine(xPos, y+height-tickHeight, xPos, y+height)
			dc.Stroke()
		}
	}
}

// findYearlyWeeklyPositions finds all weekly (every 7 days) positions within the given time range
func (yg *YearlySparklineGenerator) findYearlyWeeklyPositions(startTime, endTime time.Time) []time.Time {
	var weeklyPositions []time.Time

	// Start from startTime, then add 7 days for each weekly tick
	firstWeekly := startTime.Truncate(24 * time.Hour) // Round to midnight
	
	// Find all weekly positions (every 7 days)
	for current := firstWeekly; !current.After(endTime); current = current.AddDate(0, 0, 7) {
		// Only add if it's after or equal to startTime
		if !current.Before(startTime) {
			weeklyPositions = append(weeklyPositions, current)
		}
	}

	return weeklyPositions
}

// drawYearlyStartEndLabels draws start and end date labels on the x-axis
func (yg *YearlySparklineGenerator) drawYearlyStartEndLabels(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64) {
	if len(dataPoints) == 0 {
		return
	}

	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp

	// Load font for labels
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 10); err != nil {
		if fallbackErr := dc.LoadFontFace("/System/Library/Fonts/Symbol.ttf", 10); fallbackErr != nil {
			_ = fallbackErr
		}
	}

	// Draw start date label at the left edge (50 pixels below chart to avoid overlap)
	startLabel := startTime.Format("2 Jan") // Format: "18 Sep"
	dc.SetColor(color.RGBA{80, 80, 80, 255}) // Dark gray for start/end labels
	dc.DrawStringAnchored(startLabel, x, y+height+50, 0, 0)

	// Draw end date label at the right edge (50 pixels below chart to avoid overlap)
	endLabel := endTime.Format("2 Jan") // Format: "31 Oct"
	dc.SetColor(color.RGBA{80, 80, 80, 255})
	dc.DrawStringAnchored(endLabel, x+width, y+height+50, 1, 0)
}

// drawYearlyExtremeLabels draws labels for the highest and lowest sentiment points
func (yg *YearlySparklineGenerator) drawYearlyExtremeLabels(dc *gg.Context, dataPoints []state.YearlySparklineDataPoint, x, y, width, height float64, yRange YearlyYRange) {
	if len(dataPoints) == 0 {
		return
	}

	// Load font for labels
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 11); err != nil {
		if fallbackErr := dc.LoadFontFace("/System/Library/Fonts/Symbol.ttf", 11); fallbackErr != nil {
			_ = fallbackErr
		}
	}

	// Find the lowest and highest sentiment points
	var lowest, highest state.YearlySparklineDataPoint
	lowest = dataPoints[0]
	highest = dataPoints[0]

	for _, dp := range dataPoints {
		if dp.AverageSentiment < lowest.AverageSentiment {
			lowest = dp
		}
		if dp.AverageSentiment > highest.AverageSentiment {
			highest = dp
		}
	}

	// Calculate time range for positioning
	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	// Draw lowest sentiment label with marker circle
	lowestXPos := x + (lowest.Timestamp.Sub(startTime).Seconds()/timeRange)*width
	normalizedLowestY := (lowest.AverageSentiment - yRange.Center) * yRange.Scale / 100.0
	lowestYPos := y + height/2 - normalizedLowestY*(height/2)

	// Verify the point is within bounds before drawing
	if lowestXPos >= x && lowestXPos <= x+width && lowestYPos >= y && lowestYPos <= y+height {
		// Draw a larger, more visible circle marker at the lowest point
		dc.SetColor(color.RGBA{220, 53, 69, 255}) // Red for lowest
		dc.DrawCircle(lowestXPos, lowestYPos, 6)
		dc.Fill()

		// Format date as "Jan 2"
		lowestDateLabel := lowest.Timestamp.Format("Jan 2")
		lowestLabel := fmt.Sprintf("%.1f%%\n%s", lowest.AverageSentiment, lowestDateLabel)
		dc.SetColor(color.RGBA{220, 53, 69, 255}) // Use red color for visibility
		// Draw label below the point with more spacing
		yg.drawYearlyMultilineStringAnchored(dc, lowestLabel, lowestXPos, lowestYPos+30, 0.5, 0)
	}

	// Draw highest sentiment label with marker circle (only if different from lowest)
	if highest.AverageSentiment != lowest.AverageSentiment || !highest.Timestamp.Equal(lowest.Timestamp) {
		highestXPos := x + (highest.Timestamp.Sub(startTime).Seconds()/timeRange)*width
		normalizedHighestY := (highest.AverageSentiment - yRange.Center) * yRange.Scale / 100.0
		highestYPos := y + height/2 - normalizedHighestY*(height/2)

		// Verify the point is within bounds before drawing
		if highestXPos >= x && highestXPos <= x+width && highestYPos >= y && highestYPos <= y+height {
			// Draw a larger, more visible circle marker at the highest point
			dc.SetColor(color.RGBA{40, 167, 69, 255}) // Green for highest
			dc.DrawCircle(highestXPos, highestYPos, 6)
			dc.Fill()

			// Format date as "Jan 2"
			highestDateLabel := highest.Timestamp.Format("Jan 2")
			highestLabel := fmt.Sprintf("%.1f%%\n%s", highest.AverageSentiment, highestDateLabel)
			dc.SetColor(color.RGBA{40, 167, 69, 255}) // Use green color for visibility
			// Draw label above the point with more spacing
			yg.drawYearlyMultilineStringAnchored(dc, highestLabel, highestXPos, highestYPos-30, 0.5, 1)
		}
	}
}

// drawYearlyMultilineStringAnchored draws multi-line text with proper anchoring for yearly view
func (yg *YearlySparklineGenerator) drawYearlyMultilineStringAnchored(dc *gg.Context, text string, x, y, anchorX, anchorY float64) {
	lines := strings.Split(text, "\n")
	lineHeight := 13.0 // Font height for 11pt font

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
		// Use same font size as extreme labels (11pt)
		if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 11); err != nil {
			if fallbackErr := dc.LoadFontFace("/System/Library/Fonts/Symbol.ttf", 11); fallbackErr != nil {
				_ = fallbackErr
			}
		}
		
		label := fmt.Sprintf("Avg: %.1f%%", average)
		dc.SetColor(yg.config.TextColor)
		// Position label on the right side of the chart, 10 pixels below the average line
		dc.DrawStringAnchored(label, x+width-10, yPos+10, 1, 0.5)
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
