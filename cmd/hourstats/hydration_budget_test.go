package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestResolveHydrationBudget(t *testing.T) {
	tests := []struct {
		name                    string
		interval, timeout, rate int
		wantTimeout             time.Duration
		wantRate, wantCeiling   int
		wantNotes               int
		wantNoteSubstr          string
	}{
		{"defaults", 30, 0, 0, 12 * time.Minute, 500, 150000, 0, ""},
		{"explicit defaults", 60, 12, 500, 12 * time.Minute, 500, 150000, 0, ""},
		{"clamp at 30", 30, 25, 500, 20 * time.Minute, 500, 250000, 1, "clamped to 20m0s"},
		{"clamp at 60", 60, 55, 500, 50 * time.Minute, 500, 625000, 1, "clamped to 50m0s"},
		{"no clamp at 60", 60, 25, 500, 25 * time.Minute, 500, 312500, 0, ""},
		{"rate 700 clamped", 60, 12, 700, 12 * time.Minute, 600, 180000, 2, "clamped to 600/min"},
		{"rate 560 note only", 60, 12, 560, 12 * time.Minute, 560, 168000, 1, "little headroom"},
		{"interval 10", 10, 12, 500, 5 * time.Minute, 500, 62500, 1, "clamped to 5m0s"},
		{"interval 1", 1, 12, 500, 1 * time.Minute, 500, 12500, 1, "clamped to 1m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := resolveHydrationBudget(tt.interval, tt.timeout, tt.rate)
			if b.Timeout != tt.wantTimeout {
				t.Errorf("Timeout = %s, want %s", b.Timeout, tt.wantTimeout)
			}
			if b.RatePerMinute != tt.wantRate {
				t.Errorf("RatePerMinute = %d, want %d", b.RatePerMinute, tt.wantRate)
			}
			if b.CeilingPosts != tt.wantCeiling {
				t.Errorf("CeilingPosts = %d, want %d", b.CeilingPosts, tt.wantCeiling)
			}
			if len(b.Notes) != tt.wantNotes {
				t.Errorf("Notes = %q, want %d notes", b.Notes, tt.wantNotes)
			}
			if tt.wantNoteSubstr != "" && !strings.Contains(strings.Join(b.Notes, "|"), tt.wantNoteSubstr) {
				t.Errorf("Notes = %q, want one containing %q", b.Notes, tt.wantNoteSubstr)
			}
		})
	}
}

func TestLogHydrationBudgetLogsCeiling(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logHydrationBudget(resolveHydrationBudget(60, 25, 500))
	out := buf.String()
	for _, want := range []string{`"msg":"hydration budget"`, `"level":"INFO"`, `"timeout":"25m0s"`, `"rate_per_minute":500`, `"ceiling_posts":312500`} {
		if !strings.Contains(out, want) {
			t.Errorf("log %s missing %s", out, want)
		}
	}

	buf.Reset()
	logHydrationBudget(resolveHydrationBudget(30, 25, 500))
	if !strings.Contains(buf.String(), `"level":"WARN"`) {
		t.Errorf("clamped budget should log at WARN: %s", buf.String())
	}
}
