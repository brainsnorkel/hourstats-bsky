// graph-lab is a local test harness for experimenting with chart designs.
//
// It generates synthetic sentiment data (no AWS required) and renders charts
// using the sparkline generators. Output PNGs are saved to test-results/graph-lab/
// for visual comparison.
//
// Usage:
//
//	go run cmd/graph-lab/main.go
//	go run cmd/graph-lab/main.go -type sparkline
//	go run cmd/graph-lab/main.go -type yearly
//	go run cmd/graph-lab/main.go -type all
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

const outputDir = "test-results/graph-lab"

func main() {
	chartType := flag.String("type", "all", "Chart type to generate: sparkline, yearly, or all")
	seed := flag.Int64("seed", 42, "Random seed for reproducible data (0 for random)")
	flag.Parse()

	if *seed == 0 {
		*seed = time.Now().UnixNano()
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	switch *chartType {
	case "sparkline":
		generateSparklineCharts(*seed)
	case "yearly":
		generateYearlyCharts(*seed)
	case "all":
		generateSparklineCharts(*seed)
		generateYearlyCharts(*seed)
	default:
		log.Fatalf("Unknown chart type: %s (use sparkline, yearly, or all)", *chartType)
	}

	fmt.Printf("\nAll charts saved to %s/\n", outputDir)
}

func generateSparklineCharts(seed int64) {
	fmt.Println("=== Generating 48-hour sparkline charts ===")

	scenarios := []struct {
		name string
		data func(*rand.Rand) []state.SentimentDataPoint
	}{
		{"baseline", syntheticSparkline48hNormal},
		{"volatile", syntheticSparkline48hVolatile},
		{"trending-positive", syntheticSparkline48hTrendUp},
		{"trending-negative", syntheticSparkline48hTrendDown},
		{"mostly-neutral", syntheticSparkline48hNeutral},
	}

	gen := sparkline.NewSparklineGenerator(nil)

	for _, sc := range scenarios {
		rng := rand.New(rand.NewSource(seed))
		data := sc.data(rng)

		img, err := gen.GenerateSentimentSparkline(data)
		if err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}

		path := filepath.Join(outputDir, fmt.Sprintf("sparkline-%s.png", sc.name))
		if err := os.WriteFile(path, img, 0644); err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}
		fmt.Printf("  [OK] %s -> %s (%d bytes)\n", sc.name, path, len(img))
	}
}

// syntheticSparkline48hNormal generates a typical 48-hour dataset with moderate swings.
func syntheticSparkline48hNormal(rng *rand.Rand) []state.SentimentDataPoint {
	return generateSparklineData(rng, 48, 30*time.Minute, 8.0, 12.0)
}

// syntheticSparkline48hVolatile generates a highly volatile 48-hour dataset.
func syntheticSparkline48hVolatile(rng *rand.Rand) []state.SentimentDataPoint {
	return generateSparklineData(rng, 48, 30*time.Minute, 0.0, 30.0)
}

// syntheticSparkline48hTrendUp generates a 48-hour dataset trending positive.
func syntheticSparkline48hTrendUp(rng *rand.Rand) []state.SentimentDataPoint {
	points := generateSparklineData(rng, 48, 30*time.Minute, -5.0, 8.0)
	for i := range points {
		drift := float64(i) / float64(len(points)) * 25.0
		points[i].NetSentimentPercent += drift
	}
	return points
}

// syntheticSparkline48hTrendDown generates a 48-hour dataset trending negative.
func syntheticSparkline48hTrendDown(rng *rand.Rand) []state.SentimentDataPoint {
	points := generateSparklineData(rng, 48, 30*time.Minute, 10.0, 8.0)
	for i := range points {
		drift := float64(i) / float64(len(points)) * -30.0
		points[i].NetSentimentPercent += drift
	}
	return points
}

// syntheticSparkline48hNeutral generates a 48-hour dataset hovering near zero.
func syntheticSparkline48hNeutral(rng *rand.Rand) []state.SentimentDataPoint {
	return generateSparklineData(rng, 48, 30*time.Minute, 2.0, 4.0)
}

// generateSparklineData creates synthetic SentimentDataPoints.
// count is the number of data points, interval is the time between points,
// center is the mean sentiment, amplitude controls noise range.
func generateSparklineData(rng *rand.Rand, count int, interval time.Duration, center, amplitude float64) []state.SentimentDataPoint {
	now := time.Now().UTC().Truncate(time.Minute)
	start := now.Add(-time.Duration(count) * interval)

	points := make([]state.SentimentDataPoint, count)
	for i := 0; i < count; i++ {
		ts := start.Add(time.Duration(i) * interval)
		sinComponent := math.Sin(float64(i)/float64(count)*4*math.Pi) * amplitude * 0.6
		noise := (rng.Float64()*2 - 1) * amplitude * 0.4
		value := center + sinComponent + noise

		if value > 50 {
			value = 50
		}
		if value < -50 {
			value = -50
		}

		cat := "neutral"
		if value > 10 {
			cat = "positive"
		} else if value < -10 {
			cat = "negative"
		}

		points[i] = state.SentimentDataPoint{
			RunID:               fmt.Sprintf("lab-run-%d", i),
			Timestamp:           ts,
			NetSentimentPercent: value,
			SentimentCategory:   cat,
			TotalPosts:          500 + rng.Intn(2000),
		}
	}
	return points
}

func generateYearlyCharts(seed int64) {
	fmt.Println("=== Generating yearly sentiment charts ===")

	scenarios := []struct {
		name string
		data func(*rand.Rand) []state.YearlySparklineDataPoint
	}{
		{"baseline", syntheticYearlyNormal},
		{"volatile", syntheticYearlyVolatile},
		{"seasonal", syntheticYearlySeasonal},
		{"crash-recovery", syntheticYearlyCrashRecovery},
		{"steadily-positive", syntheticYearlySteadilyPositive},
	}

	gen := sparkline.NewYearlySparklineGenerator(nil)

	for _, sc := range scenarios {
		rng := rand.New(rand.NewSource(seed))
		data := sc.data(rng)

		img, err := gen.GenerateYearlySentimentSparkline(data)
		if err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}

		path := filepath.Join(outputDir, fmt.Sprintf("yearly-%s.png", sc.name))
		if err := os.WriteFile(path, img, 0644); err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}
		fmt.Printf("  [OK] %s -> %s (%d bytes)\n", sc.name, path, len(img))
	}
}

// syntheticYearlyNormal generates a typical year of daily sentiment.
func syntheticYearlyNormal(rng *rand.Rand) []state.YearlySparklineDataPoint {
	return generateYearlyData(rng, 365, 8.0, 6.0)
}

// syntheticYearlyVolatile generates a volatile year with wide swings.
func syntheticYearlyVolatile(rng *rand.Rand) []state.YearlySparklineDataPoint {
	return generateYearlyData(rng, 365, 5.0, 18.0)
}

// syntheticYearlySeasonal generates a year with clear seasonal patterns.
func syntheticYearlySeasonal(rng *rand.Rand) []state.YearlySparklineDataPoint {
	points := generateYearlyData(rng, 365, 5.0, 5.0)
	for i := range points {
		// Seasonal sine wave: peaks mid-year (summer), troughs at year boundaries (winter)
		seasonalWave := math.Sin(float64(i)/365.0*2*math.Pi-math.Pi/2) * 15.0
		points[i].AverageSentiment += seasonalWave
		points[i].NetSentimentPercent = points[i].AverageSentiment
		points[i].MinSentiment = points[i].AverageSentiment - 3.0 - rng.Float64()*5.0
		points[i].MaxSentiment = points[i].AverageSentiment + 3.0 + rng.Float64()*5.0
	}
	return points
}

// syntheticYearlyCrashRecovery generates a year with a major dip mid-year and recovery.
func syntheticYearlyCrashRecovery(rng *rand.Rand) []state.YearlySparklineDataPoint {
	points := generateYearlyData(rng, 365, 12.0, 4.0)
	for i := range points {
		// Simulates a sentiment crash centered at day 165: 15-day ramp down, 60-day recovery
		dayFromCrash := float64(i) - 165.0
		crashEffect := 0.0
		if dayFromCrash >= -15 && dayFromCrash <= 0 {
			crashEffect = dayFromCrash / 15.0 * 30.0
		} else if dayFromCrash > 0 && dayFromCrash <= 60 {
			crashEffect = -30.0 * (1.0 - dayFromCrash/60.0)
		}
		points[i].AverageSentiment += crashEffect
		points[i].NetSentimentPercent = points[i].AverageSentiment
		points[i].MinSentiment = points[i].AverageSentiment - 3.0 - rng.Float64()*5.0
		points[i].MaxSentiment = points[i].AverageSentiment + 3.0 + rng.Float64()*5.0
	}
	return points
}

// syntheticYearlySteadilyPositive generates a year that stays mostly in positive territory.
func syntheticYearlySteadilyPositive(rng *rand.Rand) []state.YearlySparklineDataPoint {
	return generateYearlyData(rng, 365, 18.0, 5.0)
}

// generateYearlyData creates synthetic YearlySparklineDataPoints.
func generateYearlyData(rng *rand.Rand, days int, center, amplitude float64) []state.YearlySparklineDataPoint {
	endDate := time.Now().UTC().Truncate(24 * time.Hour)
	startDate := endDate.AddDate(0, 0, -days+1)

	points := make([]state.YearlySparklineDataPoint, days)
	for i := 0; i < days; i++ {
		ts := startDate.AddDate(0, 0, i)

		slowWave := math.Sin(float64(i)/30.0*2*math.Pi) * amplitude * 0.5
		fastWave := math.Sin(float64(i)/7.0*2*math.Pi) * amplitude * 0.2
		noise := (rng.Float64()*2 - 1) * amplitude * 0.3
		value := center + slowWave + fastWave + noise

		if value > 50 {
			value = 50
		}
		if value < -50 {
			value = -50
		}

		minVal := value - 3.0 - rng.Float64()*5.0
		maxVal := value + 3.0 + rng.Float64()*5.0

		points[i] = state.YearlySparklineDataPoint{
			Date:                ts.Format("2006-01-02"),
			AverageSentiment:    value,
			MinSentiment:        minVal,
			MaxSentiment:        maxVal,
			Timestamp:           ts,
			NetSentimentPercent: value,
		}
	}
	return points
}
