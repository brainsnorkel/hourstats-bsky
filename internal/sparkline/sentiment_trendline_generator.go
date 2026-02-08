package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/fogleman/gg"
)

type SentimentTrendlineConfig struct {
	Width      int
	Height     int
	Padding    int
	Background color.RGBA
	RootLine   color.RGBA
	ReplyLine  color.RGBA
	GridColor  color.RGBA
	TextColor  color.RGBA
}

func DefaultSentimentTrendlineConfig() *SentimentTrendlineConfig {
	return &SentimentTrendlineConfig{
		Width:      1200,
		Height:     800,
		Padding:    80,
		Background: color.RGBA{248, 249, 250, 255},
		RootLine:   color.RGBA{0, 114, 178, 255}, // Blue (Okabe-Ito)
		ReplyLine:  color.RGBA{230, 159, 0, 255}, // Orange (Okabe-Ito, colour-blind safe)
		GridColor:  color.RGBA{200, 200, 200, 255},
		TextColor:  color.RGBA{33, 37, 41, 255},
	}
}

type SentimentTrendlineGenerator struct {
	config *SentimentTrendlineConfig
}

func NewSentimentTrendlineGenerator(config *SentimentTrendlineConfig) *SentimentTrendlineGenerator {
	if config == nil {
		config = DefaultSentimentTrendlineConfig()
	}
	return &SentimentTrendlineGenerator{config: config}
}

func (g *SentimentTrendlineGenerator) GenerateSentimentTrendline(dataPoints []store.SentimentDataPoint) ([]byte, error) {
	if len(dataPoints) < 2 {
		return nil, fmt.Errorf("need at least 2 data points, got %d", len(dataPoints))
	}

	dc := gg.NewContext(g.config.Width, g.config.Height)
	dc.SetColor(g.config.Background)
	dc.Clear()

	leftPadding := g.config.Padding + 50
	rightPadding := g.config.Padding
	topPadding := g.config.Padding
	bottomPadding := g.config.Padding + 20

	drawWidth := float64(g.config.Width - leftPadding - rightPadding)
	drawHeight := float64(g.config.Height - topPadding - bottomPadding)
	drawX := float64(leftPadding)
	drawY := float64(topPadding)

	yMin, yMax := g.calculateYRange(dataPoints)
	yRange := yMax - yMin
	if yRange == 0 {
		yRange = 1
	}

	getRootVal := func(dp store.SentimentDataPoint) float64 { return dp.RootSentimentPct }
	getReplyVal := func(dp store.SentimentDataPoint) float64 { return dp.ReplySentimentPct }

	g.drawGrid(dc, drawX, drawY, drawWidth, drawHeight, yMin, yMax)
	g.drawLine(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yMin, yRange, getRootVal, g.config.RootLine, 3.0)
	g.drawLine(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yMin, yRange, getReplyVal, g.config.ReplyLine, 3.0)
	g.drawGaussianTrend(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yMin, yRange, getRootVal, g.config.RootLine)
	g.drawGaussianTrend(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yMin, yRange, getReplyVal, g.config.ReplyLine)
	g.drawDayMarkers(dc, dataPoints, drawX, drawY, drawWidth, drawHeight)
	g.drawTitle(dc, drawX, drawY, drawWidth)
	g.drawLegend(dc, drawX, drawY, drawWidth)
	g.drawExtremeLabels(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yMin, yRange, getRootVal, g.config.RootLine, "Original")
	g.drawExtremeLabels(dc, dataPoints, drawX, drawY, drawWidth, drawHeight, yMin, yRange, getReplyVal, g.config.ReplyLine, "Reply")
	g.drawBranding(dc, drawX, drawY, drawWidth, drawHeight)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

func (g *SentimentTrendlineGenerator) calculateYRange(dataPoints []store.SentimentDataPoint) (float64, float64) {
	min := dataPoints[0].RootSentimentPct
	max := dataPoints[0].RootSentimentPct

	for _, dp := range dataPoints {
		for _, v := range []float64{dp.RootSentimentPct, dp.ReplySentimentPct} {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}

	padding := (max - min) * 0.15
	if padding < 5.0 {
		padding = 5.0
	}

	niceMin, niceMax, _ := niceRange(min-padding, max+padding)
	return niceMin, niceMax
}

func (g *SentimentTrendlineGenerator) drawGrid(dc *gg.Context, x, y, w, h, yMin, yMax float64) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}

	dc.SetColor(g.config.GridColor)
	dc.SetLineWidth(0.5)

	yRange := yMax - yMin
	gridLines := 5
	for i := 0; i <= gridLines; i++ {
		frac := float64(i) / float64(gridLines)
		yPos := y + h - frac*h
		dc.DrawLine(x, yPos, x+w, yPos)
		dc.Stroke()

		val := yMin + frac*yRange
		dc.SetColor(g.config.TextColor)
		dc.DrawStringAnchored(fmt.Sprintf("%.0f%%", val), x-10, yPos, 1, 0.5)
		dc.SetColor(g.config.GridColor)
	}

	// Draw 0% line if within range
	if yMin <= 0 && yMax >= 0 {
		zeroY := y + h - (-yMin/yRange)*h
		dc.SetColor(color.RGBA{150, 150, 150, 255})
		dc.SetLineWidth(1.0)
		dc.DrawLine(x, zeroY, x+w, zeroY)
		dc.Stroke()
	}
}

func (g *SentimentTrendlineGenerator) drawLine(dc *gg.Context, dataPoints []store.SentimentDataPoint, x, y, w, h, yMin, yRange float64, getValue func(store.SentimentDataPoint) float64, lineColor color.RGBA, lineWidth float64) {
	if len(dataPoints) < 2 {
		return
	}

	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()
	if timeRange == 0 {
		return
	}

	dc.SetColor(lineColor)
	dc.SetLineWidth(lineWidth)

	for i := 0; i < len(dataPoints)-1; i++ {
		x1 := x + (dataPoints[i].Timestamp.Sub(startTime).Seconds()/timeRange)*w
		y1 := y + h - ((getValue(dataPoints[i])-yMin)/yRange)*h
		x2 := x + (dataPoints[i+1].Timestamp.Sub(startTime).Seconds()/timeRange)*w
		y2 := y + h - ((getValue(dataPoints[i+1])-yMin)/yRange)*h

		dc.DrawLine(x1, y1, x2, y2)
		dc.Stroke()

		dc.DrawCircle(x1, y1, 1.5)
		dc.Fill()
	}

	last := dataPoints[len(dataPoints)-1]
	xLast := x + w
	yLast := y + h - ((getValue(last)-yMin)/yRange)*h
	dc.DrawCircle(xLast, yLast, 1.5)
	dc.Fill()
}

func (g *SentimentTrendlineGenerator) drawDayMarkers(dc *gg.Context, dataPoints []store.SentimentDataPoint, x, y, w, h float64) {
	if len(dataPoints) < 2 {
		return
	}

	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}

	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()

	firstMidnight := startTime.Truncate(24 * time.Hour)
	if firstMidnight.Before(startTime) {
		firstMidnight = firstMidnight.Add(24 * time.Hour)
	}

	for current := firstMidnight; !current.After(endTime); current = current.Add(24 * time.Hour) {
		xPos := x + (current.Sub(startTime).Seconds()/timeRange)*w
		if xPos >= x && xPos <= x+w {
			dc.SetColor(g.config.GridColor)
			dc.SetLineWidth(0.5)
			dc.DrawLine(xPos, y, xPos, y+h)
			dc.Stroke()

			dc.SetColor(g.config.TextColor)
			dc.DrawStringAnchored(current.Format("Mon"), xPos, y+h+15, 0.5, 0)
		}
	}
}

func (g *SentimentTrendlineGenerator) drawTitle(dc *gg.Context, x, y, w float64) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 16); err != nil {
		_ = dc.LoadFontFace("", 16)
	}
	dc.SetColor(g.config.TextColor)
	dc.DrawStringAnchored("Original vs Reply Sentiment (UTC)", x+w/2, y-10, 0.5, 0)
}

func (g *SentimentTrendlineGenerator) drawLegend(dc *gg.Context, x, y, w float64) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}

	legendY := y - 40
	legendX := x + w - 10

	dc.SetColor(g.config.RootLine)
	dc.SetLineWidth(3.0)
	dc.DrawLine(legendX-240, legendY, legendX-220, legendY)
	dc.Stroke()
	dc.SetColor(g.config.TextColor)
	dc.DrawStringAnchored("Original Posts", legendX-215, legendY, 0, 0.5)

	dc.SetColor(g.config.ReplyLine)
	dc.SetLineWidth(3.0)
	dc.DrawLine(legendX-100, legendY, legendX-80, legendY)
	dc.Stroke()
	dc.SetColor(g.config.TextColor)
	dc.DrawStringAnchored("Replies", legendX-75, legendY, 0, 0.5)
}

func (g *SentimentTrendlineGenerator) drawGaussianTrend(dc *gg.Context, dataPoints []store.SentimentDataPoint, x, y, w, h, yMin, yRange float64, getValue func(store.SentimentDataPoint) float64, lineColor color.RGBA) {
	if len(dataPoints) < 3 {
		return
	}

	values := make([]float64, len(dataPoints))
	for i, dp := range dataPoints {
		values[i] = getValue(dp)
	}

	smoothed := gaussianSmoothing(values, 4.0)

	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeRange := endTime.Sub(startTime).Seconds()
	if timeRange == 0 {
		return
	}

	dc.SetColor(color.RGBA{255, 255, 255, 200})
	dc.SetLineWidth(3.5)
	dc.SetDash(4, 3)

	for i := 0; i < len(smoothed)-1; i++ {
		x1 := x + (dataPoints[i].Timestamp.Sub(startTime).Seconds()/timeRange)*w
		y1 := y + h - ((smoothed[i]-yMin)/yRange)*h
		x2 := x + (dataPoints[i+1].Timestamp.Sub(startTime).Seconds()/timeRange)*w
		y2 := y + h - ((smoothed[i+1]-yMin)/yRange)*h
		dc.DrawLine(x1, y1, x2, y2)
		dc.Stroke()
	}
	dc.SetDash()

	dc.SetColor(lineColor)
	dc.SetLineWidth(1.5)
	dc.SetDash(4, 3)

	for i := 0; i < len(smoothed)-1; i++ {
		x1 := x + (dataPoints[i].Timestamp.Sub(startTime).Seconds()/timeRange)*w
		y1 := y + h - ((smoothed[i]-yMin)/yRange)*h
		x2 := x + (dataPoints[i+1].Timestamp.Sub(startTime).Seconds()/timeRange)*w
		y2 := y + h - ((smoothed[i+1]-yMin)/yRange)*h
		dc.DrawLine(x1, y1, x2, y2)
		dc.Stroke()
	}
	dc.SetDash()
}

func (g *SentimentTrendlineGenerator) drawExtremeLabels(dc *gg.Context, dataPoints []store.SentimentDataPoint, x, y, w, h, yMin, yRange float64, getValue func(store.SentimentDataPoint) float64, lineColor color.RGBA, seriesName string) {
	if len(dataPoints) == 0 {
		return
	}

	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 11); err != nil {
		_ = dc.LoadFontFace("", 11)
	}

	startTime := dataPoints[0].Timestamp
	endTime := dataPoints[len(dataPoints)-1].Timestamp
	timeSpan := endTime.Sub(startTime).Seconds()
	if timeSpan == 0 {
		return
	}

	var latestIdx, lowIdx, highIdx int
	latestIdx = len(dataPoints) - 1
	lowIdx = 0
	highIdx = 0
	for i, dp := range dataPoints {
		v := getValue(dp)
		if v < getValue(dataPoints[lowIdx]) {
			lowIdx = i
		}
		if v > getValue(dataPoints[highIdx]) {
			highIdx = i
		}
	}

	toXY := func(idx int) (float64, float64) {
		dp := dataPoints[idx]
		px := x + (dp.Timestamp.Sub(startTime).Seconds()/timeSpan)*w
		py := y + h - ((getValue(dp)-yMin)/yRange)*h
		return px, py
	}

	drawLabel := func(idx int, prefix string, above bool) {
		px, py := toXY(idx)
		val := getValue(dataPoints[idx])
		label := fmt.Sprintf("%s: %.1f%%", prefix, val)
		textW, textH := dc.MeasureString(label)
		pad := 3.0

		offsetY := 15.0
		if !above {
			offsetY = -15.0
		}
		labelY := py - offsetY
		anchorY := 0.5
		if above {
			anchorY = 1.0
		} else {
			anchorY = 0.0
		}

		boxX := px - textW/2 - pad
		boxY := labelY - textH*anchorY - pad
		if boxX < x {
			boxX = x
			px = boxX + textW/2 + pad
		}
		if boxX+textW+pad*2 > x+w {
			boxX = x + w - textW - pad*2
			px = boxX + textW/2 + pad
		}

		dc.SetColor(color.RGBA{255, 255, 230, 255})
		dc.DrawRoundedRectangle(boxX, boxY, textW+pad*2, textH+pad*2, 3)
		dc.Fill()
		dc.SetColor(lineColor)
		dc.DrawStringAnchored(label, px, labelY, 0.5, anchorY)
	}

	drawLabel(latestIdx, "Latest", true)

	if lowIdx != latestIdx {
		drawLabel(lowIdx, "Low", false)
	}
	if highIdx != latestIdx && highIdx != lowIdx {
		drawLabel(highIdx, "High", true)
	}
}

func (g *SentimentTrendlineGenerator) drawMultilineLabel(dc *gg.Context, text string, x, y, anchorX, anchorY float64, labelColor color.RGBA) {
	lines := strings.Split(text, "\n")
	lineHeight := 14.0
	pad := 3.0

	totalHeight := float64(len(lines)-1)*lineHeight + lineHeight

	startY := y
	switch anchorY {
	case 0.5:
		startY = y - totalHeight/2 + lineHeight/2
	case 1.0:
		startY = y - totalHeight + lineHeight/2
	default:
		startY = y + lineHeight/2
	}

	var maxW float64
	for _, line := range lines {
		w, _ := dc.MeasureString(line)
		if w > maxW {
			maxW = w
		}
	}

	boxX := x - anchorX*maxW - pad
	boxY := startY - lineHeight/2 - pad
	boxW := maxW + pad*2
	boxH := totalHeight + pad*2

	dc.SetColor(color.RGBA{255, 255, 230, 255})
	dc.DrawRoundedRectangle(boxX, boxY, boxW, boxH, 3)
	dc.Fill()

	dc.SetColor(labelColor)
	for i, line := range lines {
		lineY := startY + float64(i)*lineHeight
		dc.DrawStringAnchored(line, x, lineY, anchorX, 0.5)
	}
}

func (g *SentimentTrendlineGenerator) drawBranding(dc *gg.Context, x, y, w, h float64) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}
	dc.SetColor(color.RGBA{100, 100, 100, 150})
	dc.DrawStringAnchored("@hourstats.bsky.social", x+10, y+h-10, 0, 1)
}
