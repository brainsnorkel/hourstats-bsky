package topics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type AnalyzerStore interface {
	TopicStore
	ExemplarCandidateStore
	GetTopicTokensSince(ctx context.Context, cutoff string) ([]store.TopicTokenRow, error)
	GetTopicTokensSinceLimit(ctx context.Context, cutoff string, limit int) ([]store.TopicTokenRow, error)
	CountTopicTokensSince(ctx context.Context, cutoff string) (int64, error)
	PurgeTopicTokens(ctx context.Context, cutoff string) (int64, error)
	InsertTopicSnapshot(ctx context.Context, snapshotTime string, rank int, topicID, label, description string, postCount int, keywordsJSON, synonymsJSON, exemplarURI, exemplarHandle string, isMeme bool, justification string) error
	GetTopicSnapshotsSince(ctx context.Context, cutoff string) ([]store.TopicSnapshotRow, error)
	PurgeTopicSnapshots(ctx context.Context, cutoff string) (int64, error)
	UpdateSnapshotExemplar(ctx context.Context, snapshotID int64, exemplarURI, exemplarHandle string) error
	SetKeyValue(ctx context.Context, key, value string) error
}

type TrendingPoster interface {
	PostWithFacets(ctx context.Context, text string, facets []*bsky.RichtextFacet) error
	PostWithFacetsAsReply(ctx context.Context, text string, facets []*bsky.RichtextFacet, rootURI, rootCID, parentURI, parentCID string) (string, string, error)
}

type Analyzer struct {
	store    AnalyzerStore
	grouper  *Grouper
	tracker  *Tracker
	hydrator *ExemplarHydrator

	// docFreq carries this cycle's document frequencies from RunAnalysisCycle
	// to RunTrendingPost so exemplar ranking can downweight keywords that are
	// generic in the current corpus. The two run on different goroutines
	// within a cycle, hence the mutex.
	mu      sync.Mutex
	docFreq *DocFreqStats
}

func NewAnalyzer(s AnalyzerStore, geminiAPIKey, geminiModel, geminiFallbackModel string) *Analyzer {
	grouper := NewGrouper(geminiAPIKey, geminiModel, geminiFallbackModel)
	tracker := NewTracker(s)
	exemplarHydrator := NewExemplarHydrator(s)
	if geminiAPIKey != "" {
		exemplarHydrator.SetValidator(grouper)
	}

	return &Analyzer{
		store:    s,
		grouper:  grouper,
		tracker:  tracker,
		hydrator: exemplarHydrator,
	}
}

// SetBudgetExhaustedHandler registers a callback fired when the daily Gemini
// call budget is exhausted, so the caller can record it as a stats event.
func (a *Analyzer) SetBudgetExhaustedHandler(fn func(dailyCalls int)) {
	a.grouper.SetBudgetExhaustedHandler(fn)
}

func (a *Analyzer) setDocFreq(df *DocFreqStats) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.docFreq = df
}

func (a *Analyzer) currentDocFreq() *DocFreqStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.docFreq
}

const maxTFIDFRows = 20000

// ErrTopicsUnavailable reports that a pipeline step genuinely failed to produce
// topics this cycle. It is distinct from the legitimate "nothing to publish"
// cases (corpus below MinCorpusSize), which return a nil error and an empty
// snapshot time.
var ErrTopicsUnavailable = errors.New("topics unavailable")

// RunAnalysisCycle recomputes trending topics and persists them as a snapshot.
// It returns the snapshot time it wrote, which RunTrendingPost requires in
// order to publish only this cycle's topics. An empty string means no snapshot
// was produced and there is nothing to post.
func (a *Analyzer) RunAnalysisCycle(ctx context.Context) (string, error) {
	start := time.Now()

	purgeTokenCutoff := time.Now().UTC().Add(-26 * time.Hour).Format(time.RFC3339)
	purged, err := a.store.PurgeTopicTokens(ctx, purgeTokenCutoff)
	if err != nil {
		return "", fmt.Errorf("purge tokens: %w", err)
	}
	if purged > 0 {
		slog.Info("topics: purged expired tokens", "count", purged)
	}

	snapshotPurge := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	if _, err := a.store.PurgeTopicSnapshots(ctx, snapshotPurge); err != nil {
		return "", fmt.Errorf("purge snapshots: %w", err)
	}

	tokenCutoff := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	count, err := a.store.CountTopicTokensSince(ctx, tokenCutoff)
	if err != nil {
		return "", fmt.Errorf("count tokens: %w", err)
	}
	if count < int64(MinCorpusSize) {
		// Not a failure: there genuinely isn't enough material this window.
		slog.Info("topics: corpus too small, skipping", "count", count, "min", MinCorpusSize)
		return "", nil
	}

	rows, err := a.store.GetTopicTokensSinceLimit(ctx, tokenCutoff, maxTFIDFRows)
	if err != nil {
		return "", fmt.Errorf("get tokens: %w", err)
	}
	slog.Info("topics: loaded tokens for TF-IDF", "rows", len(rows), "total_available", count)

	terms := ComputeTFIDF(rows)
	if len(terms) == 0 {
		return "", fmt.Errorf("%w: TF-IDF produced no significant terms from %d rows", ErrTopicsUnavailable, len(rows))
	}
	a.setDocFreq(newDocFreqStats(terms, len(rows)))
	slog.Info("topics: TF-IDF computed", "terms", len(terms), "elapsed", fmt.Sprintf("%.1fs", time.Since(start).Seconds()))

	clusters, err := a.grouper.GroupAndLabel(ctx, terms)
	if err != nil {
		// Every Gemini tier (primary -> fallback model) failed. Try offline
		// co-occurrence grouping before giving up; only suppress the post if
		// even that yields nothing. This is strictly better than re-posting a
		// stale snapshot or publishing raw underscore terms.
		clusters = AlgorithmicGroup(rows, terms)
		if len(clusters) == 0 {
			return "", fmt.Errorf("%w: grouping failed and offline fallback empty: %w", ErrTopicsUnavailable, err)
		}
		slog.Warn("topics: Gemini grouping failed, using offline co-occurrence fallback",
			"error", err, "clusters", len(clusters))
	}
	if len(clusters) == 0 {
		return "", fmt.Errorf("%w: grouping produced no clusters from %d terms", ErrTopicsUnavailable, len(terms))
	}

	clusters = MergeSimilarClusters(clusters, terms)

	ranked := RankTopics(clusters, rows)
	if len(ranked) == 0 {
		return "", fmt.Errorf("%w: ranking produced no topics from %d clusters", ErrTopicsUnavailable, len(clusters))
	}

	identified, err := a.tracker.AssignIdentities(ctx, ranked)
	if err != nil {
		return "", fmt.Errorf("assign identities: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, topic := range identified {
		kwJSON, _ := json.Marshal(topic.Cluster.Keywords)
		synJSON, _ := json.Marshal(topic.Cluster.Synonyms)
		if err := a.store.InsertTopicSnapshot(ctx, now, topic.Rank, topic.TopicID,
			topic.Cluster.Label, topic.Cluster.Description, topic.UniqueAuthorCount,
			string(kwJSON), string(synJSON), "", "", topic.Cluster.IsMeme, topic.Cluster.Justification); err != nil {
			return "", fmt.Errorf("insert snapshot: %w", err)
		}
	}

	slog.Info("topics: analysis cycle complete", "topics", len(identified), "snapshot_time", now, "elapsed", fmt.Sprintf("%.1fs", time.Since(start).Seconds()))
	return now, nil
}

// RunTrendingPost publishes the snapshot identified by snapshotTime, which must
// come from the RunAnalysisCycle call for this cycle.
//
// Selecting by exact snapshot time rather than "latest within 24h" is what
// stops a failed analysis from republishing the previous window's topics as if
// they were current. An empty snapshotTime means this cycle produced nothing,
// so there is nothing to post.
func (a *Analyzer) RunTrendingPost(ctx context.Context, poster TrendingPoster, dryRun bool, snapshotTime, rootURI, rootCID, parentURI, parentCID string) error {
	start := time.Now()

	if snapshotTime == "" {
		slog.Info("topics: no snapshot from this cycle, skipping trending post")
		return nil
	}

	snapshotCutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	snapshots, err := a.store.GetTopicSnapshotsSince(ctx, snapshotCutoff)
	if err != nil {
		return fmt.Errorf("get snapshots: %w", err)
	}

	if len(snapshots) == 0 {
		slog.Info("topics: no snapshots for trending post, skipping")
		return nil
	}

	var latestTopics []IdentifiedTopic
	for _, s := range snapshots {
		if s.SnapshotTime == snapshotTime {
			var kws []string
			if err := json.Unmarshal([]byte(s.Keywords), &kws); err != nil {
				slog.Warn("topics: unmarshal snapshot keywords", "error", err, "snapshot_id", s.ID, "label", s.Label)
			}
			var syns []string
			if err := json.Unmarshal([]byte(s.Synonyms), &syns); err != nil {
				slog.Warn("topics: unmarshal snapshot synonyms", "error", err, "snapshot_id", s.ID, "label", s.Label)
			}
			latestTopics = append(latestTopics, IdentifiedTopic{
				RankedTopic: RankedTopic{
					Cluster:           TopicCluster{Label: s.Label, Description: s.Description, Keywords: kws, Synonyms: syns, IsMeme: s.IsMeme},
					UniqueAuthorCount: s.UniqueAuthorCount,
				},
				TopicID: s.TopicID,
				Rank:    s.Rank,
			})
		}
	}

	if len(latestTopics) == 0 {
		slog.Info("topics: snapshot from this cycle holds no topics, skipping", "snapshot_time", snapshotTime)
		return nil
	}

	previousCutoff := time.Now().UTC().Add(-12 * time.Hour).Format(time.RFC3339)
	prevSnapshots, _ := a.store.GetTopicSnapshotsSince(ctx, previousCutoff)
	var previous []IdentifiedTopic
	if len(prevSnapshots) > 0 {
		prevTime := ""
		for _, s := range prevSnapshots {
			if s.SnapshotTime != snapshotTime && s.SnapshotTime > prevTime {
				prevTime = s.SnapshotTime
			}
		}
		for _, s := range prevSnapshots {
			if s.SnapshotTime == prevTime {
				previous = append(previous, IdentifiedTopic{TopicID: s.TopicID, Rank: s.Rank})
			}
		}
	}

	latestTopics, err = a.hydrator.HydrateExemplars(ctx, latestTopics, a.currentDocFreq())
	if err != nil {
		slog.Warn("topics: exemplar hydration error", "error", err)
	}

	text, facets := FormatTrendingPost(latestTopics, previous, 2)

	if dryRun {
		slog.Info("topics: DRY RUN trending post", "text", text, "facets", len(facets))
		return nil
	}

	if poster == nil {
		return fmt.Errorf("poster is nil, cannot post")
	}

	bskyFacets := convertFacets(facets)

	if rootURI != "" && rootCID != "" && parentURI != "" && parentCID != "" {
		_, _, err := poster.PostWithFacetsAsReply(ctx, text, bskyFacets, rootURI, rootCID, parentURI, parentCID)
		if err != nil {
			slog.Warn("topics: reply post failed, falling back to standalone", "error", err)
			if err2 := poster.PostWithFacets(ctx, text, bskyFacets); err2 != nil {
				return fmt.Errorf("post trending (fallback): %w", err2)
			}
			slog.Info("topics: trending post published as standalone (fallback)")
		} else {
			slog.Info("topics: trending post published as reply", "elapsed", fmt.Sprintf("%.1fs", time.Since(start).Seconds()))
		}
	} else {
		if err := poster.PostWithFacets(ctx, text, bskyFacets); err != nil {
			return fmt.Errorf("post trending: %w", err)
		}
		slog.Info("topics: trending post published as standalone", "elapsed", fmt.Sprintf("%.1fs", time.Since(start).Seconds()))
	}

	if err := a.store.SetKeyValue(ctx, "trending_post_last_time", time.Now().UTC().Format(time.RFC3339)); err != nil {
		slog.Warn("topics: failed to record trending post time", "error", err)
	}
	return nil
}

func convertFacets(facets []Facet) []*bsky.RichtextFacet {
	if len(facets) == 0 {
		return nil
	}
	result := make([]*bsky.RichtextFacet, len(facets))
	for i, f := range facets {
		rf := &bsky.RichtextFacet{
			Index: &bsky.RichtextFacet_ByteSlice{
				ByteStart: int64(f.ByteStart),
				ByteEnd:   int64(f.ByteEnd),
			},
		}
		switch f.Type {
		case FacetTag:
			rf.Features = []*bsky.RichtextFacet_Features_Elem{
				{RichtextFacet_Tag: &bsky.RichtextFacet_Tag{Tag: f.Value}},
			}
		case FacetLink:
			rf.Features = []*bsky.RichtextFacet_Features_Elem{
				{RichtextFacet_Link: &bsky.RichtextFacet_Link{Uri: f.Value}},
			}
		}
		result[i] = rf
	}
	return result
}
