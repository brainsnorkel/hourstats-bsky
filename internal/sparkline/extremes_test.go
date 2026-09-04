package sparkline

import (
	"os"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func TestTruncateNote(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"  Charlie   Kirk\nshooting ":   "Charlie Kirk shooting",
		"abcdefghijklmnopqrstuvwxyz12":  "abcdefghijklmnopqrstuvwxyz12",
		"abcdefghijklmnopqrstuvwxyz123": "abcdefghijklmnopqrstuvwxyz1…",
		"ααααααααααααααααααααααααααααα": "ααααααααααααααααααααααααααα…",
	}
	for in, want := range cases {
		if got := TruncateNote(in); got != want {
			t.Errorf("TruncateNote(%q) = %q, want %q", in, got, want)
		}
	}
	if n := len([]rune(TruncateNote("abcdefghijklmnopqrstuvwxyz123"))); n > MaxNoteRunes {
		t.Errorf("truncated note has %d runes, want <= %d", n, MaxNoteRunes)
	}
}

// TestSparklineTopTopicNotes renders a week where the low sits early, the
// high is the latest point, and both carry topic notes. Set SPARKLINE_OUT
// to a path to keep the PNG for inspection.
func TestSparklineTopTopicNotes(t *testing.T) {
	start := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	var pts []state.SentimentDataPoint
	for i := 0; i < 168; i++ {
		v := 10 + 3*float64(i%7) - 1.5*float64(i%5)
		if i == 30 {
			v = -8
		}
		if i == 167 {
			v = 29
		}
		p := state.SentimentDataPoint{Timestamp: start.Add(time.Duration(i) * time.Hour), NetSentimentPercent: v, TotalPosts: 1000}
		switch i {
		case 30:
			p.TopTopic = "An extremely long trending topic label that gets cut"
		case 167:
			p.TopTopic = "Charlie Kirk shooting"
		}
		pts = append(pts, p)
	}
	img, err := NewSparklineGenerator(nil).GenerateSentimentSparkline(pts)
	if err != nil || len(img) == 0 {
		t.Fatalf("generate: %d bytes, %v", len(img), err)
	}
	if out := os.Getenv("SPARKLINE_OUT"); out != "" {
		if err := os.WriteFile(out, img, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A note on a non-extreme point must not change the output.
	base := append([]state.SentimentDataPoint(nil), pts...)
	pts[50].TopTopic = "Ignored note"
	again, err := NewSparklineGenerator(nil).GenerateSentimentSparkline(pts)
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	ref, _ := NewSparklineGenerator(nil).GenerateSentimentSparkline(base)
	if string(again) != string(ref) {
		t.Error("a note on a non-extreme point changed the rendered chart")
	}
}

func TestCaptionStack(t *testing.T) {
	const top, bottom, lh, gap, lineGap = 100.0, 500.0, 18.0, 12.0, 5.0
	cases := []struct {
		name      string
		y         float64
		n         int
		above     bool
		wantAbove bool
		wantYs    []float64
	}{
		{"above with room", 300, 2, true, true, []float64{288, 265}},
		{"below with room", 300, 2, false, false, []float64{312, 335}},
		{"single line above", 300, 1, true, true, []float64{288}},
		{"two lines near top flip below", 140, 2, true, false, []float64{152, 175}},
		{"one line near top still fits", 140, 1, true, true, []float64{128}},
		{"two lines near bottom flip above", 460, 2, false, true, []float64{448, 425}},
		{"no lines", 300, 0, true, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			above, ys := captionStack(tc.y, top, bottom, lh, gap, lineGap, tc.n, tc.above)
			if above != tc.wantAbove {
				t.Errorf("above = %v, want %v", above, tc.wantAbove)
			}
			if len(ys) != len(tc.wantYs) {
				t.Fatalf("ys = %v, want %v", ys, tc.wantYs)
			}
			for i := range ys {
				if ys[i] != tc.wantYs[i] {
					t.Errorf("ys[%d] = %v, want %v", i, ys[i], tc.wantYs[i])
				}
			}
		})
	}
}

// TestFitRangePadWithNotes checks that charts whose extremes carry notes get
// the wider margin, so a two-line caption fits without flipping.
func TestFitRangePadWithNotes(t *testing.T) {
	vals := []float64{0, 50}
	plain := fitRange(vals, 0.75, rangePadFraction)
	noted := fitRange(vals, 0.75, rangePadFractionNotes)
	if noted.Max-noted.Min < plain.Max-plain.Min {
		t.Errorf("noted range %+v narrower than plain %+v", noted, plain)
	}
	pts := []seriesPoint{{V: 0}, {V: 50, Note: "x"}}
	if got := plotPadFraction(seriesChartSpec{Points: pts, MarkExtremes: true}); got != rangePadFractionNotes {
		t.Errorf("plotPadFraction with notes = %v", got)
	}
	if got := plotPadFraction(seriesChartSpec{Points: pts}); got != rangePadFraction {
		t.Errorf("plotPadFraction without MarkExtremes = %v", got)
	}
	if got := plotPadFraction(seriesChartSpec{Points: []seriesPoint{{V: 0}, {V: 1, Note: "  "}}, MarkExtremes: true}); got != rangePadFraction {
		t.Errorf("plotPadFraction with blank note = %v", got)
	}
}
