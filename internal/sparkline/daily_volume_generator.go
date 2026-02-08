package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"time"

	"github.com/fogleman/gg"
)

// DailyVolume holds per-day post counts for the 7-day volume chart.
type DailyVolume struct {
	Date       time.Time
	TotalPosts int // all firehose posts (0 if not tracked yet)
	ENPosts    int // English posts analysed
}

// DailyVolumeConfig holds configuration for the daily volume chart.
type DailyVolumeConfig struct {
	Width      int
	Height     int
	Padding    int
	Background color.RGBA
	TotalBar   color.RGBA
	ENBar      color.RGBA
	GridColor  color.RGBA
	TextColor  color.RGBA
}

func DefaultDailyVolumeConfig() *DailyVolumeConfig {
	return &DailyVolumeConfig{
		Width:      1200,
		Height:     800,
		Padding:    80,
		Background: color.RGBA{248, 249, 250, 255},
		TotalBar:   color.RGBA{173, 216, 230, 140},
		ENBar:      color.RGBA{0, 114, 178, 200},
		GridColor:  color.RGBA{200, 200, 200, 255},
		TextColor:  color.RGBA{33, 37, 41, 255},
	}
}

type DailyVolumeGenerator struct {
	config *DailyVolumeConfig
}

func NewDailyVolumeGenerator(config *DailyVolumeConfig) *DailyVolumeGenerator {
	if config == nil {
		config = DefaultDailyVolumeConfig()
	}
	return &DailyVolumeGenerator{config: config}
}

func (g *DailyVolumeGenerator) GenerateDailyVolumeChart(days []DailyVolume) ([]byte, error) {
	if len(days) == 0 {
		return nil, fmt.Errorf("no daily volume data provided")
	}

	dc := gg.NewContext(g.config.Width, g.config.Height)
	dc.SetColor(g.config.Background)
	dc.Clear()

	leftPadding := g.config.Padding + 60
	rightPadding := g.config.Padding
	topPadding := g.config.Padding
	bottomPadding := g.config.Padding + 30

	drawWidth := float64(g.config.Width - leftPadding - rightPadding)
	drawHeight := float64(g.config.Height - topPadding - bottomPadding)
	drawX := float64(leftPadding)
	drawY := float64(topPadding)

	hasTotalPosts := false
	for _, d := range days {
		if d.TotalPosts > 0 {
			hasTotalPosts = true
			break
		}
	}

	maxCount := 0
	for _, d := range days {
		if d.TotalPosts > maxCount {
			maxCount = d.TotalPosts
		}
		if d.ENPosts > maxCount {
			maxCount = d.ENPosts
		}
	}
	if maxCount == 0 {
		maxCount = 1
	}
	niceMax := niceAxisMax(maxCount)

	g.drawGrid(dc, drawX, drawY, drawWidth, drawHeight, niceMax)
	g.drawBars(dc, days, drawX, drawY, drawWidth, drawHeight, niceMax, hasTotalPosts)
	g.drawDayLabels(dc, days, drawX, drawY, drawWidth, drawHeight)
	g.drawTitle(dc, days, drawX, drawY, drawWidth)
	g.drawLegend(dc, drawX, drawY, drawWidth, hasTotalPosts)
	g.drawBranding(dc, drawX, drawY, drawWidth, drawHeight)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

func (g *DailyVolumeGenerator) drawGrid(dc *gg.Context, x, y, w, h float64, maxCount int) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}

	dc.SetColor(g.config.GridColor)
	dc.SetLineWidth(0.5)

	gridLines := 5
	for i := 0; i <= gridLines; i++ {
		yPos := y + h - (float64(i)/float64(gridLines))*h
		dc.DrawLine(x, yPos, x+w, yPos)
		dc.Stroke()

		val := int(float64(i) / float64(gridLines) * float64(maxCount))
		dc.SetColor(g.config.TextColor)
		dc.DrawStringAnchored(formatCount(val), x-10, yPos, 1, 0.5)
		dc.SetColor(g.config.GridColor)
	}
}

func (g *DailyVolumeGenerator) drawBars(dc *gg.Context, days []DailyVolume, x, y, w, h float64, maxCount int, hasTotalPosts bool) {
	n := len(days)
	barSlotWidth := w / float64(n)
	gap := barSlotWidth * 0.12
	if gap < 4 {
		gap = 4
	}

	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}

	for i, day := range days {
		slotX := x + float64(i)*barSlotWidth
		barW := barSlotWidth - gap*2
		barX := slotX + gap

		if hasTotalPosts && day.TotalPosts > 0 {
			totalBarH := (float64(day.TotalPosts) / float64(maxCount)) * h
			enBarH := (float64(day.ENPosts) / float64(maxCount)) * h
			otherBarH := totalBarH - enBarH
			if otherBarH < 0 {
				otherBarH = 0
			}

			dc.SetColor(g.config.TotalBar)
			dc.DrawRectangle(barX, y+h-totalBarH, barW, otherBarH)
			dc.Fill()

			dc.SetColor(g.config.ENBar)
			dc.DrawRectangle(barX, y+h-enBarH, barW, enBarH)
			dc.Fill()

			barTopY := y + h - totalBarH
			labelX := slotX + barSlotWidth/2
			dc.SetColor(g.config.TextColor)
			dc.DrawStringAnchored(formatCountShort(day.ENPosts), labelX, barTopY-16, 0.5, 1)
		} else {
			barH := (float64(day.ENPosts) / float64(maxCount)) * h
			barY := y + h - barH

			dc.SetColor(g.config.ENBar)
			dc.DrawRectangle(barX, barY, barW, barH)
			dc.Fill()

			labelX := slotX + barSlotWidth/2
			dc.SetColor(g.config.TextColor)
			dc.DrawStringAnchored(formatCountShort(day.ENPosts), labelX, barY-16, 0.5, 1)
		}
	}
}

func (g *DailyVolumeGenerator) drawDayLabels(dc *gg.Context, days []DailyVolume, x, y, w, h float64) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}

	n := len(days)
	barSlotWidth := w / float64(n)

	for i, day := range days {
		labelX := x + float64(i)*barSlotWidth + barSlotWidth/2
		dc.SetColor(g.config.TextColor)
		dc.DrawStringAnchored(day.Date.Format("Mon"), labelX, y+h+15, 0.5, 0)
		dc.SetColor(color.RGBA{120, 120, 120, 255})
		dc.DrawStringAnchored(day.Date.Format("Jan 2"), labelX, y+h+30, 0.5, 0)
	}
}

func (g *DailyVolumeGenerator) drawTitle(dc *gg.Context, days []DailyVolume, x, y, w float64) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 16); err != nil {
		_ = dc.LoadFontFace("", 16)
	}

	dc.SetColor(g.config.TextColor)
	dc.DrawStringAnchored("Post Volumes (UTC)", x+w/2, y-10, 0.5, 0)
}

func (g *DailyVolumeGenerator) drawLegend(dc *gg.Context, x, y, w float64, hasTotalPosts bool) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}

	legendY := y - 40
	legendX := x + w - 10

	if hasTotalPosts {
		dc.SetColor(g.config.TotalBar)
		dc.DrawRectangle(legendX-200, legendY-6, 12, 12)
		dc.Fill()
		dc.SetColor(g.config.TextColor)
		dc.DrawStringAnchored("All Posts", legendX-185, legendY, 0, 0.5)

		dc.SetColor(g.config.ENBar)
		dc.DrawRectangle(legendX-90, legendY-6, 12, 12)
		dc.Fill()
		dc.SetColor(g.config.TextColor)
		dc.DrawStringAnchored("Language=EN Posts", legendX-75, legendY, 0, 0.5)
	} else {
		dc.SetColor(g.config.ENBar)
		dc.DrawRectangle(legendX-145, legendY-6, 12, 12)
		dc.Fill()
		dc.SetColor(g.config.TextColor)
		dc.DrawStringAnchored("Language=EN Posts", legendX-130, legendY, 0, 0.5)
	}
}

func (g *DailyVolumeGenerator) drawBranding(dc *gg.Context, x, y, w, h float64) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}
	dc.SetColor(color.RGBA{100, 100, 100, 150})
	dc.DrawStringAnchored("@hourstats.bsky.social", x+10, y+h-10, 0, 1)
}
