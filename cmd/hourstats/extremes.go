package main

import (
	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

// weekExtremes picks the highest and lowest hour of the seven-day history so
// the trending reply can close with them. It returns nil when there is
// nothing to say: fewer than two readings, or a flat series where the high
// and the low are the same hour.
func weekExtremes(points []state.SentimentDataPoint) *topics.WeekExtremes {
	if len(points) < 2 {
		return nil
	}
	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.NetSentimentPercent
	}
	hi, lo := extremes(values)
	if hi == lo {
		return nil
	}
	return &topics.WeekExtremes{
		High: topics.SentimentExtreme{
			Value: points[hi].NetSentimentPercent,
			At:    points[hi].Timestamp,
			Topic: points[hi].TopTopic,
		},
		Low: topics.SentimentExtreme{
			Value: points[lo].NetSentimentPercent,
			At:    points[lo].Timestamp,
			Topic: points[lo].TopTopic,
		},
	}
}
