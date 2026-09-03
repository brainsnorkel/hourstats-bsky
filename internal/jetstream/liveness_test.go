package jetstream

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Cursor age gate
// ---------------------------------------------------------------------------

func TestResolveStartCursor(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	usAt := func(d time.Duration) int64 { return now.Add(-d).UnixMicro() }

	tests := []struct {
		name          string
		cursor        int64
		maxAge        time.Duration
		wantStart     int64
		wantDiscarded bool
	}{
		{
			name:      "no persisted cursor starts live",
			cursor:    0,
			maxAge:    6 * time.Hour,
			wantStart: 0,
		},
		{
			name:      "negative cursor starts live",
			cursor:    -1,
			maxAge:    6 * time.Hour,
			wantStart: 0,
		},
		{
			name:      "fresh cursor is kept",
			cursor:    usAt(2 * time.Minute),
			maxAge:    6 * time.Hour,
			wantStart: usAt(2 * time.Minute),
		},
		{
			name:      "cursor exactly at the limit is kept",
			cursor:    usAt(6 * time.Hour),
			maxAge:    6 * time.Hour,
			wantStart: usAt(6 * time.Hour),
		},
		{
			// The staging incident: a 33h-old cursor replayed at wire speed and
			// overran the write buffer.
			name:          "stale cursor is discarded",
			cursor:        usAt(33 * time.Hour),
			maxAge:        6 * time.Hour,
			wantStart:     0,
			wantDiscarded: true,
		},
		{
			name:      "non-positive maxAge disables the age check",
			cursor:    usAt(72 * time.Hour),
			maxAge:    -1,
			wantStart: usAt(72 * time.Hour),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, age, discarded := resolveStartCursor(tt.cursor, tt.maxAge, now)
			if start != tt.wantStart {
				t.Errorf("start = %d, want %d", start, tt.wantStart)
			}
			if discarded != tt.wantDiscarded {
				t.Errorf("discarded = %v, want %v", discarded, tt.wantDiscarded)
			}
			if tt.cursor > 0 && age <= 0 {
				t.Errorf("age = %v, want a positive age for a past cursor", age)
			}
		})
	}
}

func TestResolveStartCursorReportsAgeOnDiscard(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	cursor := now.Add(-33 * time.Hour).UnixMicro()

	_, age, discarded := resolveStartCursor(cursor, 6*time.Hour, now)
	if !discarded {
		t.Fatal("expected a 33h cursor to be discarded")
	}
	if age.Round(time.Minute) != 33*time.Hour {
		t.Errorf("age = %v, want 33h so the WARN line is actionable", age)
	}
}

// ---------------------------------------------------------------------------
// Cursor rewind
// ---------------------------------------------------------------------------

func TestRewindCursor(t *testing.T) {
	const base = 1_725_911_162_329_308 // a realistic time_us value

	tests := []struct {
		name   string
		cursor int64
		rewind time.Duration
		want   int64
	}{
		{"subtracts the rewind", base, 5 * time.Second, base - 5_000_000},
		{"zero cursor is left alone", 0, 5 * time.Second, 0},
		{"negative cursor is left alone", -7, 5 * time.Second, -7},
		{"zero rewind disables the shift", base, 0, base},
		{"negative rewind disables the shift", base, -1, base},
		{"clamps to 1 rather than going non-positive", 1_000_000, 5 * time.Second, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rewindCursor(tt.cursor, tt.rewind); got != tt.want {
				t.Errorf("rewindCursor(%d, %v) = %d, want %d", tt.cursor, tt.rewind, got, tt.want)
			}
		})
	}
}

func TestBuildURLUsesRewoundCursor(t *testing.T) {
	const base = 1_725_911_162_329_308

	c := NewConsumer(ConsumerConfig{
		Endpoint:     "wss://example.test/subscribe",
		CursorRewind: 5 * time.Second,
	})
	c.cursor.Store(base)

	got := c.buildURL()
	if !strings.Contains(got, "cursor=1725911157329308") {
		t.Errorf("buildURL() = %q, want the cursor rewound by 5s", got)
	}
}

func TestBuildURLOmitsCursorWhenUnset(t *testing.T) {
	c := NewConsumer(ConsumerConfig{Endpoint: "wss://example.test/subscribe"})
	if got := c.buildURL(); strings.Contains(got, "cursor=") {
		t.Errorf("buildURL() = %q, want no cursor parameter", got)
	}
}

// ---------------------------------------------------------------------------
// Config defaults
// ---------------------------------------------------------------------------

func TestConsumerConfigCursorDefaults(t *testing.T) {
	var cfg ConsumerConfig
	cfg.setDefaults()

	if cfg.CursorRewind != DefaultCursorRewind {
		t.Errorf("CursorRewind = %v, want %v", cfg.CursorRewind, DefaultCursorRewind)
	}
	if cfg.MaxCursorAge != DefaultMaxCursorAge {
		t.Errorf("MaxCursorAge = %v, want %v", cfg.MaxCursorAge, DefaultMaxCursorAge)
	}
}

func TestConsumerConfigPreservesExplicitCursorSettings(t *testing.T) {
	cfg := ConsumerConfig{
		CursorRewind: 30 * time.Second,
		MaxCursorAge: 90 * time.Minute,
	}
	cfg.setDefaults()

	if cfg.CursorRewind != 30*time.Second {
		t.Errorf("CursorRewind = %v, want 30s", cfg.CursorRewind)
	}
	if cfg.MaxCursorAge != 90*time.Minute {
		t.Errorf("MaxCursorAge = %v, want 90m", cfg.MaxCursorAge)
	}

	// Negative values are the documented way to disable each behaviour and
	// must survive setDefaults.
	off := ConsumerConfig{CursorRewind: -1, MaxCursorAge: -1}
	off.setDefaults()
	if off.CursorRewind != -1 || off.MaxCursorAge != -1 {
		t.Errorf("negative overrides were reset: rewind=%v age=%v", off.CursorRewind, off.MaxCursorAge)
	}
}

// ---------------------------------------------------------------------------
// Backoff jitter
// ---------------------------------------------------------------------------

func TestJitterBackoffStaysWithinBand(t *testing.T) {
	const base = 4 * time.Second
	low := time.Duration(float64(base) * (1 - backoffJitter))
	high := time.Duration(float64(base) * (1 + backoffJitter))

	sawBelow, sawAbove := false, false
	for i := 0; i < 500; i++ {
		got := jitterBackoff(base)
		if got < low || got > high {
			t.Fatalf("jitterBackoff(%v) = %v, outside [%v, %v]", base, got, low, high)
		}
		if got < base {
			sawBelow = true
		}
		if got > base {
			sawAbove = true
		}
	}
	if !sawBelow || !sawAbove {
		t.Error("jitterBackoff never varied in both directions over 500 samples")
	}
}

func TestJitterBackoffHandlesNonPositive(t *testing.T) {
	if got := jitterBackoff(0); got != 0 {
		t.Errorf("jitterBackoff(0) = %v, want 0", got)
	}
	if got := jitterBackoff(-time.Second); got != -time.Second {
		t.Errorf("jitterBackoff(-1s) = %v, want -1s", got)
	}
}

// ---------------------------------------------------------------------------
// ForceReconnect
// ---------------------------------------------------------------------------

func TestForceReconnectWithoutConnection(t *testing.T) {
	c := NewConsumer(ConsumerConfig{Endpoint: "wss://example.test/subscribe"})
	if c.ForceReconnect() {
		t.Error("ForceReconnect reported a closed connection when none was open")
	}
}

// TestLivenessTimingsAreConsistent guards the relationship the read deadline
// depends on: pings must have room to refresh the deadline before it expires.
func TestLivenessTimingsAreConsistent(t *testing.T) {
	if pingInterval >= readTimeout {
		t.Errorf("pingInterval %v must be shorter than readTimeout %v", pingInterval, readTimeout)
	}
	if 2*pingInterval > readTimeout {
		t.Errorf("readTimeout %v leaves no room for a missed ping at pingInterval %v", readTimeout, pingInterval)
	}
	if pingWriteTimeout >= pingInterval {
		t.Errorf("pingWriteTimeout %v must be shorter than pingInterval %v", pingWriteTimeout, pingInterval)
	}
}
