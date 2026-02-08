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
	chartType := flag.String("type", "all", "Chart type to generate: sparkline, yearly, daily-volume, yearly-volume, or all")
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
	case "daily-volume":
		generateDailyVolumeCharts(*seed)
	case "yearly-volume":
		generateYearlyVolumeCharts(*seed)
	case "all":
		generateSparklineCharts(*seed)
		generateYearlyCharts(*seed)
		generateDailyVolumeCharts(*seed)
		generateYearlyVolumeCharts(*seed)
	default:
		log.Fatalf("Unknown chart type: %s (use sparkline, yearly, daily-volume, yearly-volume, or all)", *chartType)
	}

	fmt.Printf("\nAll charts saved to %s/\n", outputDir)
}

func generateSparklineCharts(seed int64) {
	fmt.Println("=== Generating 7-day sparkline charts ===")

	scenarios := []struct {
		name string
		data func(*rand.Rand) []state.SentimentDataPoint
	}{
		{"baseline", syntheticSparkline7dNormal},
		{"volatile", syntheticSparkline7dVolatile},
		{"trending-positive", syntheticSparkline7dTrendUp},
		{"trending-negative", syntheticSparkline7dTrendDown},
		{"mostly-neutral", syntheticSparkline7dNeutral},
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

// syntheticSparkline7dNormal generates a typical 7-day dataset with moderate swings.
func syntheticSparkline7dNormal(rng *rand.Rand) []state.SentimentDataPoint {
	return generateSparklineData(rng, 336, 30*time.Minute, 8.0, 12.0)
}

// syntheticSparkline7dVolatile generates a highly volatile 7-day dataset.
func syntheticSparkline7dVolatile(rng *rand.Rand) []state.SentimentDataPoint {
	return generateSparklineData(rng, 336, 30*time.Minute, 0.0, 30.0)
}

// syntheticSparkline7dTrendUp generates a 7-day dataset trending positive.
func syntheticSparkline7dTrendUp(rng *rand.Rand) []state.SentimentDataPoint {
	points := generateSparklineData(rng, 336, 30*time.Minute, -5.0, 8.0)
	for i := range points {
		drift := float64(i) / float64(len(points)) * 25.0
		points[i].NetSentimentPercent += drift
	}
	return points
}

// syntheticSparkline7dTrendDown generates a 7-day dataset trending negative.
func syntheticSparkline7dTrendDown(rng *rand.Rand) []state.SentimentDataPoint {
	points := generateSparklineData(rng, 336, 30*time.Minute, 10.0, 8.0)
	for i := range points {
		drift := float64(i) / float64(len(points)) * -30.0
		points[i].NetSentimentPercent += drift
	}
	return points
}

// syntheticSparkline7dNeutral generates a 7-day dataset hovering near zero.
func syntheticSparkline7dNeutral(rng *rand.Rand) []state.SentimentDataPoint {
	return generateSparklineData(rng, 336, 30*time.Minute, 2.0, 4.0)
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
		seasonalWave := math.Sin(float64(i)/365.0*2*math.Pi-math.Pi/2) * 15.0
		avg := points[i].AverageSentiment + seasonalWave
		minVal := avg - 3.0 - rng.Float64()*5.0
		maxVal := avg + 3.0 + rng.Float64()*5.0
		points[i].AverageSentiment = avg
		points[i].NetSentimentPercent = avg
		points[i].MinSentiment = minVal
		points[i].MaxSentiment = maxVal
		points[i].Q1Sentiment = minVal + (avg-minVal)*0.4 + (rng.Float64()-0.5)*1.0
		points[i].MedianSentiment = avg + (rng.Float64()-0.5)*1.5
		points[i].Q3Sentiment = avg + (maxVal-avg)*0.6 + (rng.Float64()-0.5)*1.0
	}
	return points
}

// syntheticYearlyCrashRecovery generates a year with a major dip mid-year and recovery.
func syntheticYearlyCrashRecovery(rng *rand.Rand) []state.YearlySparklineDataPoint {
	points := generateYearlyData(rng, 365, 12.0, 4.0)
	for i := range points {
		dayFromCrash := float64(i) - 165.0
		crashEffect := 0.0
		if dayFromCrash >= -15 && dayFromCrash <= 0 {
			crashEffect = dayFromCrash / 15.0 * 30.0
		} else if dayFromCrash > 0 && dayFromCrash <= 60 {
			crashEffect = -30.0 * (1.0 - dayFromCrash/60.0)
		}
		avg := points[i].AverageSentiment + crashEffect
		minVal := avg - 3.0 - rng.Float64()*5.0
		maxVal := avg + 3.0 + rng.Float64()*5.0
		points[i].AverageSentiment = avg
		points[i].NetSentimentPercent = avg
		points[i].MinSentiment = minVal
		points[i].MaxSentiment = maxVal
		points[i].Q1Sentiment = minVal + (avg-minVal)*0.4 + (rng.Float64()-0.5)*1.0
		points[i].MedianSentiment = avg + (rng.Float64()-0.5)*1.5
		points[i].Q3Sentiment = avg + (maxVal-avg)*0.6 + (rng.Float64()-0.5)*1.0
	}
	return points
}

// syntheticYearlySteadilyPositive generates a year that stays mostly in positive territory.
func syntheticYearlySteadilyPositive(rng *rand.Rand) []state.YearlySparklineDataPoint {
	return generateYearlyData(rng, 365, 18.0, 5.0)
}

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

		q1 := minVal + (value-minVal)*0.4 + (rng.Float64()-0.5)*1.0
		median := value + (rng.Float64()-0.5)*1.5
		q3 := value + (maxVal-value)*0.6 + (rng.Float64()-0.5)*1.0

		points[i] = state.YearlySparklineDataPoint{
			Date:                ts.Format("2006-01-02"),
			AverageSentiment:    value,
			MinSentiment:        minVal,
			MaxSentiment:        maxVal,
			Q1Sentiment:         q1,
			MedianSentiment:     median,
			Q3Sentiment:         q3,
			Timestamp:           ts,
			NetSentimentPercent: value,
		}
	}
	return points
}

// ---------------------------------------------------------------------------
// Daily volume charts (7-day bar charts)
// ---------------------------------------------------------------------------

func generateDailyVolumeCharts(seed int64) {
	fmt.Println("=== Generating 7-day volume charts ===")

	scenarios := []struct {
		name string
		data func(*rand.Rand) []sparkline.DailyVolume
	}{
		{"baseline", syntheticDailyVolumeBaseline},
		{"high-volume", syntheticDailyVolumeHigh},
		{"growing", syntheticDailyVolumeGrowing},
		{"with-total-posts", syntheticDailyVolumeWithTotal},
	}

	gen := sparkline.NewDailyVolumeGenerator(nil)

	for _, sc := range scenarios {
		rng := rand.New(rand.NewSource(seed))
		data := sc.data(rng)

		img, err := gen.GenerateDailyVolumeChart(data)
		if err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}

		path := filepath.Join(outputDir, fmt.Sprintf("daily-volume-%s.png", sc.name))
		if err := os.WriteFile(path, img, 0644); err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}
		fmt.Printf("  [OK] %s -> %s (%d bytes)\n", sc.name, path, len(img))
	}
}

// syntheticDailyVolumeBaseline generates a typical 7-day EN-only dataset (~90k-120k/day).
func syntheticDailyVolumeBaseline(rng *rand.Rand) []sparkline.DailyVolume {
	return generateDailyVolumeData(rng, 7, 100_000, 20_000, false, 0)
}

// syntheticDailyVolumeHigh generates high-volume days (~200k+/day).
func syntheticDailyVolumeHigh(rng *rand.Rand) []sparkline.DailyVolume {
	return generateDailyVolumeData(rng, 7, 220_000, 30_000, false, 0)
}

// syntheticDailyVolumeGrowing generates a week with steadily increasing volume.
func syntheticDailyVolumeGrowing(rng *rand.Rand) []sparkline.DailyVolume {
	days := make([]sparkline.DailyVolume, 7)
	now := time.Now().UTC().Truncate(24 * time.Hour)
	for i := 0; i < 7; i++ {
		base := 60_000 + i*20_000
		days[i] = sparkline.DailyVolume{
			Date:    now.AddDate(0, 0, i-6),
			ENPosts: base + rng.Intn(10_000),
		}
	}
	return days
}

// syntheticDailyVolumeWithTotal generates 7 days with both total firehose and EN counts.
func syntheticDailyVolumeWithTotal(rng *rand.Rand) []sparkline.DailyVolume {
	return generateDailyVolumeData(rng, 7, 110_000, 15_000, true, 3.0)
}

func generateDailyVolumeData(rng *rand.Rand, count, center, amplitude int, withTotal bool, totalMultiplier float64) []sparkline.DailyVolume {
	now := time.Now().UTC().Truncate(24 * time.Hour)
	days := make([]sparkline.DailyVolume, count)
	for i := 0; i < count; i++ {
		enPosts := center + rng.Intn(amplitude*2) - amplitude
		if enPosts < 1000 {
			enPosts = 1000
		}
		d := sparkline.DailyVolume{
			Date:    now.AddDate(0, 0, i-count+1),
			ENPosts: enPosts,
		}
		if withTotal {
			d.TotalPosts = int(float64(enPosts)*totalMultiplier) + rng.Intn(amplitude)
		}
		days[i] = d
	}
	return days
}

// ---------------------------------------------------------------------------
// Yearly volume charts (weekly bar charts for full year)
// ---------------------------------------------------------------------------

func generateYearlyVolumeCharts(seed int64) {
	fmt.Println("=== Generating yearly volume charts ===")

	scenarios := []struct {
		name string
		data func(*rand.Rand) []sparkline.WeeklyVolume
	}{
		{"baseline", syntheticYearlyVolumeBaseline},
		{"growing", syntheticYearlyVolumeGrowing},
		{"with-total-posts", syntheticYearlyVolumeWithTotal},
		{"seasonal", syntheticYearlyVolumeSeasonal},
	}

	gen := sparkline.NewYearlyVolumeGenerator(nil)

	for _, sc := range scenarios {
		rng := rand.New(rand.NewSource(seed))
		data := sc.data(rng)

		img, err := gen.GenerateYearlyVolumeChart(data)
		if err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}

		path := filepath.Join(outputDir, fmt.Sprintf("yearly-volume-%s.png", sc.name))
		if err := os.WriteFile(path, img, 0644); err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}
		fmt.Printf("  [OK] %s -> %s (%d bytes)\n", sc.name, path, len(img))
	}
}

// syntheticYearlyVolumeBaseline generates ~52 weeks of steady EN-only volume (~600k-800k/week).
func syntheticYearlyVolumeBaseline(rng *rand.Rand) []sparkline.WeeklyVolume {
	return generateWeeklyVolumeData(rng, 52, 700_000, 100_000, false, 0)
}

// syntheticYearlyVolumeGrowing generates a year with steadily increasing weekly volume.
func syntheticYearlyVolumeGrowing(rng *rand.Rand) []sparkline.WeeklyVolume {
	weeks := make([]sparkline.WeeklyVolume, 52)
	now := time.Now().UTC().Truncate(24 * time.Hour)
	// find most recent Monday
	for now.Weekday() != time.Monday {
		now = now.AddDate(0, 0, -1)
	}
	for i := 0; i < 52; i++ {
		base := 300_000 + i*12_000
		weeks[i] = sparkline.WeeklyVolume{
			WeekStart: now.AddDate(0, 0, (i-51)*7),
			ENPosts:   base + rng.Intn(50_000),
		}
	}
	return weeks
}

// syntheticYearlyVolumeWithTotal generates 52 weeks with paired total + EN bars.
func syntheticYearlyVolumeWithTotal(rng *rand.Rand) []sparkline.WeeklyVolume {
	return generateWeeklyVolumeData(rng, 52, 650_000, 80_000, true, 2.8)
}

// syntheticYearlyVolumeSeasonal generates a year with a seasonal sine pattern.
func syntheticYearlyVolumeSeasonal(rng *rand.Rand) []sparkline.WeeklyVolume {
	weeks := make([]sparkline.WeeklyVolume, 52)
	now := time.Now().UTC().Truncate(24 * time.Hour)
	for now.Weekday() != time.Monday {
		now = now.AddDate(0, 0, -1)
	}
	for i := 0; i < 52; i++ {
		seasonal := math.Sin(float64(i)/52.0*2*math.Pi) * 200_000
		enPosts := 600_000 + int(seasonal) + rng.Intn(60_000) - 30_000
		if enPosts < 50_000 {
			enPosts = 50_000
		}
		weeks[i] = sparkline.WeeklyVolume{
			WeekStart: now.AddDate(0, 0, (i-51)*7),
			ENPosts:   enPosts,
		}
	}
	return weeks
}

func generateWeeklyVolumeData(rng *rand.Rand, count, center, amplitude int, withTotal bool, totalMultiplier float64) []sparkline.WeeklyVolume {
	now := time.Now().UTC().Truncate(24 * time.Hour)
	// find most recent Monday
	for now.Weekday() != time.Monday {
		now = now.AddDate(0, 0, -1)
	}
	weeks := make([]sparkline.WeeklyVolume, count)
	for i := 0; i < count; i++ {
		enPosts := center + rng.Intn(amplitude*2) - amplitude
		if enPosts < 10_000 {
			enPosts = 10_000
		}
		w := sparkline.WeeklyVolume{
			WeekStart: now.AddDate(0, 0, (i-count+1)*7),
			ENPosts:   enPosts,
		}
		if withTotal {
			w.TotalPosts = int(float64(enPosts)*totalMultiplier) + rng.Intn(amplitude)
		}
		weeks[i] = w
	}
	return weeks
}
