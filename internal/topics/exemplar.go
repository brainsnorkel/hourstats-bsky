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

type ExemplarValidator interface {
	ValidateExemplars(ctx context.Context, pairs []ExemplarValidation) ([]ExemplarValidation, error)
}

type ExemplarHydrator struct {
	store     ExemplarCandidateStore
	validator ExemplarValidator
}

func NewExemplarHydrator(s ExemplarCandidateStore) *ExemplarHydrator {
	return &ExemplarHydrator{store: s}
}

func (h *ExemplarHydrator) SetValidator(v ExemplarValidator) {
	h.validator = v
}

type exemplarResult struct {
	index      int
	candidates []store.ExemplarCandidate
}

const minMatchScoreThreshold = 2

func minMatchScore(keywordCount int) int {
	if keywordCount <= 2 {
		return 1
	}
	return minMatchScoreThreshold
}

func (h *ExemplarHydrator) HydrateExemplars(ctx context.Context, topics []IdentifiedTopic) ([]IdentifiedTopic, error) {
	if len(topics) == 0 {
		return topics, nil
	}

	cutoff := time.Now().UTC().Add(-6 * time.Hour).Format(time.RFC3339)
	result := make([]IdentifiedTopic, len(topics))
	copy(result, topics)

	type exemplarQuery struct {
		index    int
		label    string
		keywords []string
		kwCount  int
	}

	var wg sync.WaitGroup
	resultCh := make(chan exemplarResult, len(topics))
	queries := make([]exemplarQuery, 0, len(topics))

	for i, topic := range result {
		if topic.Cluster.IsMeme {
			slog.Info("exemplar: skipping meme topic", "topic", topic.Cluster.Label)
			continue
		}
		allKeywords := make([]string, 0, len(topic.Cluster.Keywords)+len(topic.Cluster.Synonyms))
		allKeywords = append(allKeywords, topic.Cluster.Keywords...)
		allKeywords = append(allKeywords, topic.Cluster.Synonyms...)
		if len(allKeywords) == 0 {
			slog.Info("exemplar: no keywords", "topic", topic.Cluster.Label)
			continue
		}
		queries = append(queries, exemplarQuery{index: i, label: topic.Cluster.Label, keywords: allKeywords, kwCount: len(allKeywords)})
	}

	for _, q := range queries {
		wg.Add(1)
		go func(eq exemplarQuery) {
			defer wg.Done()
			candidates, err := h.store.GetExemplarCandidates(ctx, eq.keywords, cutoff, 20)
			if err != nil {
				slog.Warn("exemplar: query failed", "topic", eq.label, "error", err)
				return
			}
			if len(candidates) == 0 {
				slog.Warn("exemplar: no candidates", "topic", eq.label, "keywords", eq.keywords)
				return
			}
			slog.Info("exemplar: found candidates", "topic", eq.label, "count", len(candidates), "top_engagement", candidates[0].Engagement, "top_score", candidates[0].MatchScore)
			resultCh <- exemplarResult{index: eq.index, candidates: candidates}
		}(q)
	}

	go func() { wg.Wait(); close(resultCh) }()

	candidatesByIndex := make(map[int][]store.ExemplarCandidate)
	for cr := range resultCh {
		candidatesByIndex[cr.index] = cr.candidates
	}

	kwCountByIndex := make(map[int]int)
	for _, q := range queries {
		kwCountByIndex[q.index] = q.kwCount
	}

	usedHandles := make(map[string]bool)
	selectedText := make(map[int]string)

	for i := range result {
		candidates, ok := candidatesByIndex[i]
		if !ok {
			continue
		}

		threshold := minMatchScore(kwCountByIndex[i])
		found := false
		for _, c := range candidates {
			if c.Handle == "" {
				continue
			}
			if usedHandles[c.Handle] {
				continue
			}
			if c.MatchScore < threshold {
				slog.Info("exemplar: below threshold", "topic", result[i].Cluster.Label, "handle", c.Handle, "score", c.MatchScore, "threshold", threshold)
				continue
			}
			if IsRepetitive(c.Text) {
				slog.Info("exemplar: skipping repetitive post", "topic", result[i].Cluster.Label, "handle", c.Handle)
				continue
			}
			result[i].ExemplarURI = c.URI
			result[i].ExemplarHandle = c.Handle
			selectedText[i] = c.Text
			usedHandles[c.Handle] = true
			slog.Info("exemplar: selected", "topic", result[i].Cluster.Label, "handle", c.Handle, "engagement", c.Engagement, "score", c.MatchScore)
			found = true
			break
		}
		if !found {
			slog.Warn("exemplar: no candidate met threshold", "topic", result[i].Cluster.Label, "candidates", len(candidates), "threshold", threshold)
		}
	}

	if h.validator != nil && len(selectedText) > 0 {
		result = h.validateAndReplace(ctx, result, candidatesByIndex, kwCountByIndex, usedHandles, selectedText)
	}

	return result, nil
}

func (h *ExemplarHydrator) validateAndReplace(ctx context.Context, result []IdentifiedTopic, candidatesByIndex map[int][]store.ExemplarCandidate, kwCountByIndex map[int]int, usedHandles map[string]bool, selectedText map[int]string) []IdentifiedTopic {
	var pairs []ExemplarValidation
	var pairIndices []int

	for i, text := range selectedText {
		if text == "" {
			continue
		}
		truncated := text
		if len(truncated) > 300 {
			truncated = truncated[:300]
		}
		pairs = append(pairs, ExemplarValidation{
			TopicLabel: result[i].Cluster.Label,
			PostText:   truncated,
			IsRelevant: true,
		})
		pairIndices = append(pairIndices, i)
	}

	if len(pairs) == 0 {
		return result
	}

	validated, err := h.validator.ValidateExemplars(ctx, pairs)
	if err != nil {
		slog.Warn("exemplar: validation error", "error", err)
		return result
	}

	for j, v := range validated {
		if v.IsRelevant {
			continue
		}
		i := pairIndices[j]
		slog.Warn("exemplar: Gemini rejected", "topic", result[i].Cluster.Label, "handle", result[i].ExemplarHandle)

		rejectedURI := result[i].ExemplarURI
		delete(usedHandles, result[i].ExemplarHandle)
		result[i].ExemplarURI = ""
		result[i].ExemplarHandle = ""

		candidates := candidatesByIndex[i]
		threshold := minMatchScore(kwCountByIndex[i])
		for _, c := range candidates {
			if c.Handle == "" || usedHandles[c.Handle] || c.MatchScore < threshold {
				continue
			}
			if c.URI == rejectedURI {
				continue
			}
			result[i].ExemplarURI = c.URI
			result[i].ExemplarHandle = c.Handle
			usedHandles[c.Handle] = true
			slog.Info("exemplar: replaced after rejection", "topic", result[i].Cluster.Label, "handle", c.Handle, "score", c.MatchScore)
			break
		}
	}

	return result
}
