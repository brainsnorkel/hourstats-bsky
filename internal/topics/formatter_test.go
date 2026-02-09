package topics

import (
	"strings"
	"testing"
)

func TestFormatTrendingPost_AllNew(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics"}}, TopicID: "t1", Rank: 1, ExemplarHandle: "alice.bsky.social", ExemplarURI: "at://did:plc:abc/app.bsky.feed.post/123"},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Weather"}}, TopicID: "t2", Rank: 2},
	}

	text, facets := FormatTrendingPost(ranked, nil)

	if !strings.Contains(text, "#1 Politics (NEW)") {
		t.Errorf("expected '#1 Politics (NEW)', text: %q", text)
	}
	if !strings.Contains(text, "@alice.bsky.social") {
		t.Errorf("expected exemplar mention, text: %q", text)
	}
	if !strings.Contains(text, "#2 Weather (NEW)") {
		t.Errorf("expected '#2 Weather (NEW)', text: %q", text)
	}
	if !strings.Contains(text, "#hstrend") {
		t.Errorf("expected hashtag, text: %q", text)
	}

	tagCount := 0
	linkCount := 0
	for _, f := range facets {
		if f.Type == FacetTag {
			tagCount++
		}
		if f.Type == FacetLink {
			linkCount++
		}
	}
	if tagCount != 1 {
		t.Errorf("expected 1 tag facet, got %d", tagCount)
	}
	if linkCount != 1 {
		t.Errorf("expected 1 link facet (exemplar), got %d", linkCount)
	}
}

func TestFormatTrendingPost_MovementIndicators(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "A"}}, TopicID: "t1", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "B"}}, TopicID: "t2", Rank: 2},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "C"}}, TopicID: "t3", Rank: 3},
	}
	previous := []IdentifiedTopic{
		{TopicID: "t1", Rank: 3},
		{TopicID: "t2", Rank: 2},
	}

	text, _ := FormatTrendingPost(ranked, previous)

	if !strings.Contains(text, "#1 A (+2)") {
		t.Errorf("expected rose indicator, text: %q", text)
	}
	if !strings.Contains(text, "#2 B (->)") {
		t.Errorf("expected unchanged indicator, text: %q", text)
	}
	if !strings.Contains(text, "#3 C (NEW)") {
		t.Errorf("expected NEW indicator, text: %q", text)
	}
}

func TestFormatTrendingPost_FellIndicator(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "A"}}, TopicID: "t1", Rank: 3},
	}
	previous := []IdentifiedTopic{
		{TopicID: "t1", Rank: 1},
	}

	text, _ := FormatTrendingPost(ranked, previous)
	if !strings.Contains(text, "(-2)") {
		t.Errorf("expected fell indicator, text: %q", text)
	}
}

func TestFormatTrendingPost_HashtagFacetByteOffsets(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Test"}}, TopicID: "t1", Rank: 1},
	}

	text, facets := FormatTrendingPost(ranked, nil)

	for _, f := range facets {
		if f.Type != FacetTag {
			continue
		}
		extracted := text[f.ByteStart:f.ByteEnd]
		if extracted != "#"+f.Value {
			t.Errorf("facet byte offset mismatch: expected %q, extracted %q", "#"+f.Value, extracted)
		}
	}
}

func TestFormatTrendingPost_ExemplarLinkFacetOffset(t *testing.T) {
	ranked := []IdentifiedTopic{
		{
			RankedTopic:    RankedTopic{Cluster: TopicCluster{Label: "Test"}},
			TopicID:        "t1",
			Rank:           1,
			ExemplarHandle: "user.bsky.social",
			ExemplarURI:    "at://did:plc:abc/app.bsky.feed.post/xyz",
		},
	}

	text, facets := FormatTrendingPost(ranked, nil)

	var linkFacet *Facet
	for i := range facets {
		if facets[i].Type == FacetLink {
			linkFacet = &facets[i]
			break
		}
	}
	if linkFacet == nil {
		t.Fatal("expected link facet for exemplar")
	}
	extracted := text[linkFacet.ByteStart:linkFacet.ByteEnd]
	if extracted != "@user.bsky.social" {
		t.Errorf("link facet offset mismatch: expected '@user.bsky.social', got %q", extracted)
	}
	if linkFacet.Value != "https://bsky.app/profile/did:plc:abc/post/xyz" {
		t.Errorf("unexpected URL: %q", linkFacet.Value)
	}
}

func TestFormatAltText(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics"}}, Rank: 1, ExemplarHandle: "alice.bsky.social"},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Weather"}}, Rank: 2},
	}

	alt := FormatAltText(ranked)
	if !strings.Contains(alt, "#1 Politics (top post by @alice.bsky.social)") {
		t.Errorf("expected exemplar in alt text, got: %q", alt)
	}
	if !strings.Contains(alt, "#2 Weather") {
		t.Errorf("expected topic without exemplar, got: %q", alt)
	}
}

func TestFormatTrendingPost_NoExemplar(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Test"}}, TopicID: "t1", Rank: 1},
	}

	text, facets := FormatTrendingPost(ranked, nil)
	if strings.Contains(text, "@") {
		t.Errorf("expected no @ mention without exemplar, text: %q", text)
	}
	for _, f := range facets {
		if f.Type == FacetLink {
			t.Error("expected no link facets without exemplar")
		}
	}
}

func TestConvertExemplarURI(t *testing.T) {
	got := convertExemplarURI("at://did:plc:abc/app.bsky.feed.post/xyz")
	want := "https://bsky.app/profile/did:plc:abc/post/xyz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	got2 := convertExemplarURI("https://example.com")
	if got2 != "https://example.com" {
		t.Errorf("non-AT URI should pass through, got %q", got2)
	}
}
