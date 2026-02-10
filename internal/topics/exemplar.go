package topics

import (
	"context"
	"log/slog"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type ExemplarCandidateStore interface {
	GetExemplarCandidates(ctx context.Context, keywords []string, cutoff string, limit int) ([]store.ExemplarCandidate, error)
}

type ExemplarHydrator struct {
	store ExemplarCandidateStore
}

func NewExemplarHydrator(s ExemplarCandidateStore) *ExemplarHydrator {
	return &ExemplarHydrator{store: s}
}

func (h *ExemplarHydrator) HydrateExemplars(ctx context.Context, topics []IdentifiedTopic) ([]IdentifiedTopic, error) {
	if len(topics) == 0 {
		return topics, nil
	}

	cutoff := time.Now().UTC().Add(-6 * time.Hour).Format(time.RFC3339)
	result := make([]IdentifiedTopic, len(topics))
	copy(result, topics)

	usedHandles := make(map[string]bool)

	for i, topic := range result {
		allKeywords := append(topic.Cluster.Keywords, topic.Cluster.Synonyms...)
		if len(allKeywords) == 0 {
			slog.Info("exemplar: no keywords", "topic", topic.Cluster.Label)
			continue
		}

		candidates, err := h.store.GetExemplarCandidates(ctx, allKeywords, cutoff, 20)
		if err != nil {
			slog.Warn("exemplar: query failed", "topic", topic.Cluster.Label, "error", err)
			continue
		}
		if len(candidates) == 0 {
			slog.Warn("exemplar: no candidates", "topic", topic.Cluster.Label, "keywords", allKeywords)
			continue
		}
		slog.Info("exemplar: found candidates", "topic", topic.Cluster.Label, "count", len(candidates), "top_engagement", candidates[0].Engagement)

		found := false
		for _, c := range candidates {
			if usedHandles[c.Handle] {
				continue
			}
			result[i].ExemplarURI = c.URI
			result[i].ExemplarHandle = c.Handle
			usedHandles[c.Handle] = true
			slog.Info("exemplar: selected", "topic", topic.Cluster.Label, "handle", c.Handle, "engagement", c.Engagement)
			found = true
			break
		}
		if !found {
			slog.Warn("exemplar: all candidates had used handles", "topic", topic.Cluster.Label, "candidates", len(candidates))
		}
	}

	return result, nil
}
