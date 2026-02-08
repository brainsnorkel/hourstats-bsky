package sparkline

import (
	"bytes"
	"fmt"
	"image/color"
	"sort"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/fogleman/gg"
)

var trendingColors = []color.RGBA{
	{0, 114, 178, 255},   // Blue
	{213, 94, 0, 255},    // Vermillion
	{0, 158, 115, 255},   // Bluish Green
	{230, 159, 0, 255},   // Orange
	{204, 121, 167, 255}, // Reddish Purple
	{86, 180, 233, 255},  // Sky Blue
	{240, 228, 66, 255},  // Yellow
}

func GenerateTrendingChart(snapshots []store.TopicSnapshotRow) ([]byte, error) {
	if len(snapshots) == 0 {
		return nil, nil
	}

	times := distinctTimes(snapshots)
	if len(times) < 2 {
		return nil, nil
	}

	const (
		width         = 1200
		height        = 800
		leftPad       = 130
		rightPad      = 180
		topPad        = 80
		bottomPad     = 80
		maxVisRank    = 5
		entryExitRank = 6.0
	)

	drawW := float64(width - leftPad - rightPad)
	drawH := float64(height - topPad - bottomPad)
	drawX := float64(leftPad)
	drawY := float64(topPad)

	startTime := times[0]
	endTime := times[len(times)-1]
	timeRange := endTime.Sub(startTime).Seconds()

	dc := gg.NewContext(width, height)

	dc.SetColor(color.RGBA{248, 249, 250, 255})
	dc.Clear()

	loadFont(dc, 14)

	dc.SetColor(color.RGBA{33, 37, 41, 255})
	loadFont(dc, 18)
	dc.DrawStringAnchored("Bluesky Trending Topics (24h UTC)", float64(width)/2, drawY-30, 0.5, 0.5)

	loadFont(dc, 12)
	dc.SetColor(color.RGBA{100, 100, 100, 150})
	dc.DrawStringAnchored("@hourstats.bsky.social", drawX+10, drawY+drawH-10, 0, 1)

	loadFont(dc, 14)

	for rank := 1; rank <= maxVisRank; rank++ {
		yPos := drawY + (float64(rank)-0.5)/(float64(maxVisRank))*drawH
		dc.SetColor(color.RGBA{200, 200, 200, 255})
		dc.SetLineWidth(0.5)
		dc.SetDash(6, 4)
		dc.DrawLine(drawX, yPos, drawX+drawW, yPos)
		dc.Stroke()
		dc.SetDash()

		dc.SetColor(color.RGBA{33, 37, 41, 255})
		dc.DrawStringAnchored(fmt.Sprintf("#%d", rank), drawX-15, yPos, 1, 0.5)
	}

	drawTimeAxis(dc, startTime, endTime, drawX, drawY, drawW, drawH)

	topicData := groupByTopic(snapshots)

	colorMap := make(map[string]color.RGBA)
	colorIdx := 0
	for _, td := range topicData {
		if _, ok := colorMap[td.topicID]; !ok {
			colorMap[td.topicID] = trendingColors[colorIdx%len(trendingColors)]
			colorIdx++
		}
	}

	rankToY := func(rank float64) float64 {
		return drawY + (rank-0.5)/(float64(maxVisRank))*drawH
	}
	timeToX := func(t time.Time) float64 {
		return drawX + (t.Sub(startTime).Seconds()/timeRange)*drawW
	}

	for _, td := range topicData {
		if len(td.points) < 1 {
			continue
		}

		c := colorMap[td.topicID]
		dc.SetColor(c)
		dc.SetLineWidth(3.0)

		ranks := make([]float64, len(td.points))
		for i, p := range td.points {
			ranks[i] = float64(p.rank)
		}
		smoothed := gaussianSmoothing(ranks, 0.5)

		var xs, ys []float64

		if !td.points[0].time.Equal(startTime) {
			xs = append(xs, timeToX(td.points[0].time)-20)
			ys = append(ys, rankToY(entryExitRank))
		}

		for i, p := range td.points {
			xs = append(xs, timeToX(p.time))
			ys = append(ys, rankToY(smoothed[i]))
		}

		if !td.points[len(td.points)-1].time.Equal(endTime) {
			xs = append(xs, timeToX(td.points[len(td.points)-1].time)+20)
			ys = append(ys, rankToY(entryExitRank))
		}

		for i := 0; i < len(xs)-1; i++ {
			dc.DrawLine(xs[i], ys[i], xs[i+1], ys[i+1])
			dc.Stroke()
		}

		for i, p := range td.points {
			dc.DrawCircle(timeToX(p.time), rankToY(smoothed[i]), 4)
			dc.Fill()
		}

		lastX := xs[len(xs)-1]
		lastY := ys[len(ys)-1]
		if td.points[len(td.points)-1].time.Equal(endTime) {
			lastIdx := len(td.points) - 1
			lastY = rankToY(smoothed[lastIdx])
		}
		dc.DrawString(td.label, lastX+8, lastY+5)
	}

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("encode trending chart PNG: %w", err)
	}
	return buf.Bytes(), nil
}

func loadFont(dc *gg.Context, size float64) {
	if err := dc.LoadFontFace("/System/Library/Fonts/Geneva.ttf", size); err != nil {
		if fallbackErr := dc.LoadFontFace("", size); fallbackErr != nil {
			_ = fallbackErr
		}
	}
}

type topicPoint struct {
	time time.Time
	rank int
}

type topicSeries struct {
	topicID string
	label   string
	points  []topicPoint
}

func groupByTopic(snapshots []store.TopicSnapshotRow) []topicSeries {
	m := make(map[string]*topicSeries)
	var order []string

	for _, s := range snapshots {
		t, err := time.Parse(time.RFC3339, s.SnapshotTime)
		if err != nil {
			continue
		}
		if _, ok := m[s.TopicID]; !ok {
			m[s.TopicID] = &topicSeries{topicID: s.TopicID, label: s.Label}
			order = append(order, s.TopicID)
		}
		m[s.TopicID].points = append(m[s.TopicID].points, topicPoint{time: t, rank: s.Rank})
	}

	result := make([]topicSeries, 0, len(order))
	for _, id := range order {
		ts := m[id]
		sort.Slice(ts.points, func(i, j int) bool {
			return ts.points[i].time.Before(ts.points[j].time)
		})
		result = append(result, *ts)
	}
	return result
}

func distinctTimes(snapshots []store.TopicSnapshotRow) []time.Time {
	seen := make(map[string]bool)
	var times []time.Time
	for _, s := range snapshots {
		if seen[s.SnapshotTime] {
			continue
		}
		seen[s.SnapshotTime] = true
		t, err := time.Parse(time.RFC3339, s.SnapshotTime)
		if err != nil {
			continue
		}
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	return times
}

func drawTimeAxis(dc *gg.Context, start, end time.Time, x, y, w, h float64) {
	timeRange := end.Sub(start).Seconds()
	loadFont(dc, 10)

	first6h := start.Truncate(6 * time.Hour)
	if first6h.Before(start) {
		first6h = first6h.Add(6 * time.Hour)
	}

	for current := first6h; !current.After(end); current = current.Add(6 * time.Hour) {
		xPos := x + (current.Sub(start).Seconds()/timeRange)*w
		if xPos < x+10 || xPos > x+w-10 {
			continue
		}

		dc.SetColor(color.RGBA{220, 220, 220, 255})
		dc.SetLineWidth(0.3)
		dc.DrawLine(xPos, y, xPos, y+h)
		dc.Stroke()

		dc.SetColor(color.RGBA{140, 140, 140, 255})
		dc.DrawStringAnchored(current.Format("15:04"), xPos, y+h+15, 0.5, 0)
	}
}
