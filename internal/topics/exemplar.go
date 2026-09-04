package topics

import (
	"context"
	"fmt"
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

const (
	// exemplarCandidateLimit is how many rows the store returns per topic. The
	// ranking pass, not the query, decides the winner, so it needs room.
	exemplarCandidateLimit = 60
	// exemplarTopK is how many ranked candidates per topic are offered to the
	// validator, so a rejection has somewhere to fall back to.
	exemplarTopK = 3
	// maxValidationPairs caps one cycle's validation prompt.
	maxValidationPairs = 15
	// validationTextLimit caps the post text sent to the validator, in runes.
	validationTextLimit = 300
)

// pairRef locates a validation pair in the per-topic ranked candidate lists.
type pairRef struct {
	topic int
	rank  int
}

func (h *ExemplarHydrator) HydrateExemplars(ctx context.Context, topics []IdentifiedTopic, df *DocFreqStats) ([]IdentifiedTopic, error) {
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
		queries = append(queries, exemplarQuery{index: i, label: topic.Cluster.Label, keywords: allKeywords})
	}

	for _, q := range queries {
		wg.Add(1)
		go func(eq exemplarQuery) {
			defer wg.Done()
			candidates, err := h.store.GetExemplarCandidates(ctx, eq.keywords, cutoff, exemplarCandidateLimit)
			if err != nil {
				slog.Warn("exemplar: query failed", "topic", eq.label, "error", err)
				return
			}
			if len(candidates) == 0 {
				slog.Warn("exemplar: no candidates", "topic", eq.label, "keywords", eq.keywords)
				return
			}
			slog.Info("exemplar: found candidates", "topic", eq.label, "count", len(candidates), "top_engagement", candidates[0].Engagement, "top_distinct", candidates[0].DistinctMatches)
			resultCh <- exemplarResult{index: eq.index, candidates: candidates}
		}(q)
	}

	go func() { wg.Wait(); close(resultCh) }()

	candidatesByIndex := make(map[int][]store.ExemplarCandidate)
	for cr := range resultCh {
		candidatesByIndex[cr.index] = cr.candidates
	}

	// Rank each topic's candidates and reserve up to exemplarTopK of them.
	// Handles are claimed in topic order so two topics never show the same
	// author, and so a rejected pick cannot fall back onto a taken handle.
	usedHandles := make(map[string]bool)
	picks := make(map[int][]rankedExemplar)

	for i := range result {
		candidates, ok := candidatesByIndex[i]
		if !ok {
			continue
		}
		ranked := rankExemplarCandidates(result[i].Cluster.Label, result[i].Cluster.Keywords, result[i].Cluster.Synonyms, candidates, df)
		if len(ranked) == 0 {
			slog.Warn("exemplar: no candidate met relevance bar", "topic", result[i].Cluster.Label, "candidates", len(candidates), "min_relevance", minRelevance)
			continue
		}

		topK := make([]rankedExemplar, 0, exemplarTopK)
		for _, c := range ranked {
			if usedHandles[c.Handle] {
				continue
			}
			usedHandles[c.Handle] = true
			topK = append(topK, c)
			if len(topK) == exemplarTopK {
				break
			}
		}
		if len(topK) == 0 {
			slog.Warn("exemplar: all candidate handles already used", "topic", result[i].Cluster.Label)
			continue
		}

		picks[i] = topK
		result[i].ExemplarURI = topK[0].URI
		result[i].ExemplarHandle = topK[0].Handle
		slog.Info("exemplar: selected", "topic", result[i].Cluster.Label, "handle", topK[0].Handle,
			"engagement", topK[0].Engagement, "matched", topK[0].Distinct,
			"relevance", fmt.Sprintf("%.2f", topK[0].Relevance),
			"quality", fmt.Sprintf("%.2f", topK[0].Quality),
			"score", fmt.Sprintf("%.2f", topK[0].Score))
	}

	if h.validator != nil && len(picks) > 0 {
		h.validatePicks(ctx, result, picks)
	}

	return result, nil
}

// validatePicks asks the validator about every reserved candidate in a single
// call and promotes the highest-ranked approved one. A topic whose candidates
// are all rejected keeps no exemplar rather than showing an unvalidated post.
func (h *ExemplarHydrator) validatePicks(ctx context.Context, result []IdentifiedTopic, picks map[int][]rankedExemplar) {
	var pairs []ExemplarValidation
	var refs []pairRef

	// Round-robin by rank so every topic gets its top candidate validated
	// before any topic gets its second.
	for rank := 0; rank < exemplarTopK && len(pairs) < maxValidationPairs; rank++ {
		for i := range result {
			topK, ok := picks[i]
			if !ok || rank >= len(topK) {
				continue
			}
			if len(pairs) >= maxValidationPairs {
				break
			}
			text := topK[rank].Text
			if runes := []rune(text); len(runes) > validationTextLimit {
				text = string(runes[:validationTextLimit])
			}
			pairs = append(pairs, ExemplarValidation{
				TopicLabel: result[i].Cluster.Label,
				PostText:   text,
				IsRelevant: true,
			})
			refs = append(refs, pairRef{topic: i, rank: rank})
		}
	}

	if len(pairs) == 0 {
		return
	}

	validated, err := h.validator.ValidateExemplars(ctx, pairs)
	if err != nil {
		slog.Warn("exemplar: validation error, keeping top-ranked picks", "error", err)
		return
	}
	if len(validated) != len(pairs) {
		slog.Warn("exemplar: validation returned unexpected count, keeping top-ranked picks", "want", len(pairs), "got", len(validated))
		return
	}

	approved := make(map[pairRef]bool, len(refs))
	for j, v := range validated {
		approved[refs[j]] = v.IsRelevant
		if !v.IsRelevant {
			ref := refs[j]
			slog.Warn("exemplar: Gemini rejected", "topic", result[ref.topic].Cluster.Label,
				"handle", picks[ref.topic][ref.rank].Handle, "rank", ref.rank+1)
		}
	}

	sent := make(map[int]int, len(picks))
	for _, ref := range refs {
		sent[ref.topic]++
	}

	for i := range result {
		topK, ok := picks[i]
		if !ok {
			continue
		}
		if sent[i] == 0 {
			// The pair budget ran out before this topic. Keep the top-ranked
			// pick rather than dropping an unvalidated but plausible exemplar.
			slog.Info("exemplar: validation budget exceeded, keeping top-ranked pick", "topic", result[i].Cluster.Label, "handle", topK[0].Handle)
			continue
		}
		chosen := -1
		for rank := range topK {
			if approved[pairRef{topic: i, rank: rank}] {
				chosen = rank
				break
			}
		}
		if chosen < 0 {
			slog.Warn("exemplar: no candidate approved, dropping exemplar", "topic", result[i].Cluster.Label, "candidates", len(topK))
			result[i].ExemplarURI = ""
			result[i].ExemplarHandle = ""
			continue
		}
		result[i].ExemplarURI = topK[chosen].URI
		result[i].ExemplarHandle = topK[chosen].Handle
		slog.Info("exemplar: validated pick", "topic", result[i].Cluster.Label, "handle", topK[chosen].Handle,
			"rank", chosen+1, "of", len(topK), "score", fmt.Sprintf("%.2f", topK[chosen].Score))
	}
}
