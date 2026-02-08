package topics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type AnalyzerStore interface {
	TopicStore
	ExemplarTokenStore
	GetTopicTokensSince(ctx context.Context, cutoff string) ([]store.TopicTokenRow, error)
	CountTopicTokensSince(ctx context.Context, cutoff string) (int64, error)
	PurgeTopicTokens(ctx context.Context, cutoff string) (int64, error)
	InsertTopicSnapshot(ctx context.Context, snapshotTime string, rank int, topicID, label, description string, postCount int, keywordsJSON, exemplarURI, exemplarHandle string) error
	GetTopicSnapshotsSince(ctx context.Context, cutoff string) ([]store.TopicSnapshotRow, error)
	PurgeTopicSnapshots(ctx context.Context, cutoff string) (int64, error)
	UpdateSnapshotExemplar(ctx context.Context, snapshotID int64, exemplarURI, exemplarHandle string) error
}

type TrendingPoster interface {
	PostWithImage(ctx context.Context, text string, imageData []byte, altText string, facets ...[]*bsky.RichtextFacet) (string, string, error)
}

type Analyzer struct {
	store    AnalyzerStore
	grouper  *Grouper
	tracker  *Tracker
	hydrator *ExemplarHydrator
}

func NewAnalyzer(s AnalyzerStore, geminiAPIKey string, fetcher ExemplarPostFetcher) *Analyzer {
	grouper := NewGrouper(geminiAPIKey)
	tracker := NewTracker(s)
	exemplarHydrator := NewExemplarHydrator(fetcher, s)

	return &Analyzer{
		store:    s,
		grouper:  grouper,
		tracker:  tracker,
		hydrator: exemplarHydrator,
	}
}

func (a *Analyzer) RunAnalysisCycle(ctx context.Context) error {
	start := time.Now()

	purgeTokenCutoff := time.Now().UTC().Add(-26 * time.Hour).Format(time.RFC3339)
	purged, err := a.store.PurgeTopicTokens(ctx, purgeTokenCutoff)
	if err != nil {
		return fmt.Errorf("purge tokens: %w", err)
	}
	if purged > 0 {
		log.Printf("topics: purged %d expired tokens", purged)
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
		log.Printf("topics: corpus too small (%d < %d), skipping cycle", count, MinCorpusSize)
		return nil
	}

	rows, err := a.store.GetTopicTokensSince(ctx, tokenCutoff)
	if err != nil {
		return fmt.Errorf("get tokens: %w", err)
	}

	terms := ComputeTFIDF(rows)
	if len(terms) == 0 {
		log.Printf("topics: no significant terms found, skipping")
		return nil
	}
	log.Printf("topics: TF-IDF computed, %d terms (%.1fs)", len(terms), time.Since(start).Seconds())

	clusters, err := a.grouper.GroupAndLabel(ctx, terms)
	if err != nil {
		log.Printf("topics: grouper error (using fallback): %v", err)
	}
	if len(clusters) == 0 {
		log.Printf("topics: no clusters produced, skipping")
		return nil
	}

	ranked := RankTopics(clusters, rows)
	if len(ranked) == 0 {
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
			string(kwJSON), "", ""); err != nil {
			return fmt.Errorf("insert snapshot: %w", err)
		}
	}

	log.Printf("topics: analysis cycle complete, %d topics ranked (%.1fs total)", len(identified), time.Since(start).Seconds())
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
		log.Printf("topics: no snapshots for trending post, skipping")
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
					Cluster:   TopicCluster{Label: s.Label, Description: s.Description, Keywords: kws},
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

	latestTopics, err = a.hydrator.HydrateExemplars(ctx, latestTopics)
	if err != nil {
		log.Printf("topics: exemplar hydration error: %v", err)
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

	text, facets := FormatTrendingPost(latestTopics, previous)
	altText := FormatAltText(latestTopics)

	chartData, err := sparkline.GenerateTrendingChart(snapshots)
	if err != nil {
		log.Printf("topics: chart generation error: %v", err)
	}

	if dryRun {
		log.Printf("topics: DRY RUN trending post:\n%s", text)
		log.Printf("topics: alt text: %s", altText)
		log.Printf("topics: %d facets, chart=%d bytes", len(facets), len(chartData))
		return nil
	}

	if poster == nil {
		return fmt.Errorf("poster is nil, cannot post")
	}

	bskyFacets := convertFacets(facets)
	_, _, err = poster.PostWithImage(ctx, text, chartData, altText, bskyFacets)
	if err != nil {
		return fmt.Errorf("post trending: %w", err)
	}

	log.Printf("topics: trending post published (%.1fs)", time.Since(start).Seconds())
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
