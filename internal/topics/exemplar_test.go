package topics

import (
	"context"
	"fmt"
	"testing"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
)

type mockExemplarFetcher struct {
	posts []*bsky.FeedDefs_PostView
	err   error
}

func (m *mockExemplarFetcher) GetPosts(_ context.Context, uris []string) ([]*bsky.FeedDefs_PostView, error) {
	if m.err != nil {
		return nil, m.err
	}
	uriSet := make(map[string]bool)
	for _, u := range uris {
		uriSet[u] = true
	}
	var result []*bsky.FeedDefs_PostView
	for _, p := range m.posts {
		if uriSet[p.Uri] {
			result = append(result, p)
		}
	}
	return result, nil
}

type mockExemplarStore struct {
	urisByKeyword map[string][]string
	err           error
}

func (m *mockExemplarStore) GetTopicTokenURIsByKeywords(_ context.Context, keywords []string, _ string, limit int) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	seen := make(map[string]bool)
	var result []string
	for _, kw := range keywords {
		for _, uri := range m.urisByKeyword[kw] {
			if !seen[uri] && len(result) < limit {
				seen[uri] = true
				result = append(result, uri)
			}
		}
	}
	return result, nil
}

func makePostView(uri, handle string, likes, reposts, replies int64) *bsky.FeedDefs_PostView {
	return &bsky.FeedDefs_PostView{
		Uri:         uri,
		Author:      &bsky.ActorDefs_ProfileViewBasic{Handle: handle},
		LikeCount:   &likes,
		RepostCount: &reposts,
		ReplyCount:  &replies,
	}
}

func TestHydrateExemplars_PicksHighestEngagement(t *testing.T) {
	fetcher := &mockExemplarFetcher{
		posts: []*bsky.FeedDefs_PostView{
			makePostView("at://a/1", "low.bsky.social", 1, 0, 0),
			makePostView("at://a/2", "high.bsky.social", 100, 50, 25),
			makePostView("at://a/3", "mid.bsky.social", 10, 5, 2),
		},
	}
	store := &mockExemplarStore{
		urisByKeyword: map[string][]string{
			"politics": {"at://a/1", "at://a/2", "at://a/3"},
		},
	}

	hydrator := NewExemplarHydrator(fetcher, store)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics", Keywords: []string{"politics"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "at://a/2" {
		t.Errorf("expected highest engagement URI 'at://a/2', got %q", result[0].ExemplarURI)
	}
	if result[0].ExemplarHandle != "high.bsky.social" {
		t.Errorf("expected handle 'high.bsky.social', got %q", result[0].ExemplarHandle)
	}
}

func TestHydrateExemplars_NoURIs(t *testing.T) {
	fetcher := &mockExemplarFetcher{}
	store := &mockExemplarStore{urisByKeyword: map[string][]string{}}

	hydrator := NewExemplarHydrator(fetcher, store)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Empty", Keywords: []string{"nothing"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" {
		t.Errorf("expected empty exemplar URI, got %q", result[0].ExemplarURI)
	}
}

func TestHydrateExemplars_FetcherError(t *testing.T) {
	fetcher := &mockExemplarFetcher{err: fmt.Errorf("api error")}
	store := &mockExemplarStore{
		urisByKeyword: map[string][]string{
			"test": {"at://a/1"},
		},
	}

	hydrator := NewExemplarHydrator(fetcher, store)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Test", Keywords: []string{"test"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarURI != "" {
		t.Errorf("expected empty exemplar after error, got %q", result[0].ExemplarURI)
	}
}

func TestHydrateExemplars_MultipleTopics(t *testing.T) {
	fetcher := &mockExemplarFetcher{
		posts: []*bsky.FeedDefs_PostView{
			makePostView("at://a/1", "alice.bsky.social", 50, 10, 5),
			makePostView("at://b/1", "bob.bsky.social", 100, 20, 10),
		},
	}
	store := &mockExemplarStore{
		urisByKeyword: map[string][]string{
			"politics": {"at://a/1"},
			"weather":  {"at://b/1"},
		},
	}

	hydrator := NewExemplarHydrator(fetcher, store)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics", Keywords: []string{"politics"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Weather", Keywords: []string{"weather"}, Synonyms: []string{}}}, TopicID: "t2", Rank: 2},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "alice.bsky.social" {
		t.Errorf("expected 'alice.bsky.social', got %q", result[0].ExemplarHandle)
	}
	if result[1].ExemplarHandle != "bob.bsky.social" {
		t.Errorf("expected 'bob.bsky.social', got %q", result[1].ExemplarHandle)
	}
}

func TestHydrateExemplars_EmptyTopics(t *testing.T) {
	hydrator := NewExemplarHydrator(nil, nil)
	result, err := hydrator.HydrateExemplars(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestHydrateExemplars_SkipsAdultContent(t *testing.T) {
	adultPost := makePostView("at://a/1", "nsfw.bsky.social", 500, 100, 50)
	adultPost.Labels = []*atproto.LabelDefs_Label{{Val: "porn"}}
	cleanPost := makePostView("at://a/2", "clean.bsky.social", 10, 2, 1)

	fetcher := &mockExemplarFetcher{
		posts: []*bsky.FeedDefs_PostView{adultPost, cleanPost},
	}
	store := &mockExemplarStore{
		urisByKeyword: map[string][]string{
			"topic": {"at://a/1", "at://a/2"},
		},
	}

	hydrator := NewExemplarHydrator(fetcher, store)
	topics := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Topic", Keywords: []string{"topic"}, Synonyms: []string{}}}, TopicID: "t1", Rank: 1},
	}

	result, err := hydrator.HydrateExemplars(context.Background(), topics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].ExemplarHandle != "clean.bsky.social" {
		t.Errorf("expected clean post, got %q (adult content should be skipped)", result[0].ExemplarHandle)
	}
}

func TestPostEngagement(t *testing.T) {
	var likes, reposts, replies int64 = 10, 5, 3
	v := &bsky.FeedDefs_PostView{
		LikeCount:   &likes,
		RepostCount: &reposts,
		ReplyCount:  &replies,
	}
	if got := postEngagement(v); got != 18 {
		t.Errorf("expected 18, got %d", got)
	}

	vNil := &bsky.FeedDefs_PostView{}
	if got := postEngagement(vNil); got != 0 {
		t.Errorf("expected 0 for nil counts, got %d", got)
	}
}
