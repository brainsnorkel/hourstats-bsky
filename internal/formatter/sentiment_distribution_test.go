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
// It fails if the vocabulary collapses the way the Jan 2026 tiers did on
// hourly data (one word carrying 8.5% of posts, 23 words never used).
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
		wordCounts[getMoodWord100(v)]++
		tierCounts[determineTier(v)]++
		total++
	}
	if total == 0 {
		t.Fatal("no rows matched")
	}

	t.Logf("cycles=%d", total)
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
	t.Logf("distinct words used: %d/100", used)

	const minDistinct, maxTopShare = 80, 0.05
	if used < minDistinct {
		t.Errorf("only %d distinct words used; want at least %d", used, minDistinct)
	}
	if top := float64(ranked[0].n) / float64(total); top > maxTopShare {
		t.Errorf("%q carries %.1f%% of cycles; want at most %.0f%%", ranked[0].word, 100*top, 100*maxTopShare)
	}
}
