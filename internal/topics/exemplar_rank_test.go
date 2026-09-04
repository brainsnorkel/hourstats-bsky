package topics

import (
	"math"
	"strings"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func TestBuildKeywordWeights_AnchorsCompoundsAndGenerics(t *testing.T) {
	w := buildKeywordWeights("College Football",
		[]string{"colorado", "football", "college", "georgia", "georgia_tech", "school"},
		[]string{"ncaa"}, nil)

	want := map[string]float64{
		"football":     weightAnchor,
		"college":      weightAnchor,
		"georgia_tech": weightCompound,
		"colorado":     weightPlain,
		"georgia":      weightPlain,
		"school":       weightGeneric,
		"ncaa":         weightSynonym,
	}
	for term, expect := range want {
		if got := w.weights[term]; got != expect {
			t.Errorf("weight(%q) = %v, want %v", term, got, expect)
		}
	}
	if len(w.weights) != len(want) {
		t.Errorf("expected %d weighted terms, got %d", len(want), len(w.weights))
	}

	wantTotal := 2.0 + 2.0 + 3.0 + 1.0 + 1.0 + 0.5 + 0.5
	if w.total != wantTotal {
		t.Errorf("total = %v, want %v", w.total, wantTotal)
	}

	wantAnchors := []string{"football", "college", "georgia_tech"}
	if len(w.anchors) != len(wantAnchors) {
		t.Fatalf("expected %d anchors, got %d (%v)", len(wantAnchors), len(w.anchors), w.anchors)
	}
	for _, a := range wantAnchors {
		if !w.anchors[a] {
			t.Errorf("expected %q to be an anchor", a)
		}
	}
}

func TestBuildKeywordWeights_DocFreqDownweightsGeneric(t *testing.T) {
	df := &DocFreqStats{
		DocFreq:   map[string]int{"fetterman": 100, "senate": 400},
		TotalDocs: 10000,
	}
	w := buildKeywordWeights("Donald Trump", []string{"trump", "fetterman", "senate"}, nil, df)

	if got := w.weights["fetterman"]; got != weightPlain {
		t.Errorf("fetterman (1%% of corpus) weight = %v, want %v", got, weightPlain)
	}
	if got := w.weights["senate"]; got != weightGeneric {
		t.Errorf("senate (4%% of corpus) weight = %v, want %v", got, weightGeneric)
	}
	if got := w.weights["trump"]; got != weightAnchor {
		t.Errorf("label anchor weight = %v, want %v", got, weightAnchor)
	}
}

func TestBuildKeywordWeights_AnchorSurvivesGenericList(t *testing.T) {
	// "vote" is in the built-in generic list but names this topic.
	w := buildKeywordWeights("Vote Counting", []string{"vote", "party"}, nil, nil)
	if got := w.weights["vote"]; got != weightAnchor {
		t.Errorf("vote weight = %v, want anchor weight %v", got, weightAnchor)
	}
	if got := w.weights["party"]; got != weightGeneric {
		t.Errorf("party weight = %v, want %v", got, weightGeneric)
	}
}

func TestBuildKeywordWeights_KeywordBeatsSynonymDuplicate(t *testing.T) {
	w := buildKeywordWeights("Hockey", []string{"binnington"}, []string{"binnington", "nhl"}, nil)
	if got := w.weights["binnington"]; got != weightPlain {
		t.Errorf("duplicate term weight = %v, want keyword weight %v", got, weightPlain)
	}
	if w.total != weightPlain+weightSynonym {
		t.Errorf("total = %v, want %v", w.total, weightPlain+weightSynonym)
	}
}

func TestIsLabelAnchor(t *testing.T) {
	tests := []struct {
		label string
		kw    string
		want  bool
	}{
		{"Donald Trump", "trump", true},
		{"College Football", "football", true},
		{"Big Brother 28", "28", true},
		{"Trump Tariffs", "tariff", true},
		{"Golden State Warriors", "war", false},
		{"College Football", "school", false},
		{"Big Brother 28", "bb28", false},
	}
	for _, tt := range tests {
		got := isLabelAnchor(splitLabelWords(tt.label), tt.kw)
		if got != tt.want {
			t.Errorf("isLabelAnchor(%q, %q) = %v, want %v", tt.label, tt.kw, got, tt.want)
		}
	}
}

func TestKeywordWeights_Relevance(t *testing.T) {
	w := buildKeywordWeights("College Football",
		[]string{"colorado", "football", "college", "georgia", "georgia_tech", "school"}, nil, nil)
	// total = 2+2+3+1+1+0.5 = 9.5
	tests := []struct {
		matched []string
		want    float64
	}{
		{[]string{"school", "football"}, 2.5 / 9.5},
		{[]string{"colorado", "football", "college", "georgia", "georgia_tech"}, 9.0 / 9.5},
		{[]string{"football", "football", "football"}, 2.0 / 9.5},
		{[]string{"unrelated"}, 0},
	}
	for _, tt := range tests {
		got := w.relevance(w.distinctMatched(tt.matched))
		if math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("relevance(%v) = %v, want %v", tt.matched, got, tt.want)
		}
	}
}

func TestKeywordWeights_AnchorRule(t *testing.T) {
	anchored := buildKeywordWeights("College Football", []string{"football", "school", "colorado"}, nil, nil)
	if anchored.meetsAnchorRule(anchored.distinctMatched([]string{"school", "colorado"})) {
		t.Error("expected anchorless match to be rejected for an anchored topic")
	}
	if !anchored.meetsAnchorRule(anchored.distinctMatched([]string{"football"})) {
		t.Error("expected a single anchor match to pass")
	}

	// "bb28" is not in the label, so this topic has no anchors.
	free := buildKeywordWeights("Big Brother 28", []string{"bb28", "hoh", "games"}, nil, nil)
	if len(free.anchors) != 0 {
		t.Fatalf("expected no anchors, got %v", free.anchors)
	}
	if free.meetsAnchorRule(free.distinctMatched([]string{"games"})) {
		t.Error("expected a single generic match to be rejected for an anchorless topic")
	}
	if !free.meetsAnchorRule(free.distinctMatched([]string{"bb28", "hoh"})) {
		t.Error("expected two distinct matches to pass for an anchorless topic")
	}
}

func TestExemplarQuality(t *testing.T) {
	long := "this is a perfectly ordinary post about the subject at hand today"

	root := exemplarQuality(long, 0, false)
	if math.Abs(root-qualityRootBoost) > 1e-9 {
		t.Errorf("zero-engagement root quality = %v, want %v", root, qualityRootBoost)
	}
	reply := exemplarQuality(long, 0, true)
	if math.Abs(reply-1) > 1e-9 {
		t.Errorf("zero-engagement reply quality = %v, want 1", reply)
	}
	if root <= reply {
		t.Errorf("root posts should outrank replies: %v vs %v", root, reply)
	}

	if exemplarQuality(long, 100, false) <= exemplarQuality(long, 10, false) {
		t.Error("quality should increase with engagement")
	}

	short := exemplarQuality("too short here", 100, false)
	if math.Abs(short-exemplarQuality(long, 100, false)*qualityThinPenalty) > 1e-9 {
		t.Errorf("short post should be penalised: %v", short)
	}

	hashy := exemplarQuality(long+" #a #b #c #d", 100, false)
	if math.Abs(hashy-exemplarQuality(long, 100, false)*qualityThinPenalty) > 1e-9 {
		t.Errorf("hashtag-stuffed post should be penalised: %v", hashy)
	}
}

func rankCandidate(uri, handle, text string, eng int, isReply bool, matched ...string) store.ExemplarCandidate {
	return store.ExemplarCandidate{
		URI:             uri,
		Handle:          handle,
		Text:            text,
		Engagement:      eng,
		IsReply:         isReply,
		Matched:         matched,
		DistinctMatches: len(matched),
		CreatedAt:       "2026-09-04T00:00:00Z",
	}
}

func TestRankExemplarCandidates_CoverageOverOccurrences(t *testing.T) {
	long := "this is a perfectly ordinary post about the subject at hand today"
	candidates := []store.ExemplarCandidate{
		rankCandidate("at://a/1", "narrow", long, 0, false, "school", "football"),
		rankCandidate("at://a/2", "broad", long, 5, false, "colorado", "football", "college", "georgia", "georgia_tech"),
		rankCandidate("at://a/3", "offtopic", long, 900, false, "school"),
	}

	ranked := rankExemplarCandidates("College Football",
		[]string{"colorado", "football", "college", "georgia", "georgia_tech", "school"}, nil, candidates, nil)

	if len(ranked) != 2 {
		t.Fatalf("expected the anchorless candidate to be dropped, got %d: %+v", len(ranked), ranked)
	}
	if ranked[0].Handle != "broad" {
		t.Errorf("expected broad coverage first, got %q", ranked[0].Handle)
	}
	if ranked[0].Score <= ranked[1].Score {
		t.Errorf("expected descending scores, got %v then %v", ranked[0].Score, ranked[1].Score)
	}
	if ranked[0].Relevance <= ranked[1].Relevance {
		t.Errorf("expected higher relevance first, got %v then %v", ranked[0].Relevance, ranked[1].Relevance)
	}
}

func TestRankExemplarCandidates_DropsUnusable(t *testing.T) {
	long := "this is a perfectly ordinary post about the subject at hand today"
	candidates := []store.ExemplarCandidate{
		rankCandidate("at://a/1", "", long, 100, false, "hockey", "nhl"),
		rankCandidate("at://a/2", "spam", strings.Repeat("hockey nhl ", 12), 100, false, "hockey", "nhl"),
		rankCandidate("at://a/3", "ok", long, 10, false, "hockey", "nhl"),
	}

	ranked := rankExemplarCandidates("Hockey", []string{"hockey", "nhl"}, nil, candidates, nil)
	if len(ranked) != 1 {
		t.Fatalf("expected 1 usable candidate, got %d", len(ranked))
	}
	if ranked[0].Handle != "ok" {
		t.Errorf("expected %q, got %q", "ok", ranked[0].Handle)
	}
}

func TestRankExemplarCandidates_TieBreaksOnEngagementThenRecency(t *testing.T) {
	long := "this is a perfectly ordinary post about the subject at hand today"
	older := rankCandidate("at://a/1", "older", long, 10, false, "hockey")
	older.CreatedAt = "2026-09-04T00:00:00Z"
	newer := rankCandidate("at://a/2", "newer", long, 10, false, "hockey")
	newer.CreatedAt = "2026-09-04T01:00:00Z"
	loud := rankCandidate("at://a/3", "loud", long, 11, false, "hockey")

	ranked := rankExemplarCandidates("Hockey", []string{"hockey"}, nil,
		[]store.ExemplarCandidate{older, newer, loud}, nil)

	if len(ranked) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(ranked))
	}
	got := []string{ranked[0].Handle, ranked[1].Handle, ranked[2].Handle}
	want := []string{"loud", "newer", "older"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rank order = %v, want %v", got, want)
		}
	}
}
