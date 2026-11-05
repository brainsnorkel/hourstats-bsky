package main

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
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

// TestMonthlyCandleMeta checks the candlestick header gets the previous
// month's mean daily average when that month is full, and NaN when it is
// thin or absent, so the chart draws no comparison the text does not make.
func TestMonthlyCandleMeta(t *testing.T) {
	r := augustReport()
	meta := monthlyCandleMeta(r)
	if meta.MonthLabel != "August 2026" || meta.PrevLabel != "July" {
		t.Errorf("labels = %q %q", meta.MonthLabel, meta.PrevLabel)
	}
	if want := meanDailyAverage(r.PrevDays); want == 0 || math.Abs(meta.PrevMonthAvg-want) > 1e-9 {
		t.Errorf("PrevMonthAvg = %v, want %v", meta.PrevMonthAvg, want)
	}
	r.PrevDays = r.PrevDays[:5]
	if meta := monthlyCandleMeta(r); !math.IsNaN(meta.PrevMonthAvg) {
		t.Errorf("thin previous month: PrevMonthAvg = %v, want NaN", meta.PrevMonthAvg)
	}
	r.PrevDays = nil
	if meta := monthlyCandleMeta(r); !math.IsNaN(meta.PrevMonthAvg) || meta.PrevLabel != "July" {
		t.Errorf("no previous month: meta = %+v", meta)
	}
}

// withVolumeDelta gives every day of the previous month the same English
// count, chosen so volumeDelta() comes out at pct to within the rounding of
// one post a day. pct 0 copies the month's own counts for an exact zero.
func withVolumeDelta(r monthlyReport, pct float64) monthlyReport {
	mean := float64(sumPosts(r.Days)) / float64(len(r.Days))
	for i := range r.PrevDays {
		r.PrevDays[i].TotalPosts = r.Days[i].TotalPosts
		if pct != 0 {
			r.PrevDays[i].TotalPosts = int(math.Round(mean / (1 + pct/100)))
		}
	}
	return r
}

// TestMonthlyVolumeDeltaText pins the delta's form in the volume post and
// its alt text to the chart header's: "+0.5%", "-0.4%", and a bare "0.0%"
// for no change, never "+0.0%". The chart meta carries the same figure.
func TestMonthlyVolumeDeltaText(t *testing.T) {
	for _, tc := range []struct {
		pct  float64
		want string
	}{{0, "0.0%"}, {0.51, "+0.5%"}, {-0.4, "-0.4%"}} {
		r := withVolumeDelta(augustReport(), tc.pct)
		d, ok := r.volumeDelta()
		if !ok || math.Abs(d-tc.pct) > 0.001 {
			t.Fatalf("fixture delta = %v %v, want %v", d, ok, tc.pct)
		}
		want := ", " + tc.want + " vs July"
		for name, text := range map[string]string{"text": buildMonthlyVolumeText(r), "alt": buildMonthlyVolumeAltText(r)} {
			if !strings.Contains(text, want) || strings.Contains(text, "+0.0%") || strings.Contains(text, "-0.0%") {
				t.Errorf("delta %v: %s = %q, want %q", tc.pct, name, text, want)
			}
		}
		if meta := monthlyVolumeMeta(r); !meta.HasPrevDelta || meta.PrevDeltaPct != d {
			t.Errorf("delta %v: chart meta %+v carries a different figure from the text", tc.pct, meta)
		}
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

// TestRunMonthlyReportSkipsWhenGuardUnreadable: a read error that is not "no
// such key" must not fall through to the catch-up post. Posting without
// knowing what was already posted is worse than losing one night's run; an
// absent key, by contrast, is the documented catch-up case.
func TestRunMonthlyReportSkipsWhenGuardUnreadable(t *testing.T) {
	db := newTestStore(t)
	buf := captureLogs(t)

	// A cancelled context makes GetKeyValue fail before it reaches the
	// database, with a wrapped context.Canceled rather than sql.ErrNoRows.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	now := time.Date(2026, time.November, 1, 1, 0, 0, 0, time.UTC)

	runMonthlyReport(cancelled, db, "", "", true, now)

	out := buf.String()
	if !strings.Contains(out, `"level":"WARN"`) || !strings.Contains(out, `"msg":"monthly report: guard key unreadable, skipping this run"`) {
		t.Errorf("unreadable guard was not warned about: %s", out)
	}
	if strings.Contains(out, "DRY_RUN: would post monthly report") {
		t.Errorf("a read error let the report go ahead: %s", out)
	}
	if got, err := db.GetKeyValue(context.Background(), monthlyReportGuardKey); err == nil {
		t.Errorf("guard key was written (%q) on a run whose read failed, want it still unset", got)
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetKeyValue after the failed run: %v, want a not-found error", err)
	}
}
