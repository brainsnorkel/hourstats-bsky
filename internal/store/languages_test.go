package store

import (
	"context"
	"testing"
	"time"
)

func TestLanguageCounts_StoreRollupRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	if err := s.StoreLanguageCounts(ctx, day.Add(time.Hour), map[string]int64{"en": 100, "pt": 40, "": 5, "ja": 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreLanguageCounts(ctx, day.Add(2*time.Hour), map[string]int64{"en": 120, "ja": 30}); err != nil {
		t.Fatal(err)
	}
	// Same timestamp twice accumulates rather than replacing.
	if err := s.StoreLanguageCounts(ctx, day.Add(2*time.Hour), map[string]int64{"en": 5}); err != nil {
		t.Fatal(err)
	}
	// Next day must not leak in.
	if err := s.StoreLanguageCounts(ctx, day.Add(25*time.Hour), map[string]int64{"en": 999}); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreLanguageCounts(ctx, day, nil); err != nil {
		t.Fatal("empty map should be a no-op")
	}

	n, err := s.RollupLanguageDaily(ctx, "2026-09-01")
	if err != nil || n != 3 {
		t.Fatalf("rollup = %d, %v; want 3", n, err)
	}
	rows, err := s.GetLanguageDailyRange(ctx, "2026-09-01", "2026-09-01")
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.Lang] = r.Count
	}
	if got["en"] != 225 || got["pt"] != 40 || got["ja"] != 30 {
		t.Errorf("daily = %v", got)
	}
	if rows[0].Lang != "en" {
		t.Errorf("ordering: first row %s", rows[0].Lang)
	}

	// A re-run after the hourly rows are purged keeps the fuller figures.
	if _, err := s.PurgeLanguageCounts(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RollupLanguageDaily(ctx, "2026-09-01"); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.GetLanguageDailyRange(ctx, "2026-09-01", "2026-09-02")
	if len(rows) != 3 || rows[0].Count != 225 {
		t.Errorf("after purge re-run: %+v", rows)
	}
	if _, err := s.RollupLanguageDaily(ctx, "bad"); err == nil {
		t.Error("bad date should error")
	}
}
