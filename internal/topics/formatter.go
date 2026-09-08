package topics

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/christophergentle/hourstats-bsky/internal/formatter"
)

type FacetType int

const (
	FacetTag FacetType = iota
	FacetLink
)

type Facet struct {
	ByteStart int
	ByteEnd   int
	Type      FacetType
	Value     string // tag text (without #) or URL
}

const maxGraphemes = 300

// maxPostedTopics caps how many ranked topics appear in the trending post.
// Ranking and identity tracking still keep TopTopics entries; only the
// published list is shortened.
const maxPostedTopics = 3

// Rune caps on the topic named beside each weekly extreme in the footer.
// footerNoTopics keeps the footer but drops both labels; footerDropped drops
// the footer outright.
const (
	footerTopicMaxRunes = 28
	footerTopicMinRunes = 12
	footerTopicStep     = 4
	footerNoTopics      = 0
	footerDropped       = -1
)

// FormatTrendingPost renders the trending reply: the ranked topic list and,
// when extremes is non-nil, a footer naming the week's highest and lowest
// hour and each hour's top topic when one is known.
//
// Everything must fit inside maxGraphemes. The footer is the cheapest thing
// to give up, so it shrinks and then disappears before any topic content is
// touched.
func FormatTrendingPost(ranked []IdentifiedTopic, previous []IdentifiedTopic, analysisHours int, extremes *WeekExtremes) (string, []Facet) {
	if len(ranked) > maxPostedTopics {
		ranked = ranked[:maxPostedTopics]
	}
	showExemplar := make([]bool, len(ranked))
	for i := range ranked {
		showExemplar[i] = true
	}

	footerTopicCap := footerDropped
	if extremes != nil {
		footerTopicCap = footerTopicMaxRunes
	}
	build := func() (string, []Facet) {
		return buildPost(ranked, showExemplar, analysisHours, extremes, footerTopicCap)
	}
	overflows := func(text string) bool { return utf8.RuneCountInString(text) > maxGraphemes }

	text, facets := build()

	// Shrink the footer's topic labels, then drop them, then drop the footer.
	for overflows(text) && footerTopicCap != footerDropped {
		switch {
		case footerTopicCap-footerTopicStep >= footerTopicMinRunes:
			footerTopicCap -= footerTopicStep
		case footerTopicCap > footerNoTopics:
			footerTopicCap = footerNoTopics
		default:
			footerTopicCap = footerDropped
		}
		text, facets = build()
	}

	// A topic listed without its exemplar link says little, so when the list
	// itself has to shrink, trailing topics go whole rather than losing their
	// links. Only the top topic is kept at any cost.
	for overflows(text) && len(ranked) > 1 {
		ranked = ranked[:len(ranked)-1]
		showExemplar = showExemplar[:len(ranked)]
		text, facets = build()
	}

	// One topic left: give up its exemplar before cutting into its label.
	if overflows(text) && len(showExemplar) > 0 && showExemplar[0] {
		showExemplar[0] = false
		text, facets = build()
	}

	// Last resort: one topic whose label alone overflows. Cut on a rune
	// boundary — Bluesky rejects anything over 300 graphemes, and facets are
	// rebuilt from the final text so no offset can point past the end.
	if runes := []rune(text); len(runes) > maxGraphemes {
		text = string(runes[:maxGraphemes])
		facets = buildFacets(text, visibleTopics(ranked, showExemplar))
	}

	return text, facets
}

// buildPost renders the topic list and, when asked for, the extremes footer,
// returning the text with facets whose offsets are relative to it.
func buildPost(ranked []IdentifiedTopic, showExemplar []bool, analysisHours int, extremes *WeekExtremes, footerTopicCap int) (string, []Facet) {
	text := buildTrendingText(ranked, showExemplar, analysisHours)
	facets := buildFacets(text, visibleTopics(ranked, showExemplar))
	if extremes == nil || footerTopicCap == footerDropped {
		return text, facets
	}

	// The footer is appended after the topic list and carries no facets, so
	// the exemplar facets above keep their offsets.
	text += "\n\n"
	text = appendExtremeLine(text, "Week high", extremes.High, footerTopicCap)
	text += "\n"
	text = appendExtremeLine(text, "Week low", extremes.Low, footerTopicCap)
	return text, facets
}

// appendExtremeLine writes one footer line: the prefix, the signed
// percentage, the weekday and UTC time, and, when known, "Top topic:" with
// the hour's rank-1 label. The calendar date is deliberately left out; the
// weekday is enough inside a seven-day window and the extra date link made
// the line untidy.
func appendExtremeLine(text, prefix string, e SentimentExtreme, topicCap int) string {
	at := e.At.UTC()
	text += fmt.Sprintf("%s %s, %s UTC", prefix, formatter.SignedPercent(e.Value), at.Format("Mon 15:04"))
	if topic := truncateLabel(e.Topic, topicCap); topic != "" {
		text += " · Top topic: " + topic
	}
	return text
}

// truncateLabel collapses whitespace and shortens a label to maxRunes,
// ending it with an ellipsis when it was cut. A cap of zero or less drops the
// label entirely.
func truncateLabel(label string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	label = strings.Join(strings.Fields(label), " ")
	r := []rune(label)
	if len(r) <= maxRunes {
		return label
	}
	return strings.TrimRight(string(r[:maxRunes-1]), " ") + "…"
}

// visibleTopics blanks the exemplar of any topic whose exemplar was dropped,
// so buildFacets only links what the text actually shows.
func visibleTopics(ranked []IdentifiedTopic, showExemplar []bool) []IdentifiedTopic {
	visible := make([]IdentifiedTopic, len(ranked))
	for i, t := range ranked {
		visible[i] = t
		if !showExemplar[i] {
			visible[i].ExemplarHandle = ""
			visible[i].ExemplarURI = ""
			visible[i].Cluster.IsMeme = false
		}
	}
	return visible
}

func buildTrendingText(ranked []IdentifiedTopic, showExemplar []bool, analysisHours int) string {
	var b strings.Builder
	b.WriteString("Trending topic samples:\n\n")

	for i, topic := range ranked {
		line := fmt.Sprintf("%d. %s", topic.Rank, topic.Cluster.Label)
		if showExemplar[i] {
			if topic.Cluster.IsMeme {
				line += " 🔍"
			} else if topic.ExemplarHandle != "" {
				line += fmt.Sprintf(" @%s", topic.ExemplarHandle)
			}
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func buildFacets(text string, ranked []IdentifiedTopic) []Facet {
	var facets []Facet

	searchFrom := 0
	for _, topic := range ranked {
		if topic.Cluster.IsMeme {
			const searchIcon = "🔍"
			idx := strings.Index(text[searchFrom:], searchIcon)
			if idx < 0 {
				continue
			}
			byteStart := searchFrom + idx
			byteEnd := byteStart + len([]byte(searchIcon))
			searchFrom = byteEnd

			searchURL := "https://bsky.app/search?q=" + url.QueryEscape(memeSearchQuery(topic.Cluster.Keywords))
			facets = append(facets, Facet{
				ByteStart: byteStart,
				ByteEnd:   byteEnd,
				Type:      FacetLink,
				Value:     searchURL,
			})
			continue
		}
		if topic.ExemplarHandle == "" || topic.ExemplarURI == "" {
			continue
		}
		mention := "@" + topic.ExemplarHandle
		idx := strings.Index(text[searchFrom:], mention)
		if idx < 0 {
			continue
		}
		byteStart := searchFrom + idx
		byteEnd := byteStart + len([]byte(mention))
		searchFrom = byteEnd

		webURL := convertExemplarURI(topic.ExemplarURI)
		facets = append(facets, Facet{
			ByteStart: byteStart,
			ByteEnd:   byteEnd,
			Type:      FacetLink,
			Value:     webURL,
		})
	}

	return facets
}

func memeSearchQuery(keywords []string) string {
	if len(keywords) == 0 {
		return ""
	}

	var compounds []string
	for _, kw := range keywords {
		if strings.Contains(kw, "_") {
			compounds = append(compounds, kw)
		}
	}

	if len(compounds) > 0 {
		sort.Slice(compounds, func(i, j int) bool {
			return len(compounds[i]) > len(compounds[j])
		})
		return strings.ReplaceAll(compounds[0], "_", " ")
	}

	if len(keywords) > 3 {
		keywords = keywords[:3]
	}
	return strings.Join(keywords, " ")
}

func convertExemplarURI(uri string) string {
	if !strings.HasPrefix(uri, "at://") {
		return uri
	}
	trimmed := strings.TrimPrefix(uri, "at://")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 3 && parts[1] == "app.bsky.feed.post" {
		return fmt.Sprintf("https://bsky.app/profile/%s/post/%s", parts[0], parts[2])
	}
	return uri
}

func FormatAltText(ranked []IdentifiedTopic) string {
	var parts []string
	for _, topic := range ranked {
		part := fmt.Sprintf("%d. %s", topic.Rank, topic.Cluster.Label)
		if topic.Cluster.IsMeme {
			part += " (search)"
		} else if topic.ExemplarHandle != "" {
			part += fmt.Sprintf(" (top post by @%s)", topic.ExemplarHandle)
		}
		parts = append(parts, part)
	}
	return "Topics: " + strings.Join(parts, ", ")
}
