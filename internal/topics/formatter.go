package topics

import (
	"fmt"
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

	filtered := make([]IdentifiedTopic, len(ranked))
	for i, t := range ranked {
		filtered[i] = t
		if !showExemplar[i] {
			filtered[i].ExemplarHandle = ""
			filtered[i].ExemplarURI = ""
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
		if showExemplar[i] && topic.ExemplarHandle != "" {
			line += fmt.Sprintf(" @%s", topic.ExemplarHandle)
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
		if topic.ExemplarHandle != "" {
			part += fmt.Sprintf(" (top post by @%s)", topic.ExemplarHandle)
		}
		parts = append(parts, part)
	}
	return "Topics: " + strings.Join(parts, ", ")
}
