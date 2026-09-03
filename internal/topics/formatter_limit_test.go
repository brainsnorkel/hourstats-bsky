package topics

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// topicsWithLabels builds ranked topics with an exemplar each, so the formatter
// has to drop exemplars first and only then start dropping topics.
func topicsWithLabels(labels ...string) []IdentifiedTopic {
	ranked := make([]IdentifiedTopic, len(labels))
	for i, label := range labels {
		ranked[i] = IdentifiedTopic{
			RankedTopic:    RankedTopic{Cluster: TopicCluster{Label: label}},
			TopicID:        label,
			Rank:           i + 1,
			ExemplarHandle: "someverylonghandlename.bsky.social",
			ExemplarURI:    "at://did:plc:abc/app.bsky.feed.post/123",
		}
	}
	return ranked
}

// TestFormatTrendingPost_DropsTopicsWhenExemplarsExhausted is the regression
// case: once every exemplar has been dropped the formatter used to return
// over-limit text, which Bluesky then rejected.
func TestFormatTrendingPost_DropsTopicsWhenExemplarsExhausted(t *testing.T) {
	ranked := topicsWithLabels(
		strings.Repeat("Alpha", 12),
		strings.Repeat("Bravo", 12),
		strings.Repeat("Charlie", 12),
		strings.Repeat("Delta", 12),
		strings.Repeat("Echo", 12),
	)

	text, facets := FormatTrendingPost(ranked, nil, 2)

	if got := utf8.RuneCountInString(text); got > maxGraphemes {
		t.Errorf("text is %d runes, want <= %d: %q", got, maxGraphemes, text)
	}
	if !strings.Contains(text, strings.Repeat("Alpha", 12)) {
		t.Errorf("top topic was dropped, want it kept: %q", text)
	}
	if strings.Contains(text, strings.Repeat("Echo", 12)) {
		t.Errorf("lowest-ranked topic should have been dropped: %q", text)
	}
	// Dropping whole topics must be preferred over hard truncation, which would
	// cut a label mid-word and take the hashtag with it.
	if !strings.HasSuffix(text, "#hstrend") {
		t.Errorf("text should still end with the hashtag, got: %q", text)
	}
	assertFacetsInBounds(t, text, facets)
}

// TestFormatTrendingPost_HardTruncatesOversizeSingleTopic covers the last
// resort: a single label that cannot fit even alone.
func TestFormatTrendingPost_HardTruncatesOversizeSingleTopic(t *testing.T) {
	ranked := topicsWithLabels(strings.Repeat("A", 500))

	text, facets := FormatTrendingPost(ranked, nil, 2)

	if got := utf8.RuneCountInString(text); got != maxGraphemes {
		t.Errorf("text is %d runes, want exactly %d after hard truncation", got, maxGraphemes)
	}
	if !utf8.ValidString(text) {
		t.Error("hard truncation split a multi-byte rune")
	}
	assertFacetsInBounds(t, text, facets)
}

// TestFormatTrendingPost_TruncationKeepsRuneBoundaries truncates a label made of
// multi-byte runes, where a byte-wise cut would corrupt the text.
func TestFormatTrendingPost_TruncationKeepsRuneBoundaries(t *testing.T) {
	ranked := topicsWithLabels(strings.Repeat("日", 500))

	text, facets := FormatTrendingPost(ranked, nil, 2)

	if got := utf8.RuneCountInString(text); got != maxGraphemes {
		t.Errorf("text is %d runes, want exactly %d", got, maxGraphemes)
	}
	if !utf8.ValidString(text) {
		t.Error("hard truncation split a multi-byte rune")
	}
	assertFacetsInBounds(t, text, facets)
}

// TestFormatTrendingPost_ManyTopicsAlwaysFits is a broad guard: whatever the
// input, the returned text is postable.
func TestFormatTrendingPost_ManyTopicsAlwaysFits(t *testing.T) {
	var labels []string
	for i := 0; i < 25; i++ {
		labels = append(labels, strings.Repeat("Topic", 8))
	}
	ranked := topicsWithLabels(labels...)

	text, facets := FormatTrendingPost(ranked, nil, 2)

	if got := utf8.RuneCountInString(text); got > maxGraphemes {
		t.Errorf("text is %d runes, want <= %d", got, maxGraphemes)
	}
	if !strings.HasSuffix(text, "#hstrend") {
		t.Errorf("text should still end with the hashtag, got: %q", text)
	}
	assertFacetsInBounds(t, text, facets)
}

// assertFacetsInBounds guards against facets pointing past the end of a
// shortened post, which Bluesky would reject.
func assertFacetsInBounds(t *testing.T, text string, facets []Facet) {
	t.Helper()
	for i, f := range facets {
		if f.ByteStart < 0 || f.ByteEnd > len(text) || f.ByteStart > f.ByteEnd {
			t.Errorf("facet %d [%d,%d) out of bounds for %d-byte text", i, f.ByteStart, f.ByteEnd, len(text))
		}
	}
}
