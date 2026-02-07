package formatter

import (
	"strings"
	"testing"
)

func TestGetSentimentSymbol(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"positive", "+"},
		{"negative", "-"},
		{"neutral", "x"},
		{"", "x"},
		{"unknown", "x"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := getSentimentSymbol(tt.input)
			if result != tt.expected {
				t.Errorf("getSentimentSymbol(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFormatPostContent_EmptyPosts(t *testing.T) {
	result := FormatPostContent(nil, "positive", 30, 1000, 12.0)
	// Should contain the mood hashtag and sentiment line but no numbered posts
	if !strings.Contains(result, "Bluesky is #") {
		t.Errorf("expected header, got %q", result)
	}
	if !strings.Contains(result, "% sentiment") {
		t.Errorf("expected sentiment percentage, got %q", result)
	}
	if strings.Contains(result, "1.") {
		t.Errorf("should have no numbered posts, got %q", result)
	}
}

func TestFormatPostContent_SinglePost(t *testing.T) {
	posts := []Post{
		{Author: "alice.bsky.social", Sentiment: "positive"},
	}
	result := FormatPostContent(posts, "positive", 30, 1000, 12.0)
	if !strings.Contains(result, "1. @alice.bsky.social +") {
		t.Errorf("expected numbered post with + symbol, got %q", result)
	}
}

func TestFormatPostContent_FivePosts(t *testing.T) {
	posts := []Post{
		{Author: "a", Sentiment: "positive"},
		{Author: "b", Sentiment: "negative"},
		{Author: "c", Sentiment: "neutral"},
		{Author: "d", Sentiment: "positive"},
		{Author: "e", Sentiment: "negative"},
	}
	result := FormatPostContent(posts, "neutral", 30, 5000, 11.0)

	if !strings.Contains(result, "1. @a +") {
		t.Errorf("missing post 1")
	}
	if !strings.Contains(result, "2. @b -") {
		t.Errorf("missing post 2")
	}
	if !strings.Contains(result, "3. @c x") {
		t.Errorf("missing post 3")
	}
	if !strings.Contains(result, "4. @d +") {
		t.Errorf("missing post 4")
	}
	if !strings.Contains(result, "5. @e -") {
		t.Errorf("missing post 5")
	}
}

func TestFormatPostContent_PositiveNetSentiment(t *testing.T) {
	result := FormatPostContent(nil, "positive", 30, 1000, 15.5)
	if !strings.Contains(result, "+15.5% sentiment") {
		t.Errorf("expected positive sign, got %q", result)
	}
}

func TestFormatPostContent_NegativeNetSentiment(t *testing.T) {
	result := FormatPostContent(nil, "negative", 30, 1000, -5.3)
	if !strings.Contains(result, "-5.3% sentiment") {
		t.Errorf("expected negative sign (no +), got %q", result)
	}
}

func TestFormatPostContent_ZeroNetSentiment(t *testing.T) {
	result := FormatPostContent(nil, "neutral", 30, 1000, 0.0)
	// Zero should not have + prefix
	if strings.Contains(result, "+0.0") {
		t.Errorf("zero sentiment should not have + prefix, got %q", result)
	}
	if !strings.Contains(result, "0.0% sentiment") {
		t.Errorf("expected 0.0%% sentiment, got %q", result)
	}
}

func TestFormatPostContent_HeaderFormat(t *testing.T) {
	result := FormatPostContent(nil, "positive", 30, 1000, 12.0)
	lines := strings.Split(result, "\n")
	// First line: "Bluesky is #<word>"
	if !strings.HasPrefix(lines[0], "Bluesky is #") {
		t.Errorf("first line should start with 'Bluesky is #', got %q", lines[0])
	}
	// Second line: sentiment percentage
	if !strings.Contains(lines[1], "% sentiment") {
		t.Errorf("second line should contain sentiment, got %q", lines[1])
	}
}
