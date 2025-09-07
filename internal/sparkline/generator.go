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

// DefaultConfig returns a default sparkline configuration
func DefaultConfig() *SparklineConfig {
	return &SparklineConfig{
		Width:        400,
		Height:       200,
		Padding:      20,
		LineWidth:    2.0,
		PointRadius:  3.0,
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

	// Calculate drawing area
	drawWidth := float64(sg.config.Width - 2*sg.config.Padding)
	drawHeight := float64(sg.config.Height - 2*sg.config.Padding)
	drawX := float64(sg.config.Padding)
	drawY := float64(sg.config.Padding)

	// Draw grid lines
	sg.drawGrid(dc, drawX, drawY, drawWidth, drawHeight)

	// Draw sentiment line
	sg.drawSentimentLine(dc, dataPoints, drawX, drawY, drawWidth, drawHeight)

	// Draw labels
	sg.drawLabels(dc, dataPoints, drawX, drawY, drawWidth, drawHeight)

	// Encode as PNG
	var buf bytes.Buffer
	dc.EncodePNG(&buf)
	return buf.Bytes(), nil
}

// drawGrid draws grid lines and axes
func (sg *SparklineGenerator) drawGrid(dc *gg.Context, x, y, width, height float64) {
	dc.SetColor(sg.config.GridColor)
	dc.SetLineWidth(0.5)

	// Horizontal grid lines (sentiment levels)
	levels := []float64{-100, -50, 0, 50, 100}
	for _, level := range levels {
		yPos := y + height/2 - (level/100.0)*(height/2)
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
func (sg *SparklineGenerator) drawSentimentLine(dc *gg.Context, dataPoints []state.SentimentDataPoint, x, y, width, height float64) {
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
		y1 := y + height/2 - (current.NetSentimentPercent/100.0)*(height/2)
		x2 := x + (next.Timestamp.Sub(startTime).Seconds()/timeRange)*width
		y2 := y + height/2 - (next.NetSentimentPercent/100.0)*(height/2)

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
	yFinal := y + height/2 - (lastPoint.NetSentimentPercent/100.0)*(height/2)
	
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
func (sg *SparklineGenerator) drawLabels(dc *gg.Context, dataPoints []state.SentimentDataPoint, x, y, width, height float64) {
	dc.SetColor(sg.config.TextColor)
	
	// Load font (using default font for now)
	if err := dc.LoadFontFace("", 12); err != nil {
		// Fallback to default font - gg doesn't have SetFontSize, use LoadFontFace
		dc.LoadFontFace("", 12)
	}

	// Draw sentiment level labels
	levels := []struct {
		value float64
		label string
	}{
		{100, "+100%"},
		{50, "+50%"},
		{0, "0%"},
		{-50, "-50%"},
		{-100, "-100%"},
	}

	for _, level := range levels {
		yPos := y + height/2 - (level.value/100.0)*(height/2)
		dc.DrawStringAnchored(level.label, x-5, yPos, 1, 0.5)
	}

	// Draw time labels (start and end)
	if len(dataPoints) > 0 {
		startLabel := dataPoints[0].Timestamp.Format("15:04")
		endLabel := dataPoints[len(dataPoints)-1].Timestamp.Format("15:04")
		
		dc.DrawStringAnchored(startLabel, x, y+height+15, 0, 0)
		dc.DrawStringAnchored(endLabel, x+width, y+height+15, 1, 0)
	}

	// Draw title
	dc.LoadFontFace("", 14)
	dc.DrawStringAnchored("48-Hour Sentiment Trend", x+width/2, y-10, 0.5, 0)
}
