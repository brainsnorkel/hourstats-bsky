package client

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClampAltText(t *testing.T) {
	short := "Chart of Bluesky sentiment. High +12%; low -3%."
	if got := ClampAltText(short); got != short {
		t.Errorf("short text changed: %q", got)
	}

	// Long text is cut at a sentence end before the limit and ends with an ellipsis.
	sentence := "English posts were 42% of the firehose. "
	long := strings.Repeat(sentence, 80) // ~3200 runes
	got := ClampAltText(long)
	if n := utf8.RuneCountInString(got); n > MaxAltTextGraphemes || n < MaxAltTextGraphemes/2 {
		t.Errorf("clamped length = %d", n)
	}
	if !strings.HasSuffix(got, "firehose.…") {
		t.Errorf("not cut at a sentence end: %q", got[len(got)-30:])
	}

	// Without sentence punctuation the cut is a hard one at the limit.
	nopunct := strings.Repeat("ab ", 1200)
	got = ClampAltText(nopunct)
	if n := utf8.RuneCountInString(got); n != MaxAltTextGraphemes {
		t.Errorf("hard cut length = %d, want %d", n, MaxAltTextGraphemes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("missing ellipsis")
	}

	// Multibyte text counts runes, not bytes.
	greek := strings.Repeat("α", 1999)
	if got := ClampAltText(greek); got != greek {
		t.Error("1999 multibyte runes should be untouched")
	}
}
