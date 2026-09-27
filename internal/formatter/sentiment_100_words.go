package formatter

// The mood hashtag is one of 22 words (calibratedWords) spread over seven
// percentile tiers; since the 2026-09-28 review the list holds only mood words
// and each word spans at least the typical hour-to-hour move of the series.
// The tiers, thresholds and interpolation below predate that change.
//
// Sentiment thresholds: the September 2026 percentile boundaries of per-cycle
// net sentiment from prod sentiment_history (hourly-cycle era, Mar–Sep 2026,
// 4,446 cycles), shifted up by the stock→emoji-aware realignment of
// 2026-09-11 and rounded to the nearest 0.25.
//
// The percentiles the unshifted boundaries came from:
//
//	Tier 1 < 0 (never observed), Tier 2 0–p5, Tier 3 p5–p22,
//	Tier 4 p22–p78, Tier 5 p78–p95, Tier 6 p95–p99.5, Tier 7 top 0.5%.
//
// The shift S is the mean of emoji-aware minus stock over the paired cycles,
// 1.77 points at the time of the switch; the value actually applied to the
// history is recorded in key_value under sentiment_realign_shift.
//
// See docs/SENTIMENT_REALIGNMENT_PLAN.md for the switch,
// docs/SENTIMENT_CALIBRATION_REVIEW_2026-09.md for the percentile analysis and
// docs/SENTIMENT_CALIBRATION_ANALYSIS.md for the original Jan 2026 design.
// RealignShift is the stock→emoji-aware shift the thresholds below were
// derived from: the mean of (emoji-aware minus stock VADER) over the paired
// cycles as measured on 2026-09-10. cmd/realign recomputes S from the live
// database and compares it against this value before it rewrites history, so
// the thresholds and the stored series can never drift apart silently.
const RealignShift = 1.77

const (
	ThresholdExtremeNegative = 0.0   // Below: Extreme Negative (Tier 1)
	ThresholdUnusuallyLow    = 10.25 // Below: Unusually Low (Tier 2)
	ThresholdBelowAverage    = 11.5  // Below: Below Average (Tier 3)
	ThresholdTypical         = 13.25 // Below: Typical (Tier 4)
	ThresholdAboveAverage    = 14.5  // Below: Above Average (Tier 5)
	ThresholdUnusuallyHigh   = 16.75 // Below: Unusually High (Tier 6)
	// >= 16.75: Extreme Positive (Tier 7)
)

// Tier word ranges (start index, end index) - indices are inclusive
var tierRanges = map[int][2]int{
	1: {0, 1},   // Extreme Negative: 2 words (indices 0-1)
	2: {2, 5},   // Unusually Low: 4 words (indices 2-5)
	3: {6, 8},   // Below Average: 3 words (indices 6-8)
	4: {9, 12},  // Typical: 4 words (indices 9-12)
	5: {13, 15}, // Above Average: 3 words (indices 13-15)
	6: {16, 18}, // Unusually High: 3 words (indices 16-18)
	7: {19, 21}, // Extreme Positive: 3 words (indices 19-21)
}

// Tier sentiment boundaries (min, max) for interpolation within tier.
// The open-ended tiers (1, 2, 7) clamp to the range actually observed so
// that every word in the tier is reachable: the lowest full-size hourly
// cycle so far is 5.45% realigned (3.68% as scored at the time, 2026-04-07),
// and 21.75% is above any hourly-era value. Tier 1's clamp is unshifted:
// no negative cycle exists on the realigned series either.
var tierBounds = map[int][2]float64{
	1: {-10.0, 0.0},   // Extreme Negative: clamp at -10 for interpolation
	2: {5.25, 10.25},  // Unusually Low: clamp at 5.25 for interpolation
	3: {10.25, 11.5},  // Below Average
	4: {11.5, 13.25},  // Typical
	5: {13.25, 14.5},  // Above Average
	6: {14.5, 16.75},  // Unusually High
	7: {16.75, 21.75}, // Extreme Positive: clamp at 21.75 for interpolation
}

// getMoodWord100 maps a sentiment percentage to one of the 22 mood words in
// calibratedWords using the seven percentile tiers above. The name predates
// the 2026-09-28 reduction from 100 words and is kept because callers and
// docs refer to it.
func getMoodWord100(netSentiment float64) string {
	// Determine which tier the sentiment falls into
	tier := determineTier(netSentiment)

	// Get the word range for this tier
	wordRange := tierRanges[tier]
	startIdx := wordRange[0]
	endIdx := wordRange[1]

	// Get the sentiment bounds for this tier
	bounds := tierBounds[tier]
	tierMin := bounds[0]
	tierMax := bounds[1]

	// Linear interpolation within the tier to select specific word
	// Clamp sentiment to tier bounds
	clampedSentiment := netSentiment
	if clampedSentiment < tierMin {
		clampedSentiment = tierMin
	}
	if clampedSentiment > tierMax {
		clampedSentiment = tierMax
	}

	// Calculate position within tier (0.0 to 1.0)
	var position float64
	if tierMax == tierMin {
		position = 0.5 // Avoid division by zero
	} else {
		position = (clampedSentiment - tierMin) / (tierMax - tierMin)
	}

	// Divide the tier into numWords equal-width slots so every word,
	// including the last one, owns a slice of the range. (Scaling by
	// numWords-1 made the final word of each half-open tier unreachable.)
	numWords := endIdx - startIdx + 1
	wordOffset := int(position * float64(numWords))
	if wordOffset >= numWords {
		wordOffset = numWords - 1
	}

	// NaN backstop: int(NaN) is implementation-defined, so pin the offset.
	if wordOffset < 0 {
		wordOffset = 0
	}

	return calibratedWords[startIdx+wordOffset]
}

// determineTier returns the tier number (1-7) based on sentiment value
func determineTier(sentiment float64) int {
	switch {
	case sentiment < ThresholdExtremeNegative:
		return 1 // Extreme Negative
	case sentiment < ThresholdUnusuallyLow:
		return 2 // Unusually Low
	case sentiment < ThresholdBelowAverage:
		return 3 // Below Average
	case sentiment < ThresholdTypical:
		return 4 // Typical
	case sentiment < ThresholdAboveAverage:
		return 5 // Above Average
	case sentiment < ThresholdUnusuallyHigh:
		return 6 // Unusually High
	default:
		return 7 // Extreme Positive
	}
}

// calibratedWords contains the 22 mood words, reduced from 100 in the
// 2026-09-28 review. Words are posted as a hashtag:
// "Bluesky is #___ +12.4% sentiment".
//
// Two things changed. Words that do not describe a mood (curious, witty,
// ironic, creative, engaged, ...) were removed, so every hashtag reads as how
// the network feels. And each word's span was sized to the hour-to-hour noise
// of the realigned series (median |Δ| 0.30 points, p90 0.78): the old middle
// tiers gave each word 0.06–0.08 points, so nearly every hour changed the word
// on noise alone, while spans of about 0.42–0.44 points in tiers 3–5 change it
// in roughly half of hours. The seven tiers, their thresholds and the
// interpolation are unchanged. The retired list is in
// docs/MOOD_WORDS_RETIRED_2026-09-28.md; the word-to-range table is
// analysis/sentiment_mood_words.csv.
//
// Within every tier the words are ordered by rising sentiment, so the words
// either side of a tier boundary are close neighbours in mood.
var calibratedWords = []string{
	// Tier 1: Extreme Negative (< 0%) - 2 words, 5.0 points each over the
	// -10..0 clamp. Never seen from a full-size hourly cycle; last observed
	// Dec 2025 in the 30-min era.
	"miserable", // 0
	"gloomy",    // 1

	// Tier 2: Unusually Low (0% to < 10.25%) - 4 words, 1.25 points each
	// over the 5.25..10.25 clamp; anything below 5.25 reads as "dejected".
	// About 1 hour in 20.
	"dejected", // 2
	"glum",     // 3
	"downcast", // 4
	"subdued",  // 5

	// Tier 3: Below Average (10.25% to < 11.5%) - 3 words, ~0.42 points each.
	"flat",  // 6
	"muted", // 7
	"quiet", // 8

	// Tier 4: Typical (11.5% to < 13.25%) - 4 words, ~0.44 points each.
	// The everyday hum of the network.
	"calm",    // 9
	"relaxed", // 10
	"content", // 11
	"warm",    // 12

	// Tier 5: Above Average (13.25% to < 14.5%) - 3 words, ~0.42 points each.
	"cheerful", // 13
	"upbeat",   // 14
	"hopeful",  // 15

	// Tier 6: Unusually High (14.5% to < 16.75%) - 3 words, 0.75 points each.
	"happy",     // 16
	"delighted", // 17
	"joyful",    // 18

	// Tier 7: Extreme Positive (>= 16.75%) - 3 words, ~1.67 points each over
	// the 16.75..21.75 clamp. Holidays, milestones, the best hour of an
	// exceptional day.
	"elated",   // 19
	"jubilant", // 20
	"euphoric", // 21
}
