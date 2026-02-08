package topics

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func ComputeTFIDF(rows []store.TopicTokenRow) []TermScore {
	if len(rows) == 0 {
		return nil
	}

	totalDocs := float64(len(rows))
	docFreq := make(map[string]int)
	termFreqs := make([]map[string]int, len(rows))

	for i, row := range rows {
		var tokens []string
		if err := json.Unmarshal([]byte(row.Tokens), &tokens); err != nil {
			continue
		}

		tf := make(map[string]int)
		seen := make(map[string]bool)
		for _, tok := range tokens {
			tf[tok]++
			if !seen[tok] {
				docFreq[tok]++
				seen[tok] = true
			}
		}
		termFreqs[i] = tf
	}

	tfidfScores := make(map[string]float64)
	for term, df := range docFreq {
		if df < MinDocFrequency {
			continue
		}
		idf := math.Log(totalDocs / float64(df))
		var totalTFIDF float64
		for _, tf := range termFreqs {
			if tf == nil {
				continue
			}
			if count, ok := tf[term]; ok {
				totalTFIDF += float64(count) * idf
			}
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
