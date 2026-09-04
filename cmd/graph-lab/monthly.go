package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
)

// Synthetic month scenarios for the monthly candlestick and volume charts.
// The candles are built the same way production does: 24 hourly readings per
// day, sorted, then min/quartiles/max taken from the sorted series.

func generateMonthlyCharts(seed int64) {
	fmt.Println("=== Generating monthly candlestick charts ===")
	candleScenarios := []struct {
		name string
		data func(*rand.Rand) []sparkline.DailyCandle
	}{
		{"prod-like", syntheticMonthProdLike},
		{"volatile", syntheticMonthVolatile},
		{"dip", syntheticMonthDip},
	}
	cgen := sparkline.NewMonthlyCandleGenerator()
	for _, sc := range candleScenarios {
		rng := rand.New(rand.NewSource(seed))
		days := sc.data(rng)
		img, err := cgen.GenerateMonthlyCandleChart(days, sparkline.MonthlyCandleMeta{
			MonthLabel:   "August 2026",
			PrevMonthAvg: 9.8,
			PrevLabel:    "July",
		})
		if err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}
		path := filepath.Join(outputDir, fmt.Sprintf("monthly-candle-%s.png", sc.name))
		if err := os.WriteFile(path, img, 0644); err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}
		fmt.Printf("  [OK] %s -> %s (%d bytes)\n", sc.name, path, len(img))
	}

	fmt.Println("=== Generating monthly volume charts ===")
	vgen := sparkline.NewMonthlyVolumeGenerator()
	volScenarios := []struct {
		name     string
		withAll  bool
		prevMult float64
	}{
		{"prod-like", true, 0.97},
		{"english-only", false, 1.04},
	}
	for _, sc := range volScenarios {
		rng := rand.New(rand.NewSource(seed))
		days := syntheticMonthVolume(rng, sc.withAll)
		total := 0
		for _, d := range days {
			total += d.ENPosts
		}
		img, err := vgen.GenerateMonthlyVolumeChart(days, sparkline.MonthlyVolumeMeta{
			MonthLabel:  "August 2026",
			PrevMonthEN: int(float64(total) * sc.prevMult),
			PrevLabel:   "July",
		})
		if err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}
		path := filepath.Join(outputDir, fmt.Sprintf("monthly-volume-%s.png", sc.name))
		if err := os.WriteFile(path, img, 0644); err != nil {
			log.Printf("  [FAIL] %s: %v", sc.name, err)
			continue
		}
		fmt.Printf("  [OK] %s -> %s (%d bytes)\n", sc.name, path, len(img))
	}
}

var monthStart = time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)

// monthFromHourly builds candles from a per-day hourly reading generator.
func monthFromHourly(rng *rand.Rand, days int, hourly func(rng *rand.Rand, day, hour int) float64) []sparkline.DailyCandle {
	out := make([]sparkline.DailyCandle, 0, days)
	for d := 0; d < days; d++ {
		vals := make([]float64, 24)
		sum := 0.0
		for h := 0; h < 24; h++ {
			vals[h] = hourly(rng, d, h)
			sum += vals[h]
		}
		sort.Float64s(vals)
		out = append(out, sparkline.DailyCandle{
			Date:    monthStart.AddDate(0, 0, d),
			Min:     vals[0],
			Q1:      quantile(vals, 0.25),
			Median:  quantile(vals, 0.5),
			Q3:      quantile(vals, 0.75),
			Max:     vals[23],
			Average: sum / 24,
			Runs:    24,
		})
	}
	return out
}

func quantile(sorted []float64, q float64) float64 {
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

// syntheticMonthProdLike mirrors production: a ~10% base with a gentle diurnal
// swing of about two points, weekend lift, and hourly noise.
func syntheticMonthProdLike(rng *rand.Rand) []sparkline.DailyCandle {
	return monthFromHourly(rng, 31, func(rng *rand.Rand, day, hour int) float64 {
		date := monthStart.AddDate(0, 0, day)
		weekend := 0.0
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			weekend = 0.8
		}
		diurnal := 1.1 * math.Sin(float64(hour-6)/24*2*math.Pi)
		slow := 0.6 * math.Sin(float64(day)/31*2*math.Pi)
		return 10.5 + weekend + slow + diurnal + rng.NormFloat64()*0.9
	})
}

// syntheticMonthVolatile has wide daily ranges and some days crossing zero.
func syntheticMonthVolatile(rng *rand.Rand) []sparkline.DailyCandle {
	return monthFromHourly(rng, 31, func(rng *rand.Rand, day, hour int) float64 {
		base := 4.0 + 8.0*math.Sin(float64(day)/9)
		return base + 4.0*math.Sin(float64(hour)/24*2*math.Pi) + rng.NormFloat64()*3.5
	})
}

// syntheticMonthDip is prod-like with a sharp three-day negative event.
func syntheticMonthDip(rng *rand.Rand) []sparkline.DailyCandle {
	return monthFromHourly(rng, 31, func(rng *rand.Rand, day, hour int) float64 {
		v := 10.5 + 1.1*math.Sin(float64(hour-6)/24*2*math.Pi) + rng.NormFloat64()*0.9
		if day >= 17 && day <= 19 {
			v -= 9.0 - 2.5*float64(day-17)
		}
		return v
	})
}

// syntheticMonthVolume follows a weekday pattern with a slow rise and one spike.
func syntheticMonthVolume(rng *rand.Rand, withAll bool) []sparkline.DailyVolumePoint {
	out := make([]sparkline.DailyVolumePoint, 0, 31)
	for d := 0; d < 31; d++ {
		date := monthStart.AddDate(0, 0, d)
		base := 1.85e6 * (1 + 0.004*float64(d))
		switch date.Weekday() {
		case time.Saturday:
			base *= 0.9
		case time.Sunday:
			base *= 0.86
		}
		if d == 11 {
			base *= 1.28
		}
		en := int(base * (1 + rng.NormFloat64()*0.03))
		all := 0
		if withAll {
			all = int(float64(en) / (0.42 + rng.NormFloat64()*0.01))
		}
		out = append(out, sparkline.DailyVolumePoint{Date: date, ENPosts: en, TotalPosts: all})
	}
	return out
}
