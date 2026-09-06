package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/fogleman/gg"
)

// seriesChart is the shared renderer behind the weekly and yearly sentiment
// charts. Both charts share one layout:
//
//	┌──────────────────────────────────────────────────────────────┐
//	│ Title                       stat · stat · stat      HERO %   │
//	│ subtitle                                            hero sub │
//	├──────────────────────────────────────────────────────────────┤
//	│ y%  ┆ neutral band, hairline grid, soft raw line,            │
//	│     ┆ bold smoothed trend, dashed average, high/low markers  │
//	├──────────────────────────────────────────────────────────────┤
//	│ legend                                   @hourstats.bsky.social│
//	└──────────────────────────────────────────────────────────────┘
//
// The header carries the numbers a reader needs at thumbnail size (the feed
// shows the image at roughly 40% scale), and the plot is auto-ranged to the
// data so the line fills the vertical space instead of hugging the middle.

// seriesPoint is one observation on the chart.
type seriesPoint struct {
	T time.Time
	V float64
}

// statTile is a small label/value/caption block in the header.
type statTile struct {
	Label string
	Value string
	Sub   string
}

// xAxisMode selects how the time axis is divided and labelled.
type xAxisMode int

const (
	xAxisDays   xAxisMode = iota // midnight dividers, weekday labels, noon ticks
	xAxisMonths                  // month dividers, month labels (year on January)
)

// seriesChartSpec is everything the renderer needs.
type seriesChartSpec struct {
	Width, Height int

	Title    string
	Subtitle string

	HeroLabel string
	HeroValue string
	HeroSub   string
	Tiles     []statTile

	Points   []seriesPoint
	Smoothed []float64 // same length as Points; nil disables the trend line
	Average  float64

	XAxis        xAxisMode
	RawLegend    string
	TrendLegend  string
	AvgLegend    string
	LineWidth    float64 // trend line width at 1200px canvas width
	RawAsDots    bool    // draw raw samples as dots instead of a thin line
	MarkExtremes bool
	Brand        string
}

// chartRange is the vertical extent of the plot in data units.
type chartRange struct {
	Min, Max, Tick float64
}

// fitRange chooses a y-range hugging the data: 12% of the spread on each side
// (never less than minPad) and then snapped outward to tick boundaries.
func fitRange(values []float64, minPad float64) chartRange {
	if len(values) == 0 {
		return chartRange{Min: -100, Max: 100, Tick: 50}
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	pad := (hi - lo) * 0.12
	if pad < minPad {
		pad = minPad
	}
	// Aim for five to seven gridlines so the plot reads at thumbnail size
	// without the ticks snapping the range far outside the data.
	span := (hi + pad) - (lo - pad)
	if span <= 0 {
		span = 1
	}
	tick := niceNumber(span/7, true)
	min := math.Floor((lo-pad)/tick) * tick
	max := math.Ceil((hi+pad)/tick) * tick
	if max-min <= 0 {
		max = min + tick
	}
	return chartRange{Min: min, Max: max, Tick: tick}
}

// plotArea maps data to pixels.
type plotArea struct {
	x, y, w, h float64
	t0, t1     time.Time
	rng        chartRange
}

func (p plotArea) xAt(t time.Time) float64 {
	span := p.t1.Sub(p.t0).Seconds()
	if span <= 0 {
		return p.x + p.w/2
	}
	return p.x + t.Sub(p.t0).Seconds()/span*p.w
}

func (p plotArea) yAt(v float64) float64 {
	span := p.rng.Max - p.rng.Min
	if span <= 0 {
		return p.y + p.h/2
	}
	return p.y + p.h - (v-p.rng.Min)/span*p.h
}

func (p plotArea) bottom() float64 { return p.y + p.h }
func (p plotArea) right() float64  { return p.x + p.w }

// renderSeriesChart draws the chart and returns PNG bytes.
func renderSeriesChart(spec seriesChartSpec) ([]byte, error) {
	if len(spec.Points) == 0 {
		return nil, fmt.Errorf("no data points provided")
	}
	if spec.Width <= 0 || spec.Height <= 0 {
		return nil, fmt.Errorf("invalid canvas size %dx%d", spec.Width, spec.Height)
	}

	W, H := float64(spec.Width), float64(spec.Height)
	s := W / 1200.0 // all sizes are designed at 1200px wide and scaled from there

	dc := gg.NewContext(spec.Width, spec.Height)
	dc.SetColor(themeSurface)
	dc.Clear()

	values := make([]float64, len(spec.Points))
	for i, p := range spec.Points {
		values[i] = p.V
	}

	plot := plotArea{
		x:   92 * s,
		y:   186 * s,
		w:   W - 92*s - 36*s,
		h:   H - 186*s - 84*s,
		t0:  spec.Points[0].T,
		t1:  spec.Points[len(spec.Points)-1].T,
		rng: fitRange(values, 0.75),
	}

	drawHeader(dc, spec, s, W)
	drawNeutralBand(dc, plot, s, plot.yAt(spec.Average)-12*s)
	drawYGrid(dc, plot, s)
	drawXAxis(dc, plot, spec.XAxis, s)
	drawSeries(dc, plot, spec, s)
	drawAverage(dc, plot, spec, s)
	if spec.MarkExtremes {
		drawExtremes(dc, plot, spec, s)
	}
	drawLatestMarker(dc, plot, spec, s)
	drawFooter(dc, spec, s, W, H)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

func drawHeader(dc *gg.Context, spec seriesChartSpec, s, W float64) {
	left := 36 * s
	right := W - 36*s

	// Title and subtitle.
	setFont(dc, 34*s, true)
	dc.SetColor(themeInkPrimary)
	dc.DrawStringAnchored(spec.Title, left, 60*s, 0, 0)

	setFont(dc, 19*s, false)
	dc.SetColor(themeInkSecondary)
	dc.DrawStringAnchored(spec.Subtitle, left, 90*s, 0, 0)

	// Hero figure, right-aligned, spanning both header rows.
	heroLeft := right
	if spec.HeroValue != "" {
		setFont(dc, 18*s, false)
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(spec.HeroLabel, right, 44*s, 1, 0)

		setFont(dc, 64*s, true)
		dc.SetColor(themeInkPrimary)
		dc.DrawStringAnchored(spec.HeroValue, right, 110*s, 1, 0)
		hw, _ := dc.MeasureString(spec.HeroValue)
		heroLeft = right - hw

		setFont(dc, 18*s, false)
		dc.SetColor(themeInkSecondary)
		dc.DrawStringAnchored(spec.HeroSub, right, 142*s, 1, 0)
	}

	// Stat tiles, laid out left to right under the subtitle. Tiles that would
	// collide with the hero figure are dropped rather than overlapped.
	x := left
	for _, tile := range spec.Tiles {
		setFont(dc, 30*s, true)
		vw, _ := dc.MeasureString(tile.Value)
		setFont(dc, 16*s, false)
		lw, _ := dc.MeasureString(tile.Label)
		sw, _ := dc.MeasureString(tile.Sub)
		tw := math.Max(vw, math.Max(lw, sw))
		if x+tw > heroLeft-40*s {
			break
		}

		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(tile.Label, x, 124*s, 0, 0)

		setFont(dc, 30*s, true)
		dc.SetColor(themeInkPrimary)
		dc.DrawStringAnchored(tile.Value, x, 154*s, 0, 0)

		if tile.Sub != "" {
			setFont(dc, 16*s, false)
			dc.SetColor(themeInkMuted)
			dc.DrawStringAnchored(tile.Sub, x+vw+10*s, 154*s, 0, 0)
			tw = math.Max(tw, vw+10*s+sw)
		}
		x += tw + 44*s
	}
}

// ---------------------------------------------------------------------------
// Plot furniture
// ---------------------------------------------------------------------------

// drawNeutralBand shades the -10%..+10% zone and names the zones that are
// tall enough to carry a watermark. Labels are centred horizontally and in
// the visible part of their zone, and step away from the average label if the
// two would collide.
func drawNeutralBand(dc *gg.Context, p plotArea, s float64, avgY float64) {
	top := math.Max(p.yAt(10), p.y)
	bot := math.Min(p.yAt(-10), p.bottom())
	if bot > top {
		dc.SetColor(themeNeutralBand)
		dc.DrawRectangle(p.x, top, p.w, bot-top)
		dc.Fill()
	}

	// Zone names are drawn as a watermark: large, faint, horizontally centred, and
	// centred in whatever part of the zone is visible.
	setFont(dc, 30*s, true)
	// color.RGBA is premultiplied, so translucent ink must go through NRGBA.
	dc.SetColor(color.NRGBA{themeInkSecondary.R, themeInkSecondary.G, themeInkSecondary.B, 90})
	label := func(text string, zoneTop, zoneBot float64) {
		if zoneBot-zoneTop < 44*s {
			return
		}
		y := (zoneTop + zoneBot) / 2
		if math.Abs(y-avgY) < 30*s {
			if y < avgY {
				y = avgY - 30*s
			} else {
				y = avgY + 30*s
			}
		}
		dc.DrawStringAnchored(text, p.x+p.w/2, y, 0.5, 0.35)
	}
	label("positive sentiment", p.y, math.Min(p.yAt(10), p.bottom()))
	label("neutral sentiment", math.Max(top, p.y), math.Min(bot, p.bottom()))
	label("negative sentiment", math.Max(p.yAt(-10), p.y), p.bottom())
}

func drawYGrid(dc *gg.Context, p plotArea, s float64) {
	setFont(dc, 17*s, false)
	for v := p.rng.Min; v <= p.rng.Max+1e-9; v += p.rng.Tick {
		y := p.yAt(v)
		if math.Abs(v) < 1e-9 {
			dc.SetColor(themeBaseline)
			dc.SetLineWidth(1.5 * s)
		} else {
			dc.SetColor(themeGrid)
			dc.SetLineWidth(1 * s)
		}
		dc.DrawLine(p.x, y, p.right(), y)
		dc.Stroke()

		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(tickText(v, p.rng.Tick), p.x-12*s, y, 1, 0.35)
	}
}

// drawXAxis draws the time dividers and their labels.
func drawXAxis(dc *gg.Context, p plotArea, mode xAxisMode, s float64) {
	labelY := p.bottom() + 24*s

	// Axis baseline.
	dc.SetColor(themeBaseline)
	dc.SetLineWidth(1.5 * s)
	dc.DrawLine(p.x, p.bottom(), p.right(), p.bottom())
	dc.Stroke()

	var bounds []time.Time // divider instants inside (t0, t1)
	switch mode {
	case xAxisDays:
		for t := p.t0.Truncate(24 * time.Hour).Add(24 * time.Hour); t.Before(p.t1); t = t.Add(24 * time.Hour) {
			bounds = append(bounds, t)
		}
	case xAxisMonths:
		first := time.Date(p.t0.Year(), p.t0.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
		for t := first; t.Before(p.t1); t = t.AddDate(0, 1, 0) {
			bounds = append(bounds, t)
		}
	}

	// Dividers.
	dc.SetColor(themeGrid)
	dc.SetLineWidth(1 * s)
	for _, t := range bounds {
		x := p.xAt(t)
		dc.DrawLine(x, p.y, x, p.bottom())
		dc.Stroke()
	}

	// Noon ticks on the day axis.
	if mode == xAxisDays {
		dc.SetColor(themeBaseline)
		for t := p.t0.Truncate(24 * time.Hour).Add(12 * time.Hour); t.Before(p.t1); t = t.Add(24 * time.Hour) {
			if t.Before(p.t0) {
				continue
			}
			x := p.xAt(t)
			dc.DrawLine(x, p.bottom(), x, p.bottom()+6*s)
			dc.Stroke()
		}
	}

	// Labels centred in each span between dividers; the first candidate that
	// fits the span wins, so a narrow span gets the short form or nothing.
	setFont(dc, 17*s, false)
	dc.SetColor(themeInkSecondary)
	edges := append(append([]time.Time{p.t0}, bounds...), p.t1)
	for i := 0; i+1 < len(edges); i++ {
		a, b := edges[i], edges[i+1]
		xa, xb := p.xAt(a), p.xAt(b)
		span := xb - xa
		var candidates []string
		switch mode {
		case xAxisDays:
			candidates = []string{a.Format("Mon 2 Jan"), a.Format("Mon")}
		case xAxisMonths:
			if a.Month() == time.January || i == 0 {
				candidates = []string{a.Format("Jan 2006"), a.Format("Jan")}
			} else {
				candidates = []string{a.Format("Jan")}
			}
		}
		for _, label := range candidates {
			lw, _ := dc.MeasureString(label)
			if lw <= span-8*s {
				dc.DrawStringAnchored(label, (xa+xb)/2, labelY, 0.5, 0)
				break
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Data marks
// ---------------------------------------------------------------------------

// strokePolyline draws a series as one path per polarity run so the hue flips
// at the zero crossing without breaking the line into per-segment strokes.
func strokePolyline(dc *gg.Context, p plotArea, spec seriesChartSpec, vals []float64, width float64, strong bool) {
	if len(vals) < 2 {
		return
	}
	dc.SetLineWidth(width)
	dc.SetLineCap(gg.LineCapRound)
	dc.SetLineJoin(gg.LineJoinRound)

	colorFor := func(v float64) color.RGBA {
		st, so := polarityColor(v)
		if strong {
			return st
		}
		return so
	}

	runStart := 0
	current := colorFor(vals[0])
	flush := func(end int) {
		dc.SetColor(current)
		dc.MoveTo(p.xAt(spec.Points[runStart].T), p.yAt(vals[runStart]))
		for i := runStart + 1; i <= end; i++ {
			dc.LineTo(p.xAt(spec.Points[i].T), p.yAt(vals[i]))
		}
		dc.Stroke()
	}
	for i := 1; i < len(vals); i++ {
		c := colorFor(vals[i])
		if c != current {
			flush(i) // include the crossing segment in the outgoing colour
			runStart = i
			current = c
		}
	}
	flush(len(vals) - 1)
}

func drawSeries(dc *gg.Context, p plotArea, spec seriesChartSpec, s float64) {
	// gg's Push/Pop keeps the clip mask, so reset it explicitly afterwards.
	dc.DrawRectangle(p.x-2*s, p.y-2*s, p.w+4*s, p.h+4*s)
	dc.Clip()

	raw := make([]float64, len(spec.Points))
	for i, pt := range spec.Points {
		raw[i] = pt.V
	}
	if spec.RawAsDots {
		for i, pt := range spec.Points {
			_, soft := polarityColor(raw[i])
			dc.SetColor(soft)
			dc.DrawCircle(p.xAt(pt.T), p.yAt(raw[i]), 2.5*s)
			dc.Fill()
		}
	} else {
		strokePolyline(dc, p, spec, raw, 2*s, false)
	}

	if len(spec.Smoothed) == len(spec.Points) && len(spec.Points) >= 2 {
		lw := spec.LineWidth
		if lw <= 0 {
			lw = 3.5
		}
		strokePolyline(dc, p, spec, spec.Smoothed, lw*s, true)
	}
	dc.ResetClip()
}

func drawAverage(dc *gg.Context, p plotArea, spec seriesChartSpec, s float64) {
	y := p.yAt(spec.Average)
	if y < p.y || y > p.bottom() {
		return
	}
	dc.SetColor(themeAverage)
	dc.SetLineWidth(1.5 * s)
	dc.SetDash(7*s, 5*s)
	dc.DrawLine(p.x, y, p.right(), y)
	dc.Stroke()
	dc.SetDash()

	label := fmt.Sprintf("%s %.1f%%", spec.AvgLegend, spec.Average)
	setFont(dc, 15*s, false)
	drawHaloText(dc, label, p.right()-6*s, y-6*s, 1, 0, themeInkSecondary, s)
}

// drawExtremes marks the highest and lowest raw observations.
func drawExtremes(dc *gg.Context, p plotArea, spec seriesChartSpec, s float64) {
	if len(spec.Points) < 2 {
		return
	}
	hiIdx, loIdx := 0, 0
	for i, pt := range spec.Points {
		if pt.V > spec.Points[hiIdx].V {
			hiIdx = i
		}
		if pt.V < spec.Points[loIdx].V {
			loIdx = i
		}
	}
	if hiIdx == loIdx {
		return
	}
	last := len(spec.Points) - 1

	setFont(dc, 16*s, false)
	mark := func(idx int, prefix string, above bool) {
		pt := spec.Points[idx]
		x, y := p.xAt(pt.T), p.yAt(pt.V)
		strong, _ := polarityColor(pt.V)
		drawDot(dc, x, y, 5*s, strong, s)
		if idx == last {
			return // the latest marker and hero already carry this value
		}
		label := fmt.Sprintf("%s %.1f%%", prefix, pt.V)
		lw, _ := dc.MeasureString(label)
		lx := math.Min(math.Max(x, p.x+lw/2+4*s), p.right()-lw/2-4*s)
		if above {
			drawHaloText(dc, label, lx, y-12*s, 0.5, 0, themeInkSecondary, s)
		} else {
			drawHaloText(dc, label, lx, y+12*s, 0.5, 1, themeInkSecondary, s)
		}
	}
	mark(hiIdx, "High", true)
	mark(loIdx, "Low", false)
}

func drawLatestMarker(dc *gg.Context, p plotArea, spec seriesChartSpec, s float64) {
	pt := spec.Points[len(spec.Points)-1]
	strong, _ := polarityColor(pt.V)
	drawDot(dc, p.xAt(pt.T), p.yAt(pt.V), 6.5*s, strong, s)
}

// drawDot draws a filled marker with a surface-coloured ring so it separates
// from the lines beneath it.
func drawDot(dc *gg.Context, x, y, r float64, c color.RGBA, s float64) {
	dc.SetColor(themeSurface)
	dc.DrawCircle(x, y, r+2*s)
	dc.Fill()
	dc.SetColor(c)
	dc.DrawCircle(x, y, r)
	dc.Fill()
}

// drawHaloText draws text with a soft surface-coloured halo for legibility
// over lines and grid.
func drawHaloText(dc *gg.Context, text string, x, y, ax, ay float64, ink color.RGBA, s float64) {
	dc.SetColor(color.NRGBA{themeSurface.R, themeSurface.G, themeSurface.B, 230})
	for _, d := range [][2]float64{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {-1, -1}, {1, 1}, {-1, 1}, {1, -1}} {
		dc.DrawStringAnchored(text, x+d[0]*1.5*s, y+d[1]*1.5*s, ax, ay)
	}
	dc.SetColor(ink)
	dc.DrawStringAnchored(text, x, y, ax, ay)
}

// ---------------------------------------------------------------------------
// Footer
// ---------------------------------------------------------------------------

func drawFooter(dc *gg.Context, spec seriesChartSpec, s, W, H float64) {
	y := H - 26*s
	x := 36 * s
	setFont(dc, 15*s, false)

	swatch := func(c color.RGBA, width float64, dashed bool, label string) {
		dc.SetColor(c)
		dc.SetLineWidth(width)
		if dashed {
			dc.SetDash(5*s, 4*s)
		}
		dc.DrawLine(x, y-5*s, x+26*s, y-5*s)
		dc.Stroke()
		dc.SetDash()
		x += 34 * s
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(label, x, y, 0, 0)
		lw, _ := dc.MeasureString(label)
		x += lw + 26*s
	}
	if spec.RawLegend != "" && spec.RawAsDots {
		dc.SetColor(themePositiveSoft)
		for _, dx := range []float64{4, 13, 22} {
			dc.DrawCircle(x+dx*s, y-5*s, 2.5*s)
			dc.Fill()
		}
		x += 34 * s
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(spec.RawLegend, x, y, 0, 0)
		lw, _ := dc.MeasureString(spec.RawLegend)
		x += lw + 26*s
	} else if spec.RawLegend != "" {
		swatch(themePositiveSoft, 2*s, false, spec.RawLegend)
	}
	if spec.TrendLegend != "" && len(spec.Smoothed) == len(spec.Points) {
		swatch(themePositive, 3.5*s, false, spec.TrendLegend)
	}
	if spec.AvgLegend != "" {
		swatch(themeAverage, 1.5*s, true, spec.AvgLegend)
	}

	if spec.Brand != "" {
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(spec.Brand, W-36*s, y, 1, 0)
	}
}
