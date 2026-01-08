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
		{
			name:      "extreme negative",
			sentiment: -5.0,
			expected:  1,
		},
		{
			name:      "threshold extreme negative",
			sentiment: 0.0,
			expected:  2, // 0.0 is the start of Tier 2
		},
		{
			name:      "unusually low",
			sentiment: 5.0,
			expected:  2,
		},
		{
			name:      "below average",
			sentiment: 10.0,
			expected:  3,
		},
		{
			name:      "typical low",
			sentiment: 10.5,
			expected:  4,
		},
		{
			name:      "typical mid",
			sentiment: 11.5,
			expected:  4,
		},
		{
			name:      "above average",
			sentiment: 13.0,
			expected:  5,
		},
		{
			name:      "unusually high",
			sentiment: 15.0,
			expected:  6,
		},
		{
			name:      "extreme positive",
			sentiment: 20.0,
			expected:  7,
		},
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
		{
			name:         "tier 1 start",
			sentiment:    -10.0,
			expectedTier: 1,
			expectedWord: "angry", // first word in tier
		},
		// Tier 2: Unusually Low (0% to < 9.5%) - words 5-19
		{
			name:         "tier 2 start",
			sentiment:    0.0,
			expectedTier: 2,
			expectedWord: "anxious", // first word in tier
		},
		// Tier 3: Below Average (9.5% to < 10.5%) - words 20-34
		{
			name:         "tier 3 start",
			sentiment:    9.5,
			expectedTier: 3,
			expectedWord: "flat", // first word in tier
		},
		// Tier 4: Typical (10.5% to < 12.5%) - words 35-64
		{
			name:         "tier 4 start",
			sentiment:    10.5,
			expectedTier: 4,
			expectedWord: "calm", // first word in tier
		},
		// Tier 5: Above Average (12.5% to < 14%) - words 65-79
		{
			name:         "tier 5 start",
			sentiment:    12.5,
			expectedTier: 5,
			expectedWord: "happy", // first word in tier
		},
		// Tier 6: Unusually High (14% to < 18%) - words 80-94
		{
			name:         "tier 6 start",
			sentiment:    14.0,
			expectedTier: 6,
			expectedWord: "excited", // first word in tier
		},
		// Tier 7: Extreme Positive (>= 18%) - words 95-99
		{
			name:         "tier 7 start",
			sentiment:    18.0,
			expectedTier: 7,
			expectedWord: "euphoric", // first word in tier
		},
		{
			name:         "tier 7 max",
			sentiment:    30.0,
			expectedTier: 7,
			expectedWord: "celebratory", // last word in tier
		},
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

func TestGetMoodWord100_WordsWithinTier(t *testing.T) {
	// Test that interpolation within tiers produces reasonable word progression
	// We just verify the word is from the correct tier, not the exact word

	tierWords := map[int][]string{
		1: {"angry", "hostile", "grim", "miserable", "dreadful"},
		2: {"anxious", "agitated", "irritable", "tense", "pessimistic", "cynical", "uneasy", "restless", "glum", "sullen", "somber", "weary", "subdued", "melancholy", "despondent"},
		3: {"flat", "tired", "downbeat", "sluggish", "wary", "cautious", "skeptical", "reserved", "ambivalent", "uncertain", "distracted", "quiet", "pensive", "reflective", "solemn"},
		4: {"calm", "chill", "mellow", "relaxed", "content", "peaceful", "grounded", "steady", "curious", "inquisitive", "thoughtful", "introspective", "speculative", "sentimental", "nostalgic", "playful", "mischievous", "cheeky", "ironic", "witty", "candid", "sincere", "earnest", "easygoing", "sociable", "engaged", "connected", "alert", "balanced", "settled"},
		5: {"happy", "cheerful", "upbeat", "positive", "optimistic", "hopeful", "encouraged", "pleased", "amused", "friendly", "warm", "welcoming", "lively", "supportive", "bright"},
		6: {"excited", "vibrant", "energetic", "enthusiastic", "inspired", "creative", "joyful", "delighted", "thrilled", "invigorated", "passionate", "spirited", "exuberant", "buoyant", "buzzing"},
		7: {"euphoric", "ecstatic", "elated", "jubilant", "celebratory"},
	}

	tests := []struct {
		sentiment    float64
		expectedTier int
	}{
		{-5.0, 1},
		{3.0, 2},
		{7.5, 2},
		{10.0, 3},
		{11.0, 4},
		{12.0, 4},
		{13.0, 5},
		{15.0, 6},
		{17.0, 6},
		{20.0, 7},
		{25.0, 7},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := getMoodWord100(tt.sentiment)
			validWords := tierWords[tt.expectedTier]

			found := false
			for _, w := range validWords {
				if w == result {
					found = true
					break
				}
			}

			if !found {
				t.Errorf("getMoodWord100(%f) = %q, not in Tier %d words", tt.sentiment, result, tt.expectedTier)
			}
		})
	}
}

func TestGetMoodWord100_HistoricalValues(t *testing.T) {
	// Test against actual historical Bluesky sentiment values
	tests := []struct {
		name      string
		sentiment float64
		date      string
		tierName  string
	}{
		{
			name:      "Christmas 2025 (highest daily avg)",
			sentiment: 19.77,
			date:      "2025-12-25",
			tierName:  "Extreme Positive",
		},
		{
			name:      "New Year peak (highest intraday)",
			sentiment: 26.64,
			date:      "2026-01-01",
			tierName:  "Extreme Positive",
		},
		{
			name:      "Overall average",
			sentiment: 10.82,
			date:      "average",
			tierName:  "Typical",
		},
		{
			name:      "Lowest daily avg",
			sentiment: 6.14,
			date:      "2026-01-03",
			tierName:  "Unusually Low",
		},
		{
			name:      "Only negative intraday",
			sentiment: -4.53,
			date:      "2025-12-22",
			tierName:  "Extreme Negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMoodWord100(tt.sentiment)
			tier := determineTier(tt.sentiment)

			// Verify we got a non-empty word
			if result == "" {
				t.Errorf("getMoodWord100(%f) returned empty string", tt.sentiment)
			}

			// Log the result for manual verification
			t.Logf("Sentiment %.2f%% (%s) -> Tier %d (%s) -> %q",
				tt.sentiment, tt.date, tier, tt.tierName, result)
		})
	}
}

func TestCalibratedWordsLength(t *testing.T) {
	if len(calibratedWords) != 100 {
		t.Errorf("calibratedWords has %d words, expected 100", len(calibratedWords))
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
