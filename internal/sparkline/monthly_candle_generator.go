package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/fogleman/gg"
)

// DailyCandle is one day's distribution of hourly net-sentiment readings,
// as stored in daily_sentiment.
type DailyCandle struct {
	Date    time.Time
	Min     float64
	Q1      float64
	Median  float64
	Q3      float64
	Max     float64
	Average float64
	Runs    int
}

// MonthlyCandleGenerator renders a daily candlestick chart for one month.
type MonthlyCandleGenerator struct {
	width, height int
}

// NewMonthlyCandleGenerator returns a generator at the standard 1200x800 canvas.
func NewMonthlyCandleGenerator() *MonthlyCandleGenerator {
	return &MonthlyCandleGenerator{width: 1200, height: 800}
}

// MonthlyCandleMeta carries the header figures that are not derivable from
// the candles alone (the comparison with the previous month).
type MonthlyCandleMeta struct {
	MonthLabel   string  // "August 2026"
	PrevMonthAvg float64 // previous month's average; NaN when unknown
	PrevLabel    string  // "July"
}

// GenerateMonthlyCandleChart draws one candle per day: a whisker spanning the
// day's hourly range, a box for the middle half (Q1..Q3) and a tick at the
// median. Candles are coloured by the polarity of the day's median.
func (g *MonthlyCandleGenerator) GenerateMonthlyCandleChart(days []DailyCandle, meta MonthlyCandleMeta) ([]byte, error) {
	if len(days) == 0 {
		return nil, fmt.Errorf("no daily candles provided")
	}

	W, H := float64(g.width), float64(g.height)
	s := W / 1200.0

	dc := gg.NewContext(g.width, g.height)
	dc.SetColor(themeSurface)
	dc.Clear()

	// Range over the whiskers so every candle fits.
	extents := make([]float64, 0, len(days)*2)
	sum := 0.0
	best, worst, widest := 0, 0, 0
	for i, d := range days {
		extents = append(extents, d.Min, d.Max)
		sum += d.Average
		if d.Average > days[best].Average {
			best = i
		}
		if d.Average < days[worst].Average {
			worst = i
		}
		if d.Max-d.Min > days[widest].Max-days[widest].Min {
			widest = i
		}
	}
	avg := sum / float64(len(days))

	first, last := days[0].Date, days[len(days)-1].Date
	plot := plotArea{
		x:   92 * s,
		y:   186 * s,
		w:   W - 92*s - 36*s,
		h:   H - 186*s - 84*s,
		t0:  first.Truncate(24 * time.Hour),
		t1:  last.Truncate(24 * time.Hour).Add(24 * time.Hour),
		rng: fitRange(extents, 0.75, rangePadFraction),
	}

	heroSub := fmt.Sprintf("%d days of readings", len(days))
	if !math.IsNaN(meta.PrevMonthAvg) && meta.PrevLabel != "" {
		heroSub = deltaText(avg-meta.PrevMonthAvg, meta.PrevLabel)
	}
	spec := seriesChartSpec{
		Title:     "Bluesky sentiment, " + meta.MonthLabel,
		Subtitle:  fmt.Sprintf("%s – %s · daily spread of hourly net sentiment readings · UTC", first.Format("2 Jan"), last.Format("2 Jan")),
		HeroLabel: "Month average",
		HeroValue: pctText(avg),
		HeroSub:   heroSub,
		Tiles: []statTile{
			{Label: "Best day", Value: pctText(days[best].Average), Sub: days[best].Date.Format("Mon 2 Jan")},
			{Label: "Worst day", Value: pctText(days[worst].Average), Sub: days[worst].Date.Format("Mon 2 Jan")},
			{Label: "Widest swing", Value: fmt.Sprintf("%.1f pts", days[widest].Max-days[widest].Min), Sub: days[widest].Date.Format("Mon 2 Jan")},
		},
		Average:   avg,
		AvgLegend: "month avg",
		Brand:     "@hourstats.bsky.social",
	}

	drawHeader(dc, spec, s, W)
	drawNeutralBand(dc, plot, s, plot.yAt(avg)-12*s)
	drawYGrid(dc, plot, s)
	drawWeekAxis(dc, plot, s)
	drawCandles(dc, plot, days, s)
	drawAverage(dc, plot, spec, s)
	drawCandleExtremes(dc, plot, days, best, worst, s)
	drawCandleFooter(dc, spec, s, W, H)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// candleCenter returns the x pixel at the middle of a day's slot.
func candleCenter(p plotArea, d time.Time) float64 {
	return p.xAt(d.Truncate(24 * time.Hour).Add(12 * time.Hour))
}

func drawCandles(dc *gg.Context, p plotArea, days []DailyCandle, s float64) {
	slot := p.w / float64(len(days))
	body := slot * 0.62
	if body > 26*s {
		body = 26 * s
	}
	if body < 4*s {
		body = 4 * s
	}

	dc.DrawRectangle(p.x-2*s, p.y-2*s, p.w+4*s, p.h+4*s)
	dc.Clip()
	for _, d := range days {
		strong, soft := polarityColor(d.Median)
		x := candleCenter(p, d.Date)

		// Whisker: full hourly range.
		dc.SetColor(strong)
		dc.SetLineWidth(1.5 * s)
		dc.DrawLine(x, p.yAt(d.Max), x, p.yAt(d.Min))
		dc.Stroke()

		// Box: middle half of the day's readings.
		top, bot := p.yAt(d.Q3), p.yAt(d.Q1)
		if bot-top < 2*s {
			bot = top + 2*s
		}
		dc.SetColor(soft)
		dc.DrawRectangle(x-body/2, top, body, bot-top)
		dc.Fill()
		dc.SetColor(strong)
		dc.SetLineWidth(1.2 * s)
		dc.DrawRectangle(x-body/2, top, body, bot-top)
		dc.Stroke()

		// Median tick.
		y := p.yAt(d.Median)
		dc.SetLineWidth(2.5 * s)
		dc.DrawLine(x-body/2, y, x+body/2, y)
		dc.Stroke()
	}
	dc.ResetClip()
}

// drawCandleExtremes labels the best and worst days above and below their whiskers.
func drawCandleExtremes(dc *gg.Context, p plotArea, days []DailyCandle, best, worst int, s float64) {
	if best == worst {
		return
	}
	setFont(dc, 16*s, false)
	mark := func(idx int, prefix string, above bool) {
		d := days[idx]
		x := candleCenter(p, d.Date)
		label := fmt.Sprintf("%s %s", prefix, pctText(d.Average))
		lw, _ := dc.MeasureString(label)
		lx := math.Min(math.Max(x, p.x+lw/2+4*s), p.right()-lw/2-4*s)
		if above {
			drawHaloText(dc, label, lx, p.yAt(d.Max)-10*s, 0.5, 0, themeInkSecondary, s)
		} else {
			drawHaloText(dc, label, lx, p.yAt(d.Min)+10*s, 0.5, 1, themeInkSecondary, s)
		}
	}
	mark(best, "Best", true)
	mark(worst, "Worst", false)
}

// drawWeekAxis draws the baseline, a divider at each Monday and a label
// centred in each week. Narrow weeks fall back to the short date form.
func drawWeekAxis(dc *gg.Context, p plotArea, s float64) {
	labelY := p.bottom() + 24*s

	dc.SetColor(themeBaseline)
	dc.SetLineWidth(1.5 * s)
	dc.DrawLine(p.x, p.bottom(), p.right(), p.bottom())
	dc.Stroke()

	var bounds []time.Time
	for t := p.t0.Truncate(24 * time.Hour); t.Before(p.t1); t = t.Add(24 * time.Hour) {
		if t.Weekday() == time.Monday && t.After(p.t0) {
			bounds = append(bounds, t)
		}
	}

	dc.SetColor(themeGrid)
	dc.SetLineWidth(1 * s)
	for _, t := range bounds {
		x := p.xAt(t)
		dc.DrawLine(x, p.y, x, p.bottom())
		dc.Stroke()
	}

	// Day ticks along the baseline.
	dc.SetColor(themeBaseline)
	for t := p.t0.Truncate(24 * time.Hour); t.Before(p.t1); t = t.Add(24 * time.Hour) {
		if t.Before(p.t0) {
			continue
		}
		x := p.xAt(t)
		dc.DrawLine(x, p.bottom(), x, p.bottom()+5*s)
		dc.Stroke()
	}

	setFont(dc, 17*s, false)
	dc.SetColor(themeInkSecondary)
	edges := append(append([]time.Time{p.t0}, bounds...), p.t1)
	for i := 0; i+1 < len(edges); i++ {
		a, b := edges[i], edges[i+1]
		xa, xb := p.xAt(a), p.xAt(b)
		span := xb - xa
		candidates := []string{"w/c " + a.Format("Mon 2 Jan"), a.Format("Mon 2 Jan"), a.Format("2 Jan")}
		for _, label := range candidates {
			lw, _ := dc.MeasureString(label)
			if lw <= span-8*s {
				dc.DrawStringAnchored(label, (xa+xb)/2, labelY, 0.5, 0)
				break
			}
		}
	}
}

func drawCandleFooter(dc *gg.Context, spec seriesChartSpec, s, W, H float64) {
	y := H - 26*s
	x := 36 * s
	setFont(dc, 15*s, false)

	// Candle glyph.
	strong, soft := themePositive, themePositiveSoft
	dc.SetColor(strong)
	dc.SetLineWidth(1.5 * s)
	dc.DrawLine(x+8*s, y-16*s, x+8*s, y+4*s)
	dc.Stroke()
	dc.SetColor(soft)
	dc.DrawRectangle(x+2*s, y-11*s, 12*s, 11*s)
	dc.Fill()
	dc.SetColor(strong)
	dc.SetLineWidth(1.2 * s)
	dc.DrawRectangle(x+2*s, y-11*s, 12*s, 11*s)
	dc.Stroke()
	dc.SetLineWidth(2.5 * s)
	dc.DrawLine(x+2*s, y-6*s, x+14*s, y-6*s)
	dc.Stroke()
	x += 26 * s
	dc.SetColor(themeInkMuted)
	label := "one day: whisker = hourly range, box = middle half, tick = median"
	dc.DrawStringAnchored(label, x, y, 0, 0)
	lw, _ := dc.MeasureString(label)
	x += lw + 26*s

	dc.SetColor(themeAverage)
	dc.SetLineWidth(1.5 * s)
	dc.SetDash(5*s, 4*s)
	dc.DrawLine(x, y-5*s, x+26*s, y-5*s)
	dc.Stroke()
	dc.SetDash()
	x += 34 * s
	dc.SetColor(themeInkMuted)
	dc.DrawStringAnchored(spec.AvgLegend, x, y, 0, 0)

	if spec.Brand != "" {
		dc.SetColor(color.RGBA(themeInkMuted))
		dc.DrawStringAnchored(spec.Brand, W-36*s, y, 1, 0)
	}
}
