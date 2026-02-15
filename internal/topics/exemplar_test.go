package topics

import (
	"context"
	"fmt"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type mockCandidateStore struct {
	candidatesFn func(keywords []string) []store.ExemplarCandidate
	candidates   map[string][]store.ExemplarCandidate
	err          error
	callCount    int
}

func (m *mockCandidateStore) GetExemplarCandidates(_ context.Context, keywords []string, _ string, limit int) ([]store.ExemplarCandidate, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	if m.candidatesFn != nil {
		return m.candidatesFn(keywords), nil
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
				{URI: "at://a/2", Handle: "high.bsky.social", Engagement: 175, MatchScore: 1},
				{URI: "at://a/3", Handle: "mid.bsky.social", Engagement: 17, MatchScore: 1},
				{URI: "at://a/1", Handle: "low.bsky.social", Engagement: 1, MatchScore: 1},
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
			"politics": {{URI: "at://a/1", Handle: "alice.bsky.social", Engagement: 65, MatchScore: 1}},
			"weather":  {{URI: "at://b/1", Handle: "bob.bsky.social", Engagement: 130, MatchScore: 1}},
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
			"politics": {{URI: "at://a/1", Handle: "alice.bsky.social", Engagement: 100, MatchScore: 1}},
			"weather": {
				{URI: "at://a/2", Handle: "alice.bsky.social", Engagement: 200, MatchScore: 1},
				{URI: "at://b/1", Handle: "bob.bsky.social", Engagement: 50, MatchScore: 1},
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

func TestHydrateExemplars_SkipsMemeTopics(t *testing.T) {
	s := &mockCandidateStore{
		candidates: map[string][]store.ExemplarCandidate{
			"politics": {{URI: "at://a/1", Handle: "alice.bsky.social", Engagement: 100, MatchScore: 1}},
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Post a Banger", Keywords: []string{"post", "banger"}, Synonyms: []string{}, IsMeme: true}}, TopicID: "t1", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics", Keywords: []string{"politics"}, Synonyms: []string{}}}, TopicID: "t2", Rank: 2},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" {
		t.Errorf("meme topic should have empty ExemplarURI, got %q", result[0].ExemplarURI)
	}
	if result[0].ExemplarHandle != "" {
		t.Errorf("meme topic should have empty ExemplarHandle, got %q", result[0].ExemplarHandle)
	}
	if result[1].ExemplarHandle != "alice.bsky.social" {
		t.Errorf("non-meme topic should get exemplar, got %q", result[1].ExemplarHandle)
	}
	if s.callCount != 1 {
		t.Errorf("expected 1 DB query (meme skipped), got %d", s.callCount)
	}
}

func TestHydrateExemplars_ThresholdRejectsBelowMin(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func(keywords []string) []store.ExemplarCandidate {
			return []store.ExemplarCandidate{
				{URI: "at://a/1", Handle: "low.bsky.social", Engagement: 500, MatchScore: 1},
				{URI: "at://a/2", Handle: "good.bsky.social", Engagement: 50, MatchScore: 4},
			}
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{
			Label:    "Jordan Binnington",
			Keywords: []string{"jordan_binnington", "canada", "hockey"},
			Synonyms: []string{},
		}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "good.bsky.social" {
		t.Errorf("expected good.bsky.social (score 4), got %q", result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_ThresholdAllowsSingleKeyword(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func(keywords []string) []store.ExemplarCandidate {
			return []store.ExemplarCandidate{
				{URI: "at://a/1", Handle: "ok.bsky.social", Engagement: 100, MatchScore: 1},
			}
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{
			Label:    "Bitcoin",
			Keywords: []string{"bitcoin"},
			Synonyms: []string{},
		}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "ok.bsky.social" {
		t.Errorf("expected ok.bsky.social for single-keyword topic, got %q", result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_ThresholdRejectsAll(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func(keywords []string) []store.ExemplarCandidate {
			return []store.ExemplarCandidate{
				{URI: "at://a/1", Handle: "bad.bsky.social", Engagement: 1000, MatchScore: 1},
			}
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{
			Label:    "Jordan Binnington",
			Keywords: []string{"jordan_binnington", "canada", "hockey", "nhl"},
			Synonyms: []string{},
		}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" {
		t.Errorf("expected no exemplar when all below threshold, got %q", result[0].ExemplarURI)
	}
}

func TestMinMatchScore(t *testing.T) {
	if minMatchScore(1) != 1 {
		t.Errorf("1 keyword: expected threshold 1, got %d", minMatchScore(1))
	}
	if minMatchScore(2) != 1 {
		t.Errorf("2 keywords: expected threshold 1, got %d", minMatchScore(2))
	}
	if minMatchScore(3) != 2 {
		t.Errorf("3 keywords: expected threshold 2, got %d", minMatchScore(3))
	}
	if minMatchScore(10) != 2 {
		t.Errorf("10 keywords: expected threshold 2, got %d", minMatchScore(10))
	}
}

type mockValidator struct {
	rejectTopics map[string]bool
}

func (m *mockValidator) ValidateExemplars(_ context.Context, pairs []ExemplarValidation) ([]ExemplarValidation, error) {
	for i := range pairs {
		if m.rejectTopics[pairs[i].TopicLabel] {
			pairs[i].IsRelevant = false
		}
	}
	return pairs, nil
}

func TestHydrateExemplars_ValidationRejectsAndReplaces(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func(keywords []string) []store.ExemplarCandidate {
			return []store.ExemplarCandidate{
				{URI: "at://a/1", Handle: "curling.bsky.social", Text: "Canada curling great performance", Engagement: 500, MatchScore: 3},
				{URI: "at://a/2", Handle: "hockey.bsky.social", Text: "Binnington amazing save in hockey", Engagement: 50, MatchScore: 4},
			}
		},
	}

	hydrator := NewExemplarHydrator(s)
	hydrator.SetValidator(&mockValidator{rejectTopics: map[string]bool{"Jordan Binnington": true}})

	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{
			Label:    "Jordan Binnington",
			Keywords: []string{"jordan_binnington", "canada", "hockey"},
			Synonyms: []string{},
		}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "hockey.bsky.social" {
		t.Errorf("expected replacement after rejection, got %q", result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_ValidationAcceptsGoodMatch(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func(keywords []string) []store.ExemplarCandidate {
			return []store.ExemplarCandidate{
				{URI: "at://a/1", Handle: "good.bsky.social", Text: "Great hockey save by Binnington", Engagement: 200, MatchScore: 5},
			}
		},
	}

	hydrator := NewExemplarHydrator(s)
	hydrator.SetValidator(&mockValidator{rejectTopics: map[string]bool{}})

	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{
			Label:    "Jordan Binnington",
			Keywords: []string{"jordan_binnington", "hockey"},
			Synonyms: []string{},
		}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "good.bsky.social" {
		t.Errorf("expected accepted exemplar to remain, got %q", result[0].ExemplarHandle)
	}
}
