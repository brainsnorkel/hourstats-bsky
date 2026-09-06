package wikipedia

import (
	"testing"
	"time"
)

func TestCurrentEventsDayURL(t *testing.T) {
	const base = "https://en.wikipedia.org/wiki/Portal:Current_events/"
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{
			name: "utc date",
			in:   time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
			want: base + "2026_September_4",
		},
		{
			name: "single digit day is unpadded",
			in:   time.Date(2026, time.January, 3, 0, 0, 0, 0, time.UTC),
			want: base + "2026_January_3",
		},
		{
			name: "two digit day",
			in:   time.Date(2025, time.November, 15, 0, 0, 0, 0, time.UTC),
			want: base + "2025_November_15",
		},
		{
			// 2026-09-05 09:00 +11:00 is 2026-09-04 22:00 UTC.
			name: "non-utc time falling back a day in utc",
			in:   time.Date(2026, time.September, 5, 9, 0, 0, 0, time.FixedZone("UTC+11", 11*3600)),
			want: base + "2026_September_4",
		},
		{
			// 2026-09-04 20:00 -07:00 is 2026-09-05 03:00 UTC.
			name: "non-utc time moving on a day in utc",
			in:   time.Date(2026, time.September, 4, 20, 0, 0, 0, time.FixedZone("UTC-7", -7*3600)),
			want: base + "2026_September_5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CurrentEventsDayURL(tt.in); got != tt.want {
				t.Errorf("CurrentEventsDayURL(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
