package topics

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ComputeTFIDF scores corpus terms in two passes over the rows.
//
// Pass 1 counts document frequency only, into a single map[string]int.
// Pass 2 accumulates the capped TF contribution and the unique-author set, but
// only for terms that already cleared MinDocFrequency. This matters because the
// term distribution is a long tail of singletons: at ~114k posts the previous
// single-pass version allocated a postings slice and an author map for every
// distinct term, then discarded almost all of them at the MinDocFrequency gate.
//
// Scoring is unchanged. Per-term contributions are still summed in row order as
// float64(cappedTF) * idf, so results are bit-for-bit identical to the
// single-pass version.
func ComputeTFIDF(rows []store.TopicTokenRow) []TermScore {
	if len(rows) == 0 {
		return nil
	}

	totalDocs := float64(len(rows))

	// tokens is reused across rows; json.Unmarshal reuses the backing array.
	var tokens []string

	// Pass 1: document frequency (one increment per term per document).
	docFreq := make(map[string]int)
	seen := make(map[string]struct{})
	for _, row := range rows {
		if err := json.Unmarshal([]byte(row.Tokens), &tokens); err != nil {
			continue
		}
		clear(seen)
		for _, tok := range tokens {
			if _, dup := seen[tok]; dup {
				continue
			}
			seen[tok] = struct{}{}
			docFreq[tok]++
		}
	}

	// IDF for the terms that clear the document-frequency floor. Membership in
	// this map is also the pass-2 filter.
	idf := make(map[string]float64)
	for term, df := range docFreq {
		if df >= MinDocFrequency {
			idf[term] = math.Log(totalDocs / float64(df))
		}
	}

	// Pass 2: accumulate scores and author sets for surviving terms only.
	tfidfScores := make(map[string]float64, len(idf))
	authorFreq := make(map[string]map[string]struct{}, len(idf)) // term -> set of author DIDs
	tf := make(map[string]int)
	for _, row := range rows {
		if err := json.Unmarshal([]byte(row.Tokens), &tokens); err != nil {
			continue
		}

		// Count raw TF for this doc, ignoring terms below MinDocFrequency.
		clear(tf)
		for _, tok := range tokens {
			if _, ok := idf[tok]; ok {
				tf[tok]++
			}
		}

		for tok, count := range tf {
			capped := count
			if capped > MaxTermFreqPerDoc {
				capped = MaxTermFreqPerDoc
			}
			tfidfScores[tok] += float64(capped) * idf[tok]
			if row.AuthorDID != "" {
				if authorFreq[tok] == nil {
					authorFreq[tok] = make(map[string]struct{})
				}
				authorFreq[tok][row.AuthorDID] = struct{}{}
			}
		}
	}

	results := make([]TermScore, 0, len(tfidfScores))
	for term, score := range tfidfScores {
		if len(authorFreq[term]) < MinUniqueAuthors {
			continue
		}
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
