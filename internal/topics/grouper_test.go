package topics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
			Label:         "Donald Trump",
			Description:   "Discussion about Donald Trump",
			Keywords:      []string{"trump", "election", "congress"},
			Synonyms:      []string{"government", "political"},
			Justification: "Multiple terms relate to Trump and US government activity",
		},
		{
			Label:         "Weather",
			Description:   "Weather discussion",
			Keywords:      []string{"weather", "rain"},
			Synonyms:      []string{"storm"},
			Justification: "Weather-related terms cluster together",
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
	if clusters[0].Label != "Donald Trump" {
		t.Errorf("expected label 'Donald Trump', got %q", clusters[0].Label)
	}
	if len(clusters[0].Keywords) != 3 {
		t.Errorf("expected 3 keywords, got %d", len(clusters[0].Keywords))
	}
}

func TestGroupAndLabel_EmptyTerms(t *testing.T) {
	g := NewGrouper("test-key", "", "")
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
	if err == nil {
		t.Fatal("expected error on API failure, got nil")
	}
	if clusters != nil {
		t.Errorf("expected nil clusters on API failure (post must be suppressed), got %v", clusters)
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
	if err == nil {
		t.Fatal("expected error on malformed response, got nil")
	}
	if clusters != nil {
		t.Errorf("expected nil clusters on malformed response (post must be suppressed), got %v", clusters)
	}
}

func TestGroupAndLabel_FallbackOnPrimary429(t *testing.T) {
	expected := []TopicCluster{
		{Label: "Donald Trump", Description: "Trump discussion", Keywords: []string{"trump"}, Justification: "j"},
	}

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("quota exceeded"))
	}))
	defer primary.Close()

	fallbackHit := false
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHit = true
		geminiMockHandler(expected)(w, r)
	}))
	defer fallback.Close()

	g := NewGrouperWithEndpoints("test-key", primary.URL, fallback.URL)
	terms := []TermScore{{Term: "trump", Score: 12.5}}

	clusters, err := g.GroupAndLabel(context.Background(), terms)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if !fallbackHit {
		t.Error("expected fallback model to be called after primary 429")
	}
	if len(clusters) != 1 || clusters[0].Label != "Donald Trump" {
		t.Fatalf("expected fallback clusters, got %v", clusters)
	}
}

func TestGroupAndLabel_BothTiersFail(t *testing.T) {
	fail := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	})
	primary := httptest.NewServer(fail)
	defer primary.Close()
	fallback := httptest.NewServer(fail)
	defer fallback.Close()

	g := NewGrouperWithEndpoints("test-key", primary.URL, fallback.URL)
	terms := []TermScore{{Term: "trump", Score: 12.5}}

	clusters, err := g.GroupAndLabel(context.Background(), terms)
	if err == nil {
		t.Fatal("expected error when both tiers fail, got nil")
	}
	if clusters != nil {
		t.Errorf("expected nil clusters when both tiers fail (post must be suppressed), got %v", clusters)
	}
}

func TestGroupAndLabel_PrimarySuccessSkipsFallback(t *testing.T) {
	expected := []TopicCluster{
		{Label: "Weather", Description: "Weather discussion", Keywords: []string{"rain"}, Justification: "j"},
	}

	primary := httptest.NewServer(geminiMockHandler(expected))
	defer primary.Close()

	fallbackHit := false
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHit = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fallback.Close()

	g := NewGrouperWithEndpoints("test-key", primary.URL, fallback.URL)
	terms := []TermScore{{Term: "rain", Score: 5.5}}

	clusters, err := g.GroupAndLabel(context.Background(), terms)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fallbackHit {
		t.Error("fallback model must not be called when primary succeeds")
	}
	if len(clusters) != 1 || clusters[0].Label != "Weather" {
		t.Fatalf("expected primary clusters, got %v", clusters)
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
	if err == nil {
		t.Fatal("expected error on rate-limited call, got nil")
	}
	if clusters != nil {
		t.Errorf("expected nil clusters when rate-limited (post must be suppressed), got %v", clusters)
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
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "US Politics", Description: "American politics"}, UniqueAuthorCount: 500}, TopicID: "t1", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Weather", Description: "Weather discussion"}, UniqueAuthorCount: 300}, TopicID: "t2", Rank: 2},
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

func TestDetectOverlappingPhrases(t *testing.T) {
	tests := []struct {
		name          string
		terms         []TermScore
		wantPhrases   int
		wantPhrase    string
		wantTermCount int
	}{
		{
			name: "two overlapping bigrams",
			terms: []TermScore{
				{Term: "post_banger", Score: 5.0},
				{Term: "banger_that", Score: 4.5},
				{Term: "trump", Score: 12.0},
			},
			wantPhrases:   1,
			wantPhrase:    "post banger that",
			wantTermCount: 2,
		},
		{
			name: "three-term chain",
			terms: []TermScore{
				{Term: "post_banger", Score: 5.0},
				{Term: "banger_that", Score: 4.5},
				{Term: "that_isnt", Score: 4.0},
			},
			wantPhrases:   1,
			wantPhrase:    "post banger that isnt",
			wantTermCount: 3,
		},
		{
			name: "no overlaps",
			terms: []TermScore{
				{Term: "bad_bunny", Score: 5.0},
				{Term: "age_verification", Score: 4.0},
				{Term: "trump", Score: 12.0},
			},
			wantPhrases: 0,
		},
		{
			name:        "no compound terms",
			terms:       []TermScore{{Term: "trump", Score: 12.0}},
			wantPhrases: 0,
		},
		{
			name:        "empty terms",
			terms:       nil,
			wantPhrases: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phrases := detectOverlappingPhrases(tt.terms)
			if len(phrases) != tt.wantPhrases {
				t.Fatalf("got %d phrases, want %d: %+v", len(phrases), tt.wantPhrases, phrases)
			}
			if tt.wantPhrases > 0 {
				if phrases[0].Phrase != tt.wantPhrase {
					t.Errorf("got phrase %q, want %q", phrases[0].Phrase, tt.wantPhrase)
				}
				if len(phrases[0].Terms) != tt.wantTermCount {
					t.Errorf("got %d terms, want %d", len(phrases[0].Terms), tt.wantTermCount)
				}
			}
		})
	}
}

// exemplarValidationHandler replies with the given verdicts, echoing the ids
// the prompt assigned.
func exemplarValidationHandler(t *testing.T, verdicts []exemplarValidationResult, capturedPrompt *string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var req geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if capturedPrompt != nil && len(req.Contents) > 0 && len(req.Contents[0].Parts) > 0 {
			*capturedPrompt = req.Contents[0].Parts[0].Text
		}
		text, _ := json.Marshal(verdicts)
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Parts: []geminiPart{{Text: string(text)}}}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func TestValidateExemplars_MapsVerdictsByID(t *testing.T) {
	// Three pairs share one topic label, so only the id can tell them apart.
	verdicts := []exemplarValidationResult{
		{ID: 1, IsRelevant: false},
		{ID: 2, IsRelevant: true},
		{ID: 3, IsRelevant: false},
	}
	var prompt string
	srv := httptest.NewServer(exemplarValidationHandler(t, verdicts, &prompt))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	pairs := []ExemplarValidation{
		{TopicLabel: "Hockey", PostText: "first", IsRelevant: true},
		{TopicLabel: "Hockey", PostText: "second", IsRelevant: true},
		{TopicLabel: "Hockey", PostText: "third", IsRelevant: true},
	}

	got, err := g.ValidateExemplars(context.Background(), pairs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []bool{false, true, false}
	for i, w := range want {
		if got[i].IsRelevant != w {
			t.Errorf("pair %d IsRelevant = %v, want %v", i+1, got[i].IsRelevant, w)
		}
	}
	if !strings.Contains(prompt, "3. Topic:") {
		t.Errorf("expected numbered pairs in prompt, got:\n%s", prompt)
	}
}

func TestValidateExemplars_FallsBackToOrderWithoutIDs(t *testing.T) {
	verdicts := []exemplarValidationResult{{IsRelevant: false}, {IsRelevant: true}}
	srv := httptest.NewServer(exemplarValidationHandler(t, verdicts, nil))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	pairs := []ExemplarValidation{
		{TopicLabel: "Hockey", PostText: "first", IsRelevant: true},
		{TopicLabel: "Hockey", PostText: "second", IsRelevant: true},
	}

	got, err := g.ValidateExemplars(context.Background(), pairs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].IsRelevant || !got[1].IsRelevant {
		t.Errorf("expected positional fallback [false true], got [%v %v]", got[0].IsRelevant, got[1].IsRelevant)
	}
}

func TestCheckAndIncrementRate_BudgetExhaustedFiresOncePerTrip(t *testing.T) {
	g := NewGrouperWithEndpoint("test-key", "http://example.invalid")

	var trips int
	var reported int
	g.SetBudgetExhaustedHandler(func(dailyCalls int) {
		trips++
		reported = dailyCalls
	})

	for i := 0; i < maxDailyCalls; i++ {
		if !g.checkAndIncrementRate() {
			t.Fatalf("call %d should have been allowed", i+1)
		}
	}
	if g.BudgetExhausted() {
		t.Error("budget should not be flagged before the limit trips")
	}

	for i := 0; i < 3; i++ {
		if g.checkAndIncrementRate() {
			t.Fatal("call past the daily limit should be refused")
		}
	}
	if trips != 1 {
		t.Errorf("expected the handler to fire once per trip, fired %d times", trips)
	}
	if reported != maxDailyCalls {
		t.Errorf("handler reported %d daily calls, want %d", reported, maxDailyCalls)
	}
	if !g.BudgetExhausted() {
		t.Error("expected BudgetExhausted to report true after the trip")
	}
}
