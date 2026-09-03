package sparkline

import (
	"fmt"
	"image/color"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// YearlySparklineConfig holds configuration for the yearly sentiment chart.
type YearlySparklineConfig struct {
	Width        int
	Height       int
	Padding      int
	LineWidth    float64 // smoothed trend line width (raw line is thinner)
	PointRadius  float64
	Background   color.RGBA
	PositiveLine color.RGBA
	NegativeLine color.RGBA
	NeutralLine  color.RGBA
	GridColor    color.RGBA
	TextColor    color.RGBA
}

// YearlyYRange represents the Y-axis range for the yearly sparkline
type YearlyYRange struct {
	Min    float64
	Max    float64
	Center float64
	Scale  float64
}

// calculateYearlyYRange calculates the Y-axis range based on actual yearly data.
func (yg *YearlySparklineGenerator) calculateYearlyYRange(dataPoints []state.YearlySparklineDataPoint) YearlyYRange {
	if len(dataPoints) == 0 {
		return YearlyYRange{Min: -100, Max: 100, Center: 0, Scale: 1.0}
	}
	values := make([]float64, len(dataPoints))
	for i, dp := range dataPoints {
		values[i] = dp.AverageSentiment
	}
	r := fitRange(values, 0.75)
	return YearlyYRange{
		Min:    r.Min,
		Max:    r.Max,
		Center: (r.Min + r.Max) / 2,
		Scale:  200.0 / (r.Max - r.Min),
	}
}

// DefaultYearlyConfig returns a default yearly sparkline configuration (25% larger)
func DefaultYearlyConfig() *YearlySparklineConfig {
	return &YearlySparklineConfig{
		Width:        1500, // 25% larger than 1200
		Height:       1000, // 25% larger than 800
		Padding:      100,  // Scaled proportionally
		LineWidth:    4.0,  // Scaled proportionally
		PointRadius:  6.5,
		Background:   themeSurface,
		PositiveLine: themePositive,
		NegativeLine: themeNegative,
		NeutralLine:  themeInkMuted,
		GridColor:    themeGrid,
		TextColor:    themeInkPrimary,
	}
}

// YearlySparklineGenerator generates yearly sentiment sparkline images
type YearlySparklineGenerator struct {
	config *YearlySparklineConfig
}

// NewYearlySparklineGenerator creates a new yearly sparkline generator
func NewYearlySparklineGenerator(config *YearlySparklineConfig) *YearlySparklineGenerator {
	if config == nil {
		config = DefaultYearlyConfig()
	}
	return &YearlySparklineGenerator{config: config}
}

// yearlySmoothingSigma is the Gaussian sigma in days for the yearly trend line.
const yearlySmoothingSigma = 5.0

// GenerateYearlySentimentSparkline creates a PNG image of yearly sentiment data.
func (yg *YearlySparklineGenerator) GenerateYearlySentimentSparkline(dataPoints []state.YearlySparklineDataPoint) ([]byte, error) {
	if len(dataPoints) == 0 {
		return nil, fmt.Errorf("no data points provided")
	}

	points := make([]seriesPoint, len(dataPoints))
	values := make([]float64, len(dataPoints))
	for i, dp := range dataPoints {
		points[i] = seriesPoint{T: dp.Timestamp, V: dp.AverageSentiment}
		values[i] = dp.AverageSentiment
	}

	var smoothed []float64
	if len(values) >= 2 {
		smoothed = yearlyGaussianSmoothing(values, yearlySmoothingSigma)
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
	first := dataPoints[0]
	latest := dataPoints[len(dataPoints)-1]

	spec := seriesChartSpec{
		Width:     yg.config.Width,
		Height:    yg.config.Height,
		Title:     "Bluesky sentiment, past year",
		Subtitle:  fmt.Sprintf("%s – %s · daily average net sentiment of English posts · UTC", first.Timestamp.Format("2 Jan 2006"), latest.Timestamp.Format("2 Jan 2006")),
		HeroLabel: "Year average",
		HeroValue: pctText(avg),
		HeroSub:   fmt.Sprintf("%d days of readings", len(dataPoints)),
		Tiles: []statTile{
			{Label: "Latest", Value: pctText(latest.AverageSentiment), Sub: latest.Timestamp.Format("2 Jan")},
			{Label: "High", Value: pctText(values[hi]), Sub: dataPoints[hi].Timestamp.Format("2 Jan")},
			{Label: "Low", Value: pctText(values[lo]), Sub: dataPoints[lo].Timestamp.Format("2 Jan")},
		},
		Points:       points,
		Smoothed:     smoothed,
		Average:      avg,
		XAxis:        xAxisMonths,
		RawLegend:    "daily",
		TrendLegend:  "trend",
		AvgLegend:    "year avg",
		LineWidth:    yg.config.LineWidth,
		MarkExtremes: true,
		Brand:        "@hourstats.bsky.social",
	}
	return renderSeriesChart(spec)
}

// yearlyGaussianSmoothing applies Gaussian smoothing to yearly sentiment data
func yearlyGaussianSmoothing(data []float64, sigma float64) []float64 {
	return gaussianSmoothing(data, sigma)
}
