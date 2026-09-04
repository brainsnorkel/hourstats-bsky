package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// Monthly candlestick and volume thread (1st of the month, after the yearly chart)
// ---------------------------------------------------------------------------

const (
	// monthlyReportGuardKey holds the "YYYY-MM" of the last month reported.
	monthlyReportGuardKey = "monthly_report_last_month"
	// monthlyReportMinDays is how many daily rows the month must have.
	monthlyReportMinDays = 20
)

// monthlyReport is everything the two monthly posts are built from.
type monthlyReport struct {
	First, Last time.Time
	Days        []store.DailySentimentDataPoint // the month, ordered
	PrevDays    []store.DailySentimentDataPoint // the month before; empty when too thin
	// Firehose holds each day's all-language total when every day of the
	// month has one; nil otherwise, which drops the share line and the
	// firehose line on the chart rather than drawing a misleading partial.
	Firehose map[string]int
	// Languages holds each day's firehose split by primary language subtag
	// when every day of the month has one; nil otherwise, which keeps the
	// chart on its single-line view.
	Languages map[string]map[string]int
}

// languageShares returns the month's non-English languages that get their
// own band, largest first, each with its share of the month's firehose.
type languageShare struct {
	Name  string
	Share float64
}

func (r monthlyReport) languageShares() []languageShare {
	if r.Languages == nil {
		return nil
	}
	pts := toDailyVolumePoints(r)
	total := 0
	for _, p := range pts {
		for _, n := range p.Languages {
			total += n
		}
	}
	if total == 0 {
		return nil
	}
	var out []languageShare
	for _, s := range sparkline.LanguageBreakdown(pts) {
		// Untagged posts get a band on the chart but are not a language,
		// so the prose leaves them out.
		if s.Code == "en" || s.Code == sparkline.OtherCode || s.Code == sparkline.UndeterminedCode {
			continue
		}
		out = append(out, languageShare{Name: s.Name, Share: float64(s.Total) / float64(total) * 100})
	}
	return out
}

// languageLine renders "Portuguese 18%, Japanese 12%, Spanish 6%" for the
// post and alt text, or "" without language data.
func (r monthlyReport) languageLine() string {
	shares := r.languageShares()
	if len(shares) == 0 {
		return ""
	}
	parts := make([]string, 0, len(shares))
	for _, s := range shares {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", s.Name, s.Share))
	}
	return strings.Join(parts, ", ")
}

func (r monthlyReport) monthLabel() string { return r.First.Format("January 2006") }
func (r monthlyReport) monthName() string  { return r.First.Format("January") }
func (r monthlyReport) prevName() string   { return r.First.AddDate(0, -1, 0).Format("January") }
func (r monthlyReport) hasPrev() bool      { return len(r.PrevDays) >= monthlyReportMinDays }

// widestSwing returns the index of the day with the largest hourly range.
func widestSwing(days []store.DailySentimentDataPoint) int {
	w := 0
	for i, d := range days {
		if d.MaxSentiment-d.MinSentiment > days[w].MaxSentiment-days[w].MinSentiment {
			w = i
		}
	}
	return w
}

// busiestQuietest returns the indices of the days with the most and fewest
// English posts.
func busiestQuietest(days []store.DailySentimentDataPoint) (hi, lo int) {
	for i, d := range days {
		if d.TotalPosts > days[hi].TotalPosts {
			hi = i
		}
		if d.TotalPosts < days[lo].TotalPosts {
			lo = i
		}
	}
	return hi, lo
}

func (r monthlyReport) firehoseTotal() int {
	n := 0
	for _, v := range r.Firehose {
		n += v
	}
	return n
}

// buildMonthlyMoodText renders the root post beside the candlestick chart.
func buildMonthlyMoodText(r monthlyReport) string {
	avg := meanDailyAverage(r.Days)
	hi, lo := happiestUnhappiest(r.Days)
	w := widestSwing(r.Days)

	mood := fmt.Sprintf("Mood: %s %s", signedPct(avg), moodPhrase(avg))
	if r.hasPrev() {
		mood += fmt.Sprintf(", %s vs %s", signedPoints(avg-meanDailyAverage(r.PrevDays)), r.prevName())
	}
	return strings.Join([]string{
		r.monthName() + " in review",
		"",
		mood,
		fmt.Sprintf("Best day: %s, %s", dayLabel(r.Days[hi].Date), signedPct(r.Days[hi].AverageSentiment)),
		fmt.Sprintf("Worst day: %s, %s", dayLabel(r.Days[lo].Date), signedPct(r.Days[lo].AverageSentiment)),
		fmt.Sprintf("Widest hourly swing: %s, %.1f points", dayLabel(r.Days[w].Date), r.Days[w].MaxSentiment-r.Days[w].MinSentiment),
		"",
		"One candle per day: whisker is the hourly range, box is the middle half, tick is the median.",
	}, "\n")
}

// buildMonthlyVolumeText renders the reply beside the volume chart.
func buildMonthlyVolumeText(r monthlyReport) string {
	total := sumPosts(r.Days)
	hi, lo := busiestQuietest(r.Days)

	analysed := fmt.Sprintf("%s English posts analysed", compactCount(total))
	if d, ok := r.volumeDelta(); ok {
		analysed += fmt.Sprintf(", %+.1f%% vs %s", d, r.prevName())
	}
	lines := []string{
		"Post volume · " + r.monthName(),
		"",
		analysed,
		fmt.Sprintf("%s per day on average", compactCount(total/len(r.Days))),
		fmt.Sprintf("Busiest: %s, %s", dayLabel(r.Days[hi].Date), compactCount(r.Days[hi].TotalPosts)),
		fmt.Sprintf("Quietest: %s, %s", dayLabel(r.Days[lo].Date), compactCount(r.Days[lo].TotalPosts)),
	}
	if fh := r.firehoseTotal(); fh >= total && fh > 0 {
		lines = append(lines, fmt.Sprintf("English share of the firehose: %.0f%% of %s", float64(total)/float64(fh)*100, compactCount(fh)))
	}
	if langs := r.languageLine(); langs != "" {
		withLangs := append(append([]string{}, lines...), "Next: "+langs)
		if n := postLength(strings.Join(withLangs, "\n")); n <= blueskyPostLimit {
			lines = withLangs
		} else {
			slog.Info("monthly report: language line dropped, post would exceed limit", "length", n)
		}
	}
	return strings.Join(lines, "\n")
}

// volumeDelta compares this month's mean daily English volume with the
// previous month's, so a short month or a thin previous month does not
// masquerade as a change in traffic.
func (r monthlyReport) volumeDelta() (float64, bool) {
	if !r.hasPrev() || len(r.Days) == 0 {
		return 0, false
	}
	cur := float64(sumPosts(r.Days)) / float64(len(r.Days))
	prev := float64(sumPosts(r.PrevDays)) / float64(len(r.PrevDays))
	if prev <= 0 {
		return 0, false
	}
	return (cur - prev) / prev * 100, true
}

// buildMonthlyCandleAltText describes the candlestick chart.
func buildMonthlyCandleAltText(r monthlyReport) string {
	avg := meanDailyAverage(r.Days)
	hi, lo := happiestUnhappiest(r.Days)
	w := widestSwing(r.Days)
	text := fmt.Sprintf("Candlestick chart of Bluesky net sentiment for %s, one candle per day over %d days. "+
		"Whiskers show each day's hourly range, boxes the middle half and a tick the median. Month average %s, %s",
		r.monthLabel(), len(r.Days), signedPct(avg), zonePhrase(avg))
	if r.hasPrev() {
		text += fmt.Sprintf(", %s vs %s", signedPoints(avg-meanDailyAverage(r.PrevDays)), r.prevName())
	}
	text += fmt.Sprintf(". Best day %s at %s; worst day %s at %s; widest hourly swing %s, %.1f points.",
		dayLabel(r.Days[hi].Date), signedPct(r.Days[hi].AverageSentiment),
		dayLabel(r.Days[lo].Date), signedPct(r.Days[lo].AverageSentiment),
		dayLabel(r.Days[w].Date), r.Days[w].MaxSentiment-r.Days[w].MinSentiment)
	return text
}

// buildMonthlyVolumeAltText describes the volume chart.
func buildMonthlyVolumeAltText(r monthlyReport) string {
	total := sumPosts(r.Days)
	hi, lo := busiestQuietest(r.Days)
	fh := r.firehoseTotal()
	text := fmt.Sprintf("Line chart of daily English posts analysed on Bluesky in %s", r.monthLabel())
	if r.Languages != nil {
		text += ", over a stacked area of the full firehose by language"
	} else if fh > 0 {
		text += ", with the full firehose as a softer line behind it"
	}
	text += fmt.Sprintf(". %s English posts over %d days, %s per day on average",
		compactCount(total), len(r.Days), compactCount(total/len(r.Days)))
	if d, ok := r.volumeDelta(); ok {
		text += fmt.Sprintf(", %+.1f%% vs %s", d, r.prevName())
	}
	text += fmt.Sprintf(". Busiest %s with %s; quietest %s with %s.",
		dayLabel(r.Days[hi].Date), compactCount(r.Days[hi].TotalPosts),
		dayLabel(r.Days[lo].Date), compactCount(r.Days[lo].TotalPosts))
	if fh >= total && fh > 0 {
		text += fmt.Sprintf(" English posts were %.0f%% of the %s firehose.", float64(total)/float64(fh)*100, compactCount(fh))
	}
	if langs := r.languageLine(); langs != "" {
		text += " The firehose is stacked by language behind the line; the largest after English were " + langs + "."
	}
	return text
}

// toDailyCandles maps daily rows onto the candlestick generator's input.
func toDailyCandles(days []store.DailySentimentDataPoint) []sparkline.DailyCandle {
	out := make([]sparkline.DailyCandle, 0, len(days))
	for _, d := range days {
		t, err := time.Parse(dateFormat, d.Date)
		if err != nil {
			continue
		}
		out = append(out, sparkline.DailyCandle{
			Date: t, Min: d.MinSentiment, Q1: d.Q1Sentiment, Median: d.MedianSentiment,
			Q3: d.Q3Sentiment, Max: d.MaxSentiment, Average: d.AverageSentiment, Runs: d.TotalRuns,
		})
	}
	return out
}

// toDailyVolumePoints maps daily rows onto the volume generator's input.
func toDailyVolumePoints(r monthlyReport) []sparkline.DailyVolumePoint {
	out := make([]sparkline.DailyVolumePoint, 0, len(r.Days))
	for _, d := range r.Days {
		t, err := time.Parse(dateFormat, d.Date)
		if err != nil {
			continue
		}
		out = append(out, sparkline.DailyVolumePoint{Date: t, ENPosts: d.TotalPosts, TotalPosts: r.Firehose[d.Date], Languages: r.Languages[d.Date]})
	}
	return out
}

// firehoseCountV2Key records the first full UTC day on which the firehose
// total counted every post create, including the ones the consumer's
// pre-filter drops. Earlier days hold undercounts (English plus untagged
// only), so a month that starts before this date is shown without a
// firehose line rather than with a line that steps up mid-month.
const firehoseCountV2Key = "firehose_count_v2_since"

// recordFirehoseCountCutover stores the cutover date on the first start of
// a build that counts the full firehose. Idempotent.
func recordFirehoseCountCutover(ctx context.Context, db *store.Store, now time.Time) {
	if v, _ := db.GetKeyValue(ctx, firehoseCountV2Key); v != "" {
		return
	}
	since := utcDate(now).AddDate(0, 0, 1).Format(dateFormat)
	if err := db.SetKeyValue(ctx, firehoseCountV2Key, since); err != nil {
		slog.Warn("record firehose count cutover failed", "error", err)
		return
	}
	slog.Info("firehose count cutover recorded", "since", since)
}

// firehoseCountedFully reports whether every day of the month lies on or
// after the cutover to counting the full firehose.
func firehoseCountedFully(ctx context.Context, db *store.Store, r monthlyReport) bool {
	since, _ := db.GetKeyValue(ctx, firehoseCountV2Key)
	if since == "" || r.First.Format(dateFormat) < since {
		slog.Info("monthly report: month predates the full firehose count, showing English only",
			"month", r.First.Format("2006-01"), "cutover", since)
		return false
	}
	return true
}

// completeFirehose returns each day's firehose total when every day of the
// month has one, and nil otherwise. The daily cycle's backfill is the only
// source: reading sentiment_history directly here would mix filtered and
// unfiltered cycle sets.
func completeFirehose(r monthlyReport) map[string]int {
	totals := map[string]int{}
	for _, d := range r.Days {
		if d.TotalFirehosePosts <= 0 {
			slog.Info("monthly report: firehose totals incomplete, showing English only",
				"month", r.First.Format("2006-01"), "first_missing", d.Date)
			return nil
		}
		totals[d.Date] = d.TotalFirehosePosts
	}
	return totals
}

// loadMonthlyReport gathers the previous month's data. ok is false, with the
// reason logged, when there is not enough to report on.
func loadMonthlyReport(ctx context.Context, db *store.Store, now time.Time) (monthlyReport, bool) {
	first, last := previousMonth(now)
	r := monthlyReport{First: first, Last: last}

	days, err := db.GetDailySentimentRange(ctx, first.Format(dateFormat), last.Format(dateFormat))
	if err != nil {
		slog.Error("monthly report: get daily range failed", "error", err)
		return r, false
	}
	if len(days) < monthlyReportMinDays {
		slog.Info("monthly report: not enough daily rows, skipping",
			"month", first.Format("2006-01"), "days", len(days), "min", monthlyReportMinDays)
		return r, false
	}
	r.Days = days

	prevFirst, prevLast := previousMonth(first)
	if prev, err := db.GetDailySentimentRange(ctx, prevFirst.Format(dateFormat), prevLast.Format(dateFormat)); err != nil {
		slog.Warn("monthly report: get previous month failed, omitting deltas", "error", err)
	} else {
		r.PrevDays = prev
	}
	if firehoseCountedFully(ctx, db, r) {
		r.Firehose = completeFirehose(r)
	}
	r.Languages = completeLanguages(ctx, db, r)
	return r, true
}

// completeLanguages returns each day's language split when every day of the
// month has one, and nil otherwise (the chart then stays on its line view).
func completeLanguages(ctx context.Context, db *store.Store, r monthlyReport) map[string]map[string]int {
	rows, err := db.GetLanguageDailyRange(ctx, r.First.Format(dateFormat), r.Last.Format(dateFormat))
	if err != nil {
		slog.Warn("monthly report: language rows failed, showing single line", "error", err)
		return nil
	}
	byDay := map[string]map[string]int{}
	for _, row := range rows {
		if byDay[row.Date] == nil {
			byDay[row.Date] = map[string]int{}
		}
		byDay[row.Date][row.Lang] += row.Count
	}
	for _, d := range r.Days {
		if len(byDay[d.Date]) == 0 {
			slog.Info("monthly report: language split incomplete, showing single line",
				"month", r.First.Format("2006-01"), "first_missing", d.Date, "days_with_data", len(byDay))
			return nil
		}
		// A day whose split covers only part of its cycles (a mid-day
		// deploy, a failed store) would draw a false dip under the line.
		if fh := r.Firehose[d.Date]; fh > 0 {
			sum := 0
			for _, n := range byDay[d.Date] {
				sum += n
			}
			if float64(sum) < 0.9*float64(fh) {
				slog.Info("monthly report: language split partial for a day, showing single line",
					"month", r.First.Format("2006-01"), "date", d.Date, "split_total", sum, "firehose", fh)
				return nil
			}
		}
	}
	return byDay
}

// runMonthlyReport posts the candlestick root and the volume reply for the
// previous calendar month. The guard key is set once the root is posted.
func runMonthlyReport(ctx context.Context, db *store.Store, handle, password string, dryRun bool, now time.Time) {
	first, _ := previousMonth(now)
	monthKey := first.Format("2006-01")
	if last, _ := db.GetKeyValue(ctx, monthlyReportGuardKey); last == monthKey {
		slog.Info("monthly report already posted", "month", monthKey)
		return
	}

	r, ok := loadMonthlyReport(ctx, db, now)
	if !ok {
		return
	}

	prevAvg := math.NaN()
	if r.hasPrev() {
		prevAvg = meanDailyAverage(r.PrevDays)
	}
	candlePNG, err := sparkline.NewMonthlyCandleGenerator().GenerateMonthlyCandleChart(toDailyCandles(r.Days),
		sparkline.MonthlyCandleMeta{MonthLabel: r.monthLabel(), PrevMonthAvg: prevAvg, PrevLabel: r.prevName()})
	if err != nil {
		slog.Error("generate monthly candle chart failed", "error", err)
		return
	}
	prevEN := 0
	if r.hasPrev() {
		prevEN = sumPosts(r.PrevDays)
	}
	volumePNG, err := sparkline.NewMonthlyVolumeGenerator().GenerateMonthlyVolumeChart(toDailyVolumePoints(r),
		sparkline.MonthlyVolumeMeta{MonthLabel: r.monthLabel(), PrevMonthEN: prevEN, PrevLabel: r.prevName()})
	if err != nil {
		slog.Error("generate monthly volume chart failed", "error", err)
		return
	}

	moodText, volumeText := buildMonthlyMoodText(r), buildMonthlyVolumeText(r)
	candleAlt, volumeAlt := buildMonthlyCandleAltText(r), buildMonthlyVolumeAltText(r)
	for name, text := range map[string]string{"mood": moodText, "volume": volumeText} {
		if n := postLength(text); n > blueskyPostLimit {
			slog.Error("monthly report text over limit, skipping", "post", name, "length", n)
			return
		}
	}

	if dryRun {
		slog.Info("DRY_RUN: would post monthly report",
			"month", monthKey, "days", len(r.Days), "prev_days", len(r.PrevDays),
			"firehose_complete", r.Firehose != nil, "languages_complete", r.Languages != nil,
			"candle_bytes", len(candlePNG), "volume_bytes", len(volumePNG),
			"mood_text", moodText, "mood_length", postLength(moodText),
			"volume_text", volumeText, "volume_length", postLength(volumeText),
			"candle_alt", candleAlt, "volume_alt", volumeAlt)
		return
	}

	apiCtx, apiCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer apiCancel()

	bskyClient := client.New(handle, password)
	if err := bskyClient.Authenticate(); err != nil {
		slog.Error("bluesky auth for monthly report failed", "error", err)
		return
	}

	rootURI, rootCID, err := bskyClient.PostWithImage(apiCtx, moodText, candlePNG, candleAlt)
	if err != nil {
		slog.Error("post monthly candle chart failed", "error", err)
		return
	}
	slog.Info("monthly report posted", "month", monthKey, "uri", rootURI)
	if err := db.SetKeyValue(ctx, monthlyReportGuardKey, monthKey); err != nil {
		slog.Error("persist monthly report guard failed", "error", err, "month", monthKey)
	}

	if _, _, err := bskyClient.PostWithImageAsReply(apiCtx, volumeText, volumePNG, volumeAlt, rootURI, rootCID, rootURI, rootCID); err != nil {
		slog.Warn("post monthly volume chart failed", "error", err)
		return
	}
	slog.Info("monthly volume reply posted", "month", monthKey)
}
