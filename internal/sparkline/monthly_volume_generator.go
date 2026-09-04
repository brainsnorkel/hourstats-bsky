package sparkline

import (
	"bytes"
	"fmt"
	"math"
	"time"

	"github.com/fogleman/gg"
)

// DailyVolumePoint is one day's post counts for the monthly volume chart.
type DailyVolumePoint struct {
	Date       time.Time
	ENPosts    int // English posts analysed
	TotalPosts int // all firehose posts (0 when not tracked)
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

// GenerateMonthlyVolumeChart draws English posts per day as a bold line with
// day markers, and the full firehose as a soft line behind it when tracked.
func (g *MonthlyVolumeGenerator) GenerateMonthlyVolumeChart(days []DailyVolumePoint, meta MonthlyVolumeMeta) ([]byte, error) {
	if len(days) == 0 {
		return nil, fmt.Errorf("no daily volume points provided")
	}

	W, H := float64(g.width), float64(g.height)
	s := W / 1200.0

	dc := gg.NewContext(g.width, g.height)
	dc.SetColor(themeSurface)
	dc.Clear()

	enTotal, allTotal, maxV := 0, 0, 0.0
	busiest, quietest := 0, 0
	hasTotal := false
	for i, d := range days {
		enTotal += d.ENPosts
		allTotal += d.TotalPosts
		if d.TotalPosts > 0 {
			hasTotal = true
		}
		maxV = math.Max(maxV, float64(max(d.ENPosts, d.TotalPosts)))
		if d.ENPosts > days[busiest].ENPosts {
			busiest = i
		}
		if d.ENPosts < days[quietest].ENPosts {
			quietest = i
		}
	}

	first, last := days[0].Date, days[len(days)-1].Date
	plot := plotArea{
		x:   92 * s,
		y:   186 * s,
		w:   W - 92*s - 36*s,
		h:   H - 186*s - 84*s,
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
		tiles = append(tiles, statTile{Label: "English share", Value: fmt.Sprintf("%.0f%%", float64(enTotal)/float64(allTotal)*100), Sub: "of " + countText(float64(allTotal))})
	}
	spec := seriesChartSpec{
		Title:     "Bluesky post volume, " + meta.MonthLabel,
		Subtitle:  fmt.Sprintf("%s – %s · English posts analysed per day · UTC", first.Format("2 Jan"), last.Format("2 Jan")),
		HeroLabel: "English posts",
		HeroValue: countText(float64(enTotal)),
		HeroSub:   heroSub,
		Tiles:     tiles,
		Brand:     "@hourstats.bsky.social",
	}

	drawHeader(dc, spec, s, W)
	drawCountGrid(dc, plot, s)
	drawWeekAxis(dc, plot, s)
	drawVolumeLines(dc, plot, days, hasTotal, s)
	drawVolumeExtremes(dc, plot, days, busiest, quietest, s)
	drawVolumeFooter(dc, spec, hasTotal, s, W, H)

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

func drawVolumeExtremes(dc *gg.Context, p plotArea, days []DailyVolumePoint, busiest, quietest int, s float64) {
	if busiest == quietest {
		return
	}
	setFont(dc, 16*s, false)
	mark := func(idx int, prefix string, above bool) {
		d := days[idx]
		x, y := candleCenter(p, d.Date), p.yAt(float64(d.ENPosts))
		drawDot(dc, x, y, 5.5*s, themePositive, s)
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
