package topics

import (
	"encoding/json"
	"log/slog"
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

	authorSets := make([]map[string]bool, len(clusters))
	for i := range clusters {
		authorSets[i] = make(map[string]bool)
	}

	for _, row := range rows {
		var tokens []string
		if err := json.Unmarshal([]byte(row.Tokens), &tokens); err != nil {
			slog.Warn("ranker: unmarshal tokens", "error", err, "post_uri", row.PostURI)
			continue
		}
		for i, cs := range sets {
			for _, tok := range tokens {
				if cs.terms[tok] {
					if row.AuthorDID != "" {
						authorSets[i][row.AuthorDID] = true
					}
					break
				}
			}
		}
	}

	ranked := make([]RankedTopic, len(clusters))
	for i, cs := range sets {
		ranked[i] = RankedTopic{
			Cluster:           cs.cluster,
			UniqueAuthorCount: len(authorSets[i]),
		}
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].UniqueAuthorCount != ranked[j].UniqueAuthorCount {
			return ranked[i].UniqueAuthorCount > ranked[j].UniqueAuthorCount
		}
		return ranked[i].Cluster.Label < ranked[j].Cluster.Label
	})

	if len(ranked) > TopTopics {
		ranked = ranked[:TopTopics]
	}
	return ranked
}
