package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// --- analyzeSentimentExtremes ---

func TestAnalyzeSentimentExtremes_TooFewPoints(t *testing.T) {
	h := &SparklinePosterHandler{}
	result := h.analyzeSentimentExtremes([]state.SentimentDataPoint{
		{NetSentimentPercent: 10.0},
	})
	if result != "" {
		t.Errorf("expected empty string for <2 data points, got %q", result)
	}
}

func TestAnalyzeSentimentExtremes_LatestIsLowest(t *testing.T) {
	h := &SparklinePosterHandler{}
	points := []state.SentimentDataPoint{
		{NetSentimentPercent: 20.0},
		{NetSentimentPercent: 15.0},
		{NetSentimentPercent: 5.0}, // Latest and lowest
	}
	result := h.analyzeSentimentExtremes(points)
	if result != "* Lowest sentiment for the charted period" {
		t.Errorf("got %q", result)
	}
}

func TestAnalyzeSentimentExtremes_LatestIsHighest(t *testing.T) {
	h := &SparklinePosterHandler{}
	points := []state.SentimentDataPoint{
		{NetSentimentPercent: 5.0},
		{NetSentimentPercent: 10.0},
		{NetSentimentPercent: 20.0}, // Latest and highest
	}
	result := h.analyzeSentimentExtremes(points)
	if result != "* Highest sentiment for the charted period" {
		t.Errorf("got %q", result)
	}
}

func TestAnalyzeSentimentExtremes_LatestIsNeither(t *testing.T) {
	h := &SparklinePosterHandler{}
	points := []state.SentimentDataPoint{
		{NetSentimentPercent: 5.0},
		{NetSentimentPercent: 20.0},
		{NetSentimentPercent: 12.0}, // Latest, but neither highest nor lowest
	}
	result := h.analyzeSentimentExtremes(points)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

// --- calculateSentimentStats ---

func TestCalculateSentimentStats_Empty(t *testing.T) {
	h := &SparklinePosterHandler{}
	stats := h.calculateSentimentStats(nil)
	if stats.Current != 0 || stats.Highest != 0 || stats.Lowest != 0 || stats.Average != 0 {
		t.Errorf("empty input should return zero stats, got %+v", stats)
	}
}

func TestCalculateSentimentStats_SinglePoint(t *testing.T) {
	h := &SparklinePosterHandler{}
	ts := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	stats := h.calculateSentimentStats([]state.SentimentDataPoint{
		{NetSentimentPercent: 15.0, Timestamp: ts},
	})
	if stats.Current != 15.0 {
		t.Errorf("Current: got %f, want 15.0", stats.Current)
	}
	if stats.Highest != 15.0 {
		t.Errorf("Highest: got %f, want 15.0", stats.Highest)
	}
	if stats.Lowest != 15.0 {
		t.Errorf("Lowest: got %f, want 15.0", stats.Lowest)
	}
	if stats.Average != 15.0 {
		t.Errorf("Average: got %f, want 15.0", stats.Average)
	}
}

func TestCalculateSentimentStats_Multiple(t *testing.T) {
	h := &SparklinePosterHandler{}
	ts1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	ts3 := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)

	points := []state.SentimentDataPoint{
		{NetSentimentPercent: 10.0, Timestamp: ts1},
		{NetSentimentPercent: 30.0, Timestamp: ts2},
		{NetSentimentPercent: 20.0, Timestamp: ts3},
	}
	stats := h.calculateSentimentStats(points)

	if stats.Current != 20.0 {
		t.Errorf("Current: got %f, want 20.0 (last point)", stats.Current)
	}
	if stats.Highest != 30.0 {
		t.Errorf("Highest: got %f, want 30.0", stats.Highest)
	}
	if stats.Lowest != 10.0 {
		t.Errorf("Lowest: got %f, want 10.0", stats.Lowest)
	}
	expectedAvg := (10.0 + 30.0 + 20.0) / 3.0
	if stats.Average != expectedAvg {
		t.Errorf("Average: got %f, want %f", stats.Average, expectedAvg)
	}
	// Trend = last - first = 20.0 - 10.0 = 10.0
	if stats.Trend != 10.0 {
		t.Errorf("Trend: got %f, want 10.0", stats.Trend)
	}
}

// --- generateDetailedAltText ---

func TestGenerateDetailedAltText_TooFewPoints(t *testing.T) {
	h := &SparklinePosterHandler{}
	result := h.generateDetailedAltText([]state.SentimentDataPoint{
		{NetSentimentPercent: 10.0},
	})
	if !strings.Contains(result, "Seven day sentiment trend chart") {
		t.Errorf("expected default text, got %q", result)
	}
}

func TestGenerateDetailedAltText_ContainsStats(t *testing.T) {
	h := &SparklinePosterHandler{}
	ts1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)
	ts3 := time.Date(2025, 6, 3, 0, 0, 0, 0, time.UTC)
	points := []state.SentimentDataPoint{
		{NetSentimentPercent: 10.0, Timestamp: ts1},
		{NetSentimentPercent: 20.0, Timestamp: ts2},
		{NetSentimentPercent: 15.0, Timestamp: ts3},
	}
	result := h.generateDetailedAltText(points)

	checks := []string{
		"Current sentiment:",
		"Highest sentiment:",
		"Lowest sentiment:",
		"Average sentiment:",
	}
	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("alt text missing %q", check)
		}
	}
}

func TestGenerateDetailedAltText_TrendPositive(t *testing.T) {
	h := &SparklinePosterHandler{}
	ts1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)
	points := []state.SentimentDataPoint{
		{NetSentimentPercent: 5.0, Timestamp: ts1},
		{NetSentimentPercent: 20.0, Timestamp: ts2},
	}
	result := h.generateDetailedAltText(points)
	if !strings.Contains(result, "Trending positive") {
		t.Errorf("expected 'Trending positive', got %q", result)
	}
}

func TestGenerateDetailedAltText_TrendNegative(t *testing.T) {
	h := &SparklinePosterHandler{}
	ts1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)
	points := []state.SentimentDataPoint{
		{NetSentimentPercent: 20.0, Timestamp: ts1},
		{NetSentimentPercent: 5.0, Timestamp: ts2},
	}
	result := h.generateDetailedAltText(points)
	if !strings.Contains(result, "Trending negative") {
		t.Errorf("expected 'Trending negative', got %q", result)
	}
}

// --- JSON ---

func TestStepFunctionsEvent_JSON(t *testing.T) {
	jsonData := `{"runId": "run-abc", "analysisIntervalMinutes": 30, "status": "complete"}`
	var event StepFunctionsEvent
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if event.RunID != "run-abc" {
		t.Errorf("RunID: got %q, want %q", event.RunID, "run-abc")
	}
	if event.AnalysisIntervalMinutes != 30 {
		t.Errorf("AnalysisIntervalMinutes: got %d, want 30", event.AnalysisIntervalMinutes)
	}
}

func TestSparklineResponse_JSON(t *testing.T) {
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
