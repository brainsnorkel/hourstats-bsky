package topics

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type mockTopicStore struct {
	identities []store.TopicIdentityRow
	upserted   []upsertCall
	purged     bool
}

type upsertCall struct {
	topicID, label, keywordsJSON, firstSeen, lastSeen string
	peakRank                                          int
}

func (m *mockTopicStore) GetRecentTopicIdentities(_ context.Context, _ string) ([]store.TopicIdentityRow, error) {
	return m.identities, nil
}

func (m *mockTopicStore) UpsertTopicIdentity(_ context.Context, topicID, label, keywordsJSON, firstSeen, lastSeen string, peakRank int) error {
	m.upserted = append(m.upserted, upsertCall{topicID, label, keywordsJSON, firstSeen, lastSeen, peakRank})
	return nil
}

func (m *mockTopicStore) PurgeTopicIdentities(_ context.Context, _ string) (int64, error) {
	m.purged = true
	return 0, nil
}

func TestAssignIdentities_NewTopics(t *testing.T) {
	ms := &mockTopicStore{}
	tracker := NewTracker(ms)

	ranked := []RankedTopic{
		{Cluster: TopicCluster{Label: "Politics", Keywords: []string{"trump"}, Synonyms: []string{}}, PostCount: 10},
	}

	result, err := tracker.AssignIdentities(context.Background(), ranked)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].TopicID == "" {
		t.Error("expected non-empty TopicID")
	}
	if result[0].Rank != 1 {
		t.Errorf("expected rank 1, got %d", result[0].Rank)
	}
}

func TestAssignIdentities_MatchExisting(t *testing.T) {
	kws, _ := json.Marshal([]string{"trump", "election"})
	ms := &mockTopicStore{
		identities: []store.TopicIdentityRow{
			{TopicID: "existing-id", CanonicalLabel: "Politics", Keywords: string(kws), FirstSeen: "2026-01-01T00:00:00Z", LastSeen: "2026-01-01T12:00:00Z", PeakRank: 2},
		},
	}
	tracker := NewTracker(ms)

	ranked := []RankedTopic{
		{Cluster: TopicCluster{Label: "US Politics", Keywords: []string{"trump", "election", "congress"}, Synonyms: []string{}}, PostCount: 20},
	}

	result, err := tracker.AssignIdentities(context.Background(), ranked)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].TopicID != "existing-id" {
		t.Errorf("expected reused TopicID 'existing-id', got %q", result[0].TopicID)
	}
}

func TestAssignIdentities_NoMatch(t *testing.T) {
	kws, _ := json.Marshal([]string{"weather", "rain"})
	ms := &mockTopicStore{
		identities: []store.TopicIdentityRow{
			{TopicID: "weather-id", CanonicalLabel: "Weather", Keywords: string(kws), FirstSeen: "2026-01-01T00:00:00Z", LastSeen: "2026-01-01T12:00:00Z", PeakRank: 1},
		},
	}
	tracker := NewTracker(ms)

	ranked := []RankedTopic{
		{Cluster: TopicCluster{Label: "Sports", Keywords: []string{"football", "soccer"}, Synonyms: []string{}}, PostCount: 10},
	}

	result, err := tracker.AssignIdentities(context.Background(), ranked)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].TopicID == "weather-id" {
		t.Error("expected new TopicID, got reused weather-id")
	}
}

func TestAssignIdentities_PurgesOldIdentities(t *testing.T) {
	ms := &mockTopicStore{}
	tracker := NewTracker(ms)

	_, err := tracker.AssignIdentities(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ms.purged {
		t.Error("expected purge to be called")
	}
}

func TestAssignIdentities_OneToOneMapping(t *testing.T) {
	kws, _ := json.Marshal([]string{"shared", "term"})
	ms := &mockTopicStore{
		identities: []store.TopicIdentityRow{
			{TopicID: "only-id", CanonicalLabel: "Shared", Keywords: string(kws), FirstSeen: "2026-01-01T00:00:00Z", LastSeen: "2026-01-01T12:00:00Z", PeakRank: 1},
		},
	}
	tracker := NewTracker(ms)

	ranked := []RankedTopic{
		{Cluster: TopicCluster{Label: "A", Keywords: []string{"shared", "term"}, Synonyms: []string{}}, PostCount: 20},
		{Cluster: TopicCluster{Label: "B", Keywords: []string{"shared", "term"}, Synonyms: []string{}}, PostCount: 10},
	}

	result, err := tracker.AssignIdentities(context.Background(), ranked)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].TopicID != "only-id" {
		t.Errorf("expected first topic to reuse 'only-id', got %q", result[0].TopicID)
	}
	if result[1].TopicID == "only-id" {
		t.Error("expected second topic to get new ID, but got reused 'only-id'")
	}
}

func TestJaccard_Identical(t *testing.T) {
	sim := jaccard([]string{"a", "b"}, []string{"a", "b"})
	if sim != 1.0 {
		t.Errorf("expected 1.0, got %f", sim)
	}
}

func TestJaccard_Disjoint(t *testing.T) {
	sim := jaccard([]string{"a", "b"}, []string{"c", "d"})
	if sim != 0.0 {
		t.Errorf("expected 0.0, got %f", sim)
	}
}

func TestJaccard_Partial(t *testing.T) {
	sim := jaccard([]string{"a", "b", "c"}, []string{"b", "c", "d"})
	if sim != 0.5 {
		t.Errorf("expected 0.5, got %f", sim)
	}
}

func TestJaccard_Empty(t *testing.T) {
	sim := jaccard(nil, nil)
	if sim != 0.0 {
		t.Errorf("expected 0.0, got %f", sim)
	}
}
