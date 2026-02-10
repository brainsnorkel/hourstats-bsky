package topics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type mockAnalyzerStore struct {
	tokens     []store.TopicTokenRow
	tokenCount int64
	snapshots  []store.TopicSnapshotRow
	identities []store.TopicIdentityRow

	insertedSnapshots []store.TopicSnapshotRow
	upsertedIDs       []string
	purgedTokens      bool
	purgedSnapshots   bool
	purgedIdentities  bool
}

func (m *mockAnalyzerStore) GetTopicTokensSince(_ context.Context, _ string) ([]store.TopicTokenRow, error) {
	return m.tokens, nil
}
func (m *mockAnalyzerStore) GetTopicTokensSinceLimit(_ context.Context, _ string, _ int) ([]store.TopicTokenRow, error) {
	return m.tokens, nil
}
func (m *mockAnalyzerStore) CountTopicTokensSince(_ context.Context, _ string) (int64, error) {
	return m.tokenCount, nil
}
func (m *mockAnalyzerStore) PurgeTopicTokens(_ context.Context, _ string) (int64, error) {
	m.purgedTokens = true
	return 0, nil
}
func (m *mockAnalyzerStore) InsertTopicSnapshot(_ context.Context, snapshotTime string, rank int, topicID, label, description string, postCount int, keywordsJSON, exemplarURI, exemplarHandle string, isMeme bool) error {
	m.insertedSnapshots = append(m.insertedSnapshots, store.TopicSnapshotRow{
		SnapshotTime: snapshotTime, Rank: rank, TopicID: topicID,
		Label: label, Description: description, PostCount: postCount,
		Keywords: keywordsJSON, ExemplarURI: exemplarURI, ExemplarHandle: exemplarHandle,
		IsMeme: isMeme,
	})
	return nil
}
func (m *mockAnalyzerStore) GetTopicSnapshotsSince(_ context.Context, _ string) ([]store.TopicSnapshotRow, error) {
	return m.snapshots, nil
}
func (m *mockAnalyzerStore) PurgeTopicSnapshots(_ context.Context, _ string) (int64, error) {
	m.purgedSnapshots = true
	return 0, nil
}
func (m *mockAnalyzerStore) UpdateSnapshotExemplar(_ context.Context, _ int64, _, _ string) error {
	return nil
}
func (m *mockAnalyzerStore) GetRecentTopicIdentities(_ context.Context, _ string) ([]store.TopicIdentityRow, error) {
	return m.identities, nil
}
func (m *mockAnalyzerStore) UpsertTopicIdentity(_ context.Context, topicID, _, _, _, _ string, _ int) error {
	m.upsertedIDs = append(m.upsertedIDs, topicID)
	return nil
}
func (m *mockAnalyzerStore) PurgeTopicIdentities(_ context.Context, _ string) (int64, error) {
	m.purgedIdentities = true
	return 0, nil
}
func (m *mockAnalyzerStore) GetExemplarCandidates(_ context.Context, _ []string, _ string, _ int) ([]store.ExemplarCandidate, error) {
	return nil, nil
}

func buildTestTokens() []store.TopicTokenRow {
	var rows []store.TopicTokenRow
	for i := 0; i < 50; i++ {
		b, _ := json.Marshal([]string{"trump", "election"})
		rows = append(rows, store.TopicTokenRow{
			PostURI: fmt.Sprintf("at://a/%d", i), Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z",
		})
	}
	for i := 0; i < 40; i++ {
		b, _ := json.Marshal([]string{"weather", "rain"})
		rows = append(rows, store.TopicTokenRow{
			PostURI: fmt.Sprintf("at://b/%d", i), Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z",
		})
	}
	for i := 0; i < 30; i++ {
		b, _ := json.Marshal([]string{"sports", "football"})
		rows = append(rows, store.TopicTokenRow{
			PostURI: fmt.Sprintf("at://c/%d", i), Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z",
		})
	}
	return rows
}

func TestRunAnalysisCycle_Success(t *testing.T) {
	clusters := []TopicCluster{
		{Label: "US Politics", Description: "Political discussion", Keywords: []string{"trump", "election"}, Synonyms: []string{}},
		{Label: "Weather", Description: "Weather talk", Keywords: []string{"weather", "rain"}, Synonyms: []string{}},
	}
	geminiText, _ := json.Marshal(clusters)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Parts: []geminiPart{{Text: string(geminiText)}}}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	tokens := buildTestTokens()
	ms := &mockAnalyzerStore{
		tokens:     tokens,
		tokenCount: int64(len(tokens)),
	}

	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouperWithEndpoint("test-key", srv.URL),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunAnalysisCycle(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ms.insertedSnapshots) == 0 {
		t.Error("expected snapshots to be inserted")
	}
	if !ms.purgedTokens {
		t.Error("expected tokens to be purged")
	}
}

func TestRunAnalysisCycle_InsufficientCorpus(t *testing.T) {
	ms := &mockAnalyzerStore{tokenCount: 50}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test"),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunAnalysisCycle(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ms.insertedSnapshots) != 0 {
		t.Error("expected no snapshots for insufficient corpus")
	}
}

func TestRunTrendingPost_DryRun(t *testing.T) {
	ms := &mockAnalyzerStore{
		snapshots: []store.TopicSnapshotRow{
			{SnapshotTime: "2026-01-01T00:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", PostCount: 10},
			{SnapshotTime: "2026-01-01T06:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", PostCount: 15},
		},
	}

	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test"),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunTrendingPost(context.Background(), nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunTrendingPost_NoSnapshots(t *testing.T) {
	ms := &mockAnalyzerStore{}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test"),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunTrendingPost(context.Background(), nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

type mockPoster struct {
	posted bool
}

func (m *mockPoster) PostWithFacets(_ context.Context, _ string, _ []*bsky.RichtextFacet) error {
	m.posted = true
	return nil
}

func TestRunTrendingPost_Posts(t *testing.T) {
	ms := &mockAnalyzerStore{
		snapshots: []store.TopicSnapshotRow{
			{SnapshotTime: "2026-01-01T00:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", PostCount: 10},
			{SnapshotTime: "2026-01-01T06:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", PostCount: 15},
		},
	}

	poster := &mockPoster{}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test"),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunTrendingPost(context.Background(), poster, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !poster.posted {
		t.Error("expected post to be published")
	}
}

func TestBackfillFromPrevious_FillsToFive(t *testing.T) {
	current := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "A"}}, TopicID: "t1", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "B"}}, TopicID: "t2", Rank: 2},
	}
	previous := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "C"}, PostCount: 50}, TopicID: "t3", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "D"}, PostCount: 40}, TopicID: "t4", Rank: 2},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "E"}, PostCount: 30}, TopicID: "t5", Rank: 3},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "F"}, PostCount: 20}, TopicID: "t6", Rank: 4},
	}

	result := backfillFromPrevious(current, previous)
	if len(result) != 5 {
		t.Fatalf("expected 5 topics, got %d", len(result))
	}
	if result[2].TopicID != "t3" || result[2].Rank != 3 {
		t.Errorf("backfill[0]: got id=%q rank=%d, want t3 rank=3", result[2].TopicID, result[2].Rank)
	}
	if result[4].TopicID != "t5" || result[4].Rank != 5 {
		t.Errorf("backfill[2]: got id=%q rank=%d, want t5 rank=5", result[4].TopicID, result[4].Rank)
	}
}

func TestBackfillFromPrevious_AlreadyFive(t *testing.T) {
	current := make([]IdentifiedTopic, 5)
	for i := range current {
		current[i] = IdentifiedTopic{TopicID: fmt.Sprintf("t%d", i), Rank: i + 1}
	}
	previous := []IdentifiedTopic{
		{TopicID: "prev1", Rank: 1},
	}

	result := backfillFromPrevious(current, previous)
	if len(result) != 5 {
		t.Errorf("expected 5 (unchanged), got %d", len(result))
	}
}

func TestBackfillFromPrevious_SkipsDuplicateTopicIDs(t *testing.T) {
	current := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "A"}}, TopicID: "shared-id", Rank: 1},
	}
	previous := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "A-prev"}}, TopicID: "shared-id", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "B-prev"}}, TopicID: "new-id", Rank: 2},
	}

	result := backfillFromPrevious(current, previous)
	if len(result) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(result))
	}
	if result[1].TopicID != "new-id" {
		t.Errorf("expected new-id, got %q", result[1].TopicID)
	}
}

func TestBackfillFromPrevious_ClearsExemplarForRehydration(t *testing.T) {
	current := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "A"}}, TopicID: "t1", Rank: 1},
	}
	previous := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "B"}}, TopicID: "t2", Rank: 1, ExemplarURI: "at://old/1", ExemplarHandle: "old.bsky.social"},
	}

	result := backfillFromPrevious(current, previous)
	if result[1].ExemplarURI != "" {
		t.Errorf("expected cleared ExemplarURI, got %q", result[1].ExemplarURI)
	}
	if result[1].ExemplarHandle != "" {
		t.Errorf("expected cleared ExemplarHandle, got %q", result[1].ExemplarHandle)
	}
}

func TestBackfillFromPrevious_IncludesMemes(t *testing.T) {
	current := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "A"}}, TopicID: "t1", Rank: 1},
	}
	previous := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Post a Banger", IsMeme: true}}, TopicID: "t2", Rank: 1},
	}

	result := backfillFromPrevious(current, previous)
	if len(result) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(result))
	}
	if !result[1].Cluster.IsMeme {
		t.Error("expected meme topic to be carried over")
	}
}

func TestBackfillFromPrevious_NoPrevious(t *testing.T) {
	current := []IdentifiedTopic{
		{TopicID: "t1", Rank: 1},
	}
	result := backfillFromPrevious(current, nil)
	if len(result) != 1 {
		t.Errorf("expected 1 (unchanged), got %d", len(result))
	}
}

func TestBackfillFromPrevious_SkipsSemanticOverlap(t *testing.T) {
	current := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Ice Hockey", Keywords: []string{"ice", "canada", "hockey", "olympics"}}}, TopicID: "t1", Rank: 1},
	}
	previous := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Sports & Olympics", Keywords: []string{"hockey", "ice", "canada", "olympics", "games", "goal"}}}, TopicID: "t2", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Epstein Files", Keywords: []string{"epstein", "files", "release"}}}, TopicID: "t3", Rank: 2},
	}

	result := backfillFromPrevious(current, previous)
	if len(result) != 2 {
		t.Fatalf("expected 2 topics (original + non-overlapping), got %d", len(result))
	}
	if result[1].TopicID != "t3" {
		t.Errorf("expected Epstein Files (t3) backfilled, got %q (%s)", result[1].TopicID, result[1].Cluster.Label)
	}
}

func TestOverlapsCurrent_HighJaccard(t *testing.T) {
	candidate := IdentifiedTopic{
		RankedTopic: RankedTopic{Cluster: TopicCluster{Keywords: []string{"hockey", "ice", "canada", "olympics", "games"}}},
	}
	current := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Keywords: []string{"ice", "canada", "hockey", "olympics"}}}},
	}
	if !overlapsCurrent(candidate, current) {
		t.Error("expected overlap for highly similar keyword sets")
	}
}

func TestOverlapsCurrent_NoOverlap(t *testing.T) {
	candidate := IdentifiedTopic{
		RankedTopic: RankedTopic{Cluster: TopicCluster{Keywords: []string{"epstein", "files", "release"}}},
	}
	current := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Keywords: []string{"ice", "canada", "hockey", "olympics"}}}},
	}
	if overlapsCurrent(candidate, current) {
		t.Error("expected no overlap for unrelated keyword sets")
	}
}

func TestBuildTrajectories(t *testing.T) {
	snapshots := []store.TopicSnapshotRow{
		{SnapshotTime: "2026-01-01T00:00:00Z", TopicID: "t1", Rank: 3},
		{SnapshotTime: "2026-01-01T00:00:00Z", TopicID: "t2", Rank: 1},
		{SnapshotTime: "2026-01-01T01:00:00Z", TopicID: "t1", Rank: 1},
		{SnapshotTime: "2026-01-01T01:00:00Z", TopicID: "t2", Rank: 2},
		{SnapshotTime: "2026-01-01T02:00:00Z", TopicID: "t1", Rank: 1},
	}
	current := []IdentifiedTopic{
		{TopicID: "t1", Rank: 1},
		{TopicID: "t2", Rank: 2},
	}

	traj := buildTrajectories(snapshots, current)

	if len(traj) != 2 {
		t.Fatalf("expected 2 trajectories, got %d", len(traj))
	}

	t1 := traj["t1"]
	if len(t1) != 3 || t1[0] != 3 || t1[1] != 1 || t1[2] != 1 {
		t.Errorf("t1 trajectory: expected [3 1 1], got %v", t1)
	}

	t2 := traj["t2"]
	if len(t2) != 3 || t2[0] != 1 || t2[1] != 2 || t2[2] != 0 {
		t.Errorf("t2 trajectory: expected [1 2 0], got %v", t2)
	}
}

func TestConvertFacets(t *testing.T) {
	facets := []Facet{
		{ByteStart: 0, ByteEnd: 9, Type: FacetTag, Value: "trending"},
		{ByteStart: 10, ByteEnd: 30, Type: FacetLink, Value: "https://example.com"},
	}

	bskyFacets := convertFacets(facets)
	if len(bskyFacets) != 2 {
		t.Fatalf("expected 2 bsky facets, got %d", len(bskyFacets))
	}
	if bskyFacets[0].Features[0].RichtextFacet_Tag == nil {
		t.Error("expected tag facet")
	}
	if bskyFacets[1].Features[0].RichtextFacet_Link == nil {
		t.Error("expected link facet")
	}
}
