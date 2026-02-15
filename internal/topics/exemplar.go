package topics

import (
	"context"
	"log/slog"
	"sync"
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

type exemplarResult struct {
	index      int
	candidates []store.ExemplarCandidate
}

func (h *ExemplarHydrator) HydrateExemplars(ctx context.Context, topics []IdentifiedTopic) ([]IdentifiedTopic, error) {
	if len(topics) == 0 {
		return topics, nil
	}

	cutoff := time.Now().UTC().Add(-6 * time.Hour).Format(time.RFC3339)
	result := make([]IdentifiedTopic, len(topics))
	copy(result, topics)

	var wg sync.WaitGroup
	resultCh := make(chan exemplarResult, len(topics))

	for i, topic := range result {
		if topic.Cluster.IsMeme {
			slog.Info("exemplar: skipping meme topic", "topic", topic.Cluster.Label)
			continue
		}
		allKeywords := append(topic.Cluster.Keywords, topic.Cluster.Synonyms...)
		if len(allKeywords) == 0 {
			slog.Info("exemplar: no keywords", "topic", topic.Cluster.Label)
			continue
		}

		wg.Add(1)
		go func(idx int, label string, kws []string) {
			defer wg.Done()
			candidates, err := h.store.GetExemplarCandidates(ctx, kws, cutoff, 20)
			if err != nil {
				slog.Warn("exemplar: query failed", "topic", label, "error", err)
				return
			}
			if len(candidates) == 0 {
				slog.Warn("exemplar: no candidates", "topic", label, "keywords", kws)
				return
			}
			slog.Info("exemplar: found candidates", "topic", label, "count", len(candidates), "top_engagement", candidates[0].Engagement)
			resultCh <- exemplarResult{index: idx, candidates: candidates}
		}(i, topic.Cluster.Label, allKeywords)
	}

	go func() { wg.Wait(); close(resultCh) }()

	candidatesByIndex := make(map[int][]store.ExemplarCandidate)
	for cr := range resultCh {
		candidatesByIndex[cr.index] = cr.candidates
	}

	usedHandles := make(map[string]bool)
	for i := range result {
		candidates, ok := candidatesByIndex[i]
		if !ok {
			continue
		}

		found := false
		for _, c := range candidates {
			if c.Handle == "" {
				continue
			}
			if usedHandles[c.Handle] {
				continue
			}
			result[i].ExemplarURI = c.URI
			result[i].ExemplarHandle = c.Handle
			usedHandles[c.Handle] = true
			slog.Info("exemplar: selected", "topic", result[i].Cluster.Label, "handle", c.Handle, "engagement", c.Engagement)
			found = true
			break
		}
		if !found {
			slog.Warn("exemplar: no hydrated candidate available", "topic", result[i].Cluster.Label, "candidates", len(candidates))
		}
	}

	return result, nil
}
