package formatter

import (
	"testing"
)

func TestDetermineTier(t *testing.T) {
	tests := []struct {
		name      string
		sentiment float64
		expected  int
	}{
		{name: "extreme negative", sentiment: -5.0, expected: 1},
		{name: "threshold extreme negative", sentiment: 0.0, expected: 2}, // 0.0 is the start of Tier 2
		{name: "unusually low", sentiment: 5.0, expected: 2},
		{name: "unusually low top", sentiment: 8.49, expected: 2},
		{name: "below average start", sentiment: 8.5, expected: 3},
		{name: "below average", sentiment: 9.0, expected: 3},
		{name: "typical low", sentiment: 9.75, expected: 4},
		{name: "typical median hour", sentiment: 10.6, expected: 4},
		{name: "typical top", sentiment: 11.49, expected: 4},
		{name: "above average start", sentiment: 11.5, expected: 5},
		{name: "above average", sentiment: 12.0, expected: 5},
		{name: "unusually high start", sentiment: 12.75, expected: 6},
		{name: "unusually high", sentiment: 14.0, expected: 6},
		{name: "extreme positive start", sentiment: 15.0, expected: 7},
		{name: "extreme positive", sentiment: 20.0, expected: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineTier(tt.sentiment)
			if result != tt.expected {
				t.Errorf("determineTier(%f) = %d, expected %d", tt.sentiment, result, tt.expected)
			}
		})
	}
}

func TestGetMoodWord100_TierBoundaries(t *testing.T) {
	// Test that tier boundaries select the correct first/last words in each tier
	tests := []struct {
		name         string
		sentiment    float64
		expectedTier int
		expectedWord string
	}{
		// Tier 1: Extreme Negative (< 0%) - words 0-4
		{name: "tier 1 clamp", sentiment: -10.0, expectedTier: 1, expectedWord: "hostile"},
		{name: "tier 1 top", sentiment: -0.01, expectedTier: 1, expectedWord: "miserable"},
		// Tier 2: Unusually Low (0% to < 8.5%) - words 5-19
		{name: "tier 2 start", sentiment: 0.0, expectedTier: 2, expectedWord: "despondent"},
		{name: "tier 2 clamp", sentiment: 3.5, expectedTier: 2, expectedWord: "despondent"},
		{name: "tier 2 lowest full-size cycle", sentiment: 3.68, expectedTier: 2, expectedWord: "despondent"},
		{name: "tier 2 top", sentiment: 8.49, expectedTier: 2, expectedWord: "subdued"},
		// Tier 3: Below Average (8.5% to < 9.75%) - words 20-34
		{name: "tier 3 start", sentiment: 8.5, expectedTier: 3, expectedWord: "flat"},
		{name: "tier 3 top", sentiment: 9.74, expectedTier: 3, expectedWord: "reflective"},
		// Tier 4: Typical (9.75% to < 11.5%) - words 35-64
		{name: "tier 4 start", sentiment: 9.75, expectedTier: 4, expectedWord: "calm"},
		{name: "tier 4 top", sentiment: 11.49, expectedTier: 4, expectedWord: "settled"},
		// Tier 5: Above Average (11.5% to < 12.75%) - words 65-79
		{name: "tier 5 start", sentiment: 11.5, expectedTier: 5, expectedWord: "happy"},
		{name: "tier 5 top", sentiment: 12.74, expectedTier: 5, expectedWord: "bright"},
		// Tier 6: Unusually High (12.75% to < 15%) - words 80-94
		{name: "tier 6 start", sentiment: 12.75, expectedTier: 6, expectedWord: "excited"},
		{name: "tier 6 top", sentiment: 14.99, expectedTier: 6, expectedWord: "buzzing"},
		// Tier 7: Extreme Positive (>= 15%) - words 95-99
		{name: "tier 7 start", sentiment: 15.0, expectedTier: 7, expectedWord: "celebratory"},
		{name: "tier 7 clamp", sentiment: 30.0, expectedTier: 7, expectedWord: "euphoric"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier := determineTier(tt.sentiment)
			if tier != tt.expectedTier {
				t.Errorf("determineTier(%f) = %d, expected %d", tt.sentiment, tier, tt.expectedTier)
			}

			result := getMoodWord100(tt.sentiment)
			if result != tt.expectedWord {
				t.Errorf("getMoodWord100(%f) [Tier %d] = %q, expected %q", tt.sentiment, tier, result, tt.expectedWord)
			}
		})
	}
}

func tierWordSet(tier int) map[string]bool {
	r := tierRanges[tier]
	set := make(map[string]bool, r[1]-r[0]+1)
	for i := r[0]; i <= r[1]; i++ {
		set[calibratedWords[i]] = true
	}
	return set
}

func TestGetMoodWord100_WordsWithinTier(t *testing.T) {
	// Sample values across each tier must map to a word from that tier.
	tests := []struct {
		sentiment    float64
		expectedTier int
	}{
		{-5.0, 1},
		{3.0, 2},
		{7.5, 2},
		{9.0, 3},
		{10.0, 4},
		{11.0, 4},
		{12.0, 5},
		{13.5, 6},
		{14.5, 6},
		{16.0, 7},
		{25.0, 7},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := getMoodWord100(tt.sentiment)
			if !tierWordSet(tt.expectedTier)[result] {
				t.Errorf("getMoodWord100(%f) = %q, not in Tier %d words", tt.sentiment, result, tt.expectedTier)
			}
		})
	}
}

// TestGetMoodWord100_EveryWordReachable guards against the interpolation
// off-by-one that once made the last word of every half-open tier unreachable.
func TestGetMoodWord100_EveryWordReachable(t *testing.T) {
	seen := make(map[string]bool, len(calibratedWords))
	for s := -12.0; s <= 22.0; s += 0.001 {
		seen[getMoodWord100(s)] = true
	}
	for i, w := range calibratedWords {
		if !seen[w] {
			t.Errorf("word %d %q is never returned by getMoodWord100", i, w)
		}
	}
}

// TestGetMoodWord100_Monotonic checks that rising sentiment never moves
// backwards through the word list, within or across tiers.
func TestGetMoodWord100_Monotonic(t *testing.T) {
	index := make(map[string]int, len(calibratedWords))
	for i, w := range calibratedWords {
		index[w] = i
	}
	prev := -1
	for s := -12.0; s <= 22.0; s += 0.01 {
		idx := index[getMoodWord100(s)]
		if idx < prev {
			t.Fatalf("word index went backwards at sentiment %.2f: %d -> %d", s, prev, idx)
		}
		prev = idx
	}
}

func TestGetMoodWord100_HistoricalValues(t *testing.T) {
	// Real per-cycle values from prod sentiment_history (hourly era) plus
	// the 2025 holiday extremes from daily_sentiment. Tier expectations
	// follow docs/SENTIMENT_CALIBRATION_REVIEW_2026-09.md.
	tests := []struct {
		name         string
		sentiment    float64
		expectedTier int
	}{
		{name: "hourly-era median cycle", sentiment: 10.60, expectedTier: 4},
		{name: "hourly-era p5", sentiment: 8.48, expectedTier: 2},
		{name: "hourly-era p25", sentiment: 9.85, expectedTier: 4},
		{name: "hourly-era p75", sentiment: 11.35, expectedTier: 4},
		{name: "hourly-era p95", sentiment: 12.67, expectedTier: 5},
		{name: "hourly-era p99", sentiment: 14.48, expectedTier: 6},
		{name: "highest hourly cycle 2026-06-06", sentiment: 16.58, expectedTier: 7},
		{name: "March 2026 monthly mean", sentiment: 9.40, expectedTier: 3},
		{name: "lowest cycle 2026-02-28 (30-min era)", sentiment: 2.86, expectedTier: 2},
		{name: "lowest full-size hourly cycle 2026-04-07", sentiment: 3.68, expectedTier: 2},
		{name: "Christmas 2025 daily avg", sentiment: 19.77, expectedTier: 7},
		{name: "New Year 2026 30-min peak", sentiment: 26.64, expectedTier: 7},
		{name: "only negative cycle 2025-12-22", sentiment: -4.53, expectedTier: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier := determineTier(tt.sentiment)
			if tier != tt.expectedTier {
				t.Errorf("determineTier(%.2f) = %d, expected %d", tt.sentiment, tier, tt.expectedTier)
			}
			result := getMoodWord100(tt.sentiment)
			if result == "" {
				t.Errorf("getMoodWord100(%.2f) returned empty string", tt.sentiment)
			}
			t.Logf("Sentiment %.2f%% -> Tier %d -> %q", tt.sentiment, tier, result)
		})
	}
}

func TestCalibratedWordsLength(t *testing.T) {
	if len(calibratedWords) != 100 {
		t.Errorf("calibratedWords has %d words, expected 100", len(calibratedWords))
	}
	seen := make(map[string]bool, len(calibratedWords))
	for _, w := range calibratedWords {
		if seen[w] {
			t.Errorf("duplicate word %q", w)
		}
		seen[w] = true
	}
}

func TestTierWordCounts(t *testing.T) {
	expectedCounts := map[int]int{
		1: 5,  // Extreme Negative
		2: 15, // Unusually Low
		3: 15, // Below Average
		4: 30, // Typical
		5: 15, // Above Average
		6: 15, // Unusually High
		7: 5,  // Extreme Positive
	}

	for tier, expected := range expectedCounts {
		wordRange := tierRanges[tier]
		count := wordRange[1] - wordRange[0] + 1
		if count != expected {
			t.Errorf("Tier %d has %d words, expected %d", tier, count, expected)
		}
	}
}

func TestTierBoundsMatchThresholds(t *testing.T) {
	// Interior tier bounds must line up with the threshold constants so
	// interpolation covers exactly the values that land in the tier.
	checks := []struct {
		tier     int
		min, max float64
	}{
		{3, ThresholdUnusuallyLow, ThresholdBelowAverage},
		{4, ThresholdBelowAverage, ThresholdTypical},
		{5, ThresholdTypical, ThresholdAboveAverage},
		{6, ThresholdAboveAverage, ThresholdUnusuallyHigh},
	}
	for _, c := range checks {
		b := tierBounds[c.tier]
		if b[0] != c.min || b[1] != c.max {
			t.Errorf("tier %d bounds = %v, expected [%v %v]", c.tier, b, c.min, c.max)
		}
	}
	if tierBounds[1][1] != ThresholdExtremeNegative || tierBounds[2][1] != ThresholdUnusuallyLow || tierBounds[7][0] != ThresholdUnusuallyHigh {
		t.Errorf("open-ended tier bounds do not meet their thresholds: %v %v %v", tierBounds[1], tierBounds[2], tierBounds[7])
	}
}
