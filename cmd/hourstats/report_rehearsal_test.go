package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// TestReportRehearsal prints the weekly and monthly report posts that a copy
// of a backup would produce at a pretend time, and writes the two monthly
// charts. It asserts nothing about the content: `make report-rehearsal
// DB=...` sets the HS_REPORT_REHEARSAL_* variables and the operator reads the
// output. Without HS_REPORT_REHEARSAL_DB the test skips.
func TestReportRehearsal(t *testing.T) {
	src := os.Getenv("HS_REPORT_REHEARSAL_DB")
	if src == "" {
		t.Skip("set HS_REPORT_REHEARSAL_DB to a backup copy (make report-rehearsal DB=path/to/backup.db) to rehearse the reports")
	}
	now := time.Now().UTC()
	if v := os.Getenv("HS_REPORT_REHEARSAL_NOW"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Fatalf("HS_REPORT_REHEARSAL_NOW %q: %v", v, err)
		}
		now = parsed.UTC()
	}
	out := os.Getenv("HS_REPORT_REHEARSAL_OUT")
	if out != "" {
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// The source and its WAL are only ever read as bytes; SQLite opens the copy.
	dir := t.TempDir()
	copyRehearsalFile(t, src, filepath.Join(dir, "rehearsal.db"))
	if _, err := os.Stat(src + "-wal"); err == nil {
		copyRehearsalFile(t, src+"-wal", filepath.Join(dir, "rehearsal.db-wal"))
	}
	db, err := store.New(filepath.Join(dir, "rehearsal.db"))
	if err != nil {
		t.Fatalf("open the copy: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	t.Logf("rehearsal: now=%s db=%s", now.Format(time.RFC3339), src)

	// This week and the next, so the operator sees the post that is due and
	// the one after it.
	for _, at := range []time.Time{now, now.AddDate(0, 0, 7)} {
		start, end := previousWeek(at)
		r, ok := loadWeeklyReport(ctx, db, at)
		t.Logf("weekly at %s: week %s – %s ok=%v days=%d prev_days=%d topic=%q topic_hours=%d has_top_post=%v",
			at.Format(dateFormat), start.Format(dateFormat), end.Format(dateFormat), ok,
			len(r.Days), len(r.PrevDays), r.TopicLabel, r.TopicHours, r.TopPost != nil)
		if !ok {
			continue
		}
		text := buildWeeklyReportText(r)
		t.Logf("weekly root (%d runes):\n%s", postLength(text), text)
		if r.TopPost != nil {
			reply := buildPostOfWeekText(r.Start, r.End, storedPostOfWeekFigures(*r.TopPost))
			t.Logf("weekly reply (%d runes) counts_source=stored (the live run reads the gate view):\n%s", postLength(reply), reply)
		}
	}

	first, _ := previousMonth(now)
	r, ok := loadMonthlyReport(ctx, db, now)
	t.Logf("monthly: month=%s ok=%v days=%d prev_days=%d firehose_complete=%v languages_complete=%v",
		first.Format("2006-01"), ok, len(r.Days), len(r.PrevDays), r.Firehose != nil, r.Languages != nil)
	if !ok {
		return
	}
	for _, p := range []struct{ name, text string }{
		{"mood text", buildMonthlyMoodText(r)}, {"volume text", buildMonthlyVolumeText(r)},
		{"candle alt", buildMonthlyCandleAltText(r)}, {"volume alt", buildMonthlyVolumeAltText(r)},
	} {
		t.Logf("monthly %s (%d runes):\n%s", p.name, postLength(p.text), p.text)
	}
	candle, err := sparkline.NewMonthlyCandleGenerator().GenerateMonthlyCandleChart(toDailyCandles(r.Days), monthlyCandleMeta(r))
	if err != nil {
		t.Fatalf("candle chart: %v", err)
	}
	volume, err := sparkline.NewMonthlyVolumeGenerator().GenerateMonthlyVolumeChart(toDailyVolumePoints(r), monthlyVolumeMeta(r))
	if err != nil {
		t.Fatalf("volume chart: %v", err)
	}
	t.Logf("monthly charts: candle=%d bytes volume=%d bytes", len(candle), len(volume))
	if out != "" {
		for name, png := range map[string][]byte{"monthly-candle.png": candle, "monthly-volume.png": volume} {
			if err := os.WriteFile(filepath.Join(out, name), png, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("wrote monthly-candle.png and monthly-volume.png to %s", out)
	}
}

// copyRehearsalFile copies src to dst byte for byte, so the backup itself is
// never opened by SQLite.
func copyRehearsalFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
