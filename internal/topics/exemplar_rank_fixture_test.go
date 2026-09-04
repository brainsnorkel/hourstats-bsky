package topics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// exemplarFixtureRow mirrors a row exported from production topic_tokens joined
// to post_buffer, ordered as the pre-weighting ranking returned it (occurrence
// matches desc, engagement desc). Handles and DIDs are anonymised.
type exemplarFixtureRow struct {
	URI               string `json:"uri"`
	Handle            string `json:"handle"`
	Text              string `json:"text"`
	Engagement        int    `json:"eng"`
	IsReply           int    `json:"is_reply"`
	CreatedAt         string `json:"created_at"`
	DistinctMatches   int    `json:"distinct_matches"`
	OccurrenceMatches int    `json:"occurrence_matches"`
	Matched           string `json:"matched"`
}

func loadExemplarFixture(t *testing.T, name string) []exemplarFixtureRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var rows []exemplarFixtureRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	if len(rows) == 0 {
		t.Fatalf("fixture %s is empty", name)
	}
	return rows
}

func (r exemplarFixtureRow) candidate() store.ExemplarCandidate {
	return store.ExemplarCandidate{
		URI:             r.URI,
		Handle:          r.Handle,
		Text:            r.Text,
		Engagement:      r.Engagement,
		MatchScore:      r.OccurrenceMatches,
		DistinctMatches: r.DistinctMatches,
		Matched:         strings.Split(r.Matched, ","),
		IsReply:         r.IsReply != 0,
		CreatedAt:       r.CreatedAt,
	}
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

// logTop3 prints the ranking the fixture was exported with next to the new
// weighted ranking, so a reviewer can compare the picks directly.
func logTop3(t *testing.T, label string, rows []exemplarFixtureRow, ranked []rankedExemplar) {
	t.Helper()
	t.Logf("=== %s: OLD top 3 (occurrence ranking) ===", label)
	for i, r := range rows {
		if i == 3 {
			break
		}
		t.Logf("  %d. eng=%-4d distinct=%d occurrences=%d matched=[%s] %s",
			i+1, r.Engagement, r.DistinctMatches, r.OccurrenceMatches, r.Matched, oneLine(r.Text, 90))
	}
	t.Logf("=== %s: NEW top 3 (weighted ranking) ===", label)
	for i, c := range ranked {
		if i == 3 {
			break
		}
		t.Logf("  %d. score=%.2f relevance=%.2f quality=%.2f eng=%-4d matched=[%s] %s",
			i+1, c.Score, c.Relevance, c.Quality, c.Engagement, strings.Join(c.Distinct, ","), oneLine(c.Text, 90))
	}
}

func rankFixture(t *testing.T, file, label string, keywords []string) ([]exemplarFixtureRow, []rankedExemplar) {
	t.Helper()
	rows := loadExemplarFixture(t, file)
	candidates := make([]store.ExemplarCandidate, 0, len(rows))
	for _, r := range rows {
		candidates = append(candidates, r.candidate())
	}
	ranked := rankExemplarCandidates(label, keywords, nil, candidates, nil)
	if len(ranked) < 3 {
		t.Fatalf("%s: expected at least 3 ranked candidates, got %d", label, len(ranked))
	}
	logTop3(t, label, rows, ranked)
	return rows, ranked
}

func TestRankFixture_CollegeFootball(t *testing.T) {
	_, ranked := rankFixture(t, "exemplar_football.json", "College Football",
		[]string{"colorado", "football", "college", "georgia", "georgia_tech", "school"})

	top := ranked[0]
	if len(top.Distinct) < 4 {
		t.Errorf("top pick matched %d distinct keywords (%v), want >= 4", len(top.Distinct), top.Distinct)
	}
	if top.Engagement <= 0 {
		t.Errorf("top pick engagement = %d, want > 0", top.Engagement)
	}
	// The UMass basketball post ranked first under occurrence counting purely
	// by repeating "school" and "football".
	for i := 0; i < 3; i++ {
		if strings.Contains(ranked[i].Text, "UMass") {
			t.Errorf("UMass basketball post should not be in the top 3, found at rank %d", i+1)
		}
	}
}

func TestRankFixture_BigBrother28(t *testing.T) {
	_, ranked := rankFixture(t, "exemplar_bb28.json", "Big Brother 28",
		[]string{"bb28", "hoh", "games"})

	for i := 0; i < 3; i++ {
		matched := ranked[i].Distinct
		onTopic := false
		for _, m := range matched {
			if m == "bb28" || m == "hoh" {
				onTopic = true
			}
		}
		if !onTopic {
			t.Errorf("rank %d matched %v: expected bb28 or hoh, not a generic keyword alone", i+1, matched)
		}
	}
}

func TestRankFixture_DonaldTrump(t *testing.T) {
	_, ranked := rankFixture(t, "exemplar_trump.json", "Donald Trump",
		[]string{"trump", "fetterman", "vote", "party", "court", "war", "says"})

	if ranked[0].Engagement <= 0 {
		t.Errorf("top pick engagement = %d, want > 0", ranked[0].Engagement)
	}
	if !ranked[0].hasMatch("trump") {
		t.Errorf("top pick matched %v, want the label anchor 'trump'", ranked[0].Distinct)
	}
}

func (r rankedExemplar) hasMatch(term string) bool {
	for _, m := range r.Distinct {
		if m == term {
			return true
		}
	}
	return false
}

// TestRankFixture_GenericOnlyMatchesDropped documents that posts whose only
// overlap with a topic is a generic keyword no longer qualify at all.
func TestRankFixture_GenericOnlyMatchesDropped(t *testing.T) {
	rows := loadExemplarFixture(t, "exemplar_bb28.json")
	candidates := make([]store.ExemplarCandidate, 0, len(rows))
	genericOnly := 0
	for _, r := range rows {
		if r.Matched == "games" {
			genericOnly++
		}
		candidates = append(candidates, r.candidate())
	}
	if genericOnly == 0 {
		t.Skip("fixture has no generic-only rows")
	}

	ranked := rankExemplarCandidates("Big Brother 28", []string{"bb28", "hoh", "games"}, nil, candidates, nil)
	for _, c := range ranked {
		if len(c.Distinct) == 1 && c.Distinct[0] == "games" {
			t.Errorf("generic-only candidate survived ranking: %s", oneLine(c.Text, 90))
		}
	}
	t.Log(fmt.Sprintf("dropped %d generic-only rows of %d", genericOnly, len(rows)))
}
