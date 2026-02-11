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
	InsertTopicSnapshot(ctx context.Context, snapshotTime string, rank int, topicID, label, description string, postCount int, keywordsJSON, exemplarURI, exemplarHandle string, isMeme bool, justification string) error
	GetTopicSnapshotsSince(ctx context.Context, cutoff string) ([]store.TopicSnapshotRow, error)
	PurgeTopicSnapshots(ctx context.Context, cutoff string) (int64, error)
	UpdateSnapshotExemplar(ctx context.Context, snapshotID int64, exemplarURI, exemplarHandle string) error
	SetKeyValue(ctx context.Context, key, value string) error
}

type TrendingPoster interface {
	PostWithFacets(ctx context.Context, text string, facets []*bsky.RichtextFacet) error
}

type Analyzer struct {
	store    AnalyzerStore
	grouper  *Grouper
	tracker  *Tracker
	hydrator *ExemplarHydrator
}

func NewAnalyzer(s AnalyzerStore, geminiAPIKey string) *Analyzer {
	grouper := NewGrouper(geminiAPIKey)
	tracker := NewTracker(s)
	exemplarHydrator := NewExemplarHydrator(s)

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

	tokenCutoff := time.Now().UTC().Add(-6 * time.Hour).Format(time.RFC3339)
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
		slog.Warn("topics: grouper error, using fallback", "error", err)
	}
	if len(clusters) == 0 {
		slog.Warn("topics: no clusters produced, skipping")
		return nil
	}

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
		if err := a.store.InsertTopicSnapshot(ctx, now, topic.Rank, topic.TopicID,
			topic.Cluster.Label, topic.Cluster.Description, topic.PostCount,
			string(kwJSON), "", "", topic.Cluster.IsMeme, topic.Cluster.Justification); err != nil {
			return fmt.Errorf("insert snapshot: %w", err)
		}
	}

	slog.Info("topics: analysis cycle complete", "topics", len(identified), "elapsed", fmt.Sprintf("%.1fs", time.Since(start).Seconds()))
	return nil
}

func (a *Analyzer) RunTrendingPost(ctx context.Context, poster TrendingPoster, dryRun bool) error {
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
			_ = json.Unmarshal([]byte(s.Keywords), &kws)
			latestTopics = append(latestTopics, IdentifiedTopic{
				RankedTopic: RankedTopic{
					Cluster:   TopicCluster{Label: s.Label, Description: s.Description, Keywords: kws, IsMeme: s.IsMeme},
					PostCount: s.PostCount,
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
	var previousFull []IdentifiedTopic
	if len(prevSnapshots) > 0 {
		prevTime := ""
		for _, s := range prevSnapshots {
			if s.SnapshotTime != latestTime && s.SnapshotTime > prevTime {
				prevTime = s.SnapshotTime
			}
		}
		for _, s := range prevSnapshots {
			if s.SnapshotTime == prevTime {
				var kws []string
				_ = json.Unmarshal([]byte(s.Keywords), &kws)
				full := IdentifiedTopic{
					RankedTopic: RankedTopic{
						Cluster:   TopicCluster{Label: s.Label, Description: s.Description, Keywords: kws, IsMeme: s.IsMeme},
						PostCount: s.PostCount,
					},
					TopicID: s.TopicID,
					Rank:    s.Rank,
				}
				previous = append(previous, IdentifiedTopic{TopicID: s.TopicID, Rank: s.Rank})
				previousFull = append(previousFull, full)
			}
		}
	}

	latestTopics = backfillFromPrevious(latestTopics, previousFull)

	latestTopics, err = a.hydrator.HydrateExemplars(ctx, latestTopics)
	if err != nil {
		slog.Warn("topics: exemplar hydration error", "error", err)
	}

	text, facets := FormatTrendingPost(latestTopics, previous, 6)

	if dryRun {
		slog.Info("topics: DRY RUN trending post", "text", text, "facets", len(facets))
		return nil
	}

	if poster == nil {
		return fmt.Errorf("poster is nil, cannot post")
	}

	bskyFacets := convertFacets(facets)
	if err := poster.PostWithFacets(ctx, text, bskyFacets); err != nil {
		return fmt.Errorf("post trending: %w", err)
	}

	if err := a.store.SetKeyValue(ctx, "trending_post_last_time", time.Now().UTC().Format(time.RFC3339)); err != nil {
		slog.Warn("topics: failed to record trending post time", "error", err)
	}

	slog.Info("topics: trending post published", "elapsed", fmt.Sprintf("%.1fs", time.Since(start).Seconds()))
	return nil
}

func backfillFromPrevious(current, previous []IdentifiedTopic) []IdentifiedTopic {
	if len(current) >= TopTopics || len(previous) == 0 {
		return current
	}

	currentIDs := make(map[string]bool, len(current))
	for _, t := range current {
		currentIDs[t.TopicID] = true
	}

	nextRank := len(current) + 1
	for _, prev := range previous {
		if len(current) >= TopTopics {
			break
		}
		if currentIDs[prev.TopicID] {
			continue
		}
		if overlapsCurrent(prev, current) {
			slog.Info("topics: skipped backfill (semantic overlap)", "topic", prev.Cluster.Label)
			continue
		}
		backfilled := prev
		backfilled.Rank = nextRank
		backfilled.ExemplarURI = ""
		backfilled.ExemplarHandle = ""
		current = append(current, backfilled)
		currentIDs[prev.TopicID] = true
		nextRank++
		slog.Info("topics: backfilled from previous cycle", "topic", prev.Cluster.Label, "rank", backfilled.Rank)
	}

	return current
}

func overlapsCurrent(candidate IdentifiedTopic, current []IdentifiedTopic) bool {
	for _, c := range current {
		if jaccard(candidate.Cluster.Keywords, c.Cluster.Keywords) > jaccardThreshold {
			return true
		}
	}
	return false
}

func buildTrajectories(snapshots []store.TopicSnapshotRow, current []IdentifiedTopic) map[string][]int {
	timeSlots := make([]string, 0)
	seen := make(map[string]bool)
	for _, s := range snapshots {
		if !seen[s.SnapshotTime] {
			seen[s.SnapshotTime] = true
			timeSlots = append(timeSlots, s.SnapshotTime)
		}
	}

	rankByTimeAndTopic := make(map[string]map[string]int)
	for _, s := range snapshots {
		if rankByTimeAndTopic[s.SnapshotTime] == nil {
			rankByTimeAndTopic[s.SnapshotTime] = make(map[string]int)
		}
		rankByTimeAndTopic[s.SnapshotTime][s.TopicID] = s.Rank
	}

	currentIDs := make(map[string]bool)
	for _, t := range current {
		currentIDs[t.TopicID] = true
	}

	result := make(map[string][]int)
	for id := range currentIDs {
		ranks := make([]int, len(timeSlots))
		for i, ts := range timeSlots {
			if r, ok := rankByTimeAndTopic[ts][id]; ok {
				ranks[i] = r
			}
		}
		result[id] = ranks
	}
	return result
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
