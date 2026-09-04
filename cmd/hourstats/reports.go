package main

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// Shared pieces of the weekly and monthly report posts: period arithmetic,
// number and date formatting, and the Bluesky length check.

// blueskyPostLimit is the grapheme limit for a post; runes are close enough
// for the report texts, which carry no combining sequences.
const blueskyPostLimit = 300

// dateFormat is the YYYY-MM-DD form used as the daily_sentiment key.
const dateFormat = "2006-01-02"

// utcDate truncates t to a UTC calendar day.
func utcDate(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// previousWeek returns the Monday and Sunday of the last complete
// Monday-to-Sunday week before now. On a Monday that is the week that just
// ended.
func previousWeek(now time.Time) (start, end time.Time) {
	today := utcDate(now)
	wd := int(today.Weekday())
	if wd == 0 {
		wd = 7
	}
	thisMonday := today.AddDate(0, 0, -(wd - 1))
	start = thisMonday.AddDate(0, 0, -7)
	return start, start.AddDate(0, 0, 6)
}

// previousMonth returns the first and last day of the calendar month before
// the one containing now.
func previousMonth(now time.Time) (first, last time.Time) {
	t := now.UTC()
	thisFirst := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return thisFirst.AddDate(0, -1, 0), thisFirst.AddDate(0, 0, -1)
}

// dayLabel renders a daily_sentiment date as "Sat 30 Aug".
func dayLabel(date string) string {
	t, err := time.Parse(dateFormat, date)
	if err != nil {
		return date
	}
	return t.Format("Mon 2 Jan")
}

// rangeLabel renders a date span compactly: "25–31 Aug", "29 Sep – 5 Oct" or
// "29 Dec 2025 – 4 Jan 2026".
func rangeLabel(start, end time.Time) string {
	switch {
	case start.Year() != end.Year():
		return start.Format("2 Jan 2006") + " – " + end.Format("2 Jan 2006")
	case start.Month() != end.Month():
		return start.Format("2 Jan") + " – " + end.Format("2 Jan")
	default:
		return fmt.Sprintf("%d–%d %s", start.Day(), end.Day(), end.Format("Jan"))
	}
}

// compactCount formats a count as 13.1M, 48.2k or 812.
func compactCount(n int) string {
	switch {
	case n >= 1_000_000:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1_000_000)) + "M"
	case n >= 1_000:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1_000)) + "k"
	default:
		return fmt.Sprintf("%d", n)
	}
}

// trimZero drops a trailing ".0" so 2.0M reads as 2M.
func trimZero(s string) string {
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		return s[:len(s)-2]
	}
	return s
}

// signedPoints formats a sentiment delta in points: "+0.6", "-1.2", "0.0".
func signedPoints(d float64) string {
	if math.Abs(d) < 0.05 {
		return "0.0"
	}
	return fmt.Sprintf("%+.1f", d)
}

// moodPhrase names the polarity of a net sentiment average.
func moodPhrase(avg float64) string {
	switch {
	case avg > 0.05:
		return "net positive"
	case avg < -0.05:
		return "net negative"
	default:
		return "neutral"
	}
}

// meanDailyAverage is the unweighted mean of the days' average sentiment.
func meanDailyAverage(days []store.DailySentimentDataPoint) float64 {
	if len(days) == 0 {
		return 0
	}
	sum := 0.0
	for _, d := range days {
		sum += d.AverageSentiment
	}
	return sum / float64(len(days))
}

// happiestUnhappiest returns the indices of the best and worst days by
// average sentiment (first wins ties).
func happiestUnhappiest(days []store.DailySentimentDataPoint) (hi, lo int) {
	for i, d := range days {
		if d.AverageSentiment > days[hi].AverageSentiment {
			hi = i
		}
		if d.AverageSentiment < days[lo].AverageSentiment {
			lo = i
		}
	}
	return hi, lo
}

// sumPosts totals the English posts analysed across the days.
func sumPosts(days []store.DailySentimentDataPoint) int {
	n := 0
	for _, d := range days {
		n += d.TotalPosts
	}
	return n
}

// postLength counts a post the way Bluesky's limit does, closely enough for
// these texts.
func postLength(text string) int {
	return utf8.RuneCountInString(text)
}

// reportsStartupDelay is how long after boot a REPORTS_RUN_AT_STARTUP run
// waits, so the store and consumer are settled first.
const reportsStartupDelay = 30 * time.Second

// parseStartupReports reads REPORTS_RUN_AT_STARTUP, a comma list of
// "weekly" and "monthly". Unknown names are reported back for logging.
func parseStartupReports(v string) (weekly, monthly bool, unknown []string) {
	for _, name := range strings.Split(v, ",") {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "":
		case "weekly":
			weekly = true
		case "monthly":
			monthly = true
		default:
			unknown = append(unknown, strings.TrimSpace(name))
		}
	}
	return weekly, monthly, unknown
}
