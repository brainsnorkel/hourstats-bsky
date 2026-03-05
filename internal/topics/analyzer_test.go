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
func (m *mockAnalyzerStore) InsertTopicSnapshot(_ context.Context, snapshotTime string, rank int, topicID, label, description string, postCount int, keywordsJSON, synonymsJSON, exemplarURI, exemplarHandle string, isMeme bool, justification string) error {
	m.insertedSnapshots = append(m.insertedSnapshots, store.TopicSnapshotRow{
		SnapshotTime: snapshotTime, Rank: rank, TopicID: topicID,
		Label: label, Description: description, UniqueAuthorCount: postCount,
		Keywords: keywordsJSON, Synonyms: synonymsJSON, ExemplarURI: exemplarURI, ExemplarHandle: exemplarHandle,
		IsMeme: isMeme, Justification: justification,
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
func (m *mockAnalyzerStore) SetKeyValue(_ context.Context, _, _ string) error {
	return nil
}

func buildTestTokens() []store.TopicTokenRow {
	var rows []store.TopicTokenRow
	for i := 0; i < 50; i++ {
		b, _ := json.Marshal([]string{"trump", "election"})
		rows = append(rows, store.TopicTokenRow{
			PostURI: fmt.Sprintf("at://a/%d", i), Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z", AuthorDID: fmt.Sprintf("did:plc:a%d", i),
		})
	}
	for i := 0; i < 40; i++ {
		b, _ := json.Marshal([]string{"weather", "rain"})
		rows = append(rows, store.TopicTokenRow{
			PostURI: fmt.Sprintf("at://b/%d", i), Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z", AuthorDID: fmt.Sprintf("did:plc:b%d", i),
		})
	}
	for i := 0; i < 30; i++ {
		b, _ := json.Marshal([]string{"sports", "football"})
		rows = append(rows, store.TopicTokenRow{
			PostURI: fmt.Sprintf("at://c/%d", i), Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z", AuthorDID: fmt.Sprintf("did:plc:c%d", i),
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
		grouper:  NewGrouper("test", ""),
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
			{SnapshotTime: "2026-01-01T00:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 10},
			{SnapshotTime: "2026-01-01T06:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 15},
		},
	}

	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test", ""),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunTrendingPost(context.Background(), nil, true, "", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunTrendingPost_NoSnapshots(t *testing.T) {
	ms := &mockAnalyzerStore{}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test", ""),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunTrendingPost(context.Background(), nil, false, "", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

type mockPoster struct {
	posted      bool
	postedReply bool
	replyErr    error
}

func (m *mockPoster) PostWithFacets(_ context.Context, _ string, _ []*bsky.RichtextFacet) error {
	m.posted = true
	return nil
}

func (m *mockPoster) PostWithFacetsAsReply(_ context.Context, _ string, _ []*bsky.RichtextFacet, _, _, _, _ string) (string, string, error) {
	if m.replyErr != nil {
		return "", "", m.replyErr
	}
	m.postedReply = true
	return "at://reply/uri", "replycid", nil
}

func TestRunTrendingPost_Posts(t *testing.T) {
	ms := &mockAnalyzerStore{
		snapshots: []store.TopicSnapshotRow{
			{SnapshotTime: "2026-01-01T00:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 10},
			{SnapshotTime: "2026-01-01T06:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 15},
		},
	}

	poster := &mockPoster{}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test", ""),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunTrendingPost(context.Background(), poster, false, "", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !poster.posted {
		t.Error("expected post to be published")
	}
}

func TestRunTrendingPost_AsReply(t *testing.T) {
	ms := &mockAnalyzerStore{
		snapshots: []store.TopicSnapshotRow{
			{SnapshotTime: "2026-01-01T00:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 10},
			{SnapshotTime: "2026-01-01T06:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 15},
		},
	}

	poster := &mockPoster{}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test", ""),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunTrendingPost(context.Background(), poster, false, "at://root/uri", "rootcid", "at://spark/uri", "sparkcid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !poster.postedReply {
		t.Error("expected post as reply")
	}
	if poster.posted {
		t.Error("expected reply path, not standalone")
	}
}

func TestRunTrendingPost_ReplyFallback(t *testing.T) {
	ms := &mockAnalyzerStore{
		snapshots: []store.TopicSnapshotRow{
			{SnapshotTime: "2026-01-01T00:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 10},
			{SnapshotTime: "2026-01-01T06:00:00Z", Rank: 1, TopicID: "t1", Label: "Politics", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 15},
		},
	}

	poster := &mockPoster{replyErr: fmt.Errorf("reply failed")}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test", ""),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	err := a.RunTrendingPost(context.Background(), poster, false, "at://root/uri", "rootcid", "at://spark/uri", "sparkcid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if poster.postedReply {
		t.Error("expected reply to fail")
	}
	if !poster.posted {
		t.Error("expected fallback to standalone post")
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
