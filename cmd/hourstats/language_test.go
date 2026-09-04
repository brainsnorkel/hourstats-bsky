package main

import (
	"strings"
	"testing"
)

func TestPrimaryAndPostLang(t *testing.T) {
	for in, want := range map[string]string{"pt-BR": "pt", "EN": "en", " ja ": "ja", "zh_TW": "zh", "": "und", "x": "und", "abcd": "und", "e1": "und", "fil": "fil"} {
		if got := primaryLang(in); got != want {
			t.Errorf("primaryLang(%q) = %q, want %q", in, got, want)
		}
	}
	for _, tc := range []struct {
		langs []string
		want  string
	}{
		{nil, "und"}, {[]string{"pt", "en"}, "en"}, {[]string{"en-GB"}, "en"}, {[]string{"pt-BR", "es"}, "pt"}, {[]string{"???"}, "und"},
	} {
		if got := postLang(tc.langs); got != tc.want {
			t.Errorf("postLang(%v) = %q, want %q", tc.langs, got, tc.want)
		}
	}
}

func TestMonthlyLanguageLine(t *testing.T) {
	r := monthlyReport{
		First: mustDate("2026-08-01"), Last: mustDate("2026-08-31"),
		Days:     monthDays("2026-08-01", 31, 10.8, 1_900_000),
		Firehose: map[string]int{}, Languages: map[string]map[string]int{},
	}
	for _, d := range r.Days {
		r.Firehose[d.Date] = d.TotalPosts * 2
		r.Languages[d.Date] = map[string]int{"en": d.TotalPosts, "pt": d.TotalPosts / 4, "ja": d.TotalPosts / 5, "und": d.TotalPosts / 10, "es": d.TotalPosts / 20, "de": d.TotalPosts / 40, "fr": d.TotalPosts / 50, "ko": 1000}
	}
	line := r.languageLine()
	if !strings.HasPrefix(line, "Portuguese ") || !strings.Contains(line, "Japanese ") || strings.Contains(line, "English") || strings.Contains(line, "other") || strings.Contains(line, "untagged") {
		t.Errorf("languageLine = %q", line)
	}
	vol := buildMonthlyVolumeText(r)
	if !strings.Contains(vol, "\nNext: Portuguese ") {
		t.Errorf("volume text missing language line:\n%s", vol)
	}
	if n := postLength(vol); n > blueskyPostLimit {
		t.Errorf("volume text %d runes over limit", n)
	}
	alt := buildMonthlyVolumeAltText(r)
	if !strings.Contains(alt, "stacked area") || !strings.Contains(alt, "largest after posts marked as English were Portuguese") {
		t.Errorf("alt: %s", alt)
	}
	pts := toDailyVolumePoints(r)
	if pts[0].Languages["pt"] == 0 {
		t.Error("languages not carried to points")
	}
	// Languages without a firehose column still stack; the share line goes.
	r.Firehose = nil
	vol = buildMonthlyVolumeText(r)
	if strings.Contains(vol, "English share") || !strings.Contains(vol, "Next: Portuguese") {
		t.Errorf("languages-only text:\n%s", vol)
	}
	if alt := buildMonthlyVolumeAltText(r); !strings.Contains(alt, "stacked area") || strings.Contains(alt, "of the ") && strings.Contains(alt, "firehose.") {
		t.Errorf("languages-only alt: %s", alt)
	}
	r.Languages = nil
	if r.languageLine() != "" || strings.Contains(buildMonthlyVolumeText(r), "Next:") {
		t.Error("language line should vanish without data")
	}
}
