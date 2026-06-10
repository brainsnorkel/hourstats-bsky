package topics

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// tokenRow builds a TopicTokenRow whose Tokens field is the JSON-encoded token
// list, attributed to the given author DID.
func tokenRow(did string, tokens ...string) store.TopicTokenRow {
	b, _ := json.Marshal(tokens)
	return store.TopicTokenRow{PostURI: "at://" + did + "/post", Tokens: string(b), AuthorDID: did}
}

// clusterContaining returns the cluster whose keywords include term, or nil.
func clusterContaining(clusters []TopicCluster, term string) *TopicCluster {
	for i := range clusters {
		for _, kw := range clusters[i].Keywords {
			if kw == term {
				return &clusters[i]
			}
		}
	}
	return nil
}

func hasKeyword(c *TopicCluster, term string) bool {
	if c == nil {
		return false
	}
	for _, kw := range c.Keywords {
		if kw == term {
			return true
		}
	}
	return false
}

func TestAlgorithmicGroup_CooccurringTermsCluster(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 5; i++ {
		rows = append(rows, tokenRow("a"+string(rune('0'+i)), "trump", "iran", "tariffs"))
		rows = append(rows, tokenRow("b"+string(rune('0'+i)), "weather", "rain"))
	}
	terms := []TermScore{
		{Term: "trump", Score: 10},
		{Term: "iran", Score: 8},
		{Term: "tariffs", Score: 7},
		{Term: "weather", Score: 6},
		{Term: "rain", Score: 5},
	}

	clusters := AlgorithmicGroup(rows, terms)
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d: %+v", len(clusters), clusters)
	}

	trumpCluster := clusterContaining(clusters, "trump")
	if !hasKeyword(trumpCluster, "iran") || !hasKeyword(trumpCluster, "tariffs") {
		t.Errorf("expected trump/iran/tariffs to cluster together, got %+v", trumpCluster)
	}
	weatherCluster := clusterContaining(clusters, "weather")
	if !hasKeyword(weatherCluster, "rain") {
		t.Errorf("expected weather/rain to cluster together, got %+v", weatherCluster)
	}
	if hasKeyword(trumpCluster, "weather") {
		t.Errorf("unrelated terms must not merge: %+v", trumpCluster)
	}
}

func TestAlgorithmicGroup_IsolatedTermsStaySeparate(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 4; i++ {
		rows = append(rows, tokenRow("a"+string(rune('0'+i)), "alpha"))
		rows = append(rows, tokenRow("b"+string(rune('0'+i)), "beta"))
	}
	terms := []TermScore{{Term: "alpha", Score: 5}, {Term: "beta", Score: 4}}

	clusters := AlgorithmicGroup(rows, terms)
	if len(clusters) != 2 {
		t.Fatalf("expected 2 separate clusters for non-co-occurring terms, got %d: %+v", len(clusters), clusters)
	}
	for _, c := range clusters {
		if len(c.Keywords) != 1 {
			t.Errorf("expected singleton cluster, got %+v", c)
		}
	}
}

func TestAlgorithmicGroup_BigramLabelPreferred(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 5; i++ {
		rows = append(rows, tokenRow("a"+string(rune('0'+i)), "kristi", "noem", "kristi_noem"))
	}
	terms := []TermScore{
		{Term: "kristi", Score: 9},
		{Term: "noem", Score: 8},
		{Term: "kristi_noem", Score: 12},
	}

	clusters := AlgorithmicGroup(rows, terms)
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d: %+v", len(clusters), clusters)
	}
	if clusters[0].Label != "Kristi Noem" {
		t.Errorf("expected bigram-derived label 'Kristi Noem', got %q", clusters[0].Label)
	}
}

func TestAlgorithmicGroup_NoUnderscoreLabels(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 5; i++ {
		rows = append(rows, tokenRow("a"+string(rune('0'+i)), "super", "bowl", "super_bowl", "halftime"))
	}
	terms := []TermScore{
		{Term: "super", Score: 9},
		{Term: "bowl", Score: 8},
		{Term: "halftime", Score: 6},
		{Term: "super_bowl", Score: 11},
	}

	clusters := AlgorithmicGroup(rows, terms)
	if len(clusters) == 0 {
		t.Fatal("expected at least one cluster")
	}
	for _, c := range clusters {
		if strings.Contains(c.Label, "_") {
			t.Errorf("label must never contain raw underscore tokens, got %q", c.Label)
		}
	}
}

func TestAlgorithmicGroup_Deterministic(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 5; i++ {
		rows = append(rows, tokenRow("a"+string(rune('0'+i)), "trump", "iran", "tariffs"))
		rows = append(rows, tokenRow("b"+string(rune('0'+i)), "weather", "rain"))
	}
	terms := []TermScore{
		{Term: "trump", Score: 10},
		{Term: "iran", Score: 8},
		{Term: "tariffs", Score: 7},
		{Term: "weather", Score: 6},
		{Term: "rain", Score: 5},
	}

	first := AlgorithmicGroup(rows, terms)
	for i := 0; i < 5; i++ {
		again := AlgorithmicGroup(rows, terms)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("AlgorithmicGroup not deterministic:\n run0 = %+v\n run%d = %+v", first, i+1, again)
		}
	}
}

func TestAlgorithmicGroup_Empty(t *testing.T) {
	if got := AlgorithmicGroup(nil, nil); got != nil {
		t.Errorf("expected nil for empty input, got %+v", got)
	}
	rows := []store.TopicTokenRow{tokenRow("a", "trump")}
	if got := AlgorithmicGroup(rows, nil); got != nil {
		t.Errorf("expected nil for nil terms, got %+v", got)
	}
	if got := AlgorithmicGroup(nil, []TermScore{{Term: "trump", Score: 1}}); got != nil {
		t.Errorf("expected nil for nil rows, got %+v", got)
	}
}
