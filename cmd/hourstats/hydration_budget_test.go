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

func TestApplyCycleLimits(t *testing.T) {
	tests := []struct {
		name               string
		timeout            int
		cap                int
		capSet             bool
		alert              int
		alertSet           bool
		wantCap, wantAlert int
		wantNoteSubstr     string
		wantNoNoteSubstr   string
	}{
		{"defaults at 12 min", 12, 0, false, 0, false, 300000, 1320, "", "window cap"},
		{"derived at 25 min", 25, 0, false, 0, false, 312500, 2100, "", "window cap"},
		{"explicit cap above ceiling", 25, 350000, true, 0, false, 350000, 2100, "", "window cap"},
		{"explicit cap below ceiling", 25, 300000, true, 0, false, 300000, 2100, "window cap 300000 is below the hydration ceiling 312500", ""},
		{"explicit 0 disables", 25, 0, true, 0, false, 0, 2100, "", "window cap"},
		{"explicit negative disables", 25, -1, true, 0, false, -1, 2100, "", "window cap"},
		{"explicit alert kept", 25, 0, false, 2400, true, 312500, 2400, "", "window cap"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := applyCycleLimits(resolveHydrationBudget(60, tt.timeout, 500), tt.cap, tt.capSet, tt.alert, tt.alertSet)
			if b.WindowCap != tt.wantCap {
				t.Errorf("WindowCap = %d, want %d", b.WindowCap, tt.wantCap)
			}
			if b.CycleAlertSeconds != tt.wantAlert {
				t.Errorf("CycleAlertSeconds = %d, want %d", b.CycleAlertSeconds, tt.wantAlert)
			}
			notes := strings.Join(b.Notes, "|")
			if tt.wantNoteSubstr != "" && !strings.Contains(notes, tt.wantNoteSubstr) {
				t.Errorf("Notes = %q, want one containing %q", b.Notes, tt.wantNoteSubstr)
			}
			if tt.wantNoNoteSubstr != "" && strings.Contains(notes, tt.wantNoNoteSubstr) {
				t.Errorf("Notes = %q, want none containing %q", b.Notes, tt.wantNoNoteSubstr)
			}
		})
	}
}

func TestCycleLimitsFromEnv(t *testing.T) {
	tests := []struct {
		name               string
		cap, alert         string
		wantCap, wantAlert int
	}{
		{"both unset", "", "", 312500, 2100},
		{"negative alert is derived", "", "-5", 312500, 2100},
		{"zero alert kept", "", "0", 312500, 0},
		{"garbage cap is derived", "abc", "", 312500, 2100},
		{"zero cap disables", "0", "", 0, 2100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANALYSIS_MAX_WINDOW_POSTS", tt.cap)
			t.Setenv("ALERT_CYCLE_SECONDS", tt.alert)
			b := cycleLimitsFromEnv(resolveHydrationBudget(60, 25, 500))
			if b.WindowCap != tt.wantCap {
				t.Errorf("WindowCap = %d, want %d", b.WindowCap, tt.wantCap)
			}
			if b.CycleAlertSeconds != tt.wantAlert {
				t.Errorf("CycleAlertSeconds = %d, want %d", b.CycleAlertSeconds, tt.wantAlert)
			}
		})
	}
}

func TestLogHydrationBudgetLogsCeiling(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logHydrationBudget(applyCycleLimits(resolveHydrationBudget(60, 25, 500), 0, false, 0, false))
	out := buf.String()
	for _, want := range []string{`"msg":"hydration budget"`, `"level":"INFO"`, `"timeout":"25m0s"`, `"rate_per_minute":500`, `"ceiling_posts":312500`, `"window_cap":312500`, `"cycle_alert_seconds":2100`} {
		if !strings.Contains(out, want) {
			t.Errorf("log %s missing %s", out, want)
		}
	}

	buf.Reset()
	logHydrationBudget(resolveHydrationBudget(30, 25, 500))
	if !strings.Contains(buf.String(), `"level":"WARN"`) {
		t.Errorf("clamped budget should log at WARN: %s", buf.String())
	}

	buf.Reset()
	logHydrationBudget(applyCycleLimits(resolveHydrationBudget(60, 25, 500), 300000, true, 0, false))
	if out := buf.String(); !strings.Contains(out, `"level":"WARN"`) || !strings.Contains(out, "below the hydration ceiling") {
		t.Errorf("window cap below the ceiling should log at WARN with a note: %s", out)
	}
}
