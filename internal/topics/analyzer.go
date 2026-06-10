package topics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
}

func NewAnalyzer(s AnalyzerStore, geminiAPIKey, geminiModel string) *Analyzer {
	grouper := NewGrouper(geminiAPIKey, geminiModel)
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

const maxTFIDFRows = 20000

func (a *Analyzer) RunAnalysisCycle(ctx context.Context) error {
	start := time.Now()

	purgeTokenCutoff := time.Now().UTC().Add(-26 * time.Hour).Format(time.RFC3339)
	purged, err := a.store.PurgeTopicTokens(ctx, purgeTokenCutoff)
	if err != nil {
		return fmt.Errorf("purge tokens: %w", err)
	}
	if purged > 0 {
		slog.Info("topics: purged expired tokens", "count", purged)
	}

	snapshotPurge := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	if _, err := a.store.PurgeTopicSnapshots(ctx, snapshotPurge); err != nil {
		return fmt.Errorf("purge snapshots: %w", err)
	}

	tokenCutoff := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	count, err := a.store.CountTopicTokensSince(ctx, tokenCutoff)
	if err != nil {
		return fmt.Errorf("count tokens: %w", err)
	}
	if count < int64(MinCorpusSize) {
		slog.Info("topics: corpus too small, skipping", "count", count, "min", MinCorpusSize)
		return nil
	}

	rows, err := a.store.GetTopicTokensSinceLimit(ctx, tokenCutoff, maxTFIDFRows)
	if err != nil {
		return fmt.Errorf("get tokens: %w", err)
	}
	slog.Info("topics: loaded tokens for TF-IDF", "rows", len(rows), "total_available", count)

	terms := ComputeTFIDF(rows)
	if len(terms) == 0 {
		slog.Warn("topics: no significant terms found, skipping")
		return nil
	}
	slog.Info("topics: TF-IDF computed", "terms", len(terms), "elapsed", fmt.Sprintf("%.1fs", time.Since(start).Seconds()))

	clusters, err := a.grouper.GroupAndLabel(ctx, terms)
	if err != nil {
		// A failed LLM grouping call must NOT produce a post. Returning the
		// error here causes the caller to skip RunTrendingPost entirely, rather
		// than publishing a degraded post or re-posting a stale snapshot.
		return fmt.Errorf("topics: grouping failed, skipping trending post: %w", err)
	}
	if len(clusters) == 0 {
		slog.Warn("topics: no clusters produced, skipping")
		return nil
	}

	clusters = MergeSimilarClusters(clusters, terms)

	ranked := RankTopics(clusters, rows)
	if len(ranked) == 0 {
		slog.Warn("topics: no ranked topics produced")
		return nil
	}

	identified, err := a.tracker.AssignIdentities(ctx, ranked)
	if err != nil {
		return fmt.Errorf("assign identities: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, topic := range identified {
		kwJSON, _ := json.Marshal(topic.Cluster.Keywords)
		synJSON, _ := json.Marshal(topic.Cluster.Synonyms)
		if err := a.store.InsertTopicSnapshot(ctx, now, topic.Rank, topic.TopicID,
			topic.Cluster.Label, topic.Cluster.Description, topic.UniqueAuthorCount,
			string(kwJSON), string(synJSON), "", "", topic.Cluster.IsMeme, topic.Cluster.Justification); err != nil {
			return fmt.Errorf("insert snapshot: %w", err)
		}
	}

	slog.Info("topics: analysis cycle complete", "topics", len(identified), "elapsed", fmt.Sprintf("%.1fs", time.Since(start).Seconds()))
	return nil
}

func (a *Analyzer) RunTrendingPost(ctx context.Context, poster TrendingPoster, dryRun bool, rootURI, rootCID, parentURI, parentCID string) error {
	start := time.Now()

	snapshotCutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	snapshots, err := a.store.GetTopicSnapshotsSince(ctx, snapshotCutoff)
	if err != nil {
		return fmt.Errorf("get snapshots: %w", err)
	}

	if len(snapshots) == 0 {
		slog.Info("topics: no snapshots for trending post, skipping")
		return nil
	}

	latestTime := snapshots[len(snapshots)-1].SnapshotTime
	var latestTopics []IdentifiedTopic
	for _, s := range snapshots {
		if s.SnapshotTime == latestTime {
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
		return nil
	}

	previousCutoff := time.Now().UTC().Add(-12 * time.Hour).Format(time.RFC3339)
	prevSnapshots, _ := a.store.GetTopicSnapshotsSince(ctx, previousCutoff)
	var previous []IdentifiedTopic
	if len(prevSnapshots) > 0 {
		prevTime := ""
		for _, s := range prevSnapshots {
			if s.SnapshotTime != latestTime && s.SnapshotTime > prevTime {
				prevTime = s.SnapshotTime
			}
		}
		for _, s := range prevSnapshots {
			if s.SnapshotTime == prevTime {
				previous = append(previous, IdentifiedTopic{TopicID: s.TopicID, Rank: s.Rank})
			}
		}
	}

	latestTopics, err = a.hydrator.HydrateExemplars(ctx, latestTopics)
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
