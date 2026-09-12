package topics

import (
	"strings"
	"testing"
)

// A label is written by the grouping model from firehose tokens, so it can
// contain anything that survives output validation — and an earlier line's
// text must never be able to capture a later topic's exemplar link.
func TestBuildFacets_LabelCannotStealAnotherTopicsLink(t *testing.T) {
	const handle = "@evil.bsky.social"
	ranked := []IdentifiedTopic{
		{
			RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Follow " + handle + " now"}},
			TopicID:     "t1", Rank: 1,
		},
		{
			RankedTopic:    RankedTopic{Cluster: TopicCluster{Label: "Hockey"}},
			TopicID:        "t2",
			Rank:           2,
			ExemplarHandle: "evil.bsky.social",
			ExemplarURI:    "at://did:plc:real/app.bsky.feed.post/222",
		},
	}

	text, facets := FormatTrendingPost(ranked, nil, 1, nil)

	if len(facets) != 1 {
		t.Fatalf("got %d facets, want 1: %+v", len(facets), facets)
	}
	f := facets[0]
	if got := text[f.ByteStart:f.ByteEnd]; got != handle {
		t.Errorf("facet covers %q, want %q", got, handle)
	}

	// The link must sit on topic 2's line, not on the label above it.
	lineStart := strings.Index(text, "2. Hockey")
	if lineStart < 0 {
		t.Fatalf("topic 2 line missing from %q", text)
	}
	if f.ByteStart < lineStart {
		t.Errorf("facet starts at %d, before topic 2's line at %d — the label captured the link", f.ByteStart, lineStart)
	}
	if f.Value != "https://bsky.app/profile/did:plc:real/post/222" {
		t.Errorf("facet URL = %q", f.Value)
	}
}

// When a topic's own label repeats its own exemplar handle, the facet belongs
// on the mention the renderer appended, which is the last one on the line.
func TestBuildFacets_LabelRepeatingItsOwnHandle(t *testing.T) {
	ranked := []IdentifiedTopic{
		{
			RankedTopic:    RankedTopic{Cluster: TopicCluster{Label: "Quote @alice.bsky.social"}},
			TopicID:        "t1",
			Rank:           1,
			ExemplarHandle: "alice.bsky.social",
			ExemplarURI:    "at://did:plc:alice/app.bsky.feed.post/1",
		},
	}

	text, facets := FormatTrendingPost(ranked, nil, 1, nil)

	if len(facets) != 1 {
		t.Fatalf("got %d facets, want 1", len(facets))
	}
	last := strings.LastIndex(text, "@alice.bsky.social")
	if facets[0].ByteStart != last {
		t.Errorf("facet starts at %d, want the appended mention at %d", facets[0].ByteStart, last)
	}
}

// Every facet must address the text that was actually posted: a byte range
// past the end, or one straddling a line, is a malformed record.
func TestFormatTrendingPost_FacetsStayInsideTheirLines(t *testing.T) {
	ranked := []IdentifiedTopic{
		{
			RankedTopic:    RankedTopic{Cluster: TopicCluster{Label: "Politics", Keywords: []string{"a"}}},
			TopicID:        "t1",
			Rank:           1,
			ExemplarHandle: "alice.bsky.social",
			ExemplarURI:    "at://did:plc:alice/app.bsky.feed.post/1",
		},
		{
			RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Post a Banger", Keywords: []string{"post_banger"}, IsMeme: true}},
			TopicID:     "t2", Rank: 2,
		},
		{
			RankedTopic:    RankedTopic{Cluster: TopicCluster{Label: "Weather"}},
			TopicID:        "t3",
			Rank:           3,
			ExemplarHandle: "bob.bsky.social",
			ExemplarURI:    "at://did:plc:bob/app.bsky.feed.post/3",
		},
	}

	text, facets := FormatTrendingPost(ranked, nil, 1, nil)

	if len(facets) != 3 {
		t.Fatalf("got %d facets, want 3: %+v", len(facets), facets)
	}
	for _, f := range facets {
		if f.ByteStart < 0 || f.ByteEnd > len(text) || f.ByteStart >= f.ByteEnd {
			t.Fatalf("facet %+v out of range for %d bytes", f, len(text))
		}
		if strings.Contains(text[f.ByteStart:f.ByteEnd], "\n") {
			t.Errorf("facet %+v straddles a line break", f)
		}
	}
	if got := text[facets[0].ByteStart:facets[0].ByteEnd]; got != "@alice.bsky.social" {
		t.Errorf("first facet covers %q", got)
	}
	if got := text[facets[1].ByteStart:facets[1].ByteEnd]; got != "🔍" {
		t.Errorf("second facet covers %q, want the search icon", got)
	}
	if got := text[facets[2].ByteStart:facets[2].ByteEnd]; got != "@bob.bsky.social" {
		t.Errorf("third facet covers %q", got)
	}
}

// The 300-grapheme cut is the one path that changes the text after its line
// spans were recorded, so a facet must not survive into a line the cut removed.
func TestFormatTrendingPost_FacetsSurviveTheHardCut(t *testing.T) {
	ranked := []IdentifiedTopic{
		{
			RankedTopic:    RankedTopic{Cluster: TopicCluster{Label: strings.Repeat("long label ", 40)}},
			TopicID:        "t1",
			Rank:           1,
			ExemplarHandle: "alice.bsky.social",
			ExemplarURI:    "at://did:plc:alice/app.bsky.feed.post/1",
		},
	}

	text, facets := FormatTrendingPost(ranked, nil, 1, nil)

	if n := len([]rune(text)); n > maxGraphemes {
		t.Fatalf("text is %d graphemes, want at most %d", n, maxGraphemes)
	}
	for _, f := range facets {
		if f.ByteEnd > len(text) {
			t.Errorf("facet %+v points past the %d-byte text", f, len(text))
		}
	}
}
