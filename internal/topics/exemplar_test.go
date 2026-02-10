package topics

import (
	"context"
	"fmt"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type mockCandidateStore struct {
	candidates map[string][]store.ExemplarCandidate
	err        error
}

func (m *mockCandidateStore) GetExemplarCandidates(_ context.Context, keywords []string, _ string, limit int) ([]store.ExemplarCandidate, error) {
	if m.err != nil {
		return nil, m.err
	}
	seen := make(map[string]bool)
	var result []store.ExemplarCandidate
	for _, kw := range keywords {
		for _, c := range m.candidates[kw] {
			if !seen[c.URI] && len(result) < limit {
				seen[c.URI] = true
				result = append(result, c)
			}
		}
	}
	return result, nil
}

func TestHydrateExemplars_PicksHighestEngagement(t *testing.T) {
	s := &mockCandidateStore{
		candidates: map[string][]store.ExemplarCandidate{
			"politics": {
				{URI: "at://a/2", Handle: "high.bsky.social", Engagement: 175},
				{URI: "at://a/3", Handle: "mid.bsky.social", Engagement: 17},
				{URI: "at://a/1", Handle: "low.bsky.social", Engagement: 1},
			},
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics", Keywords: []string{"politics"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "at://a/2" {
		t.Errorf("expected highest engagement URI 'at://a/2', got %q", result[0].ExemplarURI)
	}
	if result[0].ExemplarHandle != "high.bsky.social" {
		t.Errorf("expected handle 'high.bsky.social', got %q", result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_NoCandidates(t *testing.T) {
	s := &mockCandidateStore{candidates: map[string][]store.ExemplarCandidate{}}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Empty", Keywords: []string{"nothing"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" {
		t.Errorf("expected empty exemplar URI, got %q", result[0].ExemplarURI)
	}
}

func TestHydrateExemplars_StoreError(t *testing.T) {
	s := &mockCandidateStore{err: fmt.Errorf("db error")}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Test", Keywords: []string{"test"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" {
		t.Errorf("expected empty exemplar after error, got %q", result[0].ExemplarURI)
	}
}

func TestHydrateExemplars_MultipleTopics(t *testing.T) {
	s := &mockCandidateStore{
		candidates: map[string][]store.ExemplarCandidate{
			"politics": {{URI: "at://a/1", Handle: "alice.bsky.social", Engagement: 65}},
			"weather":  {{URI: "at://b/1", Handle: "bob.bsky.social", Engagement: 130}},
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics", Keywords: []string{"politics"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Weather", Keywords: []string{"weather"}, Synonyms: []string{}}}, TopicID: "t2", Rank: 2},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "alice.bsky.social" {
		t.Errorf("expected 'alice.bsky.social', got %q", result[0].ExemplarHandle)
	}
	if result[1].ExemplarHandle != "bob.bsky.social" {
		t.Errorf("expected 'bob.bsky.social', got %q", result[1].ExemplarHandle)
	}
}

func TestHydrateExemplars_DeduplicatesHandles(t *testing.T) {
	s := &mockCandidateStore{
		candidates: map[string][]store.ExemplarCandidate{
			"politics": {{URI: "at://a/1", Handle: "alice.bsky.social", Engagement: 100}},
			"weather": {
				{URI: "at://a/2", Handle: "alice.bsky.social", Engagement: 200},
				{URI: "at://b/1", Handle: "bob.bsky.social", Engagement: 50},
			},
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics", Keywords: []string{"politics"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Weather", Keywords: []string{"weather"}, Synonyms: []string{}}}, TopicID: "t2", Rank: 2},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "alice.bsky.social" {
		t.Errorf("expected 'alice.bsky.social' for topic 1, got %q", result[0].ExemplarHandle)
	}
	if result[1].ExemplarHandle != "bob.bsky.social" {
		t.Errorf("expected 'bob.bsky.social' for topic 2 (alice already used), got %q", result[1].ExemplarHandle)
	}
}

func TestHydrateExemplars_EmptyTopics(t *testing.T) {
	hydrator := NewExemplarHydrator(nil)
	result, err := hydrator.HydrateExemplars(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}
