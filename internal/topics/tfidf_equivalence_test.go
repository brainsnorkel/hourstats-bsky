package topics

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// referenceComputeTFIDF is the straightforward single-pass implementation that
// ComputeTFIDF replaced: one postings slice and one author map per distinct
// term, built for every term regardless of document frequency. It is kept here
// as the oracle for the two-pass version.
func referenceComputeTFIDF(rows []store.TopicTokenRow) []TermScore {
	if len(rows) == 0 {
		return nil
	}

	type refEntry struct{ tf int }

	totalDocs := float64(len(rows))
	inverted := make(map[string][]refEntry)
	authorFreq := make(map[string]map[string]bool)

	for _, row := range rows {
		var tokens []string
		if err := json.Unmarshal([]byte(row.Tokens), &tokens); err != nil {
			continue
		}

		tf := make(map[string]int)
		for _, tok := range tokens {
			tf[tok]++
		}

		for tok, count := range tf {
			capped := count
			if capped > MaxTermFreqPerDoc {
				capped = MaxTermFreqPerDoc
			}
			inverted[tok] = append(inverted[tok], refEntry{tf: capped})
			if row.AuthorDID != "" {
				if authorFreq[tok] == nil {
					authorFreq[tok] = make(map[string]bool)
				}
				authorFreq[tok][row.AuthorDID] = true
			}
		}
	}

	tfidfScores := make(map[string]float64, len(inverted))
	docFreq := make(map[string]int, len(inverted))
	for term, entries := range inverted {
		df := len(entries)
		docFreq[term] = df
		if df < MinDocFrequency {
			continue
		}
		if len(authorFreq[term]) < MinUniqueAuthors {
			continue
		}
		idf := math.Log(totalDocs / float64(df))
		var totalTFIDF float64
		for _, e := range entries {
			totalTFIDF += float64(e.tf) * idf
		}
		tfidfScores[term] = totalTFIDF
	}

	results := make([]TermScore, 0, len(tfidfScores))
	for term, score := range tfidfScores {
		results = append(results, TermScore{Term: term, Score: score, DocFreq: docFreq[term]})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > MaxTFIDFTerms {
		results = results[:MaxTFIDFTerms]
	}
	return results
}

// canonical sorts by score descending then term ascending so the comparison is
// insensitive to the arbitrary ordering that map iteration plus an unstable
// sort.Slice gives equal-scoring terms in both implementations.
func canonical(in []TermScore) []TermScore {
	out := append([]TermScore(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Term < out[j].Term
	})
	return out
}

func assertSameScores(t *testing.T, got, want []TermScore) {
	t.Helper()
	got, want = canonical(got), canonical(want)
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d terms, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("results[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestComputeTFIDF_MatchesReference_SingletonHeavyCorpus feeds a corpus whose
// term distribution is dominated by singletons — the shape that motivated the
// two-pass rewrite — and asserts the output still matches the reference
// implementation exactly, scores included.
func TestComputeTFIDF_MatchesReference_SingletonHeavyCorpus(t *testing.T) {
	rng := rand.New(rand.NewSource(20260903))

	// 30 shared terms spread over many authors; each doc also carries several
	// unique terms that will never clear MinDocFrequency.
	shared := make([]string, 30)
	for i := range shared {
		shared[i] = fmt.Sprintf("shared%02d", i)
	}

	var rows []store.TopicTokenRow
	for i := 0; i < 600; i++ {
		var tokens []string
		for j := 0; j < 4; j++ {
			// Repeat some terms to exercise the MaxTermFreqPerDoc cap.
			term := shared[rng.Intn(len(shared))]
			repeats := 1 + rng.Intn(5)
			for r := 0; r < repeats; r++ {
				tokens = append(tokens, term)
			}
		}
		// Long tail: terms seen in exactly one document.
		for j := 0; j < 6; j++ {
			tokens = append(tokens, fmt.Sprintf("singleton_%d_%d", i, j))
		}
		// A handful of terms just under MinDocFrequency.
		if i%97 == 0 {
			tokens = append(tokens, "borderline")
		}
		author := fmt.Sprintf("did:plc:author%d", i%40)
		rows = append(rows, makeTokenRowWithAuthor(fmt.Sprintf("at://x/%d", i), tokens, author))
	}

	got := ComputeTFIDF(rows)
	want := referenceComputeTFIDF(rows)

	if len(want) == 0 {
		t.Fatal("reference produced no results; corpus is not exercising the scorer")
	}
	assertSameScores(t, got, want)

	for _, r := range got {
		if r.DocFreq < MinDocFrequency {
			t.Errorf("term %q survived with df=%d, below MinDocFrequency=%d", r.Term, r.DocFreq, MinDocFrequency)
		}
	}
}

// TestComputeTFIDF_MatchesReference_SingleAuthorTail checks the author gate and
// the empty-result path against the reference: every term here comes from one
// author, so nothing may survive MinUniqueAuthors.
func TestComputeTFIDF_MatchesReference_SingleAuthorTail(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 200; i++ {
		tokens := []string{"solo", "solo", fmt.Sprintf("unique%d", i)}
		rows = append(rows, makeTokenRowWithAuthor(fmt.Sprintf("at://y/%d", i), tokens, "did:plc:only"))
	}

	got := ComputeTFIDF(rows)
	want := referenceComputeTFIDF(rows)

	if len(want) != 0 {
		t.Fatalf("reference should filter everything, got %+v", want)
	}
	if got == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	assertSameScores(t, got, want)
}

// TestComputeTFIDF_MatchesReference_OverMaxTerms drives more qualifying terms
// than MaxTFIDFTerms, with distinct document frequencies so the truncation
// boundary is unambiguous.
func TestComputeTFIDF_MatchesReference_OverMaxTerms(t *testing.T) {
	var rows []store.TopicTokenRow
	const docs = 400
	for i := 0; i < docs; i++ {
		var tokens []string
		// term_k appears in the first (MinDocFrequency + 2*k) documents, giving
		// every term a distinct df and therefore a distinct score.
		for k := 0; k < 80; k++ {
			if i < MinDocFrequency+2*k {
				tokens = append(tokens, fmt.Sprintf("term_%02d", k))
			}
		}
		tokens = append(tokens, fmt.Sprintf("noise%d", i))
		author := fmt.Sprintf("did:plc:a%d", i%25)
		rows = append(rows, makeTokenRowWithAuthor(fmt.Sprintf("at://z/%d", i), tokens, author))
	}

	got := ComputeTFIDF(rows)
	want := referenceComputeTFIDF(rows)

	if len(want) != MaxTFIDFTerms {
		t.Fatalf("reference returned %d terms, want %d (truncation not exercised)", len(want), MaxTFIDFTerms)
	}
	assertSameScores(t, got, want)
}

// TestComputeTFIDF_MatchesReference_MalformedTokens confirms rows with invalid
// token JSON are skipped identically and still count toward totalDocs.
func TestComputeTFIDF_MatchesReference_MalformedTokens(t *testing.T) {
	var rows []store.TopicTokenRow
	for i := 0; i < 60; i++ {
		if i%7 == 0 {
			rows = append(rows, store.TopicTokenRow{
				PostURI:   fmt.Sprintf("at://bad/%d", i),
				Tokens:    "{not json",
				CreatedAt: "2026-01-01T00:00:00Z",
				AuthorDID: fmt.Sprintf("did:plc:a%d", i%9),
			})
			continue
		}
		tokens := []string{"alpha", "beta", "beta", "beta", "beta"}
		rows = append(rows, makeTokenRowWithAuthor(fmt.Sprintf("at://ok/%d", i), tokens, fmt.Sprintf("did:plc:a%d", i%9)))
	}

	got := ComputeTFIDF(rows)
	want := referenceComputeTFIDF(rows)

	if len(want) == 0 {
		t.Fatal("reference produced no results")
	}
	assertSameScores(t, got, want)
}

// TestComputeTFIDF_DocFrequencyBoundary pins the MinDocFrequency comparison to
// >= rather than >. A term seen in exactly MinDocFrequency documents by enough
// distinct authors must survive; one document fewer must not.
func TestComputeTFIDF_DocFrequencyBoundary(t *testing.T) {
	// 200 filler docs so IDF stays positive and the corpus is realistic.
	build := func(atFloorDocs, belowFloorDocs int) []store.TopicTokenRow {
		var rows []store.TopicTokenRow
		for i := 0; i < atFloorDocs; i++ {
			rows = append(rows, makeTokenRowWithAuthor(
				fmt.Sprintf("at://floor/%d", i),
				[]string{"atfloor"},
				fmt.Sprintf("did:plc:floor%d", i), // one author per doc
			))
		}
		for i := 0; i < belowFloorDocs; i++ {
			rows = append(rows, makeTokenRowWithAuthor(
				fmt.Sprintf("at://below/%d", i),
				[]string{"belowfloor"},
				fmt.Sprintf("did:plc:below%d", i),
			))
		}
		for i := 0; i < 200; i++ {
			rows = append(rows, makeTokenRowWithAuthor(
				fmt.Sprintf("at://filler/%d", i),
				[]string{fmt.Sprintf("filler%d", i)},
				fmt.Sprintf("did:plc:filler%d", i),
			))
		}
		return rows
	}

	rows := build(MinDocFrequency, MinDocFrequency-1)

	got := make(map[string]int)
	for _, r := range ComputeTFIDF(rows) {
		got[r.Term] = r.DocFreq
	}

	if df, ok := got["atfloor"]; !ok {
		t.Errorf("term with df == MinDocFrequency (%d) was excluded; the gate must be >=, not >", MinDocFrequency)
	} else if df != MinDocFrequency {
		t.Errorf("atfloor DocFreq = %d, want %d", df, MinDocFrequency)
	}

	if _, ok := got["belowfloor"]; ok {
		t.Errorf("term with df == %d (below MinDocFrequency %d) was included", MinDocFrequency-1, MinDocFrequency)
	}
}

// TestComputeTFIDF_UniqueAuthorBoundary does the same for MinUniqueAuthors:
// exactly MinUniqueAuthors distinct authors must pass, one fewer must not.
func TestComputeTFIDF_UniqueAuthorBoundary(t *testing.T) {
	var rows []store.TopicTokenRow
	// Both terms clear MinDocFrequency; they differ only in author spread.
	for i := 0; i < MinDocFrequency*2; i++ {
		rows = append(rows, makeTokenRowWithAuthor(
			fmt.Sprintf("at://exact/%d", i),
			[]string{"exactauthors"},
			fmt.Sprintf("did:plc:exact%d", i%MinUniqueAuthors), // exactly MinUniqueAuthors
		))
		rows = append(rows, makeTokenRowWithAuthor(
			fmt.Sprintf("at://short/%d", i),
			[]string{"shortauthors"},
			fmt.Sprintf("did:plc:short%d", i%(MinUniqueAuthors-1)), // one author short
		))
	}
	for i := 0; i < 200; i++ {
		rows = append(rows, makeTokenRowWithAuthor(
			fmt.Sprintf("at://filler/%d", i),
			[]string{fmt.Sprintf("filler%d", i)},
			fmt.Sprintf("did:plc:filler%d", i),
		))
	}

	got := make(map[string]bool)
	for _, r := range ComputeTFIDF(rows) {
		got[r.Term] = true
	}

	if !got["exactauthors"] {
		t.Errorf("term with exactly MinUniqueAuthors (%d) distinct authors was excluded; the gate must be >=, not >", MinUniqueAuthors)
	}
	if got["shortauthors"] {
		t.Errorf("term with %d distinct authors (below MinUniqueAuthors %d) was included", MinUniqueAuthors-1, MinUniqueAuthors)
	}
}
