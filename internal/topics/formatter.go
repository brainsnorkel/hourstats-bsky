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

func FormatTrendingPost(ranked []IdentifiedTopic, previous []IdentifiedTopic) (string, []Facet) {
	prevRank := make(map[string]int)
	for _, p := range previous {
		prevRank[p.TopicID] = p.Rank
	}

	var b strings.Builder
	b.WriteString("Bluesky trending topics\n\n")

	for _, topic := range ranked {
		movement := movementIndicator(topic.TopicID, topic.Rank, prevRank)
		line := fmt.Sprintf("#%d %s %s", topic.Rank, topic.Cluster.Label, movement)
		if topic.ExemplarHandle != "" {
			line += fmt.Sprintf(" @%s", topic.ExemplarHandle)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n#trending #hourstatstrend")

	text := b.String()
	facets := buildFacets(text, ranked)
	return text, facets
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
	textBytes := []byte(text)
	var facets []Facet

	for _, topic := range ranked {
		if topic.ExemplarHandle == "" || topic.ExemplarURI == "" {
			continue
		}
		mention := "@" + topic.ExemplarHandle
		idx := strings.Index(text, mention)
		if idx < 0 {
			continue
		}
		byteStart := len(textBytes[:idx])
		_ = byteStart
		byteStart = idx
		byteEnd := byteStart + len([]byte(mention))

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
