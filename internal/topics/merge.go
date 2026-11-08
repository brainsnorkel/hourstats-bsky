package topics

import (
	"log/slog"
	"sort"
	"strings"
)

// MergeSimilarClusters combines clusters whose keywords are linked by a
// bigram present in the TF-IDF term list. For example, if two separate
// clusters contain keywords "kristi" and "noem" respectively, and the
// bigram "kristi_noem" exists among the TF-IDF terms, the clusters are
// merged into a single "Kristi Noem" topic.
//
// This prevents first-name / last-name splits that occur when the Gemini
// grouper falls back to single-keyword clusters or when the LLM fails to
// merge related terms.
func MergeSimilarClusters(clusters []TopicCluster, terms []TermScore) []TopicCluster {
	if len(clusters) <= 1 {
		return clusters
	}

	bigrams := make(map[string]bool)
	scores := make(map[string]float64, len(terms))
	for _, t := range terms {
		scores[strings.ToLower(t.Term)] = t.Score
		if strings.Contains(t.Term, "_") {
			bigrams[t.Term] = true
		}
	}
	if len(bigrams) == 0 {
		return clusters
	}

	// Union-find groups clusters bridged by a shared bigram.
	parent := make([]int, len(clusters))
	for i := range parent {
		parent[i] = i
	}
	for i := 0; i < len(clusters); i++ {
		for j := i + 1; j < len(clusters); j++ {
			if hasBigramBridge(clusters[i].Keywords, clusters[j].Keywords, bigrams) {
				ufUnion(parent, i, j)
			}
		}
	}

	// Collect groups preserving original index order.
	type group struct {
		root    int
		indices []int
	}
	var groups []group
	rootIdx := make(map[int]int) // root → index in groups slice
	for i := range clusters {
		root := ufFind(parent, i)
		if gi, ok := rootIdx[root]; ok {
			groups[gi].indices = append(groups[gi].indices, i)
		} else {
			rootIdx[root] = len(groups)
			groups = append(groups, group{root: root, indices: []int{i}})
		}
	}

	// If nothing was merged, return the original slice.
	if len(groups) == len(clusters) {
		return clusters
	}

	result := make([]TopicCluster, 0, len(groups))
	for _, g := range groups {
		if len(g.indices) == 1 {
			result = append(result, clusters[g.indices[0]])
			continue
		}
		mc := buildMergedCluster(clusters, g.indices, bigrams, scores)
		var labels []string
		for _, idx := range g.indices {
			labels = append(labels, clusters[idx].Label)
		}
		slog.Info("merge: combined clusters via bigram bridge",
			"label", mc.Label,
			"merged_labels", labels,
		)
		result = append(result, mc)
	}

	return result
}

// hasBigramBridge reports whether any keyword from a paired with any
// keyword from b forms a bigram (in either word order) present in the set.
func hasBigramBridge(kwsA, kwsB []string, bigrams map[string]bool) bool {
	for _, a := range kwsA {
		a = strings.ToLower(a)
		for _, b := range kwsB {
			b = strings.ToLower(b)
			if bigrams[a+"_"+b] || bigrams[b+"_"+a] {
				return true
			}
		}
	}
	return false
}

// buildMergedCluster combines multiple clusters into one, unioning their
// keywords and synonyms, adding bridging bigrams, and choosing the best label.
func buildMergedCluster(clusters []TopicCluster, indices []int, bigrams map[string]bool, scores map[string]float64) TopicCluster {
	kwSet := make(map[string]bool)
	synSet := make(map[string]bool)
	var descriptions []string
	var justifications []string
	isMeme := false

	for _, idx := range indices {
		c := clusters[idx]
		for _, kw := range c.Keywords {
			kwSet[kw] = true
		}
		for _, syn := range c.Synonyms {
			synSet[syn] = true
		}
		if c.Description != "" && c.Description != "Trending term" {
			descriptions = append(descriptions, c.Description)
		}
		if c.Justification != "" {
			justifications = append(justifications, c.Justification)
		}
		if c.IsMeme {
			isMeme = true
		}
	}

	// Include bridging bigrams in the keyword set so the ranker can
	// match posts containing the bigram token.
	origKWs := sortedKeys(kwSet)
	for i := 0; i < len(origKWs); i++ {
		for j := i + 1; j < len(origKWs); j++ {
			for _, candidate := range []string{origKWs[i] + "_" + origKWs[j], origKWs[j] + "_" + origKWs[i]} {
				if bigrams[candidate] {
					kwSet[candidate] = true
				}
			}
		}
	}

	label := pickMergedLabel(clusters, indices, bigrams, scores)

	description := "Trending term"
	if len(descriptions) > 0 {
		description = descriptions[0]
	}

	justification := ""
	if len(justifications) > 0 {
		justification = strings.Join(justifications, "; ")
	}

	return TopicCluster{
		Label:         label,
		Description:   description,
		Keywords:      sortedKeys(kwSet),
		Synonyms:      sortedKeys(synSet),
		Justification: justification,
		IsMeme:        isMeme,
	}
}

// pickMergedLabel selects the label for a merged cluster.
//
// The member cluster whose keywords carry the highest summed TF-IDF score
// names the subject: a bridge can join "2026 Midterm Results" to an election
// cluster labelled for another year, and the longer label is not the more
// relevant one. The bridging bigram's label replaces that winner only when
// it has more words and contains every word of it, which is the name split
// the merge exists for ("Kristi" + "Noem" via kristi_noem is "Kristi Noem").
// When two or more members tie on the top score, the most-words rule decides
// among the bigram label and every member label (longer string on a tie).
func pickMergedLabel(clusters []TopicCluster, indices []int, bigrams map[string]bool, scores map[string]float64) string {
	// Find the longest bridging bigram between any two merged clusters.
	var bestBigram string
	for i := 0; i < len(indices); i++ {
		for j := i + 1; j < len(indices); j++ {
			for _, a := range clusters[indices[i]].Keywords {
				a = strings.ToLower(a)
				for _, b := range clusters[indices[j]].Keywords {
					b = strings.ToLower(b)
					for _, candidate := range []string{a + "_" + b, b + "_" + a} {
						if bigrams[candidate] && len(candidate) > len(bestBigram) {
							bestBigram = candidate
						}
					}
				}
			}
		}
	}
	bigramLabel := ""
	if bestBigram != "" {
		bigramLabel = bigramToLabel(bestBigram)
	}

	winner, winnerScore, tied := -1, 0.0, false
	for _, idx := range indices {
		sum := 0.0
		for _, k := range clusters[idx].Keywords {
			sum += scores[strings.ToLower(k)]
		}
		switch {
		case winner < 0 || sum > winnerScore:
			winner, winnerScore, tied = idx, sum, false
		case sum == winnerScore:
			tied = true
		}
	}
	if !tied {
		label := clusters[winner].Label
		if bigramLabel != "" && len(strings.Fields(bigramLabel)) > len(strings.Fields(label)) && containsAllWords(bigramLabel, label) {
			return bigramLabel
		}
		return label
	}

	bestLabel := ""
	bestWordCount := 0
	candidates := []string{bigramLabel}
	for _, idx := range indices {
		candidates = append(candidates, clusters[idx].Label)
	}
	for _, label := range candidates {
		if label == "" {
			continue
		}
		wc := len(strings.Fields(label))
		if wc > bestWordCount || (wc == bestWordCount && len(label) > len(bestLabel)) {
			bestLabel = label
			bestWordCount = wc
		}
	}
	return bestLabel
}

// containsAllWords reports whether every word of sub, case-folded, is a word
// of s.
func containsAllWords(s, sub string) bool {
	words := make(map[string]bool)
	for _, w := range strings.Fields(strings.ToLower(s)) {
		words[w] = true
	}
	for _, w := range strings.Fields(strings.ToLower(sub)) {
		if !words[w] {
			return false
		}
	}
	return true
}

// bigramToLabel converts a bigram like "kristi_noem" to "Kristi Noem".
func bigramToLabel(bigram string) string {
	parts := strings.Split(bigram, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Union-find helpers.

func ufFind(parent []int, i int) int {
	for parent[i] != i {
		parent[i] = parent[parent[i]] // path compression
		i = parent[i]
	}
	return i
}

func ufUnion(parent []int, i, j int) {
	ri, rj := ufFind(parent, i), ufFind(parent, j)
	if ri != rj {
		parent[ri] = rj
	}
}
