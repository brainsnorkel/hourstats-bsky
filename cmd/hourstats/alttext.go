package main

import (
	"fmt"
	"math"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// Alt text for the sentiment charts. Each description mirrors what a sighted
// reader takes from the image: what is plotted and over what period, the
// headline figure and how it sits against the average, which sentiment zone
// it is in, the extremes with their timing, and which way the trend moved.

// signedPct formats a percentage with an explicit sign for positive values.
func signedPct(v float64) string {
	switch {
	case v >= 0.05:
		return fmt.Sprintf("+%.1f%%", v)
	case v > -0.05:
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", v)
}

// zonePhrase names the sentiment zone a value falls in, matching the chart's
// shaded band (-10% to +10% is neutral).
func zonePhrase(v float64) string {
	switch {
	case v > 10:
		return "in the positive zone (above +10%)"
	case v < -10:
		return "in the negative zone (below -10%)"
	default:
		return "in the neutral zone (between -10% and +10%)"
	}
}

// deltaPhrase describes a value relative to a named average.
func deltaPhrase(value, avg float64, avgName string) string {
	delta := value - avg
	switch {
	case math.Abs(delta) < 0.05:
		return fmt.Sprintf("level with the %s of %s", avgName, signedPct(avg))
	case delta > 0:
		return fmt.Sprintf("%.1f points above the %s of %s", delta, avgName, signedPct(avg))
	default:
		return fmt.Sprintf("%.1f points below the %s of %s", -delta, avgName, signedPct(avg))
	}
}

// trendPhrase compares the mean of the first quarter of the series with the
// mean of the last quarter and says whether sentiment rose, fell, or held.
func trendPhrase(values []float64, period string) string {
	if len(values) < 4 {
		return ""
	}
	q := len(values) / 4
	first := mean(values[:q])
	last := mean(values[len(values)-q:])
	switch {
	case last-first > 1:
		return fmt.Sprintf("Over the %s the trend rose from about %s to %s.", period, signedPct(first), signedPct(last))
	case first-last > 1:
		return fmt.Sprintf("Over the %s the trend fell from about %s to %s.", period, signedPct(first), signedPct(last))
	default:
		return fmt.Sprintf("Over the %s the trend held steady around %s.", period, signedPct(mean(values)))
	}
}

// extremes returns the indices of the highest and lowest values.
func extremes(values []float64) (hi, lo int) {
	for i, v := range values {
		if v > values[hi] {
			hi = i
		}
		if v < values[lo] {
			lo = i
		}
	}
	return hi, lo
}

// generateSparklineAltText describes the seven-day chart.
func generateSparklineAltText(points []state.SentimentDataPoint) string {
	if len(points) < 2 {
		return "Seven day sentiment trend chart"
	}
	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.NetSentimentPercent
	}
	hi, lo := extremes(values)
	avg := mean(values)
	latest := points[len(points)-1]
	first := points[0]

	text := fmt.Sprintf(
		"Chart of Bluesky net sentiment, hourly readings from %s to %s UTC, drawn as dots with a smoothed trend line. "+
			"Latest %s, %s, %s. High %s on %s; low %s on %s.",
		first.Timestamp.Format("Mon 2 Jan"), latest.Timestamp.Format("Mon 2 Jan"),
		signedPct(latest.NetSentimentPercent),
		deltaPhrase(latest.NetSentimentPercent, avg, "7-day average"),
		zonePhrase(latest.NetSentimentPercent),
		signedPct(values[hi]), points[hi].Timestamp.Format("Mon 15:04"),
		signedPct(values[lo]), points[lo].Timestamp.Format("Mon 15:04"),
	)
	if trend := trendPhrase(values, "week"); trend != "" {
		text += " " + trend
	}
	return text
}

// yearlyDay formats a yearly point's date as "2 Jan", preferring the Date
// string (always populated from the daily aggregate) over Timestamp.
func yearlyDay(p state.YearlySparklineDataPoint) string {
	if t, err := time.Parse("2006-01-02", p.Date); err == nil {
		return t.Format("2 Jan")
	}
	if !p.Timestamp.IsZero() {
		return p.Timestamp.Format("2 Jan")
	}
	return p.Date
}

// yearlyDayWithYear formats a yearly point's date as "2 Jan 2006".
func yearlyDayWithYear(p state.YearlySparklineDataPoint) string {
	if t, err := time.Parse("2006-01-02", p.Date); err == nil {
		return t.Format("2 Jan 2006")
	}
	if !p.Timestamp.IsZero() {
		return p.Timestamp.Format("2 Jan 2006")
	}
	return p.Date
}

// buildYearlyAltText describes the yearly chart.
func buildYearlyAltText(points []state.YearlySparklineDataPoint) string {
	if len(points) == 0 {
		return "Yearly Bluesky sentiment chart"
	}
	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.AverageSentiment
	}
	hi, lo := extremes(values)
	avg := mean(values)
	latest := points[len(points)-1]
	first := points[0]

	text := fmt.Sprintf(
		"Chart of Bluesky net sentiment, daily averages from %s to %s UTC, with a smoothed trend line. "+
			"Year average %s over %d days. Latest %s on %s, %s, %s. High %s on %s; low %s on %s.",
		yearlyDayWithYear(first), yearlyDayWithYear(latest),
		signedPct(avg), len(points),
		signedPct(latest.AverageSentiment), yearlyDay(latest),
		deltaPhrase(latest.AverageSentiment, avg, "year average"),
		zonePhrase(latest.AverageSentiment),
		signedPct(values[hi]), yearlyDay(points[hi]),
		signedPct(values[lo]), yearlyDay(points[lo]),
	)
	if trend := trendPhrase(values, "year"); trend != "" {
		text += " " + trend
	}
	return text
}
