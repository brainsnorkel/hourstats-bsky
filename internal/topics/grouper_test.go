package topics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func geminiMockHandler(clusters []TopicCluster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		text, _ := json.Marshal(clusters)
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{
					Parts: []geminiPart{{Text: string(text)}},
				}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func TestGroupAndLabel_Success(t *testing.T) {
	expected := []TopicCluster{
		{
			Label:       "US Politics",
			Description: "Discussion about American politics",
			Keywords:    []string{"trump", "election", "congress"},
			Synonyms:    []string{"government", "political"},
		},
		{
			Label:       "Weather",
			Description: "Weather discussion",
			Keywords:    []string{"weather", "rain"},
			Synonyms:    []string{"storm"},
		},
	}

	srv := httptest.NewServer(geminiMockHandler(expected))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	terms := []TermScore{
		{Term: "trump", Score: 12.5},
		{Term: "election", Score: 10.3},
		{Term: "congress", Score: 8.1},
		{Term: "weather", Score: 7.0},
		{Term: "rain", Score: 5.5},
	}

	clusters, err := g.GroupAndLabel(context.Background(), terms)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(clusters))
	}
	if clusters[0].Label != "US Politics" {
		t.Errorf("expected label 'US Politics', got %q", clusters[0].Label)
	}
	if len(clusters[0].Keywords) != 3 {
		t.Errorf("expected 3 keywords, got %d", len(clusters[0].Keywords))
	}
}

func TestGroupAndLabel_EmptyTerms(t *testing.T) {
	g := NewGrouper("test-key")
	clusters, err := g.GroupAndLabel(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clusters != nil {
		t.Errorf("expected nil, got %v", clusters)
	}
}

func TestGroupAndLabel_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	terms := []TermScore{
		{Term: "trump", Score: 12.5},
		{Term: "election", Score: 10.3},
	}

	clusters, err := g.GroupAndLabel(context.Background(), terms)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) == 0 {
		t.Fatal("expected fallback clusters, got empty")
	}
	if clusters[0].Label != "Trump" {
		t.Errorf("expected fallback label 'Trump', got %q", clusters[0].Label)
	}
}

func TestGroupAndLabel_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{
					Parts: []geminiPart{{Text: "not valid json"}},
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	terms := []TermScore{{Term: "test", Score: 5.0}}

	clusters, err := g.GroupAndLabel(context.Background(), terms)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 fallback cluster, got %d", len(clusters))
	}
	if clusters[0].Keywords[0] != "test" {
		t.Errorf("expected fallback keyword 'test', got %q", clusters[0].Keywords[0])
	}
}

func TestGroupAndLabel_RateLimit(t *testing.T) {
	srv := httptest.NewServer(geminiMockHandler([]TopicCluster{
		{Label: "Test", Keywords: []string{"test"}, Synonyms: []string{}},
	}))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	terms := []TermScore{{Term: "test", Score: 5.0}}

	for i := 0; i < maxDailyCalls; i++ {
		_, err := g.GroupAndLabel(context.Background(), terms)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}

	clusters, err := g.GroupAndLabel(context.Background(), terms)
	if err != nil {
		t.Fatalf("unexpected error on rate-limited call: %v", err)
	}
	if clusters[0].Label != "Test" {
		t.Errorf("expected fallback label 'Test', got %q", clusters[0].Label)
	}
}

func TestGenerateAltText_Success(t *testing.T) {
	altBody := "Bluesky users are discussing US Politics and Weather today. The bump chart shows Politics holding steady at number one while Weather climbed from number three."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{
					Parts: []geminiPart{{Text: altBody}},
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "US Politics", Description: "American politics"}, PostCount: 500}, TopicID: "t1", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Weather", Description: "Weather discussion"}, PostCount: 300}, TopicID: "t2", Rank: 2},
	}
	trajectories := map[string][]int{"t1": {1, 1, 1}, "t2": {3, 2, 2}}

	alt := g.GenerateAltText(context.Background(), ranked, trajectories)
	if alt != altBody {
		t.Errorf("expected LLM alt text, got: %q", alt)
	}
}

func TestGenerateAltText_APIError_Fallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics"}}, TopicID: "t1", Rank: 1},
	}

	alt := g.GenerateAltText(context.Background(), ranked, nil)
	if alt != FormatAltText(ranked) {
		t.Errorf("expected fallback alt text, got: %q", alt)
	}
}

func TestGenerateAltText_TruncatesLongResponse(t *testing.T) {
	longText := ""
	for i := 0; i < 200; i++ {
		longText += "This is a very long sentence. "
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{
					Parts: []geminiPart{{Text: longText}},
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Test"}}, TopicID: "t1", Rank: 1},
	}

	alt := g.GenerateAltText(context.Background(), ranked, nil)
	if len(alt) > 1000 {
		t.Errorf("expected alt text truncated to 1000 chars, got %d", len(alt))
	}
}

func TestFallbackClusters(t *testing.T) {
	terms := []TermScore{
		{Term: "trump", Score: 12.5},
		{Term: "election", Score: 10.3},
		{Term: "weather", Score: 8.0},
		{Term: "sports", Score: 7.0},
		{Term: "music", Score: 6.0},
		{Term: "movies", Score: 5.0},
	}

	clusters := fallbackClusters(terms)
	if len(clusters) != TopTopics {
		t.Fatalf("expected %d clusters, got %d", TopTopics, len(clusters))
	}
	if clusters[0].Label != "Trump" {
		t.Errorf("expected 'Trump', got %q", clusters[0].Label)
	}
	if clusters[0].Keywords[0] != "trump" {
		t.Errorf("expected keyword 'trump', got %q", clusters[0].Keywords[0])
	}
	if len(clusters[0].Synonyms) != 0 {
		t.Errorf("expected empty synonyms, got %v", clusters[0].Synonyms)
	}
}
