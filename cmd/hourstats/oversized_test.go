package main

import (
	"strings"
	"testing"
)

// TestOversizedPost: the guard OnPost applies before it tokenises or stores a
// record. The limit is on runes, so a post of wide characters is judged by the
// same yardstick as an ASCII one, and the measured length is returned so the
// caller never counts it twice.
func TestOversizedPost(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		limit     int
		wantRunes int
		wantOver  bool
	}{
		{"ordinary post", "hello world", defaultMaxPostRunes, 11, false},
		{"empty", "", defaultMaxPostRunes, 0, false},
		{"at the limit", strings.Repeat("a", defaultMaxPostRunes), defaultMaxPostRunes, defaultMaxPostRunes, false},
		{"one rune past the limit", strings.Repeat("a", defaultMaxPostRunes+1), defaultMaxPostRunes, defaultMaxPostRunes + 1, true},
		{
			// 3000 emoji are 12000 bytes but only 3000 runes, so a byte limit
			// would refuse a post the lexicon itself would accept.
			name:      "wide runes are counted as runes",
			text:      strings.Repeat("🔥", defaultMaxPostRunes),
			limit:     defaultMaxPostRunes,
			wantRunes: defaultMaxPostRunes,
			wantOver:  false,
		},
		{
			name:      "wide runes past the limit",
			text:      strings.Repeat("🔥", defaultMaxPostRunes+1),
			limit:     defaultMaxPostRunes,
			wantRunes: defaultMaxPostRunes + 1,
			wantOver:  true,
		},
		{"a lower configured limit", "hello world", 5, 11, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runes, over := oversizedPost(tt.text, tt.limit)
			if runes != tt.wantRunes {
				t.Errorf("oversizedPost() runes = %d, want %d", runes, tt.wantRunes)
			}
			if over != tt.wantOver {
				t.Errorf("oversizedPost() oversized = %v, want %v", over, tt.wantOver)
			}
		})
	}
}

// TestSplitList: the comma lists read from the environment and from key_value
// are parsed the same way, so a hand-edited row behaves like its env twin.
func TestSplitList(t *testing.T) {
	tests := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{",,", nil},
		{"did:plc:a", []string{"did:plc:a"}},
		{" did:plc:a , did:plc:b ,", []string{"did:plc:a", "did:plc:b"}},
	}
	for _, tt := range tests {
		got := splitList(tt.raw)
		if len(got) != len(tt.want) {
			t.Errorf("splitList(%q) = %v, want %v", tt.raw, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitList(%q)[%d] = %q, want %q", tt.raw, i, got[i], tt.want[i])
			}
		}
	}
}
