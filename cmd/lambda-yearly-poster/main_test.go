package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// --- analyzeYearlySentimentExtremes ---

func TestAnalyzeYearlySentimentExtremes_TooFewPoints(t *testing.T) {
	h := &YearlyPosterHandler{}
	// Less than 30 data points → empty result
	points := make([]state.YearlySparklineDataPoint, 20)
	result := h.analyzeYearlySentimentExtremes(points)
	if result.Message != "" {
		t.Errorf("expected empty message for <30 points, got %q", result.Message)
	}
}

func makeYearlyData(count int, avgSentiment float64) []state.YearlySparklineDataPoint {
	points := make([]state.YearlySparklineDataPoint, count)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range points {
		date := base.AddDate(0, 0, i)
		points[i] = state.YearlySparklineDataPoint{
			Date:             date.Format("2006-01-02"),
			AverageSentiment: avgSentiment,
			Timestamp:        date,
		}
	}
	return points
}

func TestAnalyzeYearlySentimentExtremes_AboveAverage(t *testing.T) {
	h := &YearlyPosterHandler{}
	// 30 points at 10.0, then set last to 20.0 (>avg+5 triggers "above")
	points := makeYearlyData(30, 10.0)
	points[29].AverageSentiment = 20.0
	result := h.analyzeYearlySentimentExtremes(points)
	if !strings.Contains(result.Message, "Currently above yearly average") {
		t.Errorf("expected above average message, got %q", result.Message)
	}
}

func TestAnalyzeYearlySentimentExtremes_BelowAverage(t *testing.T) {
	h := &YearlyPosterHandler{}
	points := makeYearlyData(30, 20.0)
	points[29].AverageSentiment = 5.0
	result := h.analyzeYearlySentimentExtremes(points)
	if !strings.Contains(result.Message, "Currently below yearly average") {
		t.Errorf("expected below average message, got %q", result.Message)
	}
}

func TestAnalyzeYearlySentimentExtremes_IncludesLowestHighest(t *testing.T) {
	h := &YearlyPosterHandler{}
	points := makeYearlyData(35, 12.0)
	// Set specific min and max
	points[5].AverageSentiment = 2.0   // Lowest
	points[15].AverageSentiment = 25.0 // Highest
	result := h.analyzeYearlySentimentExtremes(points)
	if !strings.Contains(result.Message, "Lowest:") {
		t.Errorf("expected Lowest in message, got %q", result.Message)
	}
	if !strings.Contains(result.Message, "Highest:") {
		t.Errorf("expected Highest in message, got %q", result.Message)
	}
}

func TestAnalyzeYearlySentimentExtremes_EventDates(t *testing.T) {
	h := &YearlyPosterHandler{}
	points := makeYearlyData(35, 12.0)
	points[5].AverageSentiment = 2.0
	points[15].AverageSentiment = 25.0
	result := h.analyzeYearlySentimentExtremes(points)
	if len(result.EventDates) != 2 {
		t.Errorf("expected 2 event dates (min + max), got %d", len(result.EventDates))
	}
}

// --- calculateYearlySentimentStats ---

func TestCalculateYearlySentimentStats_Empty(t *testing.T) {
	h := &YearlyPosterHandler{}
	stats := h.calculateYearlySentimentStats(nil)
	if stats.Current != 0 || stats.Highest != 0 || stats.Lowest != 0 {
		t.Errorf("expected zero stats for empty input, got %+v", stats)
	}
}

func TestCalculateYearlySentimentStats_SinglePoint(t *testing.T) {
	h := &YearlyPosterHandler{}
	points := []state.YearlySparklineDataPoint{
		{Date: "2025-06-01", AverageSentiment: 15.0},
	}
	stats := h.calculateYearlySentimentStats(points)
	if stats.Current != 15.0 {
		t.Errorf("Current: got %f, want 15.0", stats.Current)
	}
	if stats.Highest != 15.0 {
		t.Errorf("Highest: got %f, want 15.0", stats.Highest)
	}
	if stats.Lowest != 15.0 {
		t.Errorf("Lowest: got %f, want 15.0", stats.Lowest)
	}
}

func TestCalculateYearlySentimentStats_Multiple(t *testing.T) {
	h := &YearlyPosterHandler{}
	points := []state.YearlySparklineDataPoint{
		{Date: "2025-01-01", AverageSentiment: 5.0},
		{Date: "2025-06-01", AverageSentiment: 25.0},
		{Date: "2025-12-01", AverageSentiment: 15.0},
	}
	stats := h.calculateYearlySentimentStats(points)

	if stats.Current != 15.0 {
		t.Errorf("Current: got %f, want 15.0 (last point)", stats.Current)
	}
	if stats.CurrentDate != "2025-12-01" {
		t.Errorf("CurrentDate: got %q, want %q", stats.CurrentDate, "2025-12-01")
	}
	if stats.Highest != 25.0 {
		t.Errorf("Highest: got %f, want 25.0", stats.Highest)
	}
	if stats.HighestDate != "2025-06-01" {
		t.Errorf("HighestDate: got %q, want %q", stats.HighestDate, "2025-06-01")
	}
	if stats.Lowest != 5.0 {
		t.Errorf("Lowest: got %f, want 5.0", stats.Lowest)
	}
	if stats.LowestDate != "2025-01-01" {
		t.Errorf("LowestDate: got %q, want %q", stats.LowestDate, "2025-01-01")
	}
	expectedAvg := (5.0 + 25.0 + 15.0) / 3.0
	if stats.Average != expectedAvg {
		t.Errorf("Average: got %f, want %f", stats.Average, expectedAvg)
	}
	// Trend = last - first = 15.0 - 5.0 = 10.0
	if stats.Trend != 10.0 {
		t.Errorf("Trend: got %f, want 10.0", stats.Trend)
	}
}

// --- generateYearlyAltText ---

func TestGenerateYearlyAltText_TooFewPoints(t *testing.T) {
	h := &YearlyPosterHandler{}
	result := h.generateYearlyAltText([]state.YearlySparklineDataPoint{
		{Date: "2025-01-01", AverageSentiment: 10.0},
	})
	if !strings.Contains(result, "Yearly sentiment trend chart") {
		t.Errorf("expected default text, got %q", result)
	}
}

func TestGenerateYearlyAltText_ContainsStats(t *testing.T) {
	h := &YearlyPosterHandler{}
	points := []state.YearlySparklineDataPoint{
		{Date: "2025-01-01", AverageSentiment: 10.0},
		{Date: "2025-06-01", AverageSentiment: 20.0},
		{Date: "2025-12-01", AverageSentiment: 15.0},
	}
	result := h.generateYearlyAltText(points)

	checks := []string{
		"Current sentiment:",
		"Highest sentiment:",
		"Lowest sentiment:",
		"Yearly average sentiment:",
	}
	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("alt text missing %q in %q", check, result)
		}
	}
}

func TestGenerateYearlyAltText_TrendPositive(t *testing.T) {
	h := &YearlyPosterHandler{}
	points := []state.YearlySparklineDataPoint{
		{Date: "2025-01-01", AverageSentiment: 5.0},
		{Date: "2025-12-01", AverageSentiment: 20.0},
	}
	result := h.generateYearlyAltText(points)
	if !strings.Contains(result, "Trending positive") {
		t.Errorf("expected positive trend, got %q", result)
	}
}

func TestGenerateYearlyAltText_TrendNegative(t *testing.T) {
	h := &YearlyPosterHandler{}
	points := []state.YearlySparklineDataPoint{
		{Date: "2025-01-01", AverageSentiment: 20.0},
		{Date: "2025-12-01", AverageSentiment: 5.0},
	}
	result := h.generateYearlyAltText(points)
	if !strings.Contains(result, "Trending negative") {
		t.Errorf("expected negative trend, got %q", result)
	}
}

func TestGenerateYearlyAltText_StableTrend(t *testing.T) {
	h := &YearlyPosterHandler{}
	points := []state.YearlySparklineDataPoint{
		{Date: "2025-01-01", AverageSentiment: 10.0},
		{Date: "2025-12-01", AverageSentiment: 10.0},
	}
	result := h.generateYearlyAltText(points)
	if !strings.Contains(result, "Stable sentiment") {
		t.Errorf("expected stable trend, got %q", result)
	}
}

// --- JSON ---

func TestYearlyEvent_JSON(t *testing.T) {
	jsonData := `{"source": "aws.events", "time": "2025-01-01T01:00:00Z", "action": "post"}`
	var event Event
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if event.Source != "aws.events" {
		t.Errorf("Source: got %q, want %q", event.Source, "aws.events")
	}
	if event.Action != "post" {
		t.Errorf("Action: got %q, want %q", event.Action, "post")
	}
}

func TestYearlyResponse_JSON(t *testing.T) {
	resp := Response{StatusCode: 200, Body: "ok", Posted: true}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if !decoded.Posted {
		t.Error("Posted should be true")
	}
}
