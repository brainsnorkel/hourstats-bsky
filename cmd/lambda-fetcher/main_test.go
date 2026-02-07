package main

import (
	"encoding/json"
	"testing"

	bskyclient "github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func TestConvertToStatePosts_Empty(t *testing.T) {
	h := &FetcherHandler{}
	result := h.convertToStatePosts(nil)
	if len(result) != 0 {
		t.Errorf("expected 0 posts, got %d", len(result))
	}
}

func TestConvertToStatePosts_SinglePost(t *testing.T) {
	h := &FetcherHandler{}
	input := []bskyclient.Post{
		{
			URI:       "at://did:plc:abc/app.bsky.feed.post/123",
			CID:       "cid-123",
			Text:      "Hello world",
			Author:    "alice.bsky.social",
			Likes:     10,
			Reposts:   5,
			Replies:   3,
			CreatedAt: "2025-01-01T00:00:00Z",
			Sentiment: "positive",
		},
	}

	result := h.convertToStatePosts(input)

	if len(result) != 1 {
		t.Fatalf("expected 1 post, got %d", len(result))
	}

	post := result[0]
	if post.URI != input[0].URI {
		t.Errorf("URI: got %q, want %q", post.URI, input[0].URI)
	}
	if post.CID != input[0].CID {
		t.Errorf("CID: got %q, want %q", post.CID, input[0].CID)
	}
	if post.Text != input[0].Text {
		t.Errorf("Text: got %q, want %q", post.Text, input[0].Text)
	}
	if post.Author != input[0].Author {
		t.Errorf("Author: got %q, want %q", post.Author, input[0].Author)
	}
	if post.Likes != 10 {
		t.Errorf("Likes: got %d, want 10", post.Likes)
	}
	if post.Reposts != 5 {
		t.Errorf("Reposts: got %d, want 5", post.Reposts)
	}
	if post.Replies != 3 {
		t.Errorf("Replies: got %d, want 3", post.Replies)
	}
	if post.CreatedAt != input[0].CreatedAt {
		t.Errorf("CreatedAt: got %q, want %q", post.CreatedAt, input[0].CreatedAt)
	}
	if post.Sentiment != input[0].Sentiment {
		t.Errorf("Sentiment: got %q, want %q", post.Sentiment, input[0].Sentiment)
	}

	// EngagementScore = float64(Replies + Likes + Reposts) = 3 + 10 + 5 = 18
	expectedScore := float64(18)
	if post.EngagementScore != expectedScore {
		t.Errorf("EngagementScore: got %f, want %f", post.EngagementScore, expectedScore)
	}
}

func TestConvertToStatePosts_Multiple(t *testing.T) {
	h := &FetcherHandler{}
	input := []bskyclient.Post{
		{
			URI: "at://did:plc:1/app.bsky.feed.post/a", Author: "user1",
			Likes: 100, Reposts: 50, Replies: 25,
		},
		{
			URI: "at://did:plc:2/app.bsky.feed.post/b", Author: "user2",
			Likes: 0, Reposts: 0, Replies: 0,
		},
		{
			URI: "at://did:plc:3/app.bsky.feed.post/c", Author: "user3",
			Likes: 1, Reposts: 0, Replies: 0,
		},
	}

	result := h.convertToStatePosts(input)

	if len(result) != 3 {
		t.Fatalf("expected 3 posts, got %d", len(result))
	}

	tests := []struct {
		index         int
		expectedScore float64
	}{
		{0, 175}, // 100 + 50 + 25
		{1, 0},   // 0 + 0 + 0
		{2, 1},   // 1 + 0 + 0
	}

	for _, tt := range tests {
		if result[tt.index].EngagementScore != tt.expectedScore {
			t.Errorf("post[%d] EngagementScore: got %f, want %f",
				tt.index, result[tt.index].EngagementScore, tt.expectedScore)
		}
	}
}

func TestConvertToStatePosts_PreservesAllFields(t *testing.T) {
	h := &FetcherHandler{}
	input := []bskyclient.Post{
		{
			URI:             "at://did:plc:xyz/app.bsky.feed.post/456",
			CID:             "bafyreiabc",
			Text:            "Test post with emoji 🎉",
			Author:          "test.bsky.social",
			Likes:           42,
			Reposts:         7,
			Replies:         13,
			CreatedAt:       "2025-06-15T12:30:00Z",
			Sentiment:       "negative",
			EngagementScore: 999.0, // This should be IGNORED — recalculated
		},
	}

	result := h.convertToStatePosts(input)
	post := result[0]

	// The function recalculates engagement score, ignoring the input's EngagementScore
	expectedScore := float64(42 + 7 + 13) // 62
	if post.EngagementScore != expectedScore {
		t.Errorf("EngagementScore should be recalculated: got %f, want %f", post.EngagementScore, expectedScore)
	}

	// Verify the output type is state.Post
	var _ state.Post = post
}

func TestFetcherEvent_EventBridgeJSON(t *testing.T) {
	jsonData := `{"source": "aws.events", "time": "2025-01-01T00:00:00Z"}`
	var event FetcherEvent
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if event.Source != "aws.events" {
		t.Errorf("Source: got %q, want %q", event.Source, "aws.events")
	}
	if event.Time != "2025-01-01T00:00:00Z" {
		t.Errorf("Time: got %q, want %q", event.Time, "2025-01-01T00:00:00Z")
	}
	if event.RunID != "" {
		t.Errorf("RunID should be empty for EventBridge event, got %q", event.RunID)
	}
}

func TestFetcherEvent_DirectInvocationJSON(t *testing.T) {
	jsonData := `{"runId": "run-123456", "analysisIntervalMinutes": 30, "status": "pending"}`
	var event FetcherEvent
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if event.RunID != "run-123456" {
		t.Errorf("RunID: got %q, want %q", event.RunID, "run-123456")
	}
	if event.AnalysisIntervalMinutes != 30 {
		t.Errorf("AnalysisIntervalMinutes: got %d, want 30", event.AnalysisIntervalMinutes)
	}
	if event.Source != "" {
		t.Errorf("Source should be empty for direct invocation, got %q", event.Source)
	}
}

func TestFetcherEvent_CombinedJSON(t *testing.T) {
	jsonData := `{"source": "aws.events", "runId": "run-789", "analysisIntervalMinutes": 15}`
	var event FetcherEvent
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if event.Source != "aws.events" {
		t.Errorf("Source: got %q, want %q", event.Source, "aws.events")
	}
	if event.RunID != "run-789" {
		t.Errorf("RunID: got %q, want %q", event.RunID, "run-789")
	}
}

func TestResponse_JSONSerialization(t *testing.T) {
	resp := Response{
		StatusCode:     200,
		Body:           "Posts fetched successfully",
		PostsRetrieved: 1500,
		RunID:          "run-abc",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.StatusCode != 200 {
		t.Errorf("StatusCode: got %d, want 200", decoded.StatusCode)
	}
	if decoded.PostsRetrieved != 1500 {
		t.Errorf("PostsRetrieved: got %d, want 1500", decoded.PostsRetrieved)
	}
	if decoded.RunID != "run-abc" {
		t.Errorf("RunID: got %q, want %q", decoded.RunID, "run-abc")
	}
}

func TestResponse_EmptyRunIDOmitted(t *testing.T) {
	resp := Response{
		StatusCode:     400,
		Body:           "No runId",
		PostsRetrieved: 0,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// RunID should be omitted from JSON when empty (omitempty tag)
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}
	if _, exists := raw["runId"]; exists {
		t.Error("runId should be omitted from JSON when empty")
	}
}
