package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"time"

	"github.com/fogleman/gg"
)

// WeeklyVolume holds per-week post counts for the volume chart.
type WeeklyVolume struct {
	WeekStart  time.Time
	TotalPosts int // all firehose posts (0 if not tracked yet)
	ENPosts    int // English posts analysed
}

// YearlyVolumeConfig holds configuration for the yearly volume chart.
type YearlyVolumeConfig struct {
	Width      int
	Height     int
	Padding    int
	Background color.RGBA
	TotalBar   color.RGBA
	ENBar      color.RGBA
	GridColor  color.RGBA
	TextColor  color.RGBA
}

func DefaultYearlyVolumeConfig() *YearlyVolumeConfig {
	return &YearlyVolumeConfig{
		Width:      1500,
		Height:     1000,
		Padding:    100,
		Background: color.RGBA{248, 249, 250, 255},
		TotalBar:   color.RGBA{173, 216, 230, 140}, // light blue
		ENBar:      color.RGBA{0, 114, 178, 200},   // Okabe-Ito blue
		GridColor:  color.RGBA{200, 200, 200, 255},
		TextColor:  color.RGBA{33, 37, 41, 255},
	}
}

type YearlyVolumeGenerator struct {
	config *YearlyVolumeConfig
}

func NewYearlyVolumeGenerator(config *YearlyVolumeConfig) *YearlyVolumeGenerator {
	if config == nil {
		config = DefaultYearlyVolumeConfig()
	}
	return &YearlyVolumeGenerator{config: config}
}

func (g *YearlyVolumeGenerator) GenerateYearlyVolumeChart(weeks []WeeklyVolume) ([]byte, error) {
	if len(weeks) == 0 {
		return nil, fmt.Errorf("no weekly volume data provided")
	}

	dc := gg.NewContext(g.config.Width, g.config.Height)

	dc.SetColor(g.config.Background)
	dc.Clear()

	leftPadding := g.config.Padding + 70 // room for Y-axis labels
	rightPadding := g.config.Padding
	topPadding := g.config.Padding
	bottomPadding := g.config.Padding + 40 // room for month labels + date labels

	drawWidth := float64(g.config.Width - leftPadding - rightPadding)
	drawHeight := float64(g.config.Height - topPadding - bottomPadding)
	drawX := float64(leftPadding)
	drawY := float64(topPadding)

	hasTotalPosts := false
	for _, w := range weeks {
		if w.TotalPosts > 0 {
			hasTotalPosts = true
			break
		}
	}

	maxCount := 0
	for _, w := range weeks {
		if w.TotalPosts > maxCount {
			maxCount = w.TotalPosts
		}
		if w.ENPosts > maxCount {
			maxCount = w.ENPosts
		}
	}
	if maxCount == 0 {
		maxCount = 1
	}

	// Round max up to a nice number for the axis
	niceMax := niceAxisMax(maxCount)

	g.drawGrid(dc, drawX, drawY, drawWidth, drawHeight, niceMax)
	g.drawBars(dc, weeks, drawX, drawY, drawWidth, drawHeight, niceMax, hasTotalPosts)
	g.drawMonthMarkers(dc, weeks, drawX, drawY, drawWidth, drawHeight)
	g.drawTitle(dc, weeks, drawX, drawY, drawWidth)
	g.drawLegend(dc, drawX, drawY, drawWidth, hasTotalPosts)
	g.drawBranding(dc, drawX, drawY, drawWidth, drawHeight)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

func (g *YearlyVolumeGenerator) drawGrid(dc *gg.Context, x, y, w, h float64, maxCount int) {
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
		label := formatCount(val)
		dc.SetColor(g.config.TextColor)
		dc.DrawStringAnchored(label, x-10, yPos, 1, 0.5)
		dc.SetColor(g.config.GridColor)
	}
}

func (g *YearlyVolumeGenerator) drawBars(dc *gg.Context, weeks []WeeklyVolume, x, y, w, h float64, maxCount int, hasTotalPosts bool) {
	n := len(weeks)
	barSlotWidth := w / float64(n)
	gap := barSlotWidth * 0.15
	if gap < 2 {
		gap = 2
	}

	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 9); err != nil {
		_ = dc.LoadFontFace("", 9)
	}

	for i, wk := range weeks {
		slotX := x + float64(i)*barSlotWidth
		barW := barSlotWidth - gap*2
		barX := slotX + gap

		if hasTotalPosts && wk.TotalPosts > 0 {
			totalBarH := (float64(wk.TotalPosts) / float64(maxCount)) * h
			enBarH := (float64(wk.ENPosts) / float64(maxCount)) * h
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
			if barSlotWidth > 25 {
				dc.SetColor(g.config.TextColor)
				dc.DrawStringAnchored(formatCountShort(wk.ENPosts), labelX, barTopY-4, 0.5, 1)
			}
		} else {
			barH := (float64(wk.ENPosts) / float64(maxCount)) * h
			barY := y + h - barH

			dc.SetColor(g.config.ENBar)
			dc.DrawRectangle(barX, barY, barW, barH)
			dc.Fill()

			labelX := slotX + barSlotWidth/2
			if barSlotWidth > 25 {
				dc.SetColor(g.config.TextColor)
				dc.DrawStringAnchored(formatCountShort(wk.ENPosts), labelX, barY-4, 0.5, 1)
			}
		}
	}
}

func (g *YearlyVolumeGenerator) drawMonthMarkers(dc *gg.Context, weeks []WeeklyVolume, x, y, w, h float64) {
	if len(weeks) == 0 {
		return
	}

	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}

	n := len(weeks)
	barSlotWidth := w / float64(n)
	startTime := weeks[0].WeekStart
	endTime := weeks[n-1].WeekStart
	totalDuration := endTime.Sub(startTime).Seconds()
	if totalDuration <= 0 {
		return
	}

	// find month boundaries
	firstMonth := time.Date(startTime.Year(), startTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	for month := firstMonth; !month.After(endTime.AddDate(0, 1, 0)); month = month.AddDate(0, 1, 0) {
		if month.Before(startTime) {
			continue
		}
		xPos := x + (month.Sub(startTime).Seconds()/totalDuration)*w

		if xPos >= x && xPos <= x+w {
			dc.SetColor(g.config.GridColor)
			dc.SetLineWidth(0.5)
			dc.DrawLine(xPos, y, xPos, y+h)
			dc.Stroke()

			dc.SetColor(g.config.TextColor)
			dc.DrawStringAnchored(month.Format("Jan"), xPos, y+h+15, 0.5, 0)
		}
	}

	// start/end date labels
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 10); err != nil {
		_ = dc.LoadFontFace("", 10)
	}
	dc.SetColor(color.RGBA{80, 80, 80, 255})
	dc.DrawStringAnchored(startTime.Format("2 Jan"), x, y+h+50, 0, 0)
	endLabel := endTime.Add(7 * 24 * time.Hour).Format("2 Jan") // end of last week
	dc.DrawStringAnchored(endLabel, x+w, y+h+50, 1, 0)

	// week-start ticks below bars
	for i, wk := range weeks {
		slotX := x + float64(i)*barSlotWidth + barSlotWidth/2
		_ = wk
		dc.SetColor(color.RGBA{220, 220, 220, 255})
		dc.SetLineWidth(0.3)
		tickH := h * 0.03
		dc.DrawLine(slotX, y+h, slotX, y+h+tickH)
		dc.Stroke()
	}
}

func (g *YearlyVolumeGenerator) drawTitle(dc *gg.Context, weeks []WeeklyVolume, x, y, w float64) {
	titleFontSize := 32.0
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", titleFontSize); err != nil {
		if err2 := dc.LoadFontFace("/System/Library/Fonts/Helvetica.ttc", titleFontSize); err2 != nil {
			_ = dc.LoadFontFace("", titleFontSize)
		}
	}

	dc.SetColor(g.config.TextColor)

	title := "Post Volumes (UTC)"
	if len(weeks) > 0 {
		startDate := weeks[0].WeekStart.Format("2006-01-02")
		endDate := weeks[len(weeks)-1].WeekStart.Add(6 * 24 * time.Hour).Format("2006-01-02")
		title = fmt.Sprintf("Post Volumes (UTC) %s - %s", startDate, endDate)
	}
	dc.DrawStringAnchored(title, x+w/2, y-15, 0.5, 0)
}

func (g *YearlyVolumeGenerator) drawLegend(dc *gg.Context, x, y, w float64, hasTotalPosts bool) {
	legendFontSize := 14.0
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", legendFontSize); err != nil {
		_ = dc.LoadFontFace("", legendFontSize)
	}

	legendY := y - 50
	legendX := x + w - 10

	if hasTotalPosts {
		// total posts swatch
		dc.SetColor(g.config.TotalBar)
		dc.DrawRectangle(legendX-200, legendY-6, 12, 12)
		dc.Fill()
		dc.SetColor(g.config.TextColor)
		dc.DrawStringAnchored("All Posts", legendX-185, legendY, 0, 0.5)

		// EN posts swatch
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

func (g *YearlyVolumeGenerator) drawBranding(dc *gg.Context, x, y, w, h float64) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", 12); err != nil {
		_ = dc.LoadFontFace("", 12)
	}
	dc.SetColor(color.RGBA{100, 100, 100, 150})
	dc.DrawStringAnchored("@hourstats.bsky.social", x+10, y+h-10, 0, 1)
}

// niceAxisMax rounds up to a clean axis maximum.
func niceAxisMax(maxCount int) int {
	if maxCount <= 0 {
		return 1
	}
	n := niceNumber(float64(maxCount)*1.1, false) // 10% headroom
	if int(n) < maxCount {
		return maxCount
	}
	return int(n)
}

func formatCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func formatCountShort(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
