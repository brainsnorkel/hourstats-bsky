package formatter

import (
	"testing"
)

func TestNormalCurveMapping(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected int
	}{
		{
			name:     "extreme negative",
			input:    0.0, // -100%
			expected: 0,
		},
		{
			name:     "neutral",
			input:    0.5, // 0%
			expected: 50,
		},
		{
			name:     "extreme positive",
			input:    1.0, // +100%
			expected: 100,
		},
		{
			name:     "mild negative",
			input:    0.3, // -40%
			expected: 13,  // Should be compressed toward middle
		},
		{
			name:     "mild positive",
			input:    0.7, // +40%
			expected: 86,  // Should be compressed toward middle
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalCurveMapping(tt.input)
			// Allow some tolerance for the curve mapping
			if result < tt.expected-5 || result > tt.expected+5 {
				t.Errorf("normalCurveMapping(%f) = %d, expected around %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetMoodWord100(t *testing.T) {
	tests := []struct {
		name      string
		sentiment float64
		expected  string
	}{
		{
			name:      "extreme negative",
			sentiment: -100.0,
			expected:  "hopeless",
		},
		{
			name:      "neutral",
			sentiment: 0.0,
			expected:  "calm",
		},
		{
			name:      "extreme positive",
			sentiment: 100.0,
			expected:  "heavenly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMoodWord100(tt.sentiment)
			if result != tt.expected {
				t.Errorf("getMoodWord100(%f) = %s, expected %s", tt.sentiment, result, tt.expected)
			}
		})
	}
}
