package topics

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// RankTopics counts posts matching each cluster's keywords+synonyms and returns the top N.
func RankTopics(clusters []TopicCluster, rows []store.TopicTokenRow) []RankedTopic {
	if len(clusters) == 0 {
		return nil
	}

	type clusterSet struct {
		cluster TopicCluster
		terms   map[string]bool
	}

	sets := make([]clusterSet, len(clusters))
	for i, c := range clusters {
		terms := make(map[string]bool)
		for _, kw := range c.Keywords {
			terms[strings.ToLower(kw)] = true
		}
		for _, syn := range c.Synonyms {
			terms[strings.ToLower(syn)] = true
		}
		sets[i] = clusterSet{cluster: c, terms: terms}
	}

	counts := make([]int, len(clusters))
	for _, row := range rows {
		var tokens []string
		if err := json.Unmarshal([]byte(row.Tokens), &tokens); err != nil {
			continue
		}
		for i, cs := range sets {
			for _, tok := range tokens {
				if cs.terms[tok] {
					counts[i]++
					break
				}
			}
		}
	}

	ranked := make([]RankedTopic, len(clusters))
	for i, cs := range sets {
		ranked[i] = RankedTopic{
			Cluster:   cs.cluster,
			PostCount: counts[i],
		}
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].PostCount != ranked[j].PostCount {
			return ranked[i].PostCount > ranked[j].PostCount
		}
		return ranked[i].Cluster.Label < ranked[j].Cluster.Label
	})

	if len(ranked) > TopTopics {
		ranked = ranked[:TopTopics]
	}
	return ranked
}
