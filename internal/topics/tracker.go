package topics

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/google/uuid"
)

const jaccardThreshold = 0.3

type TopicStore interface {
	GetRecentTopicIdentities(ctx context.Context, cutoff string) ([]store.TopicIdentityRow, error)
	UpsertTopicIdentity(ctx context.Context, topicID, label, keywordsJSON, firstSeen, lastSeen string, peakRank int) error
	PurgeTopicIdentities(ctx context.Context, cutoff string) (int64, error)
}

type Tracker struct {
	store TopicStore
}

func NewTracker(store TopicStore) *Tracker {
	return &Tracker{store: store}
}

// AssignIdentities matches ranked topics to existing identities via Jaccard similarity,
// or assigns new UUIDs for unmatched topics.
func (t *Tracker) AssignIdentities(ctx context.Context, ranked []RankedTopic) ([]IdentifiedTopic, error) {
	now := time.Now().UTC()
	cutoff7d := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	nowStr := now.Format(time.RFC3339)

	existing, err := t.store.GetRecentTopicIdentities(ctx, cutoff7d)
	if err != nil {
		return nil, err
	}

	type parsedIdentity struct {
		row      store.TopicIdentityRow
		keywords []string
		used     bool
	}

	pool := make([]parsedIdentity, len(existing))
	for i, row := range existing {
		var kws []string
		if err := json.Unmarshal([]byte(row.Keywords), &kws); err != nil {
			kwsTrunc := row.Keywords
			if len(kwsTrunc) > 80 {
				kwsTrunc = kwsTrunc[:80]
			}
			slog.Warn("malformed topic keywords JSON", "topic_id", row.TopicID, "keywords", kwsTrunc)
		}
		pool[i] = parsedIdentity{row: row, keywords: kws}
	}

	result := make([]IdentifiedTopic, len(ranked))
	for i, rt := range ranked {
		allTerms := make([]string, 0, len(rt.Cluster.Keywords)+len(rt.Cluster.Synonyms))
		allTerms = append(allTerms, rt.Cluster.Keywords...)
		allTerms = append(allTerms, rt.Cluster.Synonyms...)

		bestIdx := -1
		bestSim := 0.0
		for j := range pool {
			if pool[j].used {
				continue
			}
			sim := jaccard(allTerms, pool[j].keywords)
			if sim > bestSim {
				bestSim = sim
				bestIdx = j
			}
		}

		rank := i + 1
		var topicID, firstSeen string
		if bestIdx >= 0 && bestSim > jaccardThreshold {
			pool[bestIdx].used = true
			topicID = pool[bestIdx].row.TopicID
			firstSeen = pool[bestIdx].row.FirstSeen
		} else {
			topicID = uuid.New().String()
			firstSeen = nowStr
		}

		kwJSON, _ := json.Marshal(allTerms)

		if err := t.store.UpsertTopicIdentity(ctx, topicID, rt.Cluster.Label, string(kwJSON), firstSeen, nowStr, rank); err != nil {
			return nil, err
		}

		result[i] = IdentifiedTopic{
			RankedTopic: rt,
			TopicID:     topicID,
			Rank:        rank,
		}
	}

	if _, err := t.store.PurgeTopicIdentities(ctx, cutoff7d); err != nil {
		return nil, err
	}

	return result, nil
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0.0
	}

	setA := make(map[string]bool, len(a))
	for _, s := range a {
		setA[strings.ToLower(s)] = true
	}
	setB := make(map[string]bool, len(b))
	for _, s := range b {
		setB[strings.ToLower(s)] = true
	}

	intersection := 0
	for k := range setA {
		if setB[k] {
			intersection++
		}
	}

	union := len(setA)
	for k := range setB {
		if !setA[k] {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}
