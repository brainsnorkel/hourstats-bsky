package topics

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type mockCandidateStore struct {
	candidatesFn func(keywords []string) []store.ExemplarCandidate
	candidates   map[string][]store.ExemplarCandidate
	err          error
	callCount    atomic.Int64
	lastLimit    atomic.Int64
}

func (m *mockCandidateStore) GetExemplarCandidates(_ context.Context, keywords []string, _ string, limit int) ([]store.ExemplarCandidate, error) {
	m.callCount.Add(1)
	m.lastLimit.Store(int64(limit))
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

// candidate builds an ExemplarCandidate with enough text to clear the
// short-post quality penalty, so tests exercise ranking rather than length.
func candidate(uri, handle string, eng int, matched ...string) store.ExemplarCandidate {
	return store.ExemplarCandidate{
		URI:             uri,
		Handle:          handle,
		Text:            "this is a perfectly ordinary post about the subject at hand today",
		Engagement:      eng,
		Matched:         matched,
		DistinctMatches: len(matched),
		CreatedAt:       "2026-09-04T00:00:00Z",
	}
}

func topicOf(label string, keywords []string) IdentifiedTopic {
	return IdentifiedTopic{
		RankedTopic: RankedTopic{Cluster: TopicCluster{Label: label, Keywords: keywords, Synonyms: []string{}}},
		TopicID:     "t-" + label,
		Rank:        1,
	}
}

func TestHydrateExemplars_PicksHighestEngagement(t *testing.T) {
	s := &mockCandidateStore{
		candidates: map[string][]store.ExemplarCandidate{
			"politics": {
				candidate("at://a/2", "high.bsky.social", 175, "politics"),
				candidate("at://a/3", "mid.bsky.social", 17, "politics"),
				candidate("at://a/1", "low.bsky.social", 1, "politics"),
			},
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{topicOf("Politics", []string{"politics"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "at://a/2" {
		t.Errorf("expected highest engagement URI 'at://a/2', got %q", result[0].ExemplarURI)
	}
	if result[0].ExemplarHandle != "high.bsky.social" {
		t.Errorf("expected handle 'high.bsky.social', got %q", result[0].ExemplarHandle)
	}
	if got := s.lastLimit.Load(); got != exemplarCandidateLimit {
		t.Errorf("expected candidate limit %d, got %d", exemplarCandidateLimit, got)
	}
}

func TestHydrateExemplars_CoverageBeatsEngagement(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate {
			return []store.ExemplarCandidate{
				// Repeats one generic keyword many times; engagement is zero.
				candidate("at://a/1", "narrow.bsky.social", 0, "school", "football"),
				// Matches the whole topic.
				candidate("at://a/2", "broad.bsky.social", 5, "colorado", "football", "college", "georgia", "georgia_tech"),
			}
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{topicOf("College Football",
		[]string{"colorado", "football", "college", "georgia", "georgia_tech", "school"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "broad.bsky.social" {
		t.Errorf("expected broad keyword coverage to win, got %q", result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_NoCandidates(t *testing.T) {
	s := &mockCandidateStore{candidates: map[string][]store.ExemplarCandidate{}}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{topicOf("Empty", []string{"nothing"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
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
	topics := []IdentifiedTopic{topicOf("Test", []string{"test"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
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
			"politics": {candidate("at://a/1", "alice.bsky.social", 65, "politics")},
			"weather":  {candidate("at://b/1", "bob.bsky.social", 130, "weather")},
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		topicOf("Politics", []string{"politics"}),
		topicOf("Weather", []string{"weather"}),
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
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
			"politics": {candidate("at://a/1", "alice.bsky.social", 100, "politics")},
			"weather": {
				candidate("at://a/2", "alice.bsky.social", 200, "weather"),
				candidate("at://b/1", "bob.bsky.social", 50, "weather"),
			},
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{
		topicOf("Politics", []string{"politics"}),
		topicOf("Weather", []string{"weather"}),
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
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
	result, err := hydrator.HydrateExemplars(context.Background(), nil, nil)
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
			"politics": {candidate("at://a/1", "alice.bsky.social", 100, "politics")},
		},
	}

	hydrator := NewExemplarHydrator(s)
	meme := topicOf("Post a Banger", []string{"post", "banger"})
	meme.Cluster.IsMeme = true
	topics := []IdentifiedTopic{meme, topicOf("Politics", []string{"politics"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" || result[0].ExemplarHandle != "" {
		t.Errorf("meme topic should have no exemplar, got %q / %q", result[0].ExemplarURI, result[0].ExemplarHandle)
	}
	if result[1].ExemplarHandle != "alice.bsky.social" {
		t.Errorf("non-meme topic should get exemplar, got %q", result[1].ExemplarHandle)
	}
	if got := s.callCount.Load(); got != 1 {
		t.Errorf("expected 1 DB query (meme skipped), got %d", got)
	}
}

func TestHydrateExemplars_AnchorRuleRejectsGenericOnlyMatch(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate {
			return []store.ExemplarCandidate{
				// An itch.io games post that only shares the generic "games".
				candidate("at://a/1", "gamedev.bsky.social", 900, "games"),
				candidate("at://a/2", "bbfan.bsky.social", 9, "bb28", "hoh"),
			}
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{topicOf("Big Brother 28", []string{"bb28", "hoh", "games"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "bbfan.bsky.social" {
		t.Errorf("expected on-topic post despite lower engagement, got %q", result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_NoCandidateMeetsRelevance(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate {
			return []store.ExemplarCandidate{
				candidate("at://a/1", "bad.bsky.social", 1000, "canada"),
			}
		},
	}

	hydrator := NewExemplarHydrator(s)
	topics := []IdentifiedTopic{topicOf("Jordan Binnington",
		[]string{"jordan_binnington", "canada", "hockey", "nhl"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" {
		t.Errorf("expected no exemplar when no candidate matches an anchor, got %q", result[0].ExemplarURI)
	}
}

type mockValidator struct {
	calls      atomic.Int64
	received   []ExemplarValidation
	rejectText map[string]bool
	err        error
}

func (m *mockValidator) ValidateExemplars(_ context.Context, pairs []ExemplarValidation) ([]ExemplarValidation, error) {
	m.calls.Add(1)
	m.received = append(m.received, pairs...)
	if m.err != nil {
		return nil, m.err
	}
	for i := range pairs {
		if m.rejectText[pairs[i].PostText] {
			pairs[i].IsRelevant = false
		}
	}
	return pairs, nil
}

func validationCandidates() []store.ExemplarCandidate {
	first := candidate("at://a/1", "first.bsky.social", 500, "jordan_binnington", "canada", "hockey")
	first.Text = "canada beat the usa in a hockey game that jordan binnington did not play"
	second := candidate("at://a/2", "second.bsky.social", 50, "jordan_binnington", "hockey")
	second.Text = "jordan binnington makes an unbelievable save late in the third period"
	third := candidate("at://a/3", "third.bsky.social", 5, "jordan_binnington", "canada")
	third.Text = "binnington signs a new deal to stay in canada for another season yes"
	return []store.ExemplarCandidate{first, second, third}
}

func TestHydrateExemplars_ValidationSendsTopKInOneCall(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate { return validationCandidates() },
	}
	v := &mockValidator{}

	hydrator := NewExemplarHydrator(s)
	hydrator.SetValidator(v)

	topics := []IdentifiedTopic{topicOf("Jordan Binnington",
		[]string{"jordan_binnington", "canada", "hockey"})}

	if _, err := hydrator.HydrateExemplars(context.Background(), topics, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := v.calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 validation call, got %d", got)
	}
	if len(v.received) != exemplarTopK {
		t.Fatalf("expected %d pairs, got %d", exemplarTopK, len(v.received))
	}
	for _, p := range v.received {
		if p.TopicLabel != "Jordan Binnington" {
			t.Errorf("unexpected topic label %q", p.TopicLabel)
		}
	}
}

func TestHydrateExemplars_ValidationFallsThroughToSecond(t *testing.T) {
	cands := validationCandidates()
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate { return cands },
	}
	v := &mockValidator{rejectText: map[string]bool{cands[0].Text: true}}

	hydrator := NewExemplarHydrator(s)
	hydrator.SetValidator(v)

	topics := []IdentifiedTopic{topicOf("Jordan Binnington",
		[]string{"jordan_binnington", "canada", "hockey"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "second.bsky.social" {
		t.Errorf("expected fall-through to the next approved candidate, got %q", result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_ValidationRejectsAllLeavesNoExemplar(t *testing.T) {
	cands := validationCandidates()
	reject := make(map[string]bool, len(cands))
	for _, c := range cands {
		reject[c.Text] = true
	}
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate { return cands },
	}
	v := &mockValidator{rejectText: reject}

	hydrator := NewExemplarHydrator(s)
	hydrator.SetValidator(v)

	topics := []IdentifiedTopic{topicOf("Jordan Binnington",
		[]string{"jordan_binnington", "canada", "hockey"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" || result[0].ExemplarHandle != "" {
		t.Errorf("expected no exemplar when every candidate is rejected, got %q / %q",
			result[0].ExemplarURI, result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_ValidationErrorKeepsTopPick(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate { return validationCandidates() },
	}
	v := &mockValidator{err: fmt.Errorf("gemini down")}

	hydrator := NewExemplarHydrator(s)
	hydrator.SetValidator(v)

	topics := []IdentifiedTopic{topicOf("Jordan Binnington",
		[]string{"jordan_binnington", "canada", "hockey"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "first.bsky.social" {
		t.Errorf("expected top-ranked fallback when validation fails, got %q", result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_ValidationBudgetCapsPairs(t *testing.T) {
	// Each topic needs its own handles: handles are claimed across topics.
	var nth atomic.Int64
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate {
			n := nth.Add(1)
			cands := validationCandidates()
			for i := range cands {
				cands[i].Handle = fmt.Sprintf("t%d-%s", n, cands[i].Handle)
				cands[i].URI = fmt.Sprintf("at://t%d/%d", n, i)
			}
			return cands
		},
	}
	v := &mockValidator{}

	hydrator := NewExemplarHydrator(s)
	hydrator.SetValidator(v)

	// Six topics x 3 candidates would be 18 pairs; the cap is 15.
	var topics []IdentifiedTopic
	for i := 0; i < 6; i++ {
		topics = append(topics, topicOf(fmt.Sprintf("Hockey %d", i),
			[]string{"jordan_binnington", "canada", "hockey"}))
	}

	if _, err := hydrator.HydrateExemplars(context.Background(), topics, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := v.calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 validation call, got %d", got)
	}
	if len(v.received) > maxValidationPairs {
		t.Errorf("expected at most %d pairs, got %d", maxValidationPairs, len(v.received))
	}
	// Round-robin ordering means every topic gets its top candidate validated.
	seen := make(map[string]bool)
	for _, p := range v.received[:6] {
		seen[p.TopicLabel] = true
	}
	if len(seen) != 6 {
		t.Errorf("expected all 6 topics in the first round, got %d", len(seen))
	}
}
