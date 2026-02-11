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

	text, facets := FormatTrendingPost(ranked, nil, 6)

	if !strings.Contains(text, "Trending topics (6h)") {
		t.Errorf("expected header 'Trending topics (6h)', text: %q", text)
	}
	if !strings.Contains(text, "1. Politics") {
		t.Errorf("expected '1. Politics', text: %q", text)
	}
	if !strings.Contains(text, "@alice.bsky.social") {
		t.Errorf("expected exemplar mention, text: %q", text)
	}
	if !strings.Contains(text, "2. Weather") {
		t.Errorf("expected '2. Weather', text: %q", text)
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

func TestFormatTrendingPost_NoMovementIndicators(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "A"}}, TopicID: "t1", Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "B"}}, TopicID: "t2", Rank: 2},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "C"}}, TopicID: "t3", Rank: 3},
	}
	previous := []IdentifiedTopic{
		{TopicID: "t1", Rank: 3},
		{TopicID: "t2", Rank: 2},
	}

	text, _ := FormatTrendingPost(ranked, previous, 6)

	if strings.Contains(text, "(+") || strings.Contains(text, "(-") || strings.Contains(text, "(->)") || strings.Contains(text, "(NEW)") {
		t.Errorf("expected no movement indicators, text: %q", text)
	}
	if !strings.Contains(text, "1. A") {
		t.Errorf("expected '1. A', text: %q", text)
	}
	if !strings.Contains(text, "2. B") {
		t.Errorf("expected '2. B', text: %q", text)
	}
}

func TestFormatTrendingPost_HashtagFacetByteOffsets(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Test"}}, TopicID: "t1", Rank: 1},
	}

	text, facets := FormatTrendingPost(ranked, nil, 6)

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

	text, facets := FormatTrendingPost(ranked, nil, 6)

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
	if !strings.Contains(alt, "1. Politics (top post by @alice.bsky.social)") {
		t.Errorf("expected exemplar in alt text, got: %q", alt)
	}
	if !strings.Contains(alt, "2. Weather") {
		t.Errorf("expected topic without exemplar, got: %q", alt)
	}
}

func TestFormatTrendingPost_NoExemplar(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Test"}}, TopicID: "t1", Rank: 1},
	}

	text, facets := FormatTrendingPost(ranked, nil, 6)
	if strings.Contains(text, "@") {
		t.Errorf("expected no @ mention without exemplar, text: %q", text)
	}
	for _, f := range facets {
		if f.Type == FacetLink {
			t.Error("expected no link facets without exemplar")
		}
	}
}

func TestFormatTrendingPost_MemeTopicSearchLink(t *testing.T) {
	ranked := []IdentifiedTopic{
		{
			RankedTopic: RankedTopic{Cluster: TopicCluster{
				Label:    "Post a Banger",
				IsMeme:   true,
				Keywords: []string{"post", "banger", "post_banger"},
			}},
			TopicID: "t1",
			Rank:    1,
		},
	}

	text, facets := FormatTrendingPost(ranked, nil, 6)

	if !strings.Contains(text, "1. Post a Banger 🔍") {
		t.Errorf("expected meme topic with 🔍, text: %q", text)
	}
	if strings.Contains(text, "@") {
		t.Errorf("meme topic should not have @handle, text: %q", text)
	}

	var linkFacet *Facet
	for i := range facets {
		if facets[i].Type == FacetLink {
			linkFacet = &facets[i]
			break
		}
	}
	if linkFacet == nil {
		t.Fatal("expected link facet for meme search")
	}

	extracted := text[linkFacet.ByteStart:linkFacet.ByteEnd]
	if extracted != "🔍" {
		t.Errorf("facet byte offset mismatch: expected '🔍', extracted %q (bytes %d-%d)", extracted, linkFacet.ByteStart, linkFacet.ByteEnd)
	}
	if linkFacet.Value != "https://bsky.app/search?q=post+banger" {
		t.Errorf("unexpected search URL: %q (expected keywords-based query)", linkFacet.Value)
	}
}

func TestFormatTrendingPost_MixedMemeAndExemplar(t *testing.T) {
	ranked := []IdentifiedTopic{
		{
			RankedTopic:    RankedTopic{Cluster: TopicCluster{Label: "Donald Trump"}},
			TopicID:        "t1",
			Rank:           1,
			ExemplarHandle: "alice.bsky.social",
			ExemplarURI:    "at://did:plc:abc/app.bsky.feed.post/123",
		},
		{
			RankedTopic: RankedTopic{Cluster: TopicCluster{
				Label:    "Post a Banger",
				IsMeme:   true,
				Keywords: []string{"post", "banger", "post_banger"},
			}},
			TopicID: "t2",
			Rank:    2,
		},
	}

	text, facets := FormatTrendingPost(ranked, nil, 6)

	if !strings.Contains(text, "1. Donald Trump @alice.bsky.social") {
		t.Errorf("expected exemplar for non-meme topic, text: %q", text)
	}
	if !strings.Contains(text, "2. Post a Banger 🔍") {
		t.Errorf("expected 🔍 for meme topic, text: %q", text)
	}

	linkCount := 0
	for _, f := range facets {
		if f.Type == FacetLink {
			linkCount++
			extracted := text[f.ByteStart:f.ByteEnd]
			if strings.Contains(f.Value, "search?q=") {
				if extracted != "🔍" {
					t.Errorf("search facet offset mismatch: expected '🔍', got %q", extracted)
				}
			} else {
				if extracted != "@alice.bsky.social" {
					t.Errorf("exemplar facet offset mismatch: expected '@alice.bsky.social', got %q", extracted)
				}
			}
		}
	}
	if linkCount != 2 {
		t.Errorf("expected 2 link facets (exemplar + search), got %d", linkCount)
	}
}

func TestFormatAltText_Meme(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Post a Banger", IsMeme: true}}, Rank: 1},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Weather"}}, Rank: 2, ExemplarHandle: "bob.bsky.social"},
	}

	alt := FormatAltText(ranked)
	if !strings.Contains(alt, "1. Post a Banger (search)") {
		t.Errorf("expected '(search)' for meme topic, got: %q", alt)
	}
	if !strings.Contains(alt, "2. Weather (top post by @bob.bsky.social)") {
		t.Errorf("expected exemplar for non-meme topic, got: %q", alt)
	}
}

func TestMemeSearchQuery(t *testing.T) {
	tests := []struct {
		name     string
		keywords []string
		want     string
	}{
		{"compound preferred", []string{"post", "banger", "post_banger"}, "post banger"},
		{"longest compound wins", []string{"post_banger", "banger_that_goes", "post"}, "banger that goes"},
		{"no compounds", []string{"excuse", "team", "sports"}, "excuse team sports"},
		{"no compounds truncated", []string{"a", "b", "c", "d", "e"}, "a b c"},
		{"empty", []string{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := memeSearchQuery(tt.keywords)
			if got != tt.want {
				t.Errorf("memeSearchQuery(%v) = %q, want %q", tt.keywords, got, tt.want)
			}
		})
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
