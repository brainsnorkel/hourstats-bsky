package main

import (
	"strings"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func TestPreviousWeekAndMonth(t *testing.T) {
	cases := []struct{ now, wantStart, wantEnd string }{
		{"2026-09-07T00:05:00Z", "2026-08-31", "2026-09-06"}, // Monday reports the week just ended
		{"2026-09-04T11:00:00Z", "2026-08-24", "2026-08-30"}, // Friday: last complete week
		{"2026-09-06T23:59:59Z", "2026-08-24", "2026-08-30"}, // Sunday: still last complete week
	}
	for _, tc := range cases {
		now, _ := time.Parse(time.RFC3339, tc.now)
		s, e := previousWeek(now)
		if s.Format(dateFormat) != tc.wantStart || e.Format(dateFormat) != tc.wantEnd {
			t.Errorf("previousWeek(%s) = %s..%s, want %s..%s", tc.now, s.Format(dateFormat), e.Format(dateFormat), tc.wantStart, tc.wantEnd)
		}
	}
	mcases := []struct{ now, wantFirst, wantLast string }{
		{"2026-09-01T01:00:00Z", "2026-08-01", "2026-08-31"},
		{"2026-01-15T00:00:00Z", "2025-12-01", "2025-12-31"},
		{"2026-03-01T01:00:00Z", "2026-02-01", "2026-02-28"},
	}
	for _, tc := range mcases {
		now, _ := time.Parse(time.RFC3339, tc.now)
		f, l := previousMonth(now)
		if f.Format(dateFormat) != tc.wantFirst || l.Format(dateFormat) != tc.wantLast {
			t.Errorf("previousMonth(%s) = %s..%s, want %s..%s", tc.now, f.Format(dateFormat), l.Format(dateFormat), tc.wantFirst, tc.wantLast)
		}
	}
}

func TestReportFormatting(t *testing.T) {
	d := func(s string) time.Time { t, _ := time.Parse(dateFormat, s); return t }
	if got := rangeLabel(d("2025-08-25"), d("2025-08-31")); got != "25–31 Aug" {
		t.Errorf("same month: %q", got)
	}
	if got := rangeLabel(d("2026-09-28"), d("2026-10-04")); got != "28 Sep – 4 Oct" {
		t.Errorf("cross month: %q", got)
	}
	if got := rangeLabel(d("2025-12-29"), d("2026-01-04")); got != "29 Dec 2025 – 4 Jan 2026" {
		t.Errorf("cross year: %q", got)
	}
	for n, want := range map[int]string{13_100_000: "13.1M", 48_200: "48.2k", 2_000_000: "2M", 812: "812", 999: "999", 1_000: "1k", 140_300_000: "140.3M"} {
		if got := compactCount(n); got != want {
			t.Errorf("compactCount(%d) = %q, want %q", n, got, want)
		}
	}
	for v, want := range map[float64]string{0.6: "+0.6", -1.24: "-1.2", 0.01: "0.0"} {
		if got := signedPoints(v); got != want {
			t.Errorf("signedPoints(%v) = %q, want %q", v, got, want)
		}
	}
	if dayLabel("2025-08-30") != "Sat 30 Aug" {
		t.Errorf("dayLabel = %q", dayLabel("2025-08-30"))
	}
	w, m, unknown := parseStartupReports(" Weekly, monthly ,bogus")
	if !w || !m || len(unknown) != 1 || unknown[0] != "bogus" {
		t.Errorf("parseStartupReports = %v %v %v", w, m, unknown)
	}
	if w, m, u := parseStartupReports(""); w || m || len(u) != 0 {
		t.Errorf("empty parse = %v %v %v", w, m, u)
	}
}

func weekDays(start string, avgs []float64, posts int) []store.DailySentimentDataPoint {
	s, _ := time.Parse(dateFormat, start)
	var out []store.DailySentimentDataPoint
	for i, a := range avgs {
		out = append(out, store.DailySentimentDataPoint{
			Date: s.AddDate(0, 0, i).Format(dateFormat), AverageSentiment: a,
			MinSentiment: a - 3, MaxSentiment: a + 3, Q1Sentiment: a - 1, MedianSentiment: a, Q3Sentiment: a + 1,
			TotalRuns: 24, TotalPosts: posts, TotalFirehosePosts: posts * 2,
		})
	}
	return out
}

func TestBuildWeeklyReportText(t *testing.T) {
	r := weeklyReport{
		Start: mustDate("2025-08-25"), End: mustDate("2025-08-31"),
		Days:       weekDays("2025-08-25", []float64{10.0, 10.5, 8.3, 10.2, 10.9, 12.1, 10.8}, 1_871_429),
		PrevDays:   weekDays("2025-08-18", []float64{9.8, 9.8, 9.8, 9.8, 9.8, 9.8, 9.8}, 1_800_000),
		TopicLabel: "Taylor Swift engagement", TopicHours: 31,
		TopPost: &store.Post{AuthorHandle: "nasa.gov", Likes: 48_200, Reposts: 9_100, Replies: 2_300},
	}
	got := buildWeeklyReportText(r)
	want := "Week in review · 25–31 Aug\n\n" +
		"Mood: +10.4% net positive, +0.6 vs the week before\n" +
		"Happiest day: Sat 30 Aug, +12.1%\n" +
		"Unhappiest day: Wed 27 Aug, +8.3%\n" +
		"Stickiest topic: Taylor Swift engagement, trending 31 of 168 hours\n\n" +
		"13.1M English posts analysed"
	if got != want {
		t.Errorf("weekly text:\n%s\nwant:\n%s", got, want)
	}
	if n := postLength(got); n > blueskyPostLimit {
		t.Errorf("weekly text %d runes over limit", n)
	}

	reply := buildPostOfWeekText(r)
	if reply != "Post of the week · 25–31 Aug\n\nMost engaged post by @nasa.gov: 48.2k likes, 9.1k reposts, 2.3k replies" {
		t.Errorf("reply text: %q", reply)
	}

	// No topic data and a thin previous week drop their lines, not the post.
	r.TopicLabel, r.PrevDays = "", r.PrevDays[:3]
	got = buildWeeklyReportText(r)
	if strings.Contains(got, "Stickiest") || strings.Contains(got, "week before") {
		t.Errorf("optional lines not omitted:\n%s", got)
	}
	if !strings.HasSuffix(got, "Unhappiest day: Wed 27 Aug, +8.3%\n\n13.1M English posts analysed") {
		t.Errorf("unexpected tail:\n%s", got)
	}

	// A long topic label is capped, and the post always fits.
	r.TopicLabel = strings.Repeat("Very long topic label ", 6)
	r.PrevDays = weekDays("2025-08-18", []float64{9.8, 9.8, 9.8, 9.8, 9.8, 9.8, 9.8}, 1)
	got = buildWeeklyReportText(r)
	if n := postLength(got); n > blueskyPostLimit {
		t.Errorf("long topic pushes text to %d runes", n)
	}
	if !strings.Contains(got, "Stickiest topic: Very long topic label") || !strings.Contains(got, "…, trending 31 of 168 hours") {
		t.Errorf("long topic not truncated:\n%s", got)
	}
	if truncateRunes("abc", 3) != "abc" || truncateRunes("abcd", 3) != "ab…" {
		t.Error("truncateRunes")
	}
}

func mustDate(s string) time.Time {
	t, err := time.Parse(dateFormat, s)
	if err != nil {
		panic(err)
	}
	return t
}

func monthDays(first string, n int, base float64, posts int) []store.DailySentimentDataPoint {
	avgs := make([]float64, n)
	for i := range avgs {
		avgs[i] = base + float64(i%3)*0.3
	}
	days := weekDays(first, avgs, posts)
	days[7].AverageSentiment = base + 1.1  // best: 8th
	days[19].AverageSentiment = base - 0.9 // worst: 20th
	days[19].MinSentiment, days[19].MaxSentiment = base-4, base+2.1
	days[11].TotalPosts = posts + 600_000 // busiest: 12th
	days[1].TotalPosts = posts - 300_000  // quietest: 2nd
	return days
}

func TestBuildMonthlyTexts(t *testing.T) {
	r := monthlyReport{
		First: mustDate("2026-08-01"), Last: mustDate("2026-08-31"),
		Days:     monthDays("2026-08-01", 31, 10.8, 1_900_000),
		PrevDays: monthDays("2026-07-01", 31, 9.8, 1_842_000),
		Firehose: map[string]int{},
	}
	for _, d := range r.Days {
		r.Firehose[d.Date] = d.TotalPosts * 2
	}

	mood := buildMonthlyMoodText(r)
	for _, want := range []string{
		"August in review\n\n", "Mood: +", "net positive, +1.0 vs July", "Best day: Sat 8 Aug, +11.9%",
		"Worst day: Thu 20 Aug, +9.9%", "Widest hourly swing: Thu 20 Aug, 6.1 points",
		"\n\nOne candle per day: whisker is the hourly range, box is the middle half, tick is the median.",
	} {
		if !strings.Contains(mood, want) {
			t.Errorf("mood text missing %q:\n%s", want, mood)
		}
	}
	vol := buildMonthlyVolumeText(r)
	for _, want := range []string{
		"Post volume · August\n\n", "English posts analysed, +", "% vs July", "per day on average",
		"Busiest: Wed 12 Aug, 2.5M", "Quietest: Sun 2 Aug, 1.6M", "English share of the firehose: 50% of ",
	} {
		if !strings.Contains(vol, want) {
			t.Errorf("volume text missing %q:\n%s", want, vol)
		}
	}
	for name, text := range map[string]string{"mood": mood, "volume": vol} {
		if n := postLength(text); n > blueskyPostLimit {
			t.Errorf("%s text %d runes over limit", name, n)
		}
	}
	if alt := buildMonthlyCandleAltText(r); !strings.Contains(alt, "August 2026") || !strings.Contains(alt, "31 days") {
		t.Errorf("candle alt: %s", alt)
	}
	if alt := buildMonthlyVolumeAltText(r); !strings.Contains(alt, "firehose") || !strings.Contains(alt, "Busiest Wed 12 Aug") {
		t.Errorf("volume alt: %s", alt)
	}

	// Without a full previous month or firehose data the comparisons vanish.
	r.PrevDays, r.Firehose = r.PrevDays[:5], nil
	mood, vol = buildMonthlyMoodText(r), buildMonthlyVolumeText(r)
	if strings.Contains(mood, "vs July") || strings.Contains(vol, "vs July") || strings.Contains(vol, "firehose") {
		t.Errorf("optional lines not omitted:\n%s\n%s", mood, vol)
	}
	if alt := buildMonthlyVolumeAltText(r); strings.Contains(alt, "firehose") {
		t.Errorf("volume alt still mentions firehose: %s", alt)
	}
	pts := toDailyVolumePoints(r)
	if len(pts) != 31 || pts[0].TotalPosts != 0 {
		t.Errorf("volume points without firehose = %d, first total %d", len(pts), pts[0].TotalPosts)
	}
	if c := toDailyCandles(r.Days); len(c) != 31 || c[19].Max-c[19].Min < 6 {
		t.Errorf("candles = %d, widest %v", len(c), c[19])
	}
}
