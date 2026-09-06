package main

import (
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// weekOfPoints builds a flat week of hourly readings from Sat 29 Aug 2026
// 00:00 UTC. Index 167 is Fri 4 Sep 23:00 UTC.
func weekOfPoints() []state.SentimentDataPoint {
	start := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	pts := make([]state.SentimentDataPoint, 168)
	for i := range pts {
		pts[i] = state.SentimentDataPoint{
			Timestamp:           start.Add(time.Duration(i) * time.Hour),
			NetSentimentPercent: 5,
		}
	}
	return pts
}

func TestWeekExtremes(t *testing.T) {
	pts := weekOfPoints()
	const lo = 24*5 + 6 // Thu 3 Sep 06:00 UTC
	pts[167].NetSentimentPercent = 29
	pts[167].TopTopic = "Charlie Kirk shooting"
	pts[lo].NetSentimentPercent = -8

	got := weekExtremes(pts)
	if got == nil {
		t.Fatal("weekExtremes returned nil, want the week's high and low")
	}
	if got.High.Value != 29 || !got.High.At.Equal(pts[167].Timestamp) || got.High.Topic != "Charlie Kirk shooting" {
		t.Errorf("high = %+v", got.High)
	}
	if got.Low.Value != -8 || !got.Low.At.Equal(pts[lo].Timestamp) || got.Low.Topic != "" {
		t.Errorf("low = %+v", got.Low)
	}
}

func TestWeekExtremesNil(t *testing.T) {
	flat := weekOfPoints()
	tests := []struct {
		name string
		pts  []state.SentimentDataPoint
	}{
		{"no points", nil},
		{"single point", flat[:1]},
		{"flat series", flat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := weekExtremes(tt.pts); got != nil {
				t.Errorf("weekExtremes = %+v, want nil", got)
			}
		})
	}
}

// The first occurrence wins when a value repeats, matching the extremes()
// helper the alt text uses.
func TestWeekExtremesTakesFirstOccurrence(t *testing.T) {
	pts := weekOfPoints()
	pts[10].NetSentimentPercent = 29
	pts[20].NetSentimentPercent = 29
	pts[30].NetSentimentPercent = -8
	pts[40].NetSentimentPercent = -8

	got := weekExtremes(pts)
	if got == nil {
		t.Fatal("weekExtremes returned nil")
	}
	if !got.High.At.Equal(pts[10].Timestamp) {
		t.Errorf("high at %v, want %v", got.High.At, pts[10].Timestamp)
	}
	if !got.Low.At.Equal(pts[30].Timestamp) {
		t.Errorf("low at %v, want %v", got.Low.At, pts[30].Timestamp)
	}
}
