package sparkline

import (
	"os"
	"testing"
	"time"
)

func synthLanguageMonth() []DailyVolumePoint {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	var days []DailyVolumePoint
	for i := 0; i < 31; i++ {
		en := 1_800_000 + (i%5)*60_000
		if i == 15 {
			en = 1_100_000
		}
		langs := map[string]int{
			"en": en, "pt": 700_000 + (i%3)*40_000, "ja": 520_000 + (i%4)*20_000, "es": 260_000,
			"de": 140_000, "fr": 120_000, "und": 300_000, "ko": 60_000, "it": 40_000, "nl": 25_000, "xx": 500,
		}
		total := 0
		for _, n := range langs {
			total += n
		}
		days = append(days, DailyVolumePoint{Date: start.AddDate(0, 0, i), ENPosts: en - 20_000, TotalPosts: total, Languages: langs})
	}
	return days
}

func TestLanguageBreakdown(t *testing.T) {
	series := LanguageBreakdown(synthLanguageMonth())
	if len(series) != MaxLanguageSeries+1 {
		t.Fatalf("series = %d, want %d plus other", len(series), MaxLanguageSeries)
	}
	want := []string{"en", "pt", "ja", "und", "es", "de", "other"}
	for i, s := range series {
		if s.Code != want[i] {
			t.Errorf("series[%d] = %s, want %s", i, s.Code, want[i])
		}
	}
	if series[0].Color != languagePalette[0] || series[6].Color != languageOther {
		t.Error("colour assignment")
	}
	if series[2].Name != "Japanese" || series[3].Name != "untagged" {
		t.Errorf("names: %s %s", series[2].Name, series[3].Name)
	}
	other := series[6].Total
	if other != 31*(120_000+60_000+40_000+25_000+500) {
		t.Errorf("other total = %d", other)
	}
	if LanguageBreakdown(nil) != nil || LanguageName("zz") != "ZZ" {
		t.Error("empty / unknown code handling")
	}

	// Without English the first slot still goes to the largest language.
	noEN := []DailyVolumePoint{{Languages: map[string]int{"pt": 5, "ja": 3}}}
	if s := LanguageBreakdown(noEN); len(s) != 2 || s[0].Code != "pt" || s[0].Color != languagePalette[0] {
		t.Errorf("no-English breakdown = %+v", s)
	}
}

// TestMonthlyVolumeChart_Stacked renders the stacked view; set VOLUME_OUT to
// keep the PNG. A month missing one day's split falls back to the line view.
func TestMonthlyVolumeChart_Stacked(t *testing.T) {
	days := synthLanguageMonth()
	gen := NewMonthlyVolumeGenerator()
	png, err := gen.GenerateMonthlyVolumeChart(days, MonthlyVolumeMeta{MonthLabel: "August 2026", PrevMonthEN: 58_000_000, PrevLabel: "July"})
	if err != nil || len(png) == 0 {
		t.Fatalf("stacked: %d bytes, %v", len(png), err)
	}
	if out := os.Getenv("VOLUME_OUT"); out != "" {
		if err := os.WriteFile(out, png, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	days[10].Languages = nil
	line, err := gen.GenerateMonthlyVolumeChart(days, MonthlyVolumeMeta{MonthLabel: "August 2026"})
	if err != nil || len(line) == 0 {
		t.Fatalf("fallback: %d bytes, %v", len(line), err)
	}
	if hasLanguagesEveryDay(days) {
		t.Error("hasLanguagesEveryDay should be false with a gap")
	}
}
