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
	for _, t := range terms {
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
		mc := buildMergedCluster(clusters, g.indices, bigrams)
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
func buildMergedCluster(clusters []TopicCluster, indices []int, bigrams map[string]bool) TopicCluster {
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

	label := pickMergedLabel(clusters, indices, bigrams)

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

// pickMergedLabel selects the best label for a merged cluster by comparing
// bridging bigram-derived labels against existing cluster labels, preferring
// whichever has the most words (most specific).
func pickMergedLabel(clusters []TopicCluster, indices []int, bigrams map[string]bool) string {
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

	bestLabel := ""
	bestWordCount := 0

	// Bigram-derived label candidate.
	if bestBigram != "" {
		label := bigramToLabel(bestBigram)
		wc := len(strings.Fields(label))
		if wc > bestWordCount || (wc == bestWordCount && len(label) > len(bestLabel)) {
			bestLabel = label
			bestWordCount = wc
		}
	}

	// Existing cluster labels.
	for _, idx := range indices {
		label := clusters[idx].Label
		wc := len(strings.Fields(label))
		if wc > bestWordCount || (wc == bestWordCount && len(label) > len(bestLabel)) {
			bestLabel = label
			bestWordCount = wc
		}
	}

	return bestLabel
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
