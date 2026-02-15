package main

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/state"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func TestMean(t *testing.T) {
	tests := []struct {
		name string
		vals []float64
		want float64
	}{
		{"normal values", []float64{1, 2, 3, 4, 5}, 3.0},
		{"empty slice", []float64{}, 0},
		{"single value", []float64{42}, 42},
		{"negative values", []float64{-10, -20, -30}, -20},
		{"mixed values", []float64{-1, 0, 1}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mean(tt.vals)
			if got != tt.want {
				t.Errorf("mean(%v) = %v, want %v", tt.vals, got, tt.want)
			}
		})
	}
}

func TestPercentile(t *testing.T) {
	tests := []struct {
		name   string
		sorted []float64
		p      float64
		want   float64
	}{
		{"empty slice", []float64{}, 0.5, 0},
		{"single value", []float64{42}, 0.5, 42},
		{"p=0 returns min", []float64{1, 2, 3, 4, 5}, 0, 1},
		{"p=1 returns max", []float64{1, 2, 3, 4, 5}, 1, 5},
		{"p=0.5 returns median", []float64{1, 2, 3, 4, 5}, 0.5, 3},
		{"interpolation", []float64{10, 20}, 0.5, 15},
		{"quarter interpolation", []float64{0, 100}, 0.25, 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := percentile(tt.sorted, tt.p)
			if math.Abs(got-tt.want) > 0.001 {
				t.Errorf("percentile(%v, %v) = %v, want %v", tt.sorted, tt.p, got, tt.want)
			}
		})
	}
}

func TestIsEnglish(t *testing.T) {
	tests := []struct {
		name  string
		langs []string
		want  bool
	}{
		{"contains en", []string{"en"}, true},
		{"contains en-US", []string{"en-US"}, true},
		{"contains en-GB", []string{"fr", "en-GB"}, true},
		{"no english", []string{"fr", "de"}, false},
		{"empty slice", []string{}, false},
		{"nil slice", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEnglish(tt.langs)
			if got != tt.want {
				t.Errorf("isEnglish(%v) = %v, want %v", tt.langs, got, tt.want)
			}
		})
	}
}

func TestNormalizeTimestamp(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			"valid RFC3339",
			"2026-01-15T10:30:00Z",
			"2026-01-15T10:30:00Z",
		},
		{
			"valid RFC3339 with timezone offset",
			"2026-01-15T10:30:00+05:00",
			"2026-01-15T05:30:00Z",
		},
		{
			"valid RFC3339Nano",
			"2026-01-15T10:30:00.123456789Z",
			"2026-01-15T10:30:00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeTimestamp(tt.raw)
			if got != tt.want {
				t.Errorf("normalizeTimestamp(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}

	t.Run("invalid format returns current time", func(t *testing.T) {
		before := time.Now().UTC().Add(-1 * time.Second)
		got := normalizeTimestamp("not-a-timestamp")
		after := time.Now().UTC().Add(1 * time.Second)

		parsed, err := time.Parse(time.RFC3339, got)
		if err != nil {
			t.Fatalf("normalizeTimestamp returned unparseable time: %q", got)
		}
		if parsed.Before(before) || parsed.After(after) {
			t.Errorf("normalizeTimestamp(invalid) = %q, expected time near now", got)
		}
	})
}

func TestEnvOr(t *testing.T) {
	t.Run("returns env value when set", func(t *testing.T) {
		t.Setenv("TEST_ENV_OR_KEY", "from-env")
		got := envOr("TEST_ENV_OR_KEY", "fallback")
		if got != "from-env" {
			t.Errorf("envOr() = %q, want %q", got, "from-env")
		}
	})

	t.Run("returns fallback when unset", func(t *testing.T) {
		got := envOr("TEST_ENV_OR_UNSET_KEY_12345", "fallback")
		if got != "fallback" {
			t.Errorf("envOr() = %q, want %q", got, "fallback")
		}
	})
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		set      bool
		fallback bool
		want     bool
	}{
		{"true", "true", true, false, true},
		{"TRUE", "TRUE", true, false, true},
		{"1", "1", true, false, true},
		{"false", "false", true, true, false},
		{"0", "0", true, true, false},
		{"random string", "maybe", true, false, false},
		{"unset uses fallback true", "", false, true, true},
		{"unset uses fallback false", "", false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_ENV_BOOL_" + tt.name
			if tt.set {
				t.Setenv(key, tt.value)
			}
			got := envBool(key, tt.fallback)
			if got != tt.want {
				t.Errorf("envBool(%q, %v) = %v, want %v", key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestEnvInt(t *testing.T) {
	t.Run("valid int", func(t *testing.T) {
		t.Setenv("TEST_ENV_INT", "42")
		got := envInt("TEST_ENV_INT", 0)
		if got != 42 {
			t.Errorf("envInt() = %d, want 42", got)
		}
	})

	t.Run("unset uses fallback", func(t *testing.T) {
		got := envInt("TEST_ENV_INT_UNSET_12345", 99)
		if got != 99 {
			t.Errorf("envInt() = %d, want 99", got)
		}
	})

	t.Run("non-numeric uses fallback", func(t *testing.T) {
		t.Setenv("TEST_ENV_INT_BAD", "abc")
		got := envInt("TEST_ENV_INT_BAD", 55)
		if got != 55 {
			t.Errorf("envInt() = %d, want 55", got)
		}
	})
}

func TestCalculateOverallSentiment(t *testing.T) {
	tests := []struct {
		name     string
		posts    []analyzer.AnalyzedPost
		wantCat  string
		wantSign int
	}{
		{
			"empty posts",
			nil,
			"neutral", 0,
		},
		{
			"all positive",
			[]analyzer.AnalyzedPost{
				{SentimentScore: 0.8},
				{SentimentScore: 0.6},
			},
			"positive", 1,
		},
		{
			"all negative",
			[]analyzer.AnalyzedPost{
				{SentimentScore: -0.8},
				{SentimentScore: -0.6},
			},
			"negative", -1,
		},
		{
			"mixed neutral",
			[]analyzer.AnalyzedPost{
				{SentimentScore: 0.1},
				{SentimentScore: -0.1},
			},
			"neutral", 0,
		},
		{
			"scores clamped above 1",
			[]analyzer.AnalyzedPost{
				{SentimentScore: 5.0},
				{SentimentScore: 5.0},
			},
			"positive", 1,
		},
		{
			"scores clamped below -1",
			[]analyzer.AnalyzedPost{
				{SentimentScore: -5.0},
				{SentimentScore: -5.0},
			},
			"negative", -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat, pct := calculateOverallSentiment(tt.posts)
			if cat != tt.wantCat {
				t.Errorf("category = %q, want %q", cat, tt.wantCat)
			}
			switch tt.wantSign {
			case 1:
				if pct <= 0 {
					t.Errorf("expected positive pct, got %f", pct)
				}
			case -1:
				if pct >= 0 {
					t.Errorf("expected negative pct, got %f", pct)
				}
			case 0:
				if math.Abs(pct) > 30 {
					t.Errorf("expected near-zero pct, got %f", pct)
				}
			}
		})
	}
}

func TestCalculateSplitSentiment(t *testing.T) {
	t.Run("mixed root and reply", func(t *testing.T) {
		posts := []analyzer.AnalyzedPost{
			{Post: analyzer.Post{IsReply: false}, SentimentScore: 0.5},
			{Post: analyzer.Post{IsReply: true}, SentimentScore: -0.5},
		}
		rootPct, replyPct := calculateSplitSentiment(posts)
		if rootPct <= 0 {
			t.Errorf("rootPct = %f, expected positive", rootPct)
		}
		if replyPct >= 0 {
			t.Errorf("replyPct = %f, expected negative", replyPct)
		}
	})

	t.Run("no posts", func(t *testing.T) {
		rootPct, replyPct := calculateSplitSentiment(nil)
		if rootPct != 0 || replyPct != 0 {
			t.Errorf("got (%f, %f), want (0, 0)", rootPct, replyPct)
		}
	})

	t.Run("all roots", func(t *testing.T) {
		posts := []analyzer.AnalyzedPost{
			{Post: analyzer.Post{IsReply: false}, SentimentScore: 0.5},
		}
		rootPct, replyPct := calculateSplitSentiment(posts)
		if rootPct == 0 {
			t.Error("expected non-zero rootPct")
		}
		if replyPct != 0 {
			t.Errorf("replyPct = %f, want 0", replyPct)
		}
	})
}

func TestToAnalyzerPosts(t *testing.T) {
	t.Run("basic conversion", func(t *testing.T) {
		input := []store.Post{
			{
				URI: "at://test/post/1", CID: "cid1", Text: "hello",
				AuthorHandle: "alice.bsky.social",
				Likes:        10, Reposts: 5, Replies: 2,
				CreatedAt: "2026-01-01T00:00:00Z", IsReply: true,
			},
		}
		got := toAnalyzerPosts(input)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].Author != "alice.bsky.social" {
			t.Errorf("Author = %q, want %q", got[0].Author, "alice.bsky.social")
		}
		if got[0].Likes != 10 {
			t.Errorf("Likes = %d, want 10", got[0].Likes)
		}
		if !got[0].IsReply {
			t.Error("IsReply = false, want true")
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		got := toAnalyzerPosts(nil)
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})
}

func TestToStateSentimentPoints(t *testing.T) {
	t.Run("basic conversion", func(t *testing.T) {
		ts := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
		input := []store.SentimentDataPoint{
			{
				RunID: "run1", Timestamp: ts,
				AverageCompoundScore: 0.5, NetSentimentPercent: 50,
				SentimentCategory: "positive", TotalPosts: 100,
				CreatedAt: ts, TTL: 86400,
			},
		}
		got := toStateSentimentPoints(input)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].RunID != "run1" {
			t.Errorf("RunID = %q, want %q", got[0].RunID, "run1")
		}
		if got[0].NetSentimentPercent != 50 {
			t.Errorf("NetSentimentPercent = %f, want 50", got[0].NetSentimentPercent)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		got := toStateSentimentPoints(nil)
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})
}

func TestToStateYearlyPoints(t *testing.T) {
	t.Run("basic conversion", func(t *testing.T) {
		ts := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
		input := []store.YearlySparklineDataPoint{
			{
				Date: "2026-01-15", AverageSentiment: 5.0,
				MinSentiment: 1.0, MaxSentiment: 10.0,
				Q1Sentiment: 3.0, MedianSentiment: 5.0, Q3Sentiment: 7.0,
				Timestamp: ts, NetSentimentPercent: 50,
			},
		}
		got := toStateYearlyPoints(input)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].Date != "2026-01-15" {
			t.Errorf("Date = %q, want %q", got[0].Date, "2026-01-15")
		}
		if got[0].AverageSentiment != 5.0 {
			t.Errorf("AverageSentiment = %f, want 5.0", got[0].AverageSentiment)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		got := toStateYearlyPoints(nil)
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})
}

func TestBuildYearlyPostText(t *testing.T) {
	t.Run("empty points", func(t *testing.T) {
		got := buildYearlyPostText(nil)
		if got != "Bluesky Sentiment" {
			t.Errorf("got %q, want %q", got, "Bluesky Sentiment")
		}
	})

	t.Run("multiple points with min and max", func(t *testing.T) {
		points := []state.YearlySparklineDataPoint{
			{Date: "2026-01-01", AverageSentiment: 5.0, Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{Date: "2026-01-15", AverageSentiment: -3.0, Timestamp: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)},
			{Date: "2026-01-31", AverageSentiment: 10.0, Timestamp: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)},
		}
		got := buildYearlyPostText(points)

		if !strings.HasPrefix(got, "Bluesky Sentiment 2026-01-01 - 2026-01-31") {
			t.Errorf("unexpected prefix: %q", got)
		}
		if !strings.Contains(got, "Lowest: -3.0%") {
			t.Errorf("missing lowest value in: %q", got)
		}
		if !strings.Contains(got, "Highest: 10.0%") {
			t.Errorf("missing highest value in: %q", got)
		}
		if !strings.Contains(got, "Jan 15 events") {
			t.Errorf("missing lowest date in: %q", got)
		}
		if !strings.Contains(got, "Jan 31 events") {
			t.Errorf("missing highest date in: %q", got)
		}
	})
}

func TestBuildYearlyAltText(t *testing.T) {
	t.Run("empty points", func(t *testing.T) {
		got := buildYearlyAltText(nil)
		if got != "Yearly Bluesky sentiment chart" {
			t.Errorf("got %q, want %q", got, "Yearly Bluesky sentiment chart")
		}
	})

	t.Run("normal data", func(t *testing.T) {
		points := []state.YearlySparklineDataPoint{
			{Date: "2026-01-01", AverageSentiment: 5.0},
			{Date: "2026-01-15", AverageSentiment: -3.0},
			{Date: "2026-01-31", AverageSentiment: 10.0},
		}
		got := buildYearlyAltText(points)
		if !strings.HasPrefix(got, "Yearly Bluesky sentiment trend") {
			t.Errorf("unexpected prefix: %q", got)
		}
		if !strings.Contains(got, "Current: 10.0%") {
			t.Errorf("missing current value in: %q", got)
		}
		if !strings.Contains(got, "High: 10.0%") {
			t.Errorf("missing high value in: %q", got)
		}
		if !strings.Contains(got, "Low: -3.0%") {
			t.Errorf("missing low value in: %q", got)
		}
	})
}

func TestBuildEventDates(t *testing.T) {
	t.Run("empty points", func(t *testing.T) {
		got := buildEventDates(nil)
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("returns min and max dates", func(t *testing.T) {
		points := []state.YearlySparklineDataPoint{
			{Date: "2026-01-01", AverageSentiment: 5.0},
			{Date: "2026-01-15", AverageSentiment: -3.0},
			{Date: "2026-01-31", AverageSentiment: 10.0},
		}
		got := buildEventDates(points)
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}

		minDate := got[0]
		if minDate.FullDate != "2026-01-15" {
			t.Errorf("min FullDate = %q, want %q", minDate.FullDate, "2026-01-15")
		}
		if minDate.DisplayText != "Jan 15" {
			t.Errorf("min DisplayText = %q, want %q", minDate.DisplayText, "Jan 15")
		}

		maxDate := got[1]
		if maxDate.FullDate != "2026-01-31" {
			t.Errorf("max FullDate = %q, want %q", maxDate.FullDate, "2026-01-31")
		}
	})
}

var _ client.EventDate
