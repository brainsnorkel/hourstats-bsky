package analyzer

import (
	"fmt"
	"testing"
	"unicode/utf8"
)

func score(t *testing.T, sa *SentimentAnalyzer, text string) float64 {
	t.Helper()
	analyzed, err := sa.analyzePost(Post{URI: "test://post/1", Text: text})
	if err != nil {
		t.Fatalf("analyzePost(%q): %v", text, err)
	}
	return analyzed.SentimentScore
}

func TestEmojiValencesAreSingleRunes(t *testing.T) {
	for r := range emojiValences {
		s := string(r)
		if n := utf8.RuneCountInString(s); n != 1 {
			t.Errorf("emojiValences key %U encodes to %d runes, want 1", r, n)
		}
		if got, size := utf8.DecodeRuneInString(s); got != r || size != len(s) {
			t.Errorf("string(%U) round-trip = %U (size %d of %d)", r, got, size, len(s))
		}
	}
}

func TestApplyEmojiOverridesInstallsTokens(t *testing.T) {
	sa := NewEmojiAware()
	for r, want := range emojiValences {
		token, ok := sa.analyzer.EmojiDict[string(r)]
		if !ok {
			t.Fatalf("EmojiDict missing %U", r)
		}
		if expected := fmt.Sprintf("hsemoji%x", r); token != expected {
			t.Errorf("EmojiDict[%U] = %q, want %q", r, token, expected)
		}
		got, ok := sa.analyzer.Lexicon[token]
		if !ok {
			t.Fatalf("Lexicon missing token %q for %U", token, r)
		}
		if got != want {
			t.Errorf("Lexicon[%q] = %v, want %v", token, got, want)
		}
	}
}

func TestNewEmojiAwareScores(t *testing.T) {
	sa := NewEmojiAware()

	tests := []struct {
		name  string
		text  string
		check func(float64) bool
		want  string
	}{
		{"single fire", "🔥", func(v float64) bool { return v >= 0.3 }, ">= 0.3"},
		{"triple fire", "🔥🔥🔥", func(v float64) bool { return v >= 0.3 }, ">= 0.3"},
		{"heart with variation selector", "\u2764\uFE0F", func(v float64) bool { return v > 0 }, "> 0"},
		{"heart in a sentence", "I \u2764\uFE0F this", func(v float64) bool { return v > 0 }, "> 0"},
		{"thumbs up with skin tone", "\U0001F44D\U0001F3FD", func(v float64) bool { return v > 0 }, "> 0"},
		{"ambiguous sob carries no signal", "😭", func(v float64) bool { return v == 0 }, "== 0"},
		{"star-struck (silent in stock)", "🤩", func(v float64) bool { return v >= 0.3 }, ">= 0.3"},
		{"pink heart (silent in stock)", "🩷", func(v float64) bool { return v >= 0.3 }, ">= 0.3"},
		{"laughter idiom leans positive", "💀😭", func(v float64) bool { return v > 0 }, "> 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := score(t, sa, tt.text)
			if !tt.check(got) {
				t.Errorf("compound(%q) = %v, want %s", tt.text, got, tt.want)
			}
		})
	}
}

// TestStockAnalyzerDiffersOnFire pins the behaviour the emoji table exists to
// fix: stock govader resolves 🔥 to the word "fire" and scores it negative.
func TestStockAnalyzerDiffersOnFire(t *testing.T) {
	if got := score(t, New(), "🔥"); got >= 0 {
		t.Errorf("stock compound(🔥) = %v, want < 0", got)
	}
}

func TestAnalyzersAgreeWithoutEmoji(t *testing.T) {
	texts := []string{
		"I love this new feature! It's amazing!",
		"This is terrible. I hate it so much.",
		"The weather is okay today.",
	}
	stock, emoji := New(), NewEmojiAware()
	for _, text := range texts {
		if a, b := score(t, stock, text), score(t, emoji, text); a != b {
			t.Errorf("compound(%q): stock = %v, emoji-aware = %v; want identical", text, a, b)
		}
	}
}

func TestStripEmojiModifiers(t *testing.T) {
	tests := []struct{ in, want string }{
		{"\u2764\uFE0F", "\u2764"},
		{"\u2764\uFE0E", "\u2764"},
		{"\U0001F44D\U0001F3FF", "\U0001F44D"},
		{"\U0001F469\u200D\U0001F4BB", "\U0001F469\U0001F4BB"},
		{"no modifiers here", "no modifiers here"},
	}
	for _, tt := range tests {
		if got := stripEmojiModifiers(tt.in); got != tt.want {
			t.Errorf("stripEmojiModifiers(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
