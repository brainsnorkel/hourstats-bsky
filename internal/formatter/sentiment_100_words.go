package formatter

// Sentiment thresholds based on historical Bluesky data analysis (Sep 2025 - Jan 2026)
// See docs/SENTIMENT_CALIBRATION_ANALYSIS.md for full analysis
const (
	ThresholdExtremeNegative = 0.0  // Below: Extreme Negative (Tier 1)
	ThresholdUnusuallyLow    = 9.5  // Below: Unusually Low (Tier 2)
	ThresholdBelowAverage    = 10.5 // Below: Below Average (Tier 3)
	ThresholdTypical         = 12.5 // Below: Typical (Tier 4)
	ThresholdAboveAverage    = 14.0 // Below: Above Average (Tier 5)
	ThresholdUnusuallyHigh   = 18.0 // Below: Unusually High (Tier 6)
	// >= 18.0: Extreme Positive (Tier 7)
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

// Tier sentiment boundaries (min, max) for interpolation within tier
var tierBounds = map[int][2]float64{
	1: {-10.0, 0.0}, // Extreme Negative: clamp at -10 for interpolation
	2: {0.0, 9.5},   // Unusually Low
	3: {9.5, 10.5},  // Below Average
	4: {10.5, 12.5}, // Typical
	5: {12.5, 14.0}, // Above Average
	6: {14.0, 18.0}, // Unusually High
	7: {18.0, 30.0}, // Extreme Positive: clamp at 30 for interpolation
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

	// Map position to word index within tier
	numWords := endIdx - startIdx + 1
	wordOffset := int(position * float64(numWords-1))

	// Clamp to valid range
	wordIdx := startIdx + wordOffset
	if wordIdx < startIdx {
		wordIdx = startIdx
	}
	if wordIdx > endIdx {
		wordIdx = endIdx
	}

	return calibratedWords[wordIdx]
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

// calibratedWords contains 100 words calibrated to actual Bluesky sentiment range
// Words tested against: "Bluesky is feeling ___"
// Based on analysis of 116 days of historical data (Sep 2025 - Jan 2026)
var calibratedWords = []string{
	// Tier 1: Extreme Negative (< 0%) - 5 words
	// Vibe: Actively hostile, toxic, or distressed. Rare intraday events only.
	"angry",     // 0
	"hostile",   // 1
	"grim",      // 2
	"miserable", // 3
	"dreadful",  // 4

	// Tier 2: Unusually Low (0% to < 9.5%) - 15 words
	// Vibe: Strong pessimism, unhappiness, or tension. Distinctly negative atmosphere.
	"anxious",     // 5
	"agitated",    // 6
	"irritable",   // 7
	"tense",       // 8
	"pessimistic", // 9
	"cynical",     // 10
	"uneasy",      // 11
	"restless",    // 12
	"glum",        // 13
	"sullen",      // 14
	"somber",      // 15
	"weary",       // 16
	"subdued",     // 17
	"melancholy",  // 18
	"despondent",  // 19

	// Tier 3: Below Average (9.5% to < 10.5%) - 15 words
	// Vibe: Lacking energy, muted, slightly downbeat. The "meh" zone.
	"flat",       // 20
	"tired",      // 21
	"downbeat",   // 22
	"sluggish",   // 23
	"wary",       // 24
	"cautious",   // 25
	"skeptical",  // 26
	"reserved",   // 27
	"ambivalent", // 28
	"uncertain",  // 29
	"distracted", // 30
	"quiet",      // 31
	"pensive",    // 32
	"reflective", // 33
	"solemn",     // 34

	// Tier 4: Typical (10.5% to < 12.5%) - 30 words
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

	// Tier 5: Above Average (12.5% to < 14%) - 15 words
	// Vibe: Genuinely positive and constructive. A good day online.
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

	// Tier 6: Unusually High (14% to < 18%) - 15 words
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

	// Tier 7: Extreme Positive (>= 18%) - 5 words
	// Vibe: Peak collective experience; overwhelming joy. Holidays and milestones.
	"euphoric",    // 95
	"ecstatic",    // 96
	"elated",      // 97
	"jubilant",    // 98
	"celebratory", // 99
}
