package topics

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// mockGate rejects the URIs it is told to and records what it was asked about.
type mockGate struct {
	calls    atomic.Int64
	asked    []string
	rejected map[string]bool
	err      error
}

func (m *mockGate) Check(_ context.Context, surface string, uris []string) (map[string]bool, error) {
	m.calls.Add(1)
	m.asked = append(m.asked, uris...)
	if surface != "exemplar" {
		return nil, errors.New("unexpected surface: " + surface)
	}
	if m.err != nil {
		return nil, m.err
	}
	out := make(map[string]bool, len(uris))
	for _, uri := range uris {
		out[uri] = !m.rejected[uri]
	}
	return out, nil
}

func TestHydrateExemplars_GateRejectedPrimaryPromotesFallback(t *testing.T) {
	cands := validationCandidates()
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate { return cands },
	}
	g := &mockGate{rejected: map[string]bool{cands[0].URI: true}}
	v := &mockValidator{}

	hydrator := NewExemplarHydrator(s)
	hydrator.SetGate(g)
	hydrator.SetValidator(v)

	topics := []IdentifiedTopic{topicOf("Jordan Binnington",
		[]string{"jordan_binnington", "canada", "hockey"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != cands[1].Handle {
		t.Errorf("exemplar handle = %q, want the fallback %q", result[0].ExemplarHandle, cands[1].Handle)
	}

	// The whole point of gating before validation: the rejected post's text
	// must never reach Gemini.
	for _, p := range v.received {
		if p.PostText == cands[0].Text {
			t.Error("a gated-out candidate's text was sent to the validator")
		}
	}
	if len(v.received) != len(cands)-1 {
		t.Errorf("validator received %d pairs, want %d", len(v.received), len(cands)-1)
	}
	if got := g.calls.Load(); got != 1 {
		t.Errorf("gate calls = %d, want 1", got)
	}
}

func TestHydrateExemplars_GateRejectsAllLeavesNoExemplar(t *testing.T) {
	cands := validationCandidates()
	reject := make(map[string]bool, len(cands))
	for _, c := range cands {
		reject[c.URI] = true
	}
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate { return cands },
	}
	v := &mockValidator{}

	var droppedTopic string
	hydrator := NewExemplarHydrator(s)
	hydrator.SetGate(&mockGate{rejected: reject})
	hydrator.SetValidator(v)
	hydrator.SetDroppedHandler(func(topic string, _ int) { droppedTopic = topic })

	topics := []IdentifiedTopic{topicOf("Jordan Binnington",
		[]string{"jordan_binnington", "canada", "hockey"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" || result[0].ExemplarHandle != "" {
		t.Errorf("exemplar = %q / %q, want none when every candidate is gated out",
			result[0].ExemplarURI, result[0].ExemplarHandle)
	}
	if droppedTopic != "Jordan Binnington" {
		t.Errorf("dropped handler saw %q, want the topic label", droppedTopic)
	}
	if v.calls.Load() != 0 {
		t.Error("the validator was called with no candidates left")
	}
}

func TestHydrateExemplars_GateErrorTwiceDropsEveryExemplar(t *testing.T) {
	s := &mockCandidateStore{
		candidatesFn: func([]string) []store.ExemplarCandidate { return validationCandidates() },
	}
	g := &mockGate{err: errors.New("appview down")}
	v := &mockValidator{}

	hydrator := NewExemplarHydrator(s)
	hydrator.SetGate(g)
	hydrator.SetValidator(v)

	topics := []IdentifiedTopic{topicOf("Jordan Binnington",
		[]string{"jordan_binnington", "canada", "hockey"})}

	result, err := hydrator.HydrateExemplars(context.Background(), topics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" || result[0].ExemplarHandle != "" {
		t.Errorf("exemplar = %q / %q, want none when the gate is unavailable",
			result[0].ExemplarURI, result[0].ExemplarHandle)
	}
	if got := g.calls.Load(); got != 2 {
		t.Errorf("gate calls = %d, want 2 (one retry)", got)
	}
	if v.calls.Load() != 0 {
		t.Error("validation ran on candidates the gate could not judge")
	}
}
