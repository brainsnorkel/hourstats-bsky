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

func FormatTrendingPost(ranked []IdentifiedTopic, previous []IdentifiedTopic) (string, []Facet) {
	prevRank := make(map[string]int)
	for _, p := range previous {
		prevRank[p.TopicID] = p.Rank
	}

	showExemplar := make([]bool, len(ranked))
	for i := range ranked {
		showExemplar[i] = true
	}

	text := buildTrendingText(ranked, prevRank, showExemplar)
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
		text = buildTrendingText(ranked, prevRank, showExemplar)
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

func buildTrendingText(ranked []IdentifiedTopic, prevRank map[string]int, showExemplar []bool) string {
	var b strings.Builder
	b.WriteString("Bluesky trending topics\n\n")

	for i, topic := range ranked {
		movement := movementIndicator(topic.TopicID, topic.Rank, prevRank)
		line := fmt.Sprintf("#%d %s %s", topic.Rank, topic.Cluster.Label, movement)
		if showExemplar[i] && topic.ExemplarHandle != "" {
			line += fmt.Sprintf(" @%s", topic.ExemplarHandle)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n#trending #hourstatstrend")
	return b.String()
}

func movementIndicator(topicID string, rank int, prevRank map[string]int) string {
	prev, existed := prevRank[topicID]
	if !existed {
		return "(NEW)"
	}
	diff := prev - rank
	if diff > 0 {
		return fmt.Sprintf("(+%d)", diff)
	}
	if diff < 0 {
		return fmt.Sprintf("(-%d)", -diff)
	}
	return "(->)"
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

	for _, tag := range []string{"#trending", "#hourstatstrend"} {
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
		part := fmt.Sprintf("#%d %s", topic.Rank, topic.Cluster.Label)
		if topic.ExemplarHandle != "" {
			part += fmt.Sprintf(" (top post by @%s)", topic.ExemplarHandle)
		}
		parts = append(parts, part)
	}
	return "Bluesky trending topics: " + strings.Join(parts, ", ")
}
