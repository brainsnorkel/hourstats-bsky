package topics

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// offlineJustification marks clusters produced without an LLM so downstream
// logging and snapshots can tell they came from the degraded offline path.
const offlineJustification = "offline co-occurrence grouping (Gemini unavailable)"

// AlgorithmicGroup groups TF-IDF terms into topic clusters without calling any
// external model. It builds a term co-occurrence graph over the post token
// sets, runs deterministic label-propagation community detection, and labels
// each community with its strongest in-community bigram (falling back to a
// joined multi-term label).
//
// It is the last rung before suppression when every Gemini tier fails: the
// output is coarser than LLM grouping but coherent, visibly bot-grouped, and
// never raw underscore tokens. Returns nil when there is nothing to group.
func AlgorithmicGroup(rows []store.TopicTokenRow, terms []TermScore) []TopicCluster {
	if len(terms) == 0 || len(rows) == 0 {
		return nil
	}

	// Partition TF-IDF terms into unigram nodes (clustered) and bigrams
	// (reserved for labels); record each term's score for ranking and labels.
	score := make(map[string]float64, len(terms))
	bigrams := make(map[string]bool)
	var unigrams []string
	for _, t := range terms {
		score[t.Term] = t.Score
		if strings.Contains(t.Term, "_") {
			bigrams[t.Term] = true
		} else {
			unigrams = append(unigrams, t.Term)
		}
	}
	if len(unigrams) == 0 {
		return nil
	}

	nodeSet := make(map[string]bool, len(unigrams))
	for _, u := range unigrams {
		nodeSet[u] = true
	}

	// Co-occurrence graph: edge weight = number of posts in which both
	// unigram nodes appear.
	adj := make(map[string]map[string]int, len(unigrams))
	for _, u := range unigrams {
		adj[u] = make(map[string]int)
	}
	for _, row := range rows {
		var tokens []string
		if err := json.Unmarshal([]byte(row.Tokens), &tokens); err != nil {
			continue
		}
		present := make([]string, 0, 8)
		seen := make(map[string]bool, len(tokens))
		for _, tok := range tokens {
			if nodeSet[tok] && !seen[tok] {
				seen[tok] = true
				present = append(present, tok)
			}
		}
		for i := 0; i < len(present); i++ {
			for j := i + 1; j < len(present); j++ {
				a, b := present[i], present[j]
				adj[a][b]++
				adj[b][a]++
			}
		}
	}

	communities := labelPropagation(unigrams, adj, score)

	clusters := make([]TopicCluster, 0, len(communities))
	for _, members := range communities {
		clusters = append(clusters, buildOfflineCluster(members, bigrams, score))
	}

	// Rank by aggregate TF-IDF score (descending), deterministic tie-break on
	// label, and cap to the same ceiling as the LLM path.
	sort.Slice(clusters, func(i, j int) bool {
		si, sj := clusterScore(clusters[i], score), clusterScore(clusters[j], score)
		if si != sj {
			return si > sj
		}
		return clusters[i].Label < clusters[j].Label
	})
	if len(clusters) > MaxLLMGroups {
		clusters = clusters[:MaxLLMGroups]
	}
	return clusters
}

// labelPropagation runs deterministic label propagation over the co-occurrence
// graph and returns the resulting communities, each a sorted slice of member
// terms. Nodes are processed in a fixed order (TF-IDF score desc, then term
// asc) so the result is reproducible for a given input.
func labelPropagation(nodes []string, adj map[string]map[string]int, score map[string]float64) [][]string {
	order := append([]string(nil), nodes...)
	sort.Slice(order, func(i, j int) bool {
		if score[order[i]] != score[order[j]] {
			return score[order[i]] > score[order[j]]
		}
		return order[i] < order[j]
	})

	// Each node starts in its own community, labelled by the node string.
	label := make(map[string]string, len(nodes))
	for _, n := range nodes {
		label[n] = n
	}

	const maxIters = 20
	for iter := 0; iter < maxIters; iter++ {
		changed := false
		for _, n := range order {
			neighbors := adj[n]
			if len(neighbors) == 0 {
				continue
			}
			// Weighted vote over neighbour labels.
			weight := make(map[string]int, len(neighbors))
			for nb, w := range neighbors {
				weight[label[nb]] += w
			}
			// Deterministic argmax: highest weight, lexicographic tie-break.
			best := label[n]
			bestW := weight[best]
			for lbl, w := range weight {
				if w > bestW || (w == bestW && lbl < best) {
					best, bestW = lbl, w
				}
			}
			if best != label[n] {
				label[n] = best
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	groups := make(map[string][]string)
	for _, n := range nodes {
		groups[label[n]] = append(groups[label[n]], n)
	}
	out := make([][]string, 0, len(groups))
	for _, members := range groups {
		sort.Strings(members)
		out = append(out, members)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// buildOfflineCluster turns a community of unigram terms into a TopicCluster,
// labelling it with the strongest in-community bigram (e.g. "kristi_noem" ->
// "Kristi Noem") or, failing that, a joined multi-term label ("Trump · Iran").
func buildOfflineCluster(members []string, bigrams map[string]bool, score map[string]float64) TopicCluster {
	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m] = true
	}

	// Order members by TF-IDF score desc for label and keyword prominence.
	ordered := append([]string(nil), members...)
	sort.Slice(ordered, func(i, j int) bool {
		if score[ordered[i]] != score[ordered[j]] {
			return score[ordered[i]] > score[ordered[j]]
		}
		return ordered[i] < ordered[j]
	})

	// Prefer the highest-scoring bigram whose both halves are in this community.
	bestBigram := ""
	bestBigramScore := -1.0
	for bg := range bigrams {
		parts := strings.SplitN(bg, "_", 2)
		if len(parts) != 2 {
			continue
		}
		if memberSet[parts[0]] && memberSet[parts[1]] {
			if s := score[bg]; s > bestBigramScore {
				bestBigramScore = s
				bestBigram = bg
			}
		}
	}

	var label string
	switch {
	case bestBigram != "":
		label = bigramToLabel(bestBigram)
	case len(ordered) >= 2:
		label = titleCaseWord(ordered[0]) + " · " + titleCaseWord(ordered[1])
	default:
		label = titleCaseWord(ordered[0])
	}

	return TopicCluster{
		Label:         label,
		Description:   strings.Join(ordered, ", "),
		Keywords:      ordered,
		Synonyms:      []string{},
		Justification: offlineJustification,
		IsMeme:        false,
	}
}

// clusterScore is the aggregate TF-IDF score of a cluster's keywords.
func clusterScore(c TopicCluster, score map[string]float64) float64 {
	var sum float64
	for _, kw := range c.Keywords {
		sum += score[kw]
	}
	return sum
}

// titleCaseWord upper-cases the first rune of a single token.
func titleCaseWord(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
