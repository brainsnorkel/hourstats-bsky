package main

import (
	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
)

// ---------------------------------------------------------------------------
// Sentiment calculation
// ---------------------------------------------------------------------------

func calculateOverallSentiment(posts []analyzer.AnalyzedPost) (string, float64) {
	if len(posts) == 0 {
		return "neutral", 0
	}
	var total float64
	for _, p := range posts {
		score := p.SentimentScore
		if score > 1 {
			score = 1
		} else if score < -1 {
			score = -1
		}
		total += score
	}
	avg := total / float64(len(posts))
	category := "neutral"
	if avg >= 0.3 {
		category = "positive"
	} else if avg <= -0.3 {
		category = "negative"
	}
	return category, avg * 100
}

// calculateSplitSentiment calculates separate net sentiment percentages
// for root posts and reply posts. Returns (rootPct, replyPct).
// If a group has no posts, its percentage is 0.
func calculateSplitSentiment(posts []analyzer.AnalyzedPost) (float64, float64) {
	var rootTotal, replyTotal float64
	var rootCount, replyCount int

	for _, p := range posts {
		score := p.SentimentScore
		if score > 1 {
			score = 1
		} else if score < -1 {
			score = -1
		}
		if p.IsReply {
			replyTotal += score
			replyCount++
		} else {
			rootTotal += score
			rootCount++
		}
	}

	var rootPct, replyPct float64
	if rootCount > 0 {
		rootPct = (rootTotal / float64(rootCount)) * 100
	}
	if replyCount > 0 {
		replyPct = (replyTotal / float64(replyCount)) * 100
	}
	return rootPct, replyPct
}
