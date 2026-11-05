package sparkline

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// volumeMonth builds a month of ENPosts with the given run counts (24 when
// runs is nil). Day i has 1_800_000 + i*1_000 English posts, so the first
// day is the quietest and the last the busiest unless the test says otherwise.
func volumeMonth(n int, runs []int) []DailyVolumePoint {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	days := make([]DailyVolumePoint, n)
	for i := range days {
		r := 24
		if runs != nil {
			r = runs[i]
		}
		days[i] = DailyVolumePoint{Date: start.AddDate(0, 0, i), ENPosts: 1_800_000 + i*1_000, Runs: r}
	}
	return days
}

func TestVolumeExtremes(t *testing.T) {
	// (1) The lowest-volume day ran 23 cycles; the second-lowest, a full
	// 24-cycle day, is the quietest. The busiest is unaffected.
	days := volumeMonth(31, nil)
	days[16].ENPosts, days[16].Runs = 1_615_416, 23 // Oct 17, killed by the OOM killer
	days[2].ENPosts = 1_632_473                     // Oct 3, the quietest full day
	hi, lo := VolumeExtremes(days)
	if lo != 2 || hi != 30 {
		t.Errorf("partial quietest: busiest=%d quietest=%d, want 30 and 2", hi, lo)
	}
	if note := PartialDaysNote(days); note != "Busiest and quietest exclude 1 partial day with fewer cycles" {
		t.Errorf("note = %q", note)
	}

	// (2) Every day at 23 cycles is a month of full days: same as before.
	same := make([]int, 31)
	for i := range same {
		same[i] = 23
	}
	days = volumeMonth(31, same)
	days[16].ENPosts = 1_615_416
	if hi, lo := VolumeExtremes(days); lo != 16 || hi != 30 {
		t.Errorf("all 23 runs: busiest=%d quietest=%d, want 30 and 16", hi, lo)
	}
	if note := PartialDaysNote(days); note != "" {
		t.Errorf("all-full note = %q, want none", note)
	}

	// (3) One 48-run outlier leaves fewer than two full days: every day is
	// a candidate again, and no exclusion is claimed.
	days = volumeMonth(31, nil)
	days[9].Runs = 48
	days[16].ENPosts = 1_615_416
	if hi, lo := VolumeExtremes(days); lo != 16 || hi != 30 {
		t.Errorf("outlier: busiest=%d quietest=%d, want 30 and 16", hi, lo)
	}
	if note := PartialDaysNote(days); note != "" {
		t.Errorf("outlier note = %q, want none", note)
	}

	// (4) No run counts at all: every day.
	zero := make([]int, 31)
	days = volumeMonth(31, zero)
	days[16].ENPosts = 1_615_416
	if hi, lo := VolumeExtremes(days); lo != 16 || hi != 30 {
		t.Errorf("zero runs: busiest=%d quietest=%d, want 30 and 16", hi, lo)
	}
	if note := PartialDaysNote(days); note != "" {
		t.Errorf("zero-runs note = %q, want none", note)
	}

	// Ties go to the earlier day, among full days only.
	days = volumeMonth(5, []int{24, 23, 24, 24, 24})
	for i := range days {
		days[i].ENPosts = 1_000
	}
	days[1].ENPosts = 1
	if hi, lo := VolumeExtremes(days); hi != 0 || lo != 0 {
		t.Errorf("ties: busiest=%d quietest=%d, want 0 and 0", hi, lo)
	}
	days = volumeMonth(31, nil)
	days[3].Runs, days[7].Runs = 22, 23
	if note := PartialDaysNote(days); note != "Busiest and quietest exclude 2 partial days with fewer cycles" {
		t.Errorf("two-day note = %q", note)
	}
	if hi, lo := VolumeExtremes(nil); hi != 0 || lo != 0 {
		t.Errorf("empty: %d %d", hi, lo)
	}
}

// shareMonth is a stacked month whose language split sums to 4% more than
// the daily firehose total, as happens when a daily row dropped a cycle that
// language_daily kept.
func shareMonth() []DailyVolumePoint {
	days := volumeMonth(31, nil)
	for i := range days {
		days[i].ENPosts = 1_950_000
		days[i].TotalPosts = 3_000_000
		days[i].Languages = map[string]int{"en": 2_000_000, "pt": 600_000, "ja": 400_000, "und": 120_000}
	}
	return days
}

// headline measures the month and builds its header the way the generator
// does, so the tests exercise the same two steps in the same order.
func headline(days []DailyVolumePoint, meta MonthlyVolumeMeta, stacked bool) (string, []statTile) {
	return volumeHeadline(days, meta, stacked, measureVolume(days, stacked))
}

// TestSignedPct pins the header's delta formatting, which the post text
// mirrors through formatter.SignedPercent: a plus sign for a rise, a bare
// minus for a fall, and "0.0%" for anything that rounds to zero.
func TestSignedPct(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		want string
	}{
		{0, "0.0%"}, {0.04, "0.0%"}, {-0.04, "0.0%"}, {0.05, "+0.1%"},
		{0.51, "+0.5%"}, {-0.4, "-0.4%"}, {-0.05, "-0.1%"}, {12.345, "+12.3%"},
	} {
		if got := signedPct(tc.v); got != tc.want {
			t.Errorf("signedPct(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestVolumeHeadline(t *testing.T) {
	days := volumeMonth(31, nil)

	heroSub, tiles := headline(days, MonthlyVolumeMeta{PrevDeltaPct: 0.51, HasPrevDelta: true, PrevLabel: "September"}, false)
	if heroSub != "+0.5% vs September" {
		t.Errorf("delta heroSub = %q", heroSub)
	}
	if len(tiles) != 3 || tiles[0].Label != "Per day" || tiles[1].Label != "Busiest" || tiles[2].Label != "Quietest" {
		t.Errorf("tiles without a firehose = %+v", tiles)
	}
	if tiles[1].Sub != "Sat 31 Oct" || tiles[2].Sub != "Thu 1 Oct" {
		t.Errorf("extreme tiles = %+v %+v", tiles[1], tiles[2])
	}
	if heroSub, _ := headline(days, MonthlyVolumeMeta{HasPrevDelta: true, PrevLabel: "September"}, false); heroSub != "0.0% vs September" {
		t.Errorf("zero-delta heroSub = %q", heroSub)
	}

	// Without a comparable previous month the caption is the day form,
	// whatever PrevDeltaPct holds.
	heroSub, _ = headline(days, MonthlyVolumeMeta{PrevDeltaPct: 0.51, PrevLabel: "September"}, false)
	if heroSub != "31 days · 1.8M per day" {
		t.Errorf("day-form heroSub = %q", heroSub)
	}
	heroSub, _ = headline(days, MonthlyVolumeMeta{PrevDeltaPct: -2, HasPrevDelta: true}, false)
	if heroSub != "31 days · 1.8M per day" {
		t.Errorf("heroSub without a previous label = %q", heroSub)
	}

	// The tiles name whichever days the shared figures hold, which are the
	// days the markers are drawn on: a partial quietest day moves both.
	days[0].Runs = 23
	fig := measureVolume(days, false)
	if fig.quietest != 1 || fig.note != "Busiest and quietest exclude 1 partial day with fewer cycles" {
		t.Fatalf("figures with a partial first day = %+v", fig)
	}
	_, tiles = volumeHeadline(days, MonthlyVolumeMeta{}, false, fig)
	if tiles[2].Sub != days[fig.quietest].Date.Format("Mon 2 Jan") || tiles[2].Sub != "Fri 2 Oct" {
		t.Errorf("quietest tile = %+v, want the day at fig.quietest (%d)", tiles[2], fig.quietest)
	}
	if tiles[1].Sub != days[fig.busiest].Date.Format("Mon 2 Jan") {
		t.Errorf("busiest tile = %+v, want the day at fig.busiest (%d)", tiles[1], fig.busiest)
	}

	// Stacked: the share tile divides by the firehose figure the post
	// quotes, the legend by the stack it draws.
	days = shareMonth()
	totals := sumVolumes(days, true)
	if totals.all != 31*3_120_000 || totals.tileAll != 31*3_000_000 || !totals.hasTotal {
		t.Fatalf("totals = %+v", totals)
	}
	_, tiles = headline(days, MonthlyVolumeMeta{}, true)
	if len(tiles) != 4 {
		t.Fatalf("stacked tiles = %+v", tiles)
	}
	share := tiles[3]
	if share.Label != "Analysed share" || share.Value != "65%" || share.Sub != "of "+countText(float64(31*3_000_000)) {
		t.Errorf("share tile = %+v", share)
	}
	en := LanguageBreakdown(days)[0]
	if en.Code != "en" {
		t.Fatalf("first series = %s", en.Code)
	}
	if legend := fmt.Sprintf("%.0f%%", float64(en.Total)/float64(totals.all)*100); legend != "64%" {
		t.Errorf("legend English share = %s, want 64%% of the stack sum", legend)
	}

	// One day without a firehose figure: the tile falls back to the stack.
	days[5].TotalPosts = 0
	_, tiles = headline(days, MonthlyVolumeMeta{}, true)
	if tiles[3].Sub != "of "+countText(float64(31*3_120_000)) || tiles[3].Value != "62%" {
		t.Errorf("fallback share tile = %+v", tiles[3])
	}

	// The line view labels the tile English share of the firehose.
	days = shareMonth()
	_, tiles = headline(days, MonthlyVolumeMeta{}, false)
	if tiles[3].Label != "English share" || tiles[3].Sub != "of 93.0M" {
		t.Errorf("line-view share tile = %+v", tiles[3])
	}
	if heroSub, tiles := headline(nil, MonthlyVolumeMeta{}, false); heroSub != "" || tiles != nil {
		t.Errorf("empty headline = %q %+v", heroSub, tiles)
	}
}

// TestMonthlyVolumeChart_PartialDays renders a stacked month with two
// 23-cycle days, so the footer carries the partial-day footnote; set
// VOLUME_PARTIAL_OUT to keep the PNG. The line view renders the same month.
func TestMonthlyVolumeChart_PartialDays(t *testing.T) {
	days := synthLanguageMonth()
	for i := range days {
		days[i].Runs = 24
	}
	days[13].Runs, days[16].Runs = 23, 23
	days[13].ENPosts = 1_500_000 // quieter than any full day, yet not the quietest
	if note := PartialDaysNote(days); note != "Busiest and quietest exclude 2 partial days with fewer cycles" {
		t.Fatalf("note = %q", note)
	}
	if _, lo := VolumeExtremes(days); lo == 13 || lo == 16 {
		t.Fatalf("quietest = %d, a partial day", lo)
	}

	gen := NewMonthlyVolumeGenerator()
	png, err := gen.GenerateMonthlyVolumeChart(days, MonthlyVolumeMeta{MonthLabel: "August 2026", PrevDeltaPct: 0.51, HasPrevDelta: true, PrevLabel: "July"})
	if err != nil || len(png) == 0 {
		t.Fatalf("stacked with partial days: %d bytes, %v", len(png), err)
	}
	if out := os.Getenv("VOLUME_PARTIAL_OUT"); out != "" {
		if err := os.WriteFile(out, png, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range days {
		days[i].Languages = nil
	}
	line, err := gen.GenerateMonthlyVolumeChart(days, MonthlyVolumeMeta{MonthLabel: "August 2026"})
	if err != nil || len(line) == 0 {
		t.Fatalf("line view with partial days: %d bytes, %v", len(line), err)
	}
}
