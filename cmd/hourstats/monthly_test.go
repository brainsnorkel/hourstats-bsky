package main

import (
	"math"
	"testing"
)

// augustReport is a full August with a full July before it and a firehose
// figure for every day, the shape runMonthlyReport sees on the 1st.
func augustReport() monthlyReport {
	r := monthlyReport{
		First: mustDate("2026-08-01"), Last: mustDate("2026-08-31"),
		Days:     monthDays("2026-08-01", 31, 10.8, 1_900_000),
		PrevDays: monthDays("2026-07-01", 31, 9.8, 1_842_000),
		Firehose: map[string]int{},
	}
	for _, d := range r.Days {
		r.Firehose[d.Date] = d.TotalPosts * 2
	}
	return r
}

func TestToDailyVolumePointsRuns(t *testing.T) {
	r := augustReport()
	r.Days[3].TotalRuns = 23
	pts := toDailyVolumePoints(r)
	if len(pts) != 31 {
		t.Fatalf("points = %d", len(pts))
	}
	for i, p := range pts {
		want := 24
		if i == 3 {
			want = 23
		}
		if p.Runs != want {
			t.Errorf("pts[%d].Runs = %d, want %d", i, p.Runs, want)
		}
		if p.ENPosts != r.Days[i].TotalPosts || p.TotalPosts != r.Firehose[r.Days[i].Date] {
			t.Errorf("pts[%d] = %+v, want counts from day %s", i, p, r.Days[i].Date)
		}
	}
}

// TestMonthlyVolumeMeta checks that the chart header gets the same
// month-over-month figure as the post text (volumeDelta, mean daily English
// volume), and no figure at all when the previous month is too thin.
func TestMonthlyVolumeMeta(t *testing.T) {
	r := augustReport()
	meta := monthlyVolumeMeta(r)
	want, ok := r.volumeDelta()
	if !ok {
		t.Fatal("fixture has no volume delta")
	}
	if !meta.HasPrevDelta || math.Abs(meta.PrevDeltaPct-want) > 1e-9 {
		t.Errorf("meta = %+v, want delta %.3f", meta, want)
	}
	if meta.MonthLabel != "August 2026" || meta.PrevLabel != "July" {
		t.Errorf("labels = %q %q", meta.MonthLabel, meta.PrevLabel)
	}
	// A delta of mean daily volume: a 31-day month against a 31-day month
	// with the same per-day figure on all but the two altered days.
	if meta.PrevDeltaPct < 3 || meta.PrevDeltaPct > 3.3 {
		t.Errorf("PrevDeltaPct = %.3f, want about +3.15 (1.9M vs 1.842M per day)", meta.PrevDeltaPct)
	}

	r.PrevDays = r.PrevDays[:5]
	meta = monthlyVolumeMeta(r)
	if meta.HasPrevDelta || meta.PrevDeltaPct != 0 {
		t.Errorf("thin previous month: meta = %+v", meta)
	}
}

// TestVolumeExtremesFollowChart checks the post names the days the chart
// marks: the quietest full day, with the partial day's count never quoted.
func TestVolumeExtremesFollowChart(t *testing.T) {
	r := augustReport()
	hi, lo, note, ok := volumeExtremes(r)
	if !ok || hi.Day != "Wed 12 Aug" || hi.Count != "2.5M" || lo.Day != "Sun 2 Aug" || lo.Count != "1.6M" || note != "" {
		t.Errorf("all full: %+v %+v %q %v", hi, lo, note, ok)
	}

	r.Days[1].TotalRuns = 23 // the quietest day lost a cycle
	hi, lo, note, ok = volumeExtremes(r)
	if !ok || hi.Day != "Wed 12 Aug" || lo.Day != "Sat 1 Aug" || lo.Count != "1.9M" {
		t.Errorf("partial quietest: %+v %+v %v", hi, lo, ok)
	}
	if note != "Busiest and quietest exclude 1 partial day with fewer cycles" {
		t.Errorf("note = %q", note)
	}

	if _, _, _, ok := volumeExtremes(monthlyReport{}); ok {
		t.Error("empty month reported extremes")
	}
}
