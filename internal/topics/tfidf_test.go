package topics

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func makeTokenRow(uri string, tokens []string) store.TopicTokenRow {
	b, _ := json.Marshal(tokens)
	return store.TopicTokenRow{PostURI: uri, Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z", AuthorDID: "did:plc:" + uri}
}

func makeTokenRowWithAuthor(uri string, tokens []string, authorDID string) store.TopicTokenRow {
	b, _ := json.Marshal(tokens)
	return store.TopicTokenRow{PostURI: uri, Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z", AuthorDID: authorDID}
}

func TestComputeTFIDF_BasicRanking(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 20; i++ {
		rows = append(rows, makeTokenRow(fmt.Sprintf("at://a/%d", i), []string{"common", "word", "filler"}))
	}
	for i := 0; i < 15; i++ {
		rows = append(rows, makeTokenRow(fmt.Sprintf("at://b/%d", i), []string{"trump", "election"}))
	}
	for i := 0; i < 12; i++ {
		rows = append(rows, makeTokenRow(fmt.Sprintf("at://c/%d", i), []string{"weather", "rain"}))
	}

	results := ComputeTFIDF(rows)
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	termMap := make(map[string]float64)
	for _, r := range results {
		termMap[r.Term] = r.Score
	}

	if _, ok := termMap["trump"]; !ok {
		t.Error("expected 'trump' in results")
	}
	if _, ok := termMap["election"]; !ok {
		t.Error("expected 'election' in results")
	}
}

func TestComputeTFIDF_MinDocFrequency(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 20; i++ {
		rows = append(rows, makeTokenRow(fmt.Sprintf("at://a/%d", i), []string{"common"}))
	}
	for i := 0; i < 5; i++ {
		rows = append(rows, makeTokenRow(fmt.Sprintf("at://b/%d", i), []string{"rare_term"}))
	}

	results := ComputeTFIDF(rows)
	for _, r := range results {
		if r.Term == "rare_term" {
			t.Errorf("rare_term (df=5) should be excluded (min=%d)", MinDocFrequency)
		}
	}
}

func TestComputeTFIDF_MaxTerms(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 50; i++ {
		tokens := []string{fmt.Sprintf("term%d", i%40)}
		rows = append(rows, makeTokenRow(fmt.Sprintf("at://a/%d", i), tokens))
	}

	results := ComputeTFIDF(rows)
	if len(results) > MaxTFIDFTerms {
		t.Errorf("expected max %d results, got %d", MaxTFIDFTerms, len(results))
	}
}

func TestComputeTFIDF_Empty(t *testing.T) {
	results := ComputeTFIDF(nil)
	if results != nil {
		t.Errorf("expected nil, got %v", results)
	}
}

func TestComputeTFIDF_AllIdenticalTokens(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 20; i++ {
		rows = append(rows, makeTokenRow(fmt.Sprintf("at://a/%d", i), []string{"same", "same", "same"}))
	}

	results := ComputeTFIDF(rows)
	if len(results) != 1 {
		t.Errorf("expected 1 term, got %d", len(results))
	}
	if len(results) > 0 && results[0].Term != "same" {
		t.Errorf("expected 'same', got %q", results[0].Term)
	}
}

func TestComputeTFIDF_TermFrequencyCap(t *testing.T) {
	var rows []store.TopicTokenRow

	// One post repeats "flood" 20 times — should be capped at MaxTermFreqPerDoc.
	spamTokens := make([]string, 20)
	for i := range spamTokens {
		spamTokens[i] = "flood"
	}
	rows = append(rows, makeTokenRow("at://spam/0", spamTokens))

	// 19 normal posts mention "flood" once each.
	for i := 1; i < 20; i++ {
		rows = append(rows, makeTokenRow(fmt.Sprintf("at://normal/%d", i), []string{"flood", "damage"}))
	}

	results := ComputeTFIDF(rows)
	var floodScore float64
	for _, r := range results {
		if r.Term == "flood" {
			floodScore = r.Score
			break
		}
	}

	// Without cap: spam doc contributes 20*IDF. With cap: 3*IDF.
	// 20 docs total → IDF = ln(20/20) = 0 (appears in all docs).
	// Use "damage" (appears in 19 docs) as baseline instead.
	var damageScore float64
	for _, r := range results {
		if r.Term == "damage" {
			damageScore = r.Score
			break
		}
	}

	// The spam post should not make "flood" disproportionately outscore
	// "damage". Without cap, flood would score ~38*IDF vs damage's 19*IDF.
	// With cap, flood scores ~22*IDF vs damage's 19*IDF — much closer.
	if floodScore > damageScore*2 {
		t.Errorf("TF cap should prevent spam amplification: flood=%.2f, damage=%.2f", floodScore, damageScore)
	}
}

func TestComputeTFIDF_SingleAuthorSpam(t *testing.T) {
	spammer := "did:plc:spammer"
	var rows []store.TopicTokenRow
	for i := 0; i < 20; i++ {
		rows = append(rows, makeTokenRowWithAuthor(fmt.Sprintf("at://spam/%d", i), []string{"excuse", "team_excuse"}, spammer))
	}
	for i := 0; i < 20; i++ {
		rows = append(rows, makeTokenRow(fmt.Sprintf("at://legit/%d", i), []string{"weather", "rain"}))
	}

	results := ComputeTFIDF(rows)
	termMap := make(map[string]bool)
	for _, r := range results {
		termMap[r.Term] = true
	}

	if termMap["excuse"] {
		t.Error("single-author term 'excuse' should be filtered by MinUniqueAuthors")
	}
	if termMap["team_excuse"] {
		t.Error("single-author term 'team_excuse' should be filtered by MinUniqueAuthors")
	}
	if !termMap["weather"] {
		t.Error("multi-author term 'weather' should pass MinUniqueAuthors")
	}
}
