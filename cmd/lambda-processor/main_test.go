package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// --- calculateOverallSentimentWithCompoundScores ---

func TestCalculateOverallSentiment_Empty(t *testing.T) {
	h := &ProcessorHandler{}
	sentiment, pct := h.calculateOverallSentimentWithCompoundScores(nil)
	if sentiment != "neutral" {
		t.Errorf("got %q, want %q", sentiment, "neutral")
	}
	if pct != 0.0 {
		t.Errorf("got %f, want 0.0", pct)
	}
}

func TestCalculateOverallSentiment_Positive(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []analyzer.AnalyzedPost{
		{SentimentScore: 0.8},
		{SentimentScore: 0.6},
		{SentimentScore: 0.5},
	}
	// Average = (0.8 + 0.6 + 0.5) / 3 = 0.633... >= 0.3 → positive
	sentiment, _ := h.calculateOverallSentimentWithCompoundScores(posts)
	if sentiment != "positive" {
		t.Errorf("got %q, want %q", sentiment, "positive")
	}
}

func TestCalculateOverallSentiment_Negative(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []analyzer.AnalyzedPost{
		{SentimentScore: -0.8},
		{SentimentScore: -0.6},
		{SentimentScore: -0.5},
	}
	// Average = -0.633... <= -0.3 → negative
	sentiment, _ := h.calculateOverallSentimentWithCompoundScores(posts)
	if sentiment != "negative" {
		t.Errorf("got %q, want %q", sentiment, "negative")
	}
}

func TestCalculateOverallSentiment_Neutral(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []analyzer.AnalyzedPost{
		{SentimentScore: 0.1},
		{SentimentScore: -0.1},
		{SentimentScore: 0.0},
	}
	// Average = 0.0 → neutral
	sentiment, _ := h.calculateOverallSentimentWithCompoundScores(posts)
	if sentiment != "neutral" {
		t.Errorf("got %q, want %q", sentiment, "neutral")
	}
}

func TestCalculateOverallSentiment_Clamping(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []analyzer.AnalyzedPost{
		{SentimentScore: 5.0},  // Should be clamped to 1.0
		{SentimentScore: -5.0}, // Should be clamped to -1.0
	}
	// After clamping: (1.0 + -1.0) / 2 = 0.0 → neutral
	sentiment, pct := h.calculateOverallSentimentWithCompoundScores(posts)
	if sentiment != "neutral" {
		t.Errorf("got %q, want %q", sentiment, "neutral")
	}
	if pct != 0.0 {
		t.Errorf("net percentage: got %f, want 0.0", pct)
	}
}

func TestCalculateOverallSentiment_SinglePost(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []analyzer.AnalyzedPost{
		{SentimentScore: 0.9},
	}
	sentiment, pct := h.calculateOverallSentimentWithCompoundScores(posts)
	if sentiment != "positive" {
		t.Errorf("got %q, want %q", sentiment, "positive")
	}
	// netSentimentPercentage = averageCompoundScore * 100 = 90.0
	if pct != 90.0 {
		t.Errorf("net percentage: got %f, want 90.0", pct)
	}
}

func TestCalculateOverallSentiment_NetPercentage(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []analyzer.AnalyzedPost{
		{SentimentScore: -0.5},
	}
	_, pct := h.calculateOverallSentimentWithCompoundScores(posts)
	// netSentimentPercentage = -0.5 * 100 = -50.0
	if pct != -50.0 {
		t.Errorf("net percentage: got %f, want -50.0", pct)
	}
}

// --- getTopPosts ---

func TestGetTopPosts_Empty(t *testing.T) {
	h := &ProcessorHandler{}
	result := h.getTopPosts(nil, 5)
	if len(result) != 0 {
		t.Errorf("expected 0 posts, got %d", len(result))
	}
}

func TestGetTopPosts_FewerThanN(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []state.Post{
		{Author: "a", EngagementScore: 10},
		{Author: "b", EngagementScore: 20},
	}
	result := h.getTopPosts(posts, 5)
	if len(result) != 2 {
		t.Errorf("expected 2 posts, got %d", len(result))
	}
}

func TestGetTopPosts_ExactlyN(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []state.Post{
		{Author: "a", EngagementScore: 10},
		{Author: "b", EngagementScore: 20},
		{Author: "c", EngagementScore: 30},
	}
	result := h.getTopPosts(posts, 3)
	if len(result) != 3 {
		t.Errorf("expected 3 posts, got %d", len(result))
	}
}

func TestGetTopPosts_ReturnsTopN(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []state.Post{
		{Author: "low", EngagementScore: 5},
		{Author: "high", EngagementScore: 100},
		{Author: "mid", EngagementScore: 50},
		{Author: "very-high", EngagementScore: 200},
		{Author: "medium", EngagementScore: 75},
	}
	result := h.getTopPosts(posts, 3)
	if len(result) != 3 {
		t.Fatalf("expected 3 posts, got %d", len(result))
	}

	// Should be sorted descending by engagement
	if result[0].EngagementScore != 200 {
		t.Errorf("top post score: got %f, want 200", result[0].EngagementScore)
	}
	if result[1].EngagementScore != 100 {
		t.Errorf("second post score: got %f, want 100", result[1].EngagementScore)
	}
	if result[2].EngagementScore != 75 {
		t.Errorf("third post score: got %f, want 75", result[2].EngagementScore)
	}
}

// --- filterPostsByCutoffTime ---

func TestFilterPostsByCutoffTime_Empty(t *testing.T) {
	h := &ProcessorHandler{}
	cutoff := time.Now()
	result := h.filterPostsByCutoffTime(nil, cutoff)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestFilterPostsByCutoffTime_AllAfter(t *testing.T) {
	h := &ProcessorHandler{}
	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	posts := []state.Post{
		{CreatedAt: "2025-06-01T12:00:00Z"},
		{CreatedAt: "2025-06-02T12:00:00Z"},
	}
	result := h.filterPostsByCutoffTime(posts, cutoff)
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestFilterPostsByCutoffTime_AllBefore(t *testing.T) {
	h := &ProcessorHandler{}
	cutoff := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	posts := []state.Post{
		{CreatedAt: "2025-01-01T12:00:00Z"},
		{CreatedAt: "2025-06-01T12:00:00Z"},
	}
	result := h.filterPostsByCutoffTime(posts, cutoff)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestFilterPostsByCutoffTime_Mixed(t *testing.T) {
	h := &ProcessorHandler{}
	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	posts := []state.Post{
		{Author: "before", CreatedAt: "2025-05-01T12:00:00Z"},
		{Author: "after", CreatedAt: "2025-07-01T12:00:00Z"},
		{Author: "exact", CreatedAt: "2025-06-01T00:00:00Z"}, // Exactly at cutoff = included
	}
	result := h.filterPostsByCutoffTime(posts, cutoff)
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestFilterPostsByCutoffTime_InvalidTimestamp(t *testing.T) {
	h := &ProcessorHandler{}
	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	posts := []state.Post{
		{Author: "valid", CreatedAt: "2025-06-01T12:00:00Z"},
		{Author: "invalid", CreatedAt: "not-a-date"},
	}
	result := h.filterPostsByCutoffTime(posts, cutoff)
	if len(result) != 1 {
		t.Errorf("expected 1 (invalid skipped), got %d", len(result))
	}
	if result[0].Author != "valid" {
		t.Errorf("expected valid post, got %q", result[0].Author)
	}
}

// --- deduplicatePostsByURI ---

func TestDeduplicatePostsByURI_Empty(t *testing.T) {
	h := &ProcessorHandler{}
	result := h.deduplicatePostsByURI(nil)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestDeduplicatePostsByURI_NoDuplicates(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []state.Post{
		{URI: "at://a", Likes: 10},
		{URI: "at://b", Likes: 20},
	}
	result := h.deduplicatePostsByURI(posts)
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestDeduplicatePostsByURI_KeepsHigherEngagement(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []state.Post{
		{URI: "at://same", Likes: 5, Reposts: 0, Replies: 0},
		{URI: "at://same", Likes: 50, Reposts: 10, Replies: 5},
	}
	result := h.deduplicatePostsByURI(posts)
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	// Should keep the one with higher engagement (50+10+5=65 > 5)
	if result[0].Likes != 50 {
		t.Errorf("should keep higher engagement post, got Likes=%d", result[0].Likes)
	}
}

func TestDeduplicatePostsByURI_SkipsEmptyURI(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []state.Post{
		{URI: "", Likes: 100},
		{URI: "at://real", Likes: 10},
	}
	result := h.deduplicatePostsByURI(posts)
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0].URI != "at://real" {
		t.Errorf("expected real URI, got %q", result[0].URI)
	}
}

// --- fixPostURIs ---

func TestFixPostURIs_NormalURIs(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []state.Post{
		{URI: "at://did:plc:abc/app.bsky.feed.post/123"},
		{URI: "at://did:plc:xyz/app.bsky.feed.post/456"},
	}
	result := h.fixPostURIs(posts)
	if result[0].URI != "at://did:plc:abc/app.bsky.feed.post/123" {
		t.Errorf("normal URI should be unchanged, got %q", result[0].URI)
	}
	if result[1].URI != "at://did:plc:xyz/app.bsky.feed.post/456" {
		t.Errorf("normal URI should be unchanged, got %q", result[1].URI)
	}
}

func TestFixPostURIs_OldFormat(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []state.Post{
		{URI: "at://post-12345"},
		{URI: "at://post-67890"},
	}
	result := h.fixPostURIs(posts)
	if result[0].URI != "" {
		t.Errorf("old format URI should be emptied, got %q", result[0].URI)
	}
	if result[1].URI != "" {
		t.Errorf("old format URI should be emptied, got %q", result[1].URI)
	}
}

func TestFixPostURIs_Mixed(t *testing.T) {
	h := &ProcessorHandler{}
	posts := []state.Post{
		{URI: "at://did:plc:abc/app.bsky.feed.post/123"},
		{URI: "at://post-old"},
		{URI: "at://did:plc:xyz/app.bsky.feed.post/456"},
	}
	result := h.fixPostURIs(posts)
	if result[0].URI != "at://did:plc:abc/app.bsky.feed.post/123" {
		t.Errorf("post[0] should be unchanged")
	}
	if result[1].URI != "" {
		t.Errorf("post[1] old format should be emptied, got %q", result[1].URI)
	}
	if result[2].URI != "at://did:plc:xyz/app.bsky.feed.post/456" {
		t.Errorf("post[2] should be unchanged")
	}
}

// --- formatDuration ---

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{"1 second", 1 * time.Second, "1 second"},
		{"30 seconds", 30 * time.Second, "30 seconds"},
		{"1 minute", 1 * time.Minute, "1 minute"},
		{"5 minutes", 5 * time.Minute, "5 minutes"},
		{"1 hour", 1 * time.Hour, "1 hour"},
		{"2 hours", 2 * time.Hour, "2 hours"},
		{"1 hour 1 minute", 1*time.Hour + 1*time.Minute, "1 hour 1 minute"},
		{"1 hour 30 minutes", 1*time.Hour + 30*time.Minute, "1 hour 30 minutes"},
		{"2 hours 15 minutes", 2*time.Hour + 15*time.Minute, "2 hours 15 minutes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, result, tt.expected)
			}
		})
	}
}

// --- JSON serialization ---

func TestProcessorEvent_JSON(t *testing.T) {
	jsonData := `{"runId": "run-abc", "analysisIntervalMinutes": 30, "status": "pending"}`
	var event ProcessorEvent
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if event.RunID != "run-abc" {
		t.Errorf("RunID: got %q, want %q", event.RunID, "run-abc")
	}
	if event.AnalysisIntervalMinutes != 30 {
		t.Errorf("AnalysisIntervalMinutes: got %d, want 30", event.AnalysisIntervalMinutes)
	}
	if event.Status != "pending" {
		t.Errorf("Status: got %q, want %q", event.Status, "pending")
	}
}

func TestProcessorResponse_JSON(t *testing.T) {
	resp := Response{
		StatusCode:       200,
		Body:             "success",
		PostsAnalyzed:    1500,
		TopPostsCount:    5,
		OverallSentiment: "positive",
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded.PostsAnalyzed != 1500 {
		t.Errorf("PostsAnalyzed: got %d, want 1500", decoded.PostsAnalyzed)
	}
	if decoded.OverallSentiment != "positive" {
		t.Errorf("OverallSentiment: got %q, want %q", decoded.OverallSentiment, "positive")
	}
}
