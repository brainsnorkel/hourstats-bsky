package topics

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// docEntry records a (capped) term frequency contribution from a single doc.
type docEntry struct {
	tf int // already capped at MaxTermFreqPerDoc
}

func ComputeTFIDF(rows []store.TopicTokenRow) []TermScore {
	if len(rows) == 0 {
		return nil
	}

	totalDocs := float64(len(rows))
	// inverted maps term -> per-doc TF contributions (length == DF).
	inverted := make(map[string][]docEntry)
	authorFreq := make(map[string]map[string]bool) // term -> set of author DIDs

	for _, row := range rows {
		var tokens []string
		if err := json.Unmarshal([]byte(row.Tokens), &tokens); err != nil {
			continue
		}

		// Count raw TF for this doc.
		tf := make(map[string]int)
		for _, tok := range tokens {
			tf[tok]++
		}

		// Append one docEntry per term seen in this doc; update authorFreq.
		for tok, count := range tf {
			capped := count
			if capped > MaxTermFreqPerDoc {
				capped = MaxTermFreqPerDoc
			}
			inverted[tok] = append(inverted[tok], docEntry{tf: capped})
			if row.AuthorDID != "" {
				if authorFreq[tok] == nil {
					authorFreq[tok] = make(map[string]bool)
				}
				authorFreq[tok][row.AuthorDID] = true
			}
		}
	}

	// Score: iterate inverted index once; DF == len(entries).
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
		results = append(results, TermScore{
			Term:    term,
			Score:   score,
			DocFreq: docFreq[term],
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > MaxTFIDFTerms {
		results = results[:MaxTFIDFTerms]
	}
	return results
}
