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

// ExemplarValidator judges whether each post is genuinely about its topic. A
// non-nil error means the batch was not judged at all and the returned pairs
// carry no verdict (see ErrValidationUnavailable).
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
	// maxValidationPairs is a safety cap on one cycle's validation prompt.
	// Hydration is limited to the topics that get posted, so it only binds if
	// that limit is raised past TopTopics.
	maxValidationPairs = TopTopics * exemplarTopK
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

	// Only the topics the post will actually show are worth a query and a
	// share of the validation budget.
	hydratable := result
	if len(hydratable) > maxPostedTopics {
		slog.Info("exemplar: skipping topics beyond the posted limit", "skipped", len(hydratable)-maxPostedTopics, "limit", maxPostedTopics)
		hydratable = hydratable[:maxPostedTopics]
	}

	for i, topic := range hydratable {
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

	ranked := make(map[int][]rankedExemplar, len(queries))
	for _, q := range queries {
		i := q.index
		candidates, ok := candidatesByIndex[i]
		if !ok {
			continue
		}
		list := rankExemplarCandidates(result[i].Cluster.Label, result[i].Cluster.Keywords, result[i].Cluster.Synonyms, candidates, df)
		if len(list) == 0 {
			slog.Warn("exemplar: no candidate met relevance bar", "topic", result[i].Cluster.Label, "candidates", len(candidates), "min_relevance", minRelevance)
			continue
		}
		ranked[i] = list
	}

	// Reserve one handle per topic, in topic order: only the published pick is
	// claimed up front, so a topic cannot starve a later one by holding
	// handles it will never show.
	reserved := make(map[string]bool, len(ranked))
	primary := make(map[int]rankedExemplar, len(ranked))
	for _, q := range queries {
		for _, c := range ranked[q.index] {
			if reserved[c.Handle] {
				continue
			}
			reserved[c.Handle] = true
			primary[q.index] = c
			break
		}
	}

	// Fallbacks are candidates no topic reserved, so promoting one after a
	// rejection cannot take a handle another topic is about to publish.
	picks := make(map[int][]rankedExemplar, len(primary))
	for _, q := range queries {
		i := q.index
		top, ok := primary[i]
		if !ok {
			if len(ranked[i]) > 0 {
				slog.Warn("exemplar: all candidate handles already used", "topic", result[i].Cluster.Label)
			}
			continue
		}
		topK := make([]rankedExemplar, 0, exemplarTopK)
		topK = append(topK, top)
		seen := map[string]bool{top.Handle: true}
		for _, c := range ranked[i] {
			if len(topK) == exemplarTopK {
				break
			}
			if reserved[c.Handle] || seen[c.Handle] {
				continue
			}
			seen[c.Handle] = true
			topK = append(topK, c)
		}

		picks[i] = topK
		result[i].ExemplarURI = top.URI
		result[i].ExemplarHandle = top.Handle
		slog.Info("exemplar: selected", "topic", result[i].Cluster.Label, "handle", top.Handle,
			"engagement", top.Engagement, "matched", top.Distinct,
			"relevance", fmt.Sprintf("%.2f", top.Relevance),
			"quality", fmt.Sprintf("%.2f", top.Quality),
			"score", fmt.Sprintf("%.2f", top.Score))
	}

	if h.validator != nil && len(picks) > 0 {
		h.validatePicks(ctx, result, picks)
	}

	missing := 0
	for _, q := range queries {
		if result[q.index].ExemplarHandle == "" {
			missing++
		}
	}
	if missing > 0 {
		slog.Warn("exemplar: topics without exemplar", "count", missing, "attempted", len(queries))
	}

	return result, nil
}

// validatePicks asks the validator about every candidate in a single call and
// publishes the highest-ranked approved one. A topic whose candidates are all
// rejected keeps no exemplar rather than showing an unvalidated post; a batch
// the validator could not judge falls back to the top-ranked pick.
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
		h.keepUnvalidated(result, picks, err)
		return
	}
	if len(validated) != len(pairs) {
		h.keepUnvalidated(result, picks, fmt.Errorf("validator returned %d verdicts for %d pairs", len(validated), len(pairs)))
		return
	}

	approved := make(map[pairRef]bool, len(refs))
	sent := make(map[int]int, len(picks))
	for j, v := range validated {
		ref := refs[j]
		approved[ref] = v.IsRelevant
		sent[ref.topic]++
		if !v.IsRelevant {
			slog.Warn("exemplar: Gemini rejected", "topic", result[ref.topic].Cluster.Label,
				"handle", picks[ref.topic][ref.rank].Handle, "rank", ref.rank+1)
		}
	}

	// Fallbacks were never reserved, so claim handles as they are promoted.
	used := make(map[string]bool, len(picks))
	for i := range result {
		topK, ok := picks[i]
		if !ok {
			continue
		}
		if sent[i] == 0 {
			// The pair budget ran out before this topic. Keep the top-ranked
			// pick rather than dropping a plausible exemplar.
			slog.Info("exemplar: unvalidated pick", "topic", result[i].Cluster.Label,
				"handle", topK[0].Handle, "reason", "validation budget exceeded")
			used[topK[0].Handle] = true
			continue
		}

		chosen := -1
		for rank := range topK {
			if !approved[pairRef{topic: i, rank: rank}] || used[topK[rank].Handle] {
				continue
			}
			chosen = rank
			break
		}
		if chosen < 0 {
			slog.Warn("exemplar: no candidate approved, dropping exemplar", "topic", result[i].Cluster.Label, "candidates", len(topK))
			result[i].ExemplarURI = ""
			result[i].ExemplarHandle = ""
			continue
		}

		used[topK[chosen].Handle] = true
		result[i].ExemplarURI = topK[chosen].URI
		result[i].ExemplarHandle = topK[chosen].Handle
		slog.Info("exemplar: validated pick", "topic", result[i].Cluster.Label, "handle", topK[chosen].Handle,
			"rank", chosen+1, "of", len(topK), "score", fmt.Sprintf("%.2f", topK[chosen].Score))
	}
}

// keepUnvalidated publishes the top-ranked pick for every topic when the
// validator could not judge the batch, and records how many picks went out
// unchecked.
func (h *ExemplarHydrator) keepUnvalidated(result []IdentifiedTopic, picks map[int][]rankedExemplar, reason error) {
	unvalidated := 0
	for i := range result {
		topK, ok := picks[i]
		if !ok {
			continue
		}
		unvalidated++
		slog.Info("exemplar: unvalidated pick", "topic", result[i].Cluster.Label,
			"handle", topK[0].Handle, "reason", reason)
	}
	slog.Warn("exemplar: validator unavailable, keeping top-ranked picks", "reason", reason, "unvalidated_picks", unvalidated)
}
