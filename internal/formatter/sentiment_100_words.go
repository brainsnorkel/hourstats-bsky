package formatter

// Sentiment thresholds calibrated to per-cycle net sentiment from prod
// sentiment_history, hourly-cycle era (Mar–Sep 2026, 4,446 cycles).
// Boundaries sit on distribution percentiles so tier names stay honest:
//
//	Tier 1 < 0 (never observed), Tier 2 0–p5, Tier 3 p5–p22,
//	Tier 4 p22–p78, Tier 5 p78–p95, Tier 6 p95–p99.5, Tier 7 top 0.5%.
//
// See docs/SENTIMENT_CALIBRATION_REVIEW_2026-09.md for the analysis and
// docs/SENTIMENT_CALIBRATION_ANALYSIS.md for the original Jan 2026 design.
const (
	ThresholdExtremeNegative = 0.0   // Below: Extreme Negative (Tier 1)
	ThresholdUnusuallyLow    = 8.5   // Below: Unusually Low (Tier 2)
	ThresholdBelowAverage    = 9.75  // Below: Below Average (Tier 3)
	ThresholdTypical         = 11.5  // Below: Typical (Tier 4)
	ThresholdAboveAverage    = 12.75 // Below: Above Average (Tier 5)
	ThresholdUnusuallyHigh   = 15.0  // Below: Unusually High (Tier 6)
	// >= 15.0: Extreme Positive (Tier 7)
)

// Tier word ranges (start index, end index) - indices are inclusive
var tierRanges = map[int][2]int{
	1: {0, 4},   // Extreme Negative: 5 words (indices 0-4)
	2: {5, 19},  // Unusually Low: 15 words (indices 5-19)
	3: {20, 34}, // Below Average: 15 words (indices 20-34)
	4: {35, 64}, // Typical: 30 words (indices 35-64)
	5: {65, 79}, // Above Average: 15 words (indices 65-79)
	6: {80, 94}, // Unusually High: 15 words (indices 80-94)
	7: {95, 99}, // Extreme Positive: 5 words (indices 95-99)
}

// Tier sentiment boundaries (min, max) for interpolation within tier.
// The open-ended tiers (1, 2, 7) clamp to the range actually observed so
// that every word in the tier is reachable: the lowest full-size hourly
// cycle so far is 3.68% (2026-04-07), and 20% is above any hourly-era value.
var tierBounds = map[int][2]float64{
	1: {-10.0, 0.0},  // Extreme Negative: clamp at -10 for interpolation
	2: {3.5, 8.5},    // Unusually Low: clamp at 3.5 for interpolation
	3: {8.5, 9.75},   // Below Average
	4: {9.75, 11.5},  // Typical
	5: {11.5, 12.75}, // Above Average
	6: {12.75, 15.0}, // Unusually High
	7: {15.0, 20.0},  // Extreme Positive: clamp at 20 for interpolation
}

// getMoodWord100 maps sentiment percentage to one of 100 descriptive words
// using a tier-based system calibrated to actual Bluesky sentiment distribution
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

// calibratedWords contains 100 words calibrated to actual Bluesky sentiment range.
// Words are posted as a hashtag: "Bluesky is #___ +10.6% sentiment".
// Within every tier the words are ordered by rising sentiment: the most
// intense negative word sits at the bottom of the negative tiers and the
// most intense positive word at the top of the positive tiers, so the words
// either side of a tier boundary are close neighbours in mood.
var calibratedWords = []string{
	// Tier 1: Extreme Negative (< 0%) - 5 words
	// Vibe: Actively hostile, toxic, or distressed. Never seen from a
	// full-size hourly cycle; last observed Dec 2025 in the 30-min era.
	"hostile",   // 0
	"angry",     // 1
	"dreadful",  // 2
	"grim",      // 3
	"miserable", // 4

	// Tier 2: Unusually Low (0% to < 8.5%) - 15 words
	// Vibe: Distinctly downbeat. About 1 hour in 20; the top of the tier
	// (~8%) is only mildly below normal, so the mildest words sit there.
	"despondent",  // 5
	"glum",        // 6
	"sullen",      // 7
	"somber",      // 8
	"melancholy",  // 9
	"pessimistic", // 10
	"cynical",     // 11
	"anxious",     // 12
	"agitated",    // 13
	"irritable",   // 14
	"tense",       // 15
	"uneasy",      // 16
	"restless",    // 17
	"weary",       // 18
	"subdued",     // 19

	// Tier 3: Below Average (8.5% to < 9.75%) - 15 words
	// Vibe: Lacking energy, muted, slightly downbeat. The "meh" zone.
	"flat",       // 20
	"downbeat",   // 21
	"tired",      // 22
	"sluggish",   // 23
	"solemn",     // 24
	"wary",       // 25
	"skeptical",  // 26
	"cautious",   // 27
	"uncertain",  // 28
	"ambivalent", // 29
	"distracted", // 30
	"reserved",   // 31
	"pensive",    // 32
	"quiet",      // 33
	"reflective", // 34

	// Tier 4: Typical (9.75% to < 11.5%) - 30 words
	// Vibe: The everyday hum of the network. Normal baseline mood.
	// Sub-group: Calm & Centered
	"calm",     // 35
	"chill",    // 36
	"mellow",   // 37
	"relaxed",  // 38
	"content",  // 39
	"peaceful", // 40
	"grounded", // 41
	"steady",   // 42
	// Sub-group: Curious & Thoughtful
	"curious",       // 43
	"inquisitive",   // 44
	"thoughtful",    // 45
	"introspective", // 46
	"speculative",   // 47
	"sentimental",   // 48
	"nostalgic",     // 49
	// Sub-group: Expressive & Social
	"playful",     // 50
	"mischievous", // 51
	"cheeky",      // 52
	"ironic",      // 53
	"witty",       // 54
	"candid",      // 55
	"sincere",     // 56
	"earnest",     // 57
	// Sub-group: Engaged & Balanced
	"easygoing", // 58
	"sociable",  // 59
	"engaged",   // 60
	"connected", // 61
	"alert",     // 62
	"balanced",  // 63
	"settled",   // 64

	// Tier 5: Above Average (11.5% to < 12.75%) - 15 words
	// Vibe: Genuinely positive and constructive. A good hour online.
	"happy",      // 65
	"cheerful",   // 66
	"upbeat",     // 67
	"positive",   // 68
	"optimistic", // 69
	"hopeful",    // 70
	"encouraged", // 71
	"pleased",    // 72
	"amused",     // 73
	"friendly",   // 74
	"warm",       // 75
	"welcoming",  // 76
	"lively",     // 77
	"supportive", // 78
	"bright",     // 79

	// Tier 6: Unusually High (12.75% to < 15%) - 15 words
	// Vibe: High-energy positivity, creativity, and excitement.
	"excited",      // 80
	"vibrant",      // 81
	"energetic",    // 82
	"enthusiastic", // 83
	"inspired",     // 84
	"creative",     // 85
	"joyful",       // 86
	"delighted",    // 87
	"thrilled",     // 88
	"invigorated",  // 89
	"passionate",   // 90
	"spirited",     // 91
	"exuberant",    // 92
	"buoyant",      // 93
	"buzzing",      // 94

	// Tier 7: Extreme Positive (>= 15%) - 5 words
	// Vibe: Peak collective experience. Holidays, milestones, and the
	// best hour or two of an exceptional day.
	"celebratory", // 95
	"jubilant",    // 96
	"elated",      // 97
	"ecstatic",    // 98
	"euphoric",    // 99
}
