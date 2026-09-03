package procmem

import (
	"runtime"
	"testing"
)

func TestParseStatmRSS(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		pageSize int
		want     int64
		wantErr  bool
	}{
		{
			name:     "realistic statm line",
			content:  "150000 148000 3000 1 0 120000 0\n",
			pageSize: 4096,
			want:     148000 * 4096,
		},
		{
			name:     "16k pages",
			content:  "150000 148000 3000 1 0 120000 0",
			pageSize: 16384,
			want:     148000 * 16384,
		},
		{
			name:     "too few fields",
			content:  "150000\n",
			pageSize: 4096,
			wantErr:  true,
		},
		{
			name:     "empty",
			content:  "",
			pageSize: 4096,
			wantErr:  true,
		},
		{
			name:     "non-numeric resident field",
			content:  "150000 not-a-number 3000",
			pageSize: 4096,
			wantErr:  true,
		},
		{
			name:     "negative resident field",
			content:  "150000 -5 3000",
			pageSize: 4096,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseStatmRSS(tt.content, tt.pageSize)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseStatmRSS(%q) = %d, want error", tt.content, got)
				}
				if got != 0 {
					t.Errorf("ParseStatmRSS(%q) = %d on error, want 0", tt.content, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseStatmRSS(%q) unexpected error: %v", tt.content, err)
			}
			if got != tt.want {
				t.Errorf("ParseStatmRSS(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}

func TestRSSBytes(t *testing.T) {
	got := RSSBytes()
	if got < 0 {
		t.Fatalf("RSSBytes() = %d, want >= 0", got)
	}
	// procfs only exists on Linux; elsewhere the reader degrades to 0.
	if runtime.GOOS == "linux" && got == 0 {
		t.Error("RSSBytes() = 0 on linux, want > 0")
	}
	if runtime.GOOS != "linux" && got != 0 {
		t.Errorf("RSSBytes() = %d on %s, want 0", got, runtime.GOOS)
	}
}
