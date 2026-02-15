package sparkline

import (
	"image/color"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func makeSnapshot(t string, rank int, topicID, label string) store.TopicSnapshotRow {
	return store.TopicSnapshotRow{
		SnapshotTime:      t,
		Rank:              rank,
		TopicID:           topicID,
		Label:             label,
		UniqueAuthorCount: 100,
		Keywords:          "[]",
	}
}

func TestGenerateTrendingChart_Basic(t *testing.T) {
	snapshots := []store.TopicSnapshotRow{
		makeSnapshot("2026-01-01T00:00:00Z", 1, "a", "Politics"),
		makeSnapshot("2026-01-01T00:00:00Z", 2, "b", "Weather"),
		makeSnapshot("2026-01-01T00:00:00Z", 3, "c", "Sports"),
		makeSnapshot("2026-01-01T06:00:00Z", 1, "a", "Politics"),
		makeSnapshot("2026-01-01T06:00:00Z", 2, "c", "Sports"),
		makeSnapshot("2026-01-01T06:00:00Z", 3, "b", "Weather"),
		makeSnapshot("2026-01-01T12:00:00Z", 1, "b", "Weather"),
		makeSnapshot("2026-01-01T12:00:00Z", 2, "a", "Politics"),
		makeSnapshot("2026-01-01T12:00:00Z", 3, "c", "Sports"),
		makeSnapshot("2026-01-01T18:00:00Z", 1, "a", "Politics"),
		makeSnapshot("2026-01-01T18:00:00Z", 2, "b", "Weather"),
		makeSnapshot("2026-01-01T18:00:00Z", 3, "c", "Sports"),
	}

	data, err := GenerateTrendingChart(snapshots)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data == nil || len(data) == 0 {
		t.Fatal("expected non-empty PNG data")
	}
	if data[0] != 0x89 || data[1] != 'P' || data[2] != 'N' || data[3] != 'G' {
		t.Error("output does not have PNG magic bytes")
	}
}

func TestGenerateTrendingChart_InsufficientData(t *testing.T) {
	snapshots := []store.TopicSnapshotRow{
		makeSnapshot("2026-01-01T00:00:00Z", 1, "a", "Politics"),
		makeSnapshot("2026-01-01T00:00:00Z", 2, "b", "Weather"),
	}

	data, err := GenerateTrendingChart(snapshots)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != nil {
		t.Error("expected nil for insufficient data")
	}
}

func TestGenerateTrendingChart_Empty(t *testing.T) {
	data, err := GenerateTrendingChart(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != nil {
		t.Error("expected nil for empty snapshots")
	}
}

func TestGenerateTrendingChart_SingleTopic(t *testing.T) {
	snapshots := []store.TopicSnapshotRow{
		makeSnapshot("2026-01-01T00:00:00Z", 1, "a", "Politics"),
		makeSnapshot("2026-01-01T06:00:00Z", 2, "a", "Politics"),
		makeSnapshot("2026-01-01T12:00:00Z", 1, "a", "Politics"),
	}

	data, err := GenerateTrendingChart(snapshots)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data == nil || len(data) == 0 {
		t.Fatal("expected non-empty PNG data")
	}
}

func TestAssignTopicColor_Stability(t *testing.T) {
	colorMap := make(map[string]color.RGBA)
	idx := 0

	c1 := assignColor("topic-a", colorMap, &idx)
	c2 := assignColor("topic-a", colorMap, &idx)
	if c1 != c2 {
		t.Errorf("expected same color for same topic, got %v and %v", c1, c2)
	}
}

func TestAssignTopicColor_Overflow(t *testing.T) {
	colorMap := make(map[string]color.RGBA)
	idx := 0

	for i := 0; i < len(trendingColors)+3; i++ {
		c := assignColor(string(rune('A'+i)), colorMap, &idx)
		if c == (color.RGBA{}) {
			t.Errorf("got zero color for topic %d", i)
		}
	}
}

func assignColor(topicID string, colorMap map[string]color.RGBA, idx *int) color.RGBA {
	if c, ok := colorMap[topicID]; ok {
		return c
	}
	c := trendingColors[*idx%len(trendingColors)]
	colorMap[topicID] = c
	*idx++
	return c
}
