package formatter

import "testing"

func TestSignedPercent(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{29, "+29.0%"},
		{0.05, "+0.1%"},
		{0.049, "0.0%"},
		{0, "0.0%"},
		{-0.049, "0.0%"},
		{-0.05, "-0.1%"},
		{-8, "-8.0%"},
		{-29.05, "-29.1%"},
		{100, "+100.0%"},
	}
	for _, tt := range tests {
		if got := SignedPercent(tt.in); got != tt.want {
			t.Errorf("SignedPercent(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
