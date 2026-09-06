package topics

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTruncateLabel(t *testing.T) {
	cases := []struct {
		in       string
		maxRunes int
		want     string
	}{
		{"", 28, ""},
		{"   ", 28, ""},
		{"  Charlie   Kirk\nshooting ", 28, "Charlie Kirk shooting"},
		{"abcdefghijklmnopqrstuvwxyz12", 28, "abcdefghijklmnopqrstuvwxyz12"},
		{"abcdefghijklmnopqrstuvwxyz123", 28, "abcdefghijklmnopqrstuvwxyz1…"},
		{"ααααααααααααααααααααααααααααα", 28, "ααααααααααααααααααααααααααα…"},
		{"Charlie Kirk shooting", 12, "Charlie Kir…"},
		{"Charlie Kirk shooting", 0, ""},
		{"Charlie Kirk shooting", -4, ""},
	}
	for _, tc := range cases {
		if got := truncateLabel(tc.in, tc.maxRunes); got != tc.want {
			t.Errorf("truncateLabel(%q, %d) = %q, want %q", tc.in, tc.maxRunes, got, tc.want)
		}
	}
	if n := utf8.RuneCountInString(truncateLabel(strings.Repeat("x", 99), 28)); n > 28 {
		t.Errorf("truncated label has %d runes, want <= 28", n)
	}
}

// sampleExtremes is a high on Fri 4 Sep 23:00 UTC and a low on Sat 5 Sep
// 06:00 UTC, the two days apart so their links differ.
func sampleExtremes() *WeekExtremes {
	return &WeekExtremes{
		High: SentimentExtreme{
			Value: 29,
			At:    time.Date(2026, time.September, 4, 23, 0, 0, 0, time.UTC),
			Topic: "Charlie Kirk shooting",
		},
		Low: SentimentExtreme{
			Value: -8,
			At:    time.Date(2026, time.September, 5, 6, 0, 0, 0, time.UTC),
		},
	}
}

func twoTopics() []IdentifiedTopic {
	return []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Politics"}}, TopicID: "t1", Rank: 1, ExemplarHandle: "alice.bsky.social", ExemplarURI: "at://did:plc:abc/app.bsky.feed.post/123"},
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Weather"}}, TopicID: "t2", Rank: 2},
	}
}

// footerOf returns the two week-extreme lines: everything after the blank
// line that separates them from the topic list.
func footerOf(t *testing.T, text string) string {
	t.Helper()
	i := strings.LastIndex(text, "\n\n")
	if i < 0 {
		t.Fatalf("no blank line separating a footer in %q", text)
	}
	footer := text[i+2:]
	if !strings.HasPrefix(footer, "Week high") {
		t.Fatalf("last block is not the extremes footer: %q", footer)
	}
	return footer
}

// footerFacets returns the last two facets, which are the week high and low
// links when a footer is present.
func footerFacets(t *testing.T, facets []Facet) (Facet, Facet) {
	t.Helper()
	if len(facets) < 2 {
		t.Fatalf("want at least 2 facets, got %d", len(facets))
	}
	return facets[len(facets)-2], facets[len(facets)-1]
}

func TestFormatTrendingPost_ExtremesFooter(t *testing.T) {
	text, facets := FormatTrendingPost(twoTopics(), nil, 2, sampleExtremes())

	want := "Trending topic samples:\n\n" +
		"1. Politics @alice.bsky.social\n" +
		"2. Weather\n\n" +
		"Week high +29.0%, Fri 23:00 UTC · Charlie Kirk shooting · Sep 4\n" +
		"Week low -8.0%, Sat 06:00 UTC · Sep 5"
	if text != want {
		t.Errorf("text =\n%q\nwant\n%q", text, want)
	}

	if len(facets) != 3 {
		t.Fatalf("want 3 facets (exemplar + two days), got %d", len(facets))
	}
	// The exemplar facet must still point at the mention it did before.
	if got := text[facets[0].ByteStart:facets[0].ByteEnd]; got != "@alice.bsky.social" {
		t.Errorf("exemplar facet slices %q", got)
	}

	high, low := footerFacets(t, facets)
	for _, tc := range []struct {
		name  string
		facet Facet
		span  string
		uri   string
	}{
		{"high", high, "Sep 4", "https://en.wikipedia.org/wiki/Portal:Current_events/2026_September_4"},
		{"low", low, "Sep 5", "https://en.wikipedia.org/wiki/Portal:Current_events/2026_September_5"},
	} {
		if got := text[tc.facet.ByteStart:tc.facet.ByteEnd]; got != tc.span {
			t.Errorf("%s facet slices %q, want %q", tc.name, got, tc.span)
		}
		if tc.facet.Type != FacetLink {
			t.Errorf("%s facet type = %v, want FacetLink", tc.name, tc.facet.Type)
		}
		if tc.facet.Value != tc.uri {
			t.Errorf("%s facet URI = %q, want %q", tc.name, tc.facet.Value, tc.uri)
		}
	}
}

// Multibyte labels and handles sit before the footer, so its offsets have to
// be byte offsets rather than rune offsets.
func TestFormatTrendingPost_ExtremesFooterMultibyteOffsets(t *testing.T) {
	ranked := []IdentifiedTopic{
		{RankedTopic: RankedTopic{Cluster: TopicCluster{Label: "Καθημερινή ζωή — Café"}}, TopicID: "t1", Rank: 1, ExemplarHandle: "ünïcode.bsky.social", ExemplarURI: "at://did:plc:abc/app.bsky.feed.post/123"},
	}
	extremes := sampleExtremes()
	extremes.High.Topic = "Café façade ünïcode"
	extremes.Low.Topic = "ααα βββ γγγ"

	text, facets := FormatTrendingPost(ranked, nil, 2, extremes)

	if !strings.Contains(text, "Week high +29.0%, Fri 23:00 UTC · Café façade ünïcode · Sep 4") {
		t.Errorf("unexpected high line in %q", text)
	}
	if !strings.Contains(text, "Week low -8.0%, Sat 06:00 UTC · ααα βββ γγγ · Sep 5") {
		t.Errorf("unexpected low line in %q", text)
	}
	high, low := footerFacets(t, facets)
	if got := text[high.ByteStart:high.ByteEnd]; got != "Sep 4" {
		t.Errorf("high facet slices %q", got)
	}
	if got := text[low.ByteStart:low.ByteEnd]; got != "Sep 5" {
		t.Errorf("low facet slices %q", got)
	}
}

func TestFormatTrendingPost_ExtremesFooterOmitsMissingTopic(t *testing.T) {
	extremes := sampleExtremes()
	extremes.High.Topic = "   " // whitespace only counts as missing

	text, _ := FormatTrendingPost(twoTopics(), nil, 2, extremes)
	if !strings.Contains(text, "Week high +29.0%, Fri 23:00 UTC · Sep 4") {
		t.Errorf("missing topic should drop the segment: %q", text)
	}
	if !strings.Contains(text, "Week low -8.0%, Sat 06:00 UTC · Sep 5") {
		t.Errorf("unexpected low line in %q", text)
	}
}

// A nil extremes value must leave the post exactly as it was before the
// footer existed.
func TestFormatTrendingPost_NilExtremesUnchanged(t *testing.T) {
	text, facets := FormatTrendingPost(twoTopics(), nil, 2, nil)

	want := "Trending topic samples:\n\n" +
		"1. Politics @alice.bsky.social\n" +
		"2. Weather"
	if text != want {
		t.Errorf("text =\n%q\nwant\n%q", text, want)
	}
	if len(facets) != 1 {
		t.Errorf("want 1 facet (exemplar only), got %d", len(facets))
	}
}

// A long topic list shrinks the footer's labels before anything else gives.
func TestFormatTrendingPost_ShrinksFooterTopics(t *testing.T) {
	ranked := topicsWithLabels(
		strings.Repeat("Alpha", 6),
		strings.Repeat("Bravo", 6),
	)
	extremes := sampleExtremes()
	extremes.High.Topic = strings.Repeat("h", 28)
	extremes.Low.Topic = strings.Repeat("l", 28)

	text, facets := FormatTrendingPost(ranked, nil, 2, extremes)

	if got := utf8.RuneCountInString(text); got > maxGraphemes {
		t.Fatalf("text is %d runes, want <= %d: %q", got, maxGraphemes, text)
	}
	if !strings.Contains(text, "Sep 4") || !strings.Contains(text, "Sep 5") {
		t.Fatalf("footer was dropped, want it shrunk: %q", text)
	}
	// This body overflows at 28 runes and fits at the next step down, so both
	// labels must sit at exactly footerTopicMaxRunes-footerTopicStep. Pinning
	// the cut here means a change to the step size fails loudly.
	for _, want := range []string{
		"· " + strings.Repeat("h", 23) + "… · Sep 4",
		"· " + strings.Repeat("l", 23) + "… · Sep 5",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("want a label cut to 24 runes (%q) in: %q", want, text)
		}
	}
	// Exemplars survive: the footer gives before the topic list does.
	if !strings.Contains(text, "@someverylonghandlename.bsky.social") {
		t.Errorf("an exemplar was dropped before the footer shrank: %q", text)
	}
	high, low := footerFacets(t, facets)
	if got := text[high.ByteStart:high.ByteEnd]; got != "Sep 4" {
		t.Errorf("high facet slices %q", got)
	}
	if got := text[low.ByteStart:low.ByteEnd]; got != "Sep 5" {
		t.Errorf("low facet slices %q", got)
	}
}

// A longer list pushes past the label floor: the labels go but the hour and
// its link stay.
func TestFormatTrendingPost_DropsFooterTopicsKeepsLinks(t *testing.T) {
	ranked := topicsWithLabels(
		strings.Repeat("Alpha", 10),
		strings.Repeat("Bravo", 10),
	)
	extremes := sampleExtremes()
	// Greek letters so nothing in the footer's own wording can match them.
	extremes.High.Topic = strings.Repeat("ξ", 28)
	extremes.Low.Topic = strings.Repeat("ψ", 28)

	text, facets := FormatTrendingPost(ranked, nil, 2, extremes)

	if got := utf8.RuneCountInString(text); got > maxGraphemes {
		t.Fatalf("text is %d runes, want <= %d: %q", got, maxGraphemes, text)
	}
	if !strings.Contains(text, "Week high +29.0%, Fri 23:00 UTC · Sep 4") {
		t.Errorf("want the high line without its topic: %q", text)
	}
	if !strings.Contains(text, "Week low -8.0%, Sat 06:00 UTC · Sep 5") {
		t.Errorf("want the low line without its topic: %q", text)
	}
	if got := footerOf(t, text); strings.ContainsAny(got, "ξψ…") {
		t.Errorf("footer should carry no label at this size, got %q", got)
	}
	if !strings.Contains(text, "@someverylonghandlename.bsky.social") {
		t.Errorf("an exemplar was dropped before the footer topics: %q", text)
	}
	high, low := footerFacets(t, facets)
	if got := text[high.ByteStart:high.ByteEnd]; got != "Sep 4" {
		t.Errorf("high facet slices %q", got)
	}
	if got := text[low.ByteStart:low.ByteEnd]; got != "Sep 5" {
		t.Errorf("low facet slices %q", got)
	}
}

// Once the labels are at their floor, the labels go, then the whole footer —
// all before a topic is dropped.
func TestFormatTrendingPost_DropsFooterBeforeTopics(t *testing.T) {
	ranked := topicsWithLabels(
		strings.Repeat("Alpha", 12),
		strings.Repeat("Bravo", 12),
	)
	extremes := sampleExtremes()
	extremes.High.Topic = strings.Repeat("h", 28)
	extremes.Low.Topic = strings.Repeat("l", 28)

	text, facets := FormatTrendingPost(ranked, nil, 2, extremes)

	if got := utf8.RuneCountInString(text); got > maxGraphemes {
		t.Fatalf("text is %d runes, want <= %d: %q", got, maxGraphemes, text)
	}
	if strings.Contains(text, "Week high") || strings.Contains(text, "Week low") {
		t.Errorf("footer should be gone at this size: %q", text)
	}
	if !strings.Contains(text, "@someverylonghandlename.bsky.social") {
		t.Errorf("an exemplar was dropped before the footer: %q", text)
	}
	for _, f := range facets {
		if f.ByteEnd > len(text) {
			t.Errorf("facet %+v points past the %d byte text", f, len(text))
		}
	}
}

// With extremes present but no room for the footer, the existing
// exemplar/topic ladder still runs and every facet stays inside the final
// text.
func TestFormatTrendingPost_ExemplarLadderStillFitsWithExtremes(t *testing.T) {
	ranked := topicsWithLabels(
		strings.Repeat("Alpha", 12),
		strings.Repeat("Bravo", 12),
		strings.Repeat("Charlie", 12),
		strings.Repeat("Delta", 12),
	)
	extremes := sampleExtremes()

	text, facets := FormatTrendingPost(ranked, nil, 2, extremes)

	if got := utf8.RuneCountInString(text); got > maxGraphemes {
		t.Errorf("text is %d runes, want <= %d: %q", got, maxGraphemes, text)
	}
	for _, f := range facets {
		if f.ByteStart < 0 || f.ByteEnd > len(text) || f.ByteStart > f.ByteEnd {
			t.Errorf("facet %+v is out of range for a %d byte text", f, len(text))
		}
	}
}

// The last resort is a rune cut of a single oversize label. The footer is
// long gone by then, and the cut must not leave a facet pointing past the end
// of the text.
func TestFormatTrendingPost_RuneCutWithExtremes(t *testing.T) {
	ranked := topicsWithLabels(strings.Repeat("λ", 500))

	text, facets := FormatTrendingPost(ranked, nil, 2, sampleExtremes())

	if got := utf8.RuneCountInString(text); got != maxGraphemes {
		t.Errorf("text is %d runes, want exactly %d", got, maxGraphemes)
	}
	if strings.Contains(text, "Week high") || strings.Contains(text, "Week low") {
		t.Errorf("footer should be gone before the rune cut: %q", text)
	}
	for _, f := range facets {
		if f.ByteStart < 0 || f.ByteEnd > len(text) || f.ByteStart > f.ByteEnd {
			t.Errorf("facet %+v is out of range for a %d byte text", f, len(text))
		}
	}
}

// Timestamps in another zone are rendered and linked in UTC.
func TestFormatTrendingPost_ExtremesFooterUsesUTC(t *testing.T) {
	zone := time.FixedZone("UTC+11", 11*3600)
	extremes := sampleExtremes()
	extremes.High.At = extremes.High.At.In(zone) // Sat 5 Sep 10:00 local
	extremes.Low.At = extremes.Low.At.In(zone)

	text, facets := FormatTrendingPost(twoTopics(), nil, 2, extremes)

	if !strings.Contains(text, "Week high +29.0%, Fri 23:00 UTC · Charlie Kirk shooting · Sep 4") {
		t.Errorf("high line not rendered in UTC: %q", text)
	}
	high, _ := footerFacets(t, facets)
	if high.Value != "https://en.wikipedia.org/wiki/Portal:Current_events/2026_September_4" {
		t.Errorf("high link not resolved in UTC: %q", high.Value)
	}
}
