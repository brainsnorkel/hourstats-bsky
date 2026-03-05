package topics

import (
	"reflect"
	"sort"
	"testing"
)

func TestMergeSimilarClusters(t *testing.T) {
	tests := []struct {
		name      string
		clusters  []TopicCluster
		terms     []TermScore
		wantCount int
		wantLabel string // expected label of merged cluster (checked only if non-empty)
	}{
		{
			name: "bigram bridges two fallback clusters",
			clusters: []TopicCluster{
				{Label: "Kristi", Keywords: []string{"kristi"}},
				{Label: "Noem", Keywords: []string{"noem"}},
			},
			terms: []TermScore{
				{Term: "kristi", Score: 10},
				{Term: "noem", Score: 9},
				{Term: "kristi_noem", Score: 8},
			},
			wantCount: 1,
			wantLabel: "Kristi Noem",
		},
		{
			name: "reverse bigram order still bridges",
			clusters: []TopicCluster{
				{Label: "Noem", Keywords: []string{"noem"}},
				{Label: "Kristi", Keywords: []string{"kristi"}},
			},
			terms: []TermScore{
				{Term: "kristi_noem", Score: 8},
			},
			wantCount: 1,
			wantLabel: "Kristi Noem",
		},
		{
			name: "no bigram no merge",
			clusters: []TopicCluster{
				{Label: "Trump", Keywords: []string{"trump"}},
				{Label: "Taylor", Keywords: []string{"taylor"}},
			},
			terms: []TermScore{
				{Term: "trump", Score: 10},
				{Term: "taylor", Score: 9},
			},
			wantCount: 2,
		},
		{
			name: "transitive merge via shared bigrams",
			clusters: []TopicCluster{
				{Label: "Alpha", Keywords: []string{"alpha"}},
				{Label: "Beta", Keywords: []string{"beta"}},
				{Label: "Gamma", Keywords: []string{"gamma"}},
			},
			terms: []TermScore{
				{Term: "alpha_beta", Score: 8},
				{Term: "beta_gamma", Score: 7},
			},
			wantCount: 1,
		},
		{
			name: "no bigrams in terms",
			clusters: []TopicCluster{
				{Label: "Alpha", Keywords: []string{"alpha"}},
				{Label: "Beta", Keywords: []string{"beta"}},
			},
			terms: []TermScore{
				{Term: "alpha", Score: 10},
				{Term: "beta", Score: 9},
			},
			wantCount: 2,
		},
		{
			name: "single cluster unchanged",
			clusters: []TopicCluster{
				{Label: "Kristi Noem", Keywords: []string{"kristi", "noem", "kristi_noem"}},
			},
			terms: []TermScore{
				{Term: "kristi_noem", Score: 8},
			},
			wantCount: 1,
			wantLabel: "Kristi Noem",
		},
		{
			name:     "empty clusters",
			clusters: []TopicCluster{},
			terms: []TermScore{
				{Term: "kristi_noem", Score: 8},
			},
			wantCount: 0,
		},
		{
			name: "partial merge leaves unrelated clusters alone",
			clusters: []TopicCluster{
				{Label: "Kristi", Keywords: []string{"kristi"}},
				{Label: "Noem", Keywords: []string{"noem"}},
				{Label: "Taylor", Keywords: []string{"taylor"}},
			},
			terms: []TermScore{
				{Term: "kristi_noem", Score: 8},
				{Term: "taylor", Score: 7},
			},
			wantCount: 2,
			wantLabel: "Kristi Noem",
		},
		{
			name: "existing multi-word label preferred over bigram",
			clusters: []TopicCluster{
				{Label: "Kristi Noem DHS Secretary", Keywords: []string{"kristi"}},
				{Label: "Noem", Keywords: []string{"noem"}},
			},
			terms: []TermScore{
				{Term: "kristi_noem", Score: 8},
			},
			wantCount: 1,
			wantLabel: "Kristi Noem DHS Secretary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeSimilarClusters(tt.clusters, tt.terms)
			if len(got) != tt.wantCount {
				t.Errorf("got %d clusters, want %d", len(got), tt.wantCount)
				for i, c := range got {
					t.Logf("  cluster[%d]: label=%q keywords=%v", i, c.Label, c.Keywords)
				}
			}
			if tt.wantLabel != "" {
				found := false
				for _, c := range got {
					if c.Label == tt.wantLabel {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected label %q not found", tt.wantLabel)
					for i, c := range got {
						t.Logf("  cluster[%d]: label=%q", i, c.Label)
					}
				}
			}
		})
	}
}

func TestMergeSimilarClusters_MergedKeywords(t *testing.T) {
	clusters := []TopicCluster{
		{Label: "Kristi", Keywords: []string{"kristi"}},
		{Label: "Noem", Keywords: []string{"noem"}},
	}
	terms := []TermScore{
		{Term: "kristi", Score: 10},
		{Term: "noem", Score: 9},
		{Term: "kristi_noem", Score: 8},
	}

	got := MergeSimilarClusters(clusters, terms)
	if len(got) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(got))
	}

	want := []string{"kristi", "kristi_noem", "noem"}
	sort.Strings(got[0].Keywords)
	if !reflect.DeepEqual(got[0].Keywords, want) {
		t.Errorf("keywords = %v, want %v", got[0].Keywords, want)
	}
}

func TestMergeSimilarClusters_DescriptionPreservation(t *testing.T) {
	clusters := []TopicCluster{
		{Label: "Kristi", Description: "Trending term", Keywords: []string{"kristi"}},
		{Label: "Noem", Description: "Head of DHS", Keywords: []string{"noem"}},
	}
	terms := []TermScore{
		{Term: "kristi_noem", Score: 8},
	}

	got := MergeSimilarClusters(clusters, terms)
	if len(got) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(got))
	}
	if got[0].Description != "Head of DHS" {
		t.Errorf("description = %q, want %q", got[0].Description, "Head of DHS")
	}
}

func TestMergeSimilarClusters_SynonymsUnioned(t *testing.T) {
	clusters := []TopicCluster{
		{Label: "Kristi", Keywords: []string{"kristi"}, Synonyms: []string{"kristie"}},
		{Label: "Noem", Keywords: []string{"noem"}, Synonyms: []string{"gov_noem"}},
	}
	terms := []TermScore{
		{Term: "kristi_noem", Score: 8},
	}

	got := MergeSimilarClusters(clusters, terms)
	if len(got) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(got))
	}

	want := []string{"gov_noem", "kristie"}
	sort.Strings(got[0].Synonyms)
	if !reflect.DeepEqual(got[0].Synonyms, want) {
		t.Errorf("synonyms = %v, want %v", got[0].Synonyms, want)
	}
}

func TestBigramToLabel(t *testing.T) {
	tests := []struct {
		bigram string
		want   string
	}{
		{"kristi_noem", "Kristi Noem"},
		{"bad_bunny", "Bad Bunny"},
		{"super_bowl", "Super Bowl"},
		{"ice_raids", "Ice Raids"},
	}
	for _, tt := range tests {
		t.Run(tt.bigram, func(t *testing.T) {
			got := bigramToLabel(tt.bigram)
			if got != tt.want {
				t.Errorf("bigramToLabel(%q) = %q, want %q", tt.bigram, got, tt.want)
			}
		})
	}
}

func TestHasBigramBridge(t *testing.T) {
	bigrams := map[string]bool{
		"kristi_noem": true,
		"bad_bunny":   true,
	}

	tests := []struct {
		name string
		kwsA []string
		kwsB []string
		want bool
	}{
		{"direct match", []string{"kristi"}, []string{"noem"}, true},
		{"reverse order", []string{"noem"}, []string{"kristi"}, true},
		{"no bridge", []string{"trump"}, []string{"taylor"}, false},
		{"empty keywords", []string{}, []string{"noem"}, false},
		{"both empty", []string{}, []string{}, false},
		{"case insensitive", []string{"Kristi"}, []string{"Noem"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasBigramBridge(tt.kwsA, tt.kwsB, bigrams)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
