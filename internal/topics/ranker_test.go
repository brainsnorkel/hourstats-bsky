package topics

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func makeRankerTokenRow(uri string, tokens []string) store.TopicTokenRow {
	b, _ := json.Marshal(tokens)
	return store.TopicTokenRow{PostURI: uri, Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z"}
}

func TestRankTopics_BasicRanking(t *testing.T) {
	clusters := []TopicCluster{
		{Label: "Politics", Keywords: []string{"trump", "election"}, Synonyms: []string{}},
		{Label: "Weather", Keywords: []string{"weather", "rain"}, Synonyms: []string{}},
		{Label: "Sports", Keywords: []string{"football"}, Synonyms: []string{}},
	}

	var rows []store.TopicTokenRow
	for i := 0; i < 20; i++ {
		rows = append(rows, makeRankerTokenRow(fmt.Sprintf("at://a/%d", i), []string{"trump", "election"}))
	}
	for i := 0; i < 10; i++ {
		rows = append(rows, makeRankerTokenRow(fmt.Sprintf("at://b/%d", i), []string{"weather"}))
	}
	for i := 0; i < 5; i++ {
		rows = append(rows, makeRankerTokenRow(fmt.Sprintf("at://c/%d", i), []string{"football"}))
	}

	ranked := RankTopics(clusters, rows)
	if len(ranked) != 3 {
		t.Fatalf("expected 3 ranked, got %d", len(ranked))
	}
	if ranked[0].Cluster.Label != "Politics" {
		t.Errorf("expected 'Politics' first, got %q", ranked[0].Cluster.Label)
	}
	if ranked[0].PostCount != 20 {
		t.Errorf("expected count 20, got %d", ranked[0].PostCount)
	}
	if ranked[1].PostCount != 10 {
		t.Errorf("expected count 10, got %d", ranked[1].PostCount)
	}
}

func TestRankTopics_SynonymMatching(t *testing.T) {
	clusters := []TopicCluster{
		{Label: "Weather", Keywords: []string{"weather"}, Synonyms: []string{"storm", "rain"}},
	}
	rows := []store.TopicTokenRow{
		makeRankerTokenRow("at://a/1", []string{"storm"}),
		makeRankerTokenRow("at://a/2", []string{"rain"}),
		makeRankerTokenRow("at://a/3", []string{"weather"}),
	}

	ranked := RankTopics(clusters, rows)
	if len(ranked) != 1 {
		t.Fatalf("expected 1 ranked, got %d", len(ranked))
	}
	if ranked[0].PostCount != 3 {
		t.Errorf("expected count 3 (keyword + synonyms), got %d", ranked[0].PostCount)
	}
}

func TestRankTopics_TopN(t *testing.T) {
	var clusters []TopicCluster
	for i := 0; i < 8; i++ {
		clusters = append(clusters, TopicCluster{
			Label:    fmt.Sprintf("Topic%d", i),
			Keywords: []string{fmt.Sprintf("term%d", i)},
			Synonyms: []string{},
		})
	}

	var rows []store.TopicTokenRow
	for i := 0; i < 8; i++ {
		for j := 0; j < (8-i)*10; j++ {
			rows = append(rows, makeRankerTokenRow(
				fmt.Sprintf("at://t%d/%d", i, j),
				[]string{fmt.Sprintf("term%d", i)},
			))
		}
	}

	ranked := RankTopics(clusters, rows)
	if len(ranked) != TopTopics {
		t.Fatalf("expected %d ranked, got %d", TopTopics, len(ranked))
	}
}

func TestRankTopics_EmptyClusters(t *testing.T) {
	ranked := RankTopics(nil, nil)
	if ranked != nil {
		t.Errorf("expected nil, got %v", ranked)
	}
}

func TestRankTopics_PostMatchesMultipleClusters(t *testing.T) {
	clusters := []TopicCluster{
		{Label: "A", Keywords: []string{"shared"}, Synonyms: []string{}},
		{Label: "B", Keywords: []string{"shared"}, Synonyms: []string{}},
	}
	rows := []store.TopicTokenRow{
		makeRankerTokenRow("at://a/1", []string{"shared"}),
	}

	ranked := RankTopics(clusters, rows)
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked, got %d", len(ranked))
	}
	if ranked[0].PostCount != 1 || ranked[1].PostCount != 1 {
		t.Errorf("expected both counts=1, got %d and %d", ranked[0].PostCount, ranked[1].PostCount)
	}
}

func TestRankTopics_TieBreaking(t *testing.T) {
	clusters := []TopicCluster{
		{Label: "Zebra", Keywords: []string{"zebra"}, Synonyms: []string{}},
		{Label: "Apple", Keywords: []string{"apple"}, Synonyms: []string{}},
	}
	rows := []store.TopicTokenRow{
		makeRankerTokenRow("at://a/1", []string{"zebra", "apple"}),
	}

	ranked := RankTopics(clusters, rows)
	if ranked[0].Cluster.Label != "Apple" {
		t.Errorf("expected alphabetical tie-break 'Apple' first, got %q", ranked[0].Cluster.Label)
	}
}
