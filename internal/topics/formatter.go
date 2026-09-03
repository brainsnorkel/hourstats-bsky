package topics

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
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

func FormatTrendingPost(ranked []IdentifiedTopic, previous []IdentifiedTopic, analysisHours int) (string, []Facet) {
	showExemplar := make([]bool, len(ranked))
	for i := range ranked {
		showExemplar[i] = true
	}

	text := buildTrendingText(ranked, showExemplar, analysisHours)
	for len([]rune(text)) > maxGraphemes {
		dropped := false
		for i := len(ranked) - 1; i >= 0; i-- {
			if showExemplar[i] {
				showExemplar[i] = false
				dropped = true
				break
			}
		}
		if !dropped {
			break
		}
		text = buildTrendingText(ranked, showExemplar, analysisHours)
	}

	// Exemplars are exhausted and the text still doesn't fit. Drop trailing
	// (lowest-ranked) topics until it does, keeping at least the top topic so
	// the post still says something.
	for len([]rune(text)) > maxGraphemes && len(ranked) > 1 {
		ranked = ranked[:len(ranked)-1]
		showExemplar = showExemplar[:len(ranked)]
		text = buildTrendingText(ranked, showExemplar, analysisHours)
	}

	// Last resort: one topic whose label alone overflows. Cut on a rune
	// boundary — Bluesky rejects anything over 300 graphemes, and facets are
	// built from the final text so no offset can point past the end.
	if runes := []rune(text); len(runes) > maxGraphemes {
		text = string(runes[:maxGraphemes])
	}

	filtered := make([]IdentifiedTopic, len(ranked))
	for i, t := range ranked {
		filtered[i] = t
		if !showExemplar[i] {
			filtered[i].ExemplarHandle = ""
			filtered[i].ExemplarURI = ""
			filtered[i].Cluster.IsMeme = false
		}
	}

	facets := buildFacets(text, filtered)
	return text, facets
}

func buildTrendingText(ranked []IdentifiedTopic, showExemplar []bool, analysisHours int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Trending topics (%dh)\n\n", analysisHours)

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

	b.WriteString("\n#hstrend")
	return b.String()
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

	for _, tag := range []string{"#hstrend"} {
		idx := strings.LastIndex(text, tag)
		if idx < 0 {
			continue
		}
		facets = append(facets, Facet{
			ByteStart: idx,
			ByteEnd:   idx + len([]byte(tag)),
			Type:      FacetTag,
			Value:     tag[1:],
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
