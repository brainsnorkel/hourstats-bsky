package main

import (
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

// weekExtremes picks the highest and lowest hour of the seven-day history so
// the trending reply can close with them. It returns nil when there is
// nothing to say: fewer than two readings, or a flat series where the high
// and the low are the same hour. A topic naming a year before
// topics.LabelYearFloor(now) is left off: rows stored before the grouper's
// label year rule carry 2024 election labels for the 2026 midterms.
func weekExtremes(points []state.SentimentDataPoint, now time.Time) *topics.WeekExtremes {
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
			Topic: footerTopic(points[hi].TopTopic, now),
		},
		Low: topics.SentimentExtreme{
			Value: points[lo].NetSentimentPercent,
			At:    points[lo].Timestamp,
			Topic: footerTopic(points[lo].TopTopic, now),
		},
	}
}

// footerTopic is the stored top topic, or empty when it names a past year.
// A past-year label the grouper kept because its top keyword backed the
// year is still blanked here, and that is deliberate: a one-line caption
// cannot show the backing, and a bare value beats a year that reads as a slip.
func footerTopic(topic string, now time.Time) string {
	if len(topics.PastLabelYears(topic, now)) > 0 {
		return ""
	}
	return topic
}
