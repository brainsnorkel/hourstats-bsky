package analyzer

import (
	"testing"
)

func TestSentimentAnalyzer(t *testing.T) {
	analyzer := New()

	tests := []struct {
		name     string
		text     string
		expected string
	}{
		{
			name:     "positive text",
			text:     "I love this new feature! It's amazing!",
			expected: "positive",
		},
		{
			name:     "negative text",
			text:     "This is terrible. I hate it so much.",
			expected: "negative",
		},
		{
			name:     "neutral text",
			text:     "The weather is okay today.",
			expected: "neutral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			post := Post{
				URI:       "test://post/1",
				Text:      tt.text,
				Author:    "testuser",
				Likes:     10,
				Reposts:   5,
				Replies:   2,
				CreatedAt: "2024-01-01T00:00:00Z",
			}

			analyzed, err := analyzer.analyzePost(post)
			if err != nil {
				t.Fatalf("analyzePost() error = %v", err)
			}

			if analyzed.Sentiment != tt.expected {
				t.Errorf("analyzePost() sentiment = %v (score: %f), want %v", analyzed.Sentiment, analyzed.SentimentScore, tt.expected)
			}
		})
	}
}

func TestExtractTopics(t *testing.T) {
	analyzer := New()

	tests := []struct {
		name     string
		text     string
		expected []string
	}{
		{
			name:     "hashtags and keywords",
			text:     "Great #tech news about #ai development!",
			expected: []string{"#tech", "technology", "#ai", "artificial intelligence", "news"},
		},
		{
			name:     "only hashtags",
			text:     "Check out this #music #art piece!",
			expected: []string{"#music", "music", "#art", "art"},
		},
		{
			name:     "no topics",
			text:     "Just a regular post about nothing special.",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topics := analyzer.extractTopics(tt.text)

			if len(topics) != len(tt.expected) {
				t.Errorf("extractTopics() length = %v, want %v. Got: %v", len(topics), len(tt.expected), topics)
			}

			// Check if all expected topics are present
			for _, expectedTopic := range tt.expected {
				found := false
				for _, topic := range topics {
					if topic == expectedTopic {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("extractTopics() missing topic: %v", expectedTopic)
				}
			}
		})
	}
}
