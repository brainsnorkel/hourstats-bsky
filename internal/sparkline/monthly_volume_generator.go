package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/fogleman/gg"
)

// DailyVolumePoint is one day's post counts for the monthly volume chart.
type DailyVolumePoint struct {
	Date       time.Time
	ENPosts    int // English posts analysed
	TotalPosts int // all firehose posts (0 when not tracked)
	// Languages is the firehose split by primary language subtag ("en",
	// "pt", "und" ...). When every day has one the chart stacks the top
	// languages instead of drawing the firehose as a single line.
	Languages map[string]int
}

// MonthlyVolumeMeta carries header figures not derivable from the points.
type MonthlyVolumeMeta struct {
	MonthLabel  string // "August 2026"
	PrevMonthEN int    // previous month's English total; 0 when unknown
	PrevLabel   string
}

// MonthlyVolumeGenerator renders a daily English post volume line for one month.
type MonthlyVolumeGenerator struct {
	width, height int
}

// NewMonthlyVolumeGenerator returns a generator at the standard 1200x800 canvas.
func NewMonthlyVolumeGenerator() *MonthlyVolumeGenerator {
	return &MonthlyVolumeGenerator{width: 1200, height: 800}
}

// hasLanguagesEveryDay reports whether the stacked language view can be
// drawn: a month with even one untracked day falls back to the line view
// rather than showing a false dip.
func hasLanguagesEveryDay(days []DailyVolumePoint) bool {
	for _, d := range days {
		if len(d.Languages) == 0 {
			return false
		}
	}
	return len(days) > 1
}

// languageDayTotal is the firehose total for a day from its language split.
func languageDayTotal(d DailyVolumePoint) int {
	n := 0
	for _, v := range d.Languages {
		n += v
	}
	return n
}

// GenerateMonthlyVolumeChart draws English posts per day as a bold line with
// day markers. Behind it sits either the full firehose as a soft line, or,
// when every day carries a language split, a stacked area of English plus
// the largest languages with the remainder folded into "other".
func (g *MonthlyVolumeGenerator) GenerateMonthlyVolumeChart(days []DailyVolumePoint, meta MonthlyVolumeMeta) ([]byte, error) {
	if len(days) == 0 {
		return nil, fmt.Errorf("no daily volume points provided")
	}

	W, H := float64(g.width), float64(g.height)
	s := W / 1200.0

	dc := gg.NewContext(g.width, g.height)
	dc.SetColor(themeSurface)
	dc.Clear()

	stacked := hasLanguagesEveryDay(days)
	var series []LanguageSeries
	if stacked {
		series = LanguageBreakdown(days)
		stacked = len(series) > 0
	}

	enTotal, allTotal, maxV := 0, 0, 0.0
	busiest, quietest := 0, 0
	hasTotal := false
	for i, d := range days {
		enTotal += d.ENPosts
		total := d.TotalPosts
		if stacked {
			total = languageDayTotal(d)
		}
		allTotal += total
		if total > 0 {
			hasTotal = true
		}
		maxV = math.Max(maxV, float64(max(d.ENPosts, total)))
		if d.ENPosts > days[busiest].ENPosts {
			busiest = i
		}
		if d.ENPosts < days[quietest].ENPosts {
			quietest = i
		}
	}

	// The stacked view needs a two-row legend, so its plot ends higher.
	footer := 84 * s
	if stacked {
		footer = 112 * s
	}
	first, last := days[0].Date, days[len(days)-1].Date
	plot := plotArea{
		x:   92 * s,
		y:   186 * s,
		w:   W - 92*s - 36*s,
		h:   H - 186*s - footer,
		t0:  first.Truncate(24 * time.Hour),
		t1:  last.Truncate(24 * time.Hour).Add(24 * time.Hour),
		rng: countRange(maxV),
	}

	heroSub := fmt.Sprintf("%d days · %s per day", len(days), countText(float64(enTotal)/float64(len(days))))
	if meta.PrevMonthEN > 0 && meta.PrevLabel != "" {
		pct := (float64(enTotal)/float64(meta.PrevMonthEN) - 1) * 100
		heroSub = fmt.Sprintf("%s vs %s", signedPct(pct), meta.PrevLabel)
	}
	tiles := []statTile{
		{Label: "Per day", Value: countText(float64(enTotal) / float64(len(days)))},
		{Label: "Busiest", Value: countText(float64(days[busiest].ENPosts)), Sub: days[busiest].Date.Format("Mon 2 Jan")},
		{Label: "Quietest", Value: countText(float64(days[quietest].ENPosts)), Sub: days[quietest].Date.Format("Mon 2 Jan")},
	}
	if hasTotal && allTotal > 0 {
		// In the stacked view the legend already shows English as a share
		// of the firehose; this tile is the analysed subset, so say so.
		label := "English share"
		if stacked {
			label = "Analysed share"
		}
		tiles = append(tiles, statTile{Label: label, Value: fmt.Sprintf("%.0f%%", float64(enTotal)/float64(allTotal)*100), Sub: "of " + countText(float64(allTotal))})
	}
	subtitle := fmt.Sprintf("%s – %s · English posts analysed per day · UTC", first.Format("2 Jan"), last.Format("2 Jan"))
	if stacked {
		subtitle = fmt.Sprintf("%s – %s · firehose posts per day by language tag, English posts analysed as the line · UTC", first.Format("2 Jan"), last.Format("2 Jan"))
	}
	spec := seriesChartSpec{
		Title:     "Bluesky post volume, " + meta.MonthLabel,
		Subtitle:  subtitle,
		HeroLabel: "English posts",
		HeroValue: countText(float64(enTotal)),
		HeroSub:   heroSub,
		Tiles:     tiles,
		Brand:     "@hourstats.bsky.social",
	}

	drawHeader(dc, spec, s, W)
	drawCountGrid(dc, plot, s)
	drawWeekAxis(dc, plot, s)
	if stacked {
		drawLanguageStack(dc, plot, days, series, s)
		drawAnalysedLine(dc, plot, days, s)
		drawVolumeExtremes(dc, plot, days, busiest, quietest, themeInkPrimary, s)
		drawLanguageFooter(dc, spec, series, allTotal, s, W, H)
	} else {
		drawVolumeLines(dc, plot, days, hasTotal, s)
		drawVolumeExtremes(dc, plot, days, busiest, quietest, themePositive, s)
		drawVolumeFooter(dc, spec, hasTotal, s, W, H)
	}

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// countRange is a zero-based range with five or six nice ticks.
func countRange(maxV float64) chartRange {
	if maxV <= 0 {
		return chartRange{Min: 0, Max: 1, Tick: 1}
	}
	tick := niceNumber(maxV*1.08/5, true)
	return chartRange{Min: 0, Max: math.Ceil(maxV*1.08/tick) * tick, Tick: tick}
}

// countText formats a count compactly: 1.9M, 480k, 950.
func countText(v float64) string {
	switch {
	case v >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.0fk", v/1e3)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func signedPct(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.1f%%", v)
	}
	return fmt.Sprintf("%.1f%%", v)
}

func drawCountGrid(dc *gg.Context, p plotArea, s float64) {
	setFont(dc, 17*s, false)
	for v := p.rng.Min; v <= p.rng.Max+1e-9; v += p.rng.Tick {
		y := p.yAt(v)
		if v == 0 {
			dc.SetColor(themeBaseline)
			dc.SetLineWidth(1.5 * s)
		} else {
			dc.SetColor(themeGrid)
			dc.SetLineWidth(1 * s)
		}
		dc.DrawLine(p.x, y, p.right(), y)
		dc.Stroke()
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(countText(v), p.x-12*s, y, 1, 0.35)
	}
}

func drawVolumeLines(dc *gg.Context, p plotArea, days []DailyVolumePoint, hasTotal bool, s float64) {
	dc.DrawRectangle(p.x-2*s, p.y-2*s, p.w+4*s, p.h+4*s)
	dc.Clip()
	dc.SetLineCap(gg.LineCapRound)
	dc.SetLineJoin(gg.LineJoinRound)

	if hasTotal {
		dc.SetColor(themePositiveSoft)
		dc.SetLineWidth(2 * s)
		for i, d := range days {
			x, y := candleCenter(p, d.Date), p.yAt(float64(d.TotalPosts))
			if i == 0 {
				dc.MoveTo(x, y)
			} else {
				dc.LineTo(x, y)
			}
		}
		dc.Stroke()
	}

	dc.SetColor(themePositive)
	dc.SetLineWidth(3.5 * s)
	for i, d := range days {
		x, y := candleCenter(p, d.Date), p.yAt(float64(d.ENPosts))
		if i == 0 {
			dc.MoveTo(x, y)
		} else {
			dc.LineTo(x, y)
		}
	}
	dc.Stroke()
	for _, d := range days {
		drawDot(dc, candleCenter(p, d.Date), p.yAt(float64(d.ENPosts)), 3.5*s, themePositive, s)
	}
	dc.ResetClip()
}

// drawLanguageStack fills one band per language series, bottom to top in
// series order, with a 2px surface gap between bands so adjacent fills never
// touch.
func drawLanguageStack(dc *gg.Context, p plotArea, days []DailyVolumePoint, series []LanguageSeries, s float64) {
	if len(days) < 2 {
		return
	}
	shown := shownCodes(series)
	dc.DrawRectangle(p.x-2*s, p.y-2*s, p.w+4*s, p.h+4*s)
	dc.Clip()

	lower := make([]float64, len(days))
	upper := make([]float64, len(days))
	for _, sr := range series {
		for i, d := range days {
			upper[i] = lower[i] + float64(languageValue(d, sr, shown))
		}
		// Band polygon: along the upper edge, back along the lower edge.
		for i, d := range days {
			x, y := candleCenter(p, d.Date), p.yAt(upper[i])
			if i == 0 {
				dc.MoveTo(x, y)
			} else {
				dc.LineTo(x, y)
			}
		}
		for i := len(days) - 1; i >= 0; i-- {
			dc.LineTo(candleCenter(p, days[i].Date), p.yAt(lower[i]))
		}
		dc.ClosePath()
		dc.SetColor(sr.Color)
		dc.Fill()

		// Surface-coloured seam along the band's upper edge; the next band
		// paints over its top half, leaving a hairline of surface between
		// the two fills.
		dc.SetColor(themeSurface)
		dc.SetLineWidth(2 * s)
		for i, d := range days {
			x, y := candleCenter(p, d.Date), p.yAt(upper[i])
			if i == 0 {
				dc.MoveTo(x, y)
			} else {
				dc.LineTo(x, y)
			}
		}
		dc.Stroke()
		copy(lower, upper)
	}
	dc.ResetClip()
}

// drawAnalysedLine draws the English-posts-analysed series in ink with a
// surface halo so it reads over any band colour.
func drawAnalysedLine(dc *gg.Context, p plotArea, days []DailyVolumePoint, s float64) {
	dc.DrawRectangle(p.x-2*s, p.y-2*s, p.w+4*s, p.h+4*s)
	dc.Clip()
	dc.SetLineCap(gg.LineCapRound)
	dc.SetLineJoin(gg.LineJoinRound)
	for pass, c := range []color.RGBA{themeSurface, themeInkPrimary} {
		width := 3 * s
		if pass == 0 {
			width = 7 * s
		}
		dc.SetColor(c)
		dc.SetLineWidth(width)
		for i, d := range days {
			x, y := candleCenter(p, d.Date), p.yAt(float64(d.ENPosts))
			if i == 0 {
				dc.MoveTo(x, y)
			} else {
				dc.LineTo(x, y)
			}
		}
		dc.Stroke()
	}
	for _, d := range days {
		drawDot(dc, candleCenter(p, d.Date), p.yAt(float64(d.ENPosts)), 3.5*s, themeInkPrimary, s)
	}
	dc.ResetClip()
}

// drawLanguageFooter lays out the language legend (filled swatch, name and
// share of the month) plus the analysed-line key, wrapping onto a second row
// when the entries do not fit.
func drawLanguageFooter(dc *gg.Context, spec seriesChartSpec, series []LanguageSeries, allTotal int, s, W, H float64) {
	setFont(dc, 15*s, false)
	left := 36 * s
	// The brand sits on the last row only, so only that row gives up width.
	rowRight := []float64{W - 36*s, W - 36*s}
	if spec.Brand != "" {
		bw, _ := dc.MeasureString(spec.Brand)
		rowRight[1] -= bw + 30*s
	}
	rowY := []float64{H - 50*s, H - 26*s}
	row, x := 0, left

	place := func(width float64) (float64, float64, bool) {
		if x+width > rowRight[row] && row < len(rowY)-1 && x > left {
			row++
			x = left
		}
		if x+width > rowRight[row] {
			return 0, 0, false
		}
		px, py := x, rowY[row]
		x += width + 22*s
		return px, py, true
	}
	// The analysed line is the primary series, so its key is placed first
	// and can never be the entry that overflow drops.
	label := "English posts analysed, per day"
	lw, _ := dc.MeasureString(label)
	if px, py, ok := place(26*s + 8*s + lw); ok {
		dc.SetColor(themeInkPrimary)
		dc.SetLineWidth(3 * s)
		dc.DrawLine(px, py-5*s, px+26*s, py-5*s)
		dc.Stroke()
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(label, px+34*s, py, 0, 0)
	}
	for _, sr := range series {
		label := sr.Name
		if allTotal > 0 {
			label = fmt.Sprintf("%s %.0f%%", sr.Name, float64(sr.Total)/float64(allTotal)*100)
		}
		lw, _ := dc.MeasureString(label)
		px, py, ok := place(16*s + 8*s + lw)
		if !ok {
			break
		}
		dc.SetColor(sr.Color)
		dc.DrawRoundedRectangle(px, py-12*s, 16*s, 14*s, 2*s)
		dc.Fill()
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(label, px+24*s, py, 0, 0)
	}
	if spec.Brand != "" {
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(spec.Brand, W-36*s, H-26*s, 1, 0)
	}
}

func drawVolumeExtremes(dc *gg.Context, p plotArea, days []DailyVolumePoint, busiest, quietest int, dot color.RGBA, s float64) {
	if busiest == quietest {
		return
	}
	setFont(dc, 16*s, false)
	mark := func(idx int, prefix string, above bool) {
		d := days[idx]
		x, y := candleCenter(p, d.Date), p.yAt(float64(d.ENPosts))
		drawDot(dc, x, y, 5.5*s, dot, s)
		label := fmt.Sprintf("%s %s", prefix, countText(float64(d.ENPosts)))
		lw, _ := dc.MeasureString(label)
		lx := math.Min(math.Max(x, p.x+lw/2+4*s), p.right()-lw/2-4*s)
		if above {
			drawHaloText(dc, label, lx, y-12*s, 0.5, 0, themeInkSecondary, s)
		} else {
			drawHaloText(dc, label, lx, y+12*s, 0.5, 1, themeInkSecondary, s)
		}
	}
	mark(busiest, "Busiest", true)
	mark(quietest, "Quietest", false)
}

func drawVolumeFooter(dc *gg.Context, spec seriesChartSpec, hasTotal bool, s, W, H float64) {
	y := H - 26*s
	x := 36 * s
	setFont(dc, 15*s, false)

	swatch := func(c interface {
		RGBA() (uint32, uint32, uint32, uint32)
	}, width float64, label string) {
		dc.SetColor(c)
		dc.SetLineWidth(width)
		dc.DrawLine(x, y-5*s, x+26*s, y-5*s)
		dc.Stroke()
		x += 34 * s
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(label, x, y, 0, 0)
		lw, _ := dc.MeasureString(label)
		x += lw + 26*s
	}
	swatch(themePositive, 3.5*s, "English posts analysed, per day")
	if hasTotal {
		swatch(themePositiveSoft, 2*s, "all firehose posts")
	}

	if spec.Brand != "" {
		dc.SetColor(themeInkMuted)
		dc.DrawStringAnchored(spec.Brand, W-36*s, y, 1, 0)
	}
}
