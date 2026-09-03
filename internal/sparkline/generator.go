package sparkline

import (
	"fmt"
	"image/color"
	"math"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// SparklineConfig holds configuration for the seven-day sentiment chart.
type SparklineConfig struct {
	Width        int
	Height       int
	Padding      int
	LineWidth    float64 // smoothed trend line width (raw line is thinner)
	PointRadius  float64
	RawAsDots    bool // draw hourly samples as dots (default) rather than a thin line
	Background   color.RGBA
	PositiveLine color.RGBA
	NegativeLine color.RGBA
	NeutralLine  color.RGBA
	GridColor    color.RGBA
	TextColor    color.RGBA
}

// YRange represents the Y-axis range for the sparkline
type YRange struct {
	Min    float64
	Max    float64
	Center float64
	Scale  float64
}

// calculateYRange calculates the Y-axis range based on actual data.
func (sg *SparklineGenerator) calculateYRange(dataPoints []state.SentimentDataPoint) YRange {
	if len(dataPoints) == 0 {
		return YRange{Min: -100, Max: 100, Center: 0, Scale: 1.0}
	}
	values := make([]float64, len(dataPoints))
	for i, dp := range dataPoints {
		values[i] = dp.NetSentimentPercent
	}
	r := fitRange(values, 0.75)
	return YRange{
		Min:    r.Min,
		Max:    r.Max,
		Center: (r.Min + r.Max) / 2,
		Scale:  200.0 / (r.Max - r.Min),
	}
}

// DefaultConfig returns a default sparkline configuration
func DefaultConfig() *SparklineConfig {
	return &SparklineConfig{
		Width:        1200, // Canvas 1200x800 (3:2 aspect ratio)
		Height:       800,  // Canvas 1200x800 (3:2 aspect ratio)
		Padding:      80,
		LineWidth:    3.5,
		PointRadius:  6.5,
		RawAsDots:    true,
		Background:   themeSurface,
		PositiveLine: themePositive,
		NegativeLine: themeNegative,
		NeutralLine:  themeInkMuted,
		GridColor:    themeGrid,
		TextColor:    themeInkPrimary,
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

// sparklineSmoothingSigma is the Gaussian sigma (in readings) for the trend
// line: ~8 readings, roughly 8 hours at the hourly production cadence.
const sparklineSmoothingSigma = 4.0

// GenerateSentimentSparkline creates a PNG image of sentiment data over time.
func (sg *SparklineGenerator) GenerateSentimentSparkline(dataPoints []state.SentimentDataPoint) ([]byte, error) {
	if len(dataPoints) == 0 {
		return nil, fmt.Errorf("no data points provided")
	}

	points := make([]seriesPoint, len(dataPoints))
	values := make([]float64, len(dataPoints))
	for i, dp := range dataPoints {
		points[i] = seriesPoint{T: dp.Timestamp, V: dp.NetSentimentPercent}
		values[i] = dp.NetSentimentPercent
	}

	var smoothed []float64
	if len(values) >= 2 {
		smoothed = gaussianSmoothing(values, sparklineSmoothingSigma)
	}

	sum := 0.0
	hi, lo := 0, 0
	for i, v := range values {
		sum += v
		if v > values[hi] {
			hi = i
		}
		if v < values[lo] {
			lo = i
		}
	}
	avg := sum / float64(len(values))
	latest := dataPoints[len(dataPoints)-1]
	first := dataPoints[0]

	spec := seriesChartSpec{
		Width:     sg.config.Width,
		Height:    sg.config.Height,
		Title:     "Bluesky sentiment",
		Subtitle:  fmt.Sprintf("%s – %s · net sentiment of English posts, hourly · UTC", first.Timestamp.Format("Mon 2 Jan"), latest.Timestamp.Format("Mon 2 Jan")),
		HeroLabel: "Latest",
		HeroValue: pctText(latest.NetSentimentPercent),
		HeroSub:   deltaText(latest.NetSentimentPercent-avg, "7-day avg"),
		Tiles: []statTile{
			{Label: "7-day average", Value: pctText(avg)},
			{Label: "High", Value: pctText(values[hi]), Sub: dataPoints[hi].Timestamp.Format("Mon 15:04")},
			{Label: "Low", Value: pctText(values[lo]), Sub: dataPoints[lo].Timestamp.Format("Mon 15:04")},
		},
		Points:       points,
		Smoothed:     smoothed,
		Average:      avg,
		XAxis:        xAxisDays,
		RawLegend:    "hourly",
		TrendLegend:  "trend",
		AvgLegend:    "7-day avg",
		LineWidth:    sg.config.LineWidth,
		RawAsDots:    sg.config.RawAsDots,
		MarkExtremes: true,
		Brand:        "@hourstats.bsky.social",
	}
	return renderSeriesChart(spec)
}

// deltaText describes how the latest reading sits against a reference value.
func deltaText(delta float64, reference string) string {
	switch {
	case math.Abs(delta) < 0.05:
		return "level with " + reference
	case delta > 0:
		return fmt.Sprintf("+%.1f vs %s", delta, reference)
	default:
		return fmt.Sprintf("−%.1f vs %s", -delta, reference)
	}
}

// gaussianSmoothing applies Gaussian smoothing to sentiment data
func gaussianSmoothing(data []float64, sigma float64) []float64 {
	if len(data) == 0 {
		return data
	}

	smoothed := make([]float64, len(data))

	for i := 0; i < len(data); i++ {
		sum := 0.0
		weightSum := 0.0

		for j := 0; j < len(data); j++ {
			distance := math.Abs(float64(j - i))
			weight := math.Exp(-(distance * distance) / (2 * sigma * sigma))

			sum += data[j] * weight
			weightSum += weight
		}

		smoothed[i] = sum / weightSum
	}

	return smoothed
}
