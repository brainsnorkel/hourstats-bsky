package main

import (
	"math"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// Type conversion helpers
// ---------------------------------------------------------------------------

func toAnalyzerPosts(posts []store.Post) []analyzer.Post {
	out := make([]analyzer.Post, len(posts))
	for i, p := range posts {
		out[i] = analyzer.Post{
			URI: p.URI, CID: p.CID, Text: p.Text,
			Author: p.AuthorHandle,
			Likes:  p.Likes, Reposts: p.Reposts, Replies: p.Replies,
			CreatedAt: p.CreatedAt,
			IsReply:   p.IsReply,
		}
	}
	return out
}

func toStateSentimentPoints(points []store.SentimentDataPoint) []state.SentimentDataPoint {
	out := make([]state.SentimentDataPoint, len(points))
	for i, p := range points {
		out[i] = state.SentimentDataPoint{
			RunID:                p.RunID,
			Timestamp:            p.Timestamp,
			AverageCompoundScore: p.AverageCompoundScore,
			NetSentimentPercent:  p.NetSentimentPercent,
			SentimentCategory:    p.SentimentCategory,
			TotalPosts:           p.TotalPosts,
			CreatedAt:            p.CreatedAt,
			TTL:                  p.TTL,
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Math helpers
// ---------------------------------------------------------------------------

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}
