package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// ---------------------------------------------------------------------------
// Yearly posting (1st of each month)
// ---------------------------------------------------------------------------

func runYearlyPosting(ctx context.Context, db *store.Store, handle, password string, dryRun bool) {
	slog.Info("yearly posting check triggered")

	yearlyData, err := db.GetYearlySentimentData(ctx)
	if err != nil {
		slog.Error("get yearly sentiment data failed", "error", err)
		return
	}
	if len(yearlyData) < 7 {
		slog.Info("insufficient data for yearly chart", "days", len(yearlyData))
		return
	}

	statePoints := toStateYearlyPoints(yearlyData)

	gen := sparkline.NewYearlySparklineGenerator(nil)
	imgData, err := gen.GenerateYearlySentimentSparkline(statePoints)
	if err != nil {
		slog.Error("generate yearly sparkline failed", "error", err)
		return
	}

	postText := buildYearlyPostText(statePoints)
	altText := buildYearlyAltText(statePoints)

	if dryRun {
		slog.Info("DRY_RUN: would post yearly charts", "days", len(yearlyData), "image_bytes", len(imgData))
		return
	}

	apiCtx, apiCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer apiCancel()

	bskyClient := client.New(handle, password)
	if err := bskyClient.Authenticate(); err != nil {
		slog.Error("bluesky auth for yearly post failed", "error", err)
		return
	}

	eventDates := buildEventDates(statePoints)
	facets := client.CreateWikipediaLinkFacets(postText, eventDates...)

	var sentimentURI, sentimentCID string
	if len(facets) > 0 {
		sentimentURI, sentimentCID, err = bskyClient.PostWithImage(apiCtx, postText, imgData, altText, facets)
	} else {
		sentimentURI, sentimentCID, err = bskyClient.PostWithImage(apiCtx, postText, imgData, altText)
	}
	if err != nil {
		slog.Error("post yearly sentiment chart failed", "error", err)
		return
	}
	slog.Info("yearly sentiment chart posted", "uri", sentimentURI)

	if err := db.SetKeyValue(ctx, "yearly_post_uri", sentimentURI); err != nil {
		slog.Warn("persist yearly post URI failed", "error", err)
	}
	if err := db.SetKeyValue(ctx, "yearly_post_cid", sentimentCID); err != nil {
		slog.Warn("persist yearly post CID failed", "error", err)
	}

	if err := bskyClient.PinPost(apiCtx, sentimentURI, sentimentCID); err != nil {
		slog.Warn("pin yearly post failed", "error", err)
	}
}

func toStateYearlyPoints(points []store.YearlySparklineDataPoint) []state.YearlySparklineDataPoint {
	out := make([]state.YearlySparklineDataPoint, len(points))
	for i, p := range points {
		out[i] = state.YearlySparklineDataPoint{
			Date:                p.Date,
			AverageSentiment:    p.AverageSentiment,
			MinSentiment:        p.MinSentiment,
			MaxSentiment:        p.MaxSentiment,
			Q1Sentiment:         p.Q1Sentiment,
			MedianSentiment:     p.MedianSentiment,
			Q3Sentiment:         p.Q3Sentiment,
			Timestamp:           p.Timestamp,
			NetSentimentPercent: p.NetSentimentPercent,
		}
	}
	return out
}

func buildYearlyPostText(points []state.YearlySparklineDataPoint) string {
	if len(points) == 0 {
		return "Bluesky Sentiment"
	}
	startDate := points[0].Timestamp.Format("2006-01-02")
	endDate := points[len(points)-1].Timestamp.Format("2006-01-02")
	text := fmt.Sprintf("Bluesky Sentiment %s - %s", startDate, endDate)

	var minSent, maxSent float64
	var minDate, maxDate string
	minSent = points[0].AverageSentiment
	maxSent = points[0].AverageSentiment
	for _, p := range points {
		if p.AverageSentiment < minSent {
			minSent = p.AverageSentiment
			minDate = p.Date
		}
		if p.AverageSentiment > maxSent {
			maxSent = p.AverageSentiment
			maxDate = p.Date
		}
	}

	var extremes []string
	if t, err := time.Parse("2006-01-02", minDate); err == nil {
		extremes = append(extremes, fmt.Sprintf("Lowest: %.1f%% %s events", minSent, t.Format("Jan 2")))
	}
	if t, err := time.Parse("2006-01-02", maxDate); err == nil {
		extremes = append(extremes, fmt.Sprintf("Highest: %.1f%% %s events", maxSent, t.Format("Jan 2")))
	}
	if len(extremes) > 0 {
		text += "\n\n" + strings.Join(extremes, "\n")
	}
	return text
}

func buildYearlyAltText(points []state.YearlySparklineDataPoint) string {
	if len(points) == 0 {
		return "Yearly Bluesky sentiment chart"
	}
	var sum, minS, maxS float64
	var minDate, maxDate string
	minS = points[0].AverageSentiment
	maxS = points[0].AverageSentiment
	for _, p := range points {
		sum += p.AverageSentiment
		if p.AverageSentiment < minS {
			minS = p.AverageSentiment
			minDate = p.Date
		}
		if p.AverageSentiment > maxS {
			maxS = p.AverageSentiment
			maxDate = p.Date
		}
	}
	avg := sum / float64(len(points))
	latest := points[len(points)-1]
	return fmt.Sprintf("Yearly Bluesky sentiment trend. Current: %.1f%% (%s). High: %.1f%% (%s). Low: %.1f%% (%s). Average: %.1f%%.",
		latest.AverageSentiment, latest.Date, maxS, maxDate, minS, minDate, avg)
}

func buildEventDates(points []state.YearlySparklineDataPoint) []client.EventDate {
	if len(points) == 0 {
		return nil
	}
	var minSent, maxSent float64
	var minDate, maxDate string
	minSent = points[0].AverageSentiment
	maxSent = points[0].AverageSentiment
	for _, p := range points {
		if p.AverageSentiment < minSent {
			minSent = p.AverageSentiment
			minDate = p.Date
		}
		if p.AverageSentiment > maxSent {
			maxSent = p.AverageSentiment
			maxDate = p.Date
		}
	}
	_ = minSent
	_ = maxSent

	var dates []client.EventDate
	if t, err := time.Parse("2006-01-02", minDate); err == nil {
		dates = append(dates, client.EventDate{DisplayText: t.Format("Jan 2"), FullDate: minDate})
	}
	if t, err := time.Parse("2006-01-02", maxDate); err == nil {
		dates = append(dates, client.EventDate{DisplayText: t.Format("Jan 2"), FullDate: maxDate})
	}
	return dates
}
