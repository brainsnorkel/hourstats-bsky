package formatter

import (
	"encoding/csv"
	"os"
	"sort"
	"strconv"
	"testing"
)

// TestMoodWordDistribution replays historical per-cycle sentiment through
// getMoodWord100 and logs tier shares and word frequencies. It is a
// calibration aid that only runs when pointed at a CSV export of
// sentiment_history (columns: timestamp, net_sentiment_percent). go test runs
// with the package directory as cwd, so pass an absolute path:
//
//	HS_HOURLY_CSV=$PWD/analysis/hourly_sentiment_2026.csv HS_SINCE=2026-03-01 \
//	  go test ./internal/formatter -run TestMoodWordDistribution -v
//
// HS_SHIFT adds a constant to every net value read from the CSV, so an export
// of the pre-2026-09-11 (stock-scored) series can be replayed on the realigned
// scale the thresholds are now calibrated for:
//
//	HS_HOURLY_CSV=... HS_SHIFT=1.77 \
//	  go test ./internal/formatter -run TestMoodWordDistribution -v
//
// It fails if the vocabulary collapses: more than 4 of the 22 words never
// used, or one word carrying more than a quarter of cycles.
func TestMoodWordDistribution(t *testing.T) {
	path := os.Getenv("HS_HOURLY_CSV")
	if path == "" {
		t.Skip("HS_HOURLY_CSV not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) < 2 {
		t.Fatalf("%s: no data rows", path)
	}
	since := os.Getenv("HS_SINCE")
	var shift float64
	if raw := os.Getenv("HS_SHIFT"); raw != "" {
		var err error
		shift, err = strconv.ParseFloat(raw, 64)
		if err != nil {
			t.Fatalf("HS_SHIFT=%q: %v", raw, err)
		}
	}

	wordCounts := map[string]int{}
	tierCounts := map[int]int{}
	total := 0
	for _, r := range recs[1:] {
		if len(r) < 2 {
			t.Fatalf("row %q: need timestamp and net_sentiment_percent columns", r)
		}
		if since != "" && r[0] < since {
			continue
		}
		v, err := strconv.ParseFloat(r[1], 64)
		if err != nil {
			t.Fatalf("row %q: %v", r, err)
		}
		v += shift
		wordCounts[getMoodWord100(v)]++
		tierCounts[determineTier(v)]++
		total++
	}
	if total == 0 {
		t.Fatal("no rows matched")
	}

	t.Logf("cycles=%d shift=%+.2f", total, shift)
	for tier := 1; tier <= 7; tier++ {
		t.Logf("tier %d: %5d %5.1f%%", tier, tierCounts[tier], 100*float64(tierCounts[tier])/float64(total))
	}

	type wc struct {
		word string
		n    int
	}
	ranked := make([]wc, 0, len(calibratedWords))
	for _, w := range calibratedWords {
		ranked = append(ranked, wc{w, wordCounts[w]})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].n > ranked[j].n })
	used := 0
	for _, e := range ranked {
		if e.n > 0 {
			used++
		}
		t.Logf("%-14s %5d %5.1f%%", e.word, e.n, 100*float64(e.n)/float64(total))
	}
	t.Logf("distinct words used: %d/%d", used, len(calibratedWords))

	// 22 words since 2026-09-28: tier 1 (2 words) and the top of tier 7
	// (jubilant, euphoric) have never been reached by an hourly cycle, and a
	// typical-tier word spans ~0.44 points around a median hour, so it can
	// carry a fifth of cycles by design.
	minDistinct, maxTopShare := len(calibratedWords)-4, 0.25
	if used < minDistinct {
		t.Errorf("only %d distinct words used; want at least %d", used, minDistinct)
	}
	if top := float64(ranked[0].n) / float64(total); top > maxTopShare {
		t.Errorf("%q carries %.1f%% of cycles; want at most %.0f%%", ranked[0].word, 100*top, 100*maxTopShare)
	}
}
