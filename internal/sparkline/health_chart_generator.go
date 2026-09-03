package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/fogleman/gg"
)

var (
	healthBlue      = color.RGBA{0, 114, 178, 255}
	healthVermillon = color.RGBA{213, 94, 0, 255}
	healthGreen     = color.RGBA{0, 158, 115, 255}
	healthGray      = color.RGBA{108, 117, 125, 255}
	healthBg        = color.RGBA{248, 249, 250, 255}
	healthGrid      = color.RGBA{220, 220, 220, 255}
	healthText      = color.RGBA{33, 37, 41, 255}
	healthLimit     = color.RGBA{220, 53, 69, 180}
)

const (
	healthChartWidth  = 1200
	healthChartHeight = 900
	healthPanels      = 4
	healthPadLeft     = 90
	healthPadRight    = 30
	healthPadTop      = 40
	healthPadBottom   = 50
	healthPanelGap    = 30
)

// GenerateHealthChart renders a multi-panel health chart from time-series snapshots.
// Panels: Memory, I/O Pressure, GC, Goroutines.
func GenerateHealthChart(snapshots []store.StatsSnapshot, memoryLimitMB int) ([]byte, error) {
	if len(snapshots) < 2 {
		return nil, fmt.Errorf("need at least 2 data points, got %d", len(snapshots))
	}

	dc := gg.NewContext(healthChartWidth, healthChartHeight)
	dc.SetColor(healthBg)
	dc.Clear()

	panelHeight := float64(healthChartHeight-healthPadTop-healthPadBottom-healthPanelGap*(healthPanels-1)) / float64(healthPanels)
	drawWidth := float64(healthChartWidth - healthPadLeft - healthPadRight)

	ts := make([]float64, len(snapshots))
	tMin := float64(snapshots[0].SnapshotTime.Unix())
	tMax := float64(snapshots[len(snapshots)-1].SnapshotTime.Unix())
	tRange := tMax - tMin
	if tRange == 0 {
		tRange = 1
	}
	for i, s := range snapshots {
		ts[i] = (float64(s.SnapshotTime.Unix()) - tMin) / tRange
	}

	memLimitBytes := float64(memoryLimitMB) * 1024 * 1024

	// Panel 0: Memory. RSS is what the kernel OOM-killer measures against the
	// VM limit; Go's Sys misses the ~200 MB of modernc-sqlite page cache and
	// mmap that lives outside the Go heap (prod: RSS 588 MB vs Sys 388 MB).
	// Snapshots written before rss_bytes existed store 0, so the series is
	// omitted entirely rather than flat-lining the panel at zero.
	haveRSS := false
	for _, s := range snapshots {
		if s.RSSBytes > 0 {
			haveRSS = true
			break
		}
	}
	p0y := float64(healthPadTop)
	drawPanel(dc, "Memory", p0y, panelHeight, drawWidth, ts, snapshots, memLimitBytes, func(s store.StatsSnapshot) []panelSeries {
		series := make([]panelSeries, 0, 3)
		if haveRSS {
			series = append(series, panelSeries{value: float64(s.RSSBytes), color: healthVermillon, label: "RSS"})
		}
		return append(series,
			panelSeries{value: float64(s.SysBytes), color: healthGray, label: "Sys"},
			panelSeries{value: float64(s.HeapInuseBytes), color: healthBlue, label: "Heap InUse"},
		)
	}, "MB", memLimitBytes)

	// Panel 1: I/O Pressure (WAL size + write channel depth)
	p1y := p0y + panelHeight + float64(healthPanelGap)
	drawPanel(dc, "I/O Pressure", p1y, panelHeight, drawWidth, ts, snapshots, 0, func(s store.StatsSnapshot) []panelSeries {
		return []panelSeries{
			{value: float64(s.WALSizeBytes), color: healthBlue, label: "WAL Size"},
			{value: float64(s.WriteChannelDepth), color: healthVermillon, label: "Write Queue", rightAxis: true},
		}
	}, "MB", 0)

	// Panel 2: GC. Both series are per-interval deltas. GCCPUFraction is
	// deliberately not plotted: it is a cumulative ratio (GC CPU over total
	// CPU since process start), so neither its level nor its difference
	// describes the interval — differencing a ratio of two growing totals is
	// not a rate. GC count carries the same signal per interval and is
	// additive. The absolute fraction is still exposed via the API and CLI.
	p2y := p1y + panelHeight + float64(healthPanelGap)
	drawPanel(dc, "GC", p2y, panelHeight, drawWidth, ts, snapshots, 0, func(s store.StatsSnapshot) []panelSeries {
		return []panelSeries{
			{value: float64(s.GCPauseTotalNs) / 1e6, color: healthBlue, label: "Pause (ms)"},
			{value: float64(s.GCCount), color: healthVermillon, label: "GC Count", rightAxis: true},
		}
	}, "ms", 0)

	// Panel 3: Goroutines + Cycle Duration
	p3y := p2y + panelHeight + float64(healthPanelGap)
	drawPanel(dc, "Runtime", p3y, panelHeight, drawWidth, ts, snapshots, 0, func(s store.StatsSnapshot) []panelSeries {
		return []panelSeries{
			{value: float64(s.GoroutineCount), color: healthBlue, label: "Goroutines"},
			{value: float64(s.CycleDurationMs) / 1000, color: healthVermillon, label: "Cycle (s)", rightAxis: true},
		}
	}, "", 0)

	// Draw time axis labels at bottom
	drawHealthTimeAxis(dc, snapshots, drawWidth, float64(healthChartHeight)-healthPadBottom+15)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("encode health chart PNG: %w", err)
	}
	return buf.Bytes(), nil
}

type panelSeries struct {
	value     float64
	color     color.RGBA
	label     string
	rightAxis bool
}

func drawPanel(dc *gg.Context, title string, py, ph, dw float64, ts []float64, snaps []store.StatsSnapshot, limitLine float64, seriesFn func(store.StatsSnapshot) []panelSeries, unit string, memLimit float64) {
	px := float64(healthPadLeft)

	// Panel background
	dc.SetColor(color.RGBA{255, 255, 255, 255})
	dc.DrawRectangle(px, py, dw, ph)
	dc.Fill()

	// Panel border
	dc.SetColor(healthGrid)
	dc.SetLineWidth(1)
	dc.DrawRectangle(px, py, dw, ph)
	dc.Stroke()

	// Title
	loadFont(dc, 12)
	dc.SetColor(healthText)
	dc.DrawStringAnchored(title, px+5, py-3, 0, 1)

	// Collect all values per series position for scaling
	sampleSeries := seriesFn(snaps[0])
	numSeries := len(sampleSeries)

	leftVals := make([][]float64, 0)
	rightVals := make([][]float64, 0)
	leftColors := make([]color.RGBA, 0)
	rightColors := make([]color.RGBA, 0)
	leftLabels := make([]string, 0)
	rightLabels := make([]string, 0)

	for si := 0; si < numSeries; si++ {
		vals := make([]float64, len(snaps))
		for i, s := range snaps {
			series := seriesFn(s)
			vals[i] = series[si].value
		}
		if sampleSeries[si].rightAxis {
			rightVals = append(rightVals, vals)
			rightColors = append(rightColors, sampleSeries[si].color)
			rightLabels = append(rightLabels, sampleSeries[si].label)
		} else {
			leftVals = append(leftVals, vals)
			leftColors = append(leftColors, sampleSeries[si].color)
			leftLabels = append(leftLabels, sampleSeries[si].label)
		}
	}

	// Calculate Y range for left axis
	leftMin, leftMax := rangeOf(leftVals)
	if memLimit > 0 && memLimit > leftMax {
		leftMax = memLimit * 1.05
	}
	if leftMax == leftMin {
		leftMax = leftMin + 1
	}
	leftPad := (leftMax - leftMin) * 0.1
	leftMin = math.Max(0, leftMin-leftPad)
	leftMax = leftMax + leftPad

	// Draw left axis series
	for si, vals := range leftVals {
		drawSeriesLine(dc, vals, ts, px, py, dw, ph, leftMin, leftMax, leftColors[si])
	}

	// Draw memory limit line if applicable
	if memLimit > 0 {
		yFrac := 1 - (memLimit-leftMin)/(leftMax-leftMin)
		ly := py + yFrac*ph
		dc.SetColor(healthLimit)
		dc.SetLineWidth(1.5)
		dc.SetDash(6, 4)
		dc.DrawLine(px, ly, px+dw, ly)
		dc.Stroke()
		dc.SetDash()

		loadFont(dc, 9)
		dc.SetColor(healthLimit)
		dc.DrawStringAnchored(fmt.Sprintf("Limit %dMB", int(memLimit/(1024*1024))), px+dw-5, ly-3, 1, 1)
	}

	// Draw left axis tick labels
	loadFont(dc, 9)
	dc.SetColor(healthText)
	_, niceMax, tickSpacing := niceRange(leftMin, leftMax)
	_ = niceMax
	for tick := leftMin; tick <= leftMax; tick += tickSpacing {
		yFrac := 1 - (tick-leftMin)/(leftMax-leftMin)
		ly := py + yFrac*ph
		label := formatAxisValue(tick, unit)
		dc.DrawStringAnchored(label, px-5, ly, 1, 0.5)

		dc.SetColor(healthGrid)
		dc.SetLineWidth(0.5)
		dc.DrawLine(px, ly, px+dw, ly)
		dc.Stroke()
		dc.SetColor(healthText)
	}

	// Draw right axis series if present
	if len(rightVals) > 0 {
		rightMin, rightMax := rangeOf(rightVals)
		if rightMax == rightMin {
			rightMax = rightMin + 1
		}
		rightPad := (rightMax - rightMin) * 0.1
		rightMin = math.Max(0, rightMin-rightPad)
		rightMax = rightMax + rightPad

		for si, vals := range rightVals {
			drawSeriesLine(dc, vals, ts, px, py, dw, ph, rightMin, rightMax, rightColors[si])
		}

		// Right axis labels
		_, rNiceMax, rTickSpacing := niceRange(rightMin, rightMax)
		_ = rNiceMax
		for tick := rightMin; tick <= rightMax; tick += rTickSpacing {
			yFrac := 1 - (tick-rightMin)/(rightMax-rightMin)
			ly := py + yFrac*ph
			label := formatAxisValue(tick, "")
			dc.DrawStringAnchored(label, px+dw+5, ly, 0, 0.5)
		}
		_ = rightLabels
	}

	// Draw legend
	loadFont(dc, 9)
	legendX := px + 10
	legendY := py + 12
	for i, lbl := range leftLabels {
		dc.SetColor(leftColors[i])
		dc.DrawRectangle(legendX, legendY-6, 12, 6)
		dc.Fill()
		dc.SetColor(healthText)
		dc.DrawString(lbl, legendX+15, legendY)
		legendX += float64(len(lbl)*6 + 30)
	}
	for i, lbl := range rightLabels {
		dc.SetColor(rightColors[i])
		dc.DrawRectangle(legendX, legendY-6, 12, 6)
		dc.Fill()
		dc.SetColor(healthText)
		dc.DrawString(lbl, legendX+15, legendY)
		legendX += float64(len(lbl)*6 + 30)
	}
}

func drawSeriesLine(dc *gg.Context, vals, ts []float64, px, py, dw, ph, vMin, vMax float64, c color.RGBA) {
	dc.SetColor(c)
	dc.SetLineWidth(1.5)
	vRange := vMax - vMin
	if vRange == 0 {
		vRange = 1
	}
	for i, v := range vals {
		x := px + ts[i]*dw
		yFrac := 1 - (v-vMin)/vRange
		y := py + yFrac*ph
		if i == 0 {
			dc.MoveTo(x, y)
		} else {
			dc.LineTo(x, y)
		}
	}
	dc.Stroke()
}

func drawHealthTimeAxis(dc *gg.Context, snaps []store.StatsSnapshot, dw, yPos float64) {
	if len(snaps) < 2 {
		return
	}
	px := float64(healthPadLeft)
	loadFont(dc, 10)
	dc.SetColor(healthText)

	tMin := snaps[0].SnapshotTime
	tMax := snaps[len(snaps)-1].SnapshotTime
	totalDur := tMax.Sub(tMin)
	if totalDur <= 0 {
		return
	}

	// Choose tick interval based on duration
	var tickInterval time.Duration
	switch {
	case totalDur <= 3*time.Hour:
		tickInterval = 30 * time.Minute
	case totalDur <= 12*time.Hour:
		tickInterval = time.Hour
	case totalDur <= 48*time.Hour:
		tickInterval = 4 * time.Hour
	default:
		tickInterval = 12 * time.Hour
	}

	// Start from the first clean boundary after tMin
	first := tMin.Truncate(tickInterval).Add(tickInterval)
	for t := first; !t.After(tMax); t = t.Add(tickInterval) {
		frac := float64(t.Sub(tMin)) / float64(totalDur)
		x := px + frac*dw
		label := t.UTC().Format("15:04")
		dc.DrawStringAnchored(label, x, yPos, 0.5, 0)
	}
}

func rangeOf(series [][]float64) (min, max float64) {
	min = math.MaxFloat64
	max = -math.MaxFloat64
	for _, vals := range series {
		for _, v := range vals {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}
	if min == math.MaxFloat64 {
		return 0, 1
	}
	return min, max
}

func formatAxisValue(v float64, unit string) string {
	switch unit {
	case "MB":
		mb := v / (1024 * 1024)
		if mb >= 100 {
			return fmt.Sprintf("%.0fMB", mb)
		}
		return fmt.Sprintf("%.1fMB", mb)
	case "ms":
		if v >= 1000 {
			return fmt.Sprintf("%.1fs", v/1000)
		}
		return fmt.Sprintf("%.0fms", v)
	default:
		if v >= 10000 {
			return fmt.Sprintf("%.1fk", v/1000)
		}
		if v == math.Trunc(v) {
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("%.1f", v)
	}
}
