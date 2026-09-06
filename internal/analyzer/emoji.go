package analyzer

import (
	"fmt"
	"strings"

	"github.com/jonreiter/govader"
)

// emojiValences maps single-rune emoji to a VADER-scale valence (-4..+4).
//
// The values are hand-curated for 2026 Bluesky usage rather than taken from
// govader's bundled emoji dictionary, which resolves an emoji to its Unicode
// name and then scores that phrase as English words. That indirection gets
// several of the most common emoji badly wrong: 🔥 becomes "fire" (-1.4),
// 😭 becomes "loudly crying face" (crying, -2.1), and ❤️, 💀, 🙏, 💯 and 👏
// all resolve to phrases the lexicon does not cover at all, so they score 0.
//
// 😭 is deliberately 0: on Bluesky it is ambiguous between laughing and
// crying, so it should carry no signal rather than the stock -2.1.
//
// The table only covers single-rune emoji. Multi-rune sequences (ZWJ
// families, flags, keycaps) are a follow-up: hs-kls.
var emojiValences = map[rune]float64{
	'🔥': 2.5,
	'❤': 3.0, // U+2764, without the U+FE0F variation selector
	'🧡': 2.5,
	'💛': 2.5,
	'💚': 2.5,
	'💙': 2.5,
	'💜': 2.5,
	'🤍': 2.5,
	'🤎': 2.5,
	'🖤': 2.0,
	'💖': 2.5,
	'💕': 2.5,
	'💗': 2.5,
	'💓': 2.5,
	'💞': 2.5,
	'💘': 2.5,
	'💝': 2.5,
	'🙏': 1.5,
	'💯': 2.5,
	'👏': 2.0,
	'🫶': 2.5,
	'🥹': 1.5,
	'💀': 1.0,
	'😭': 0.0,
	'🥳': 2.5,
	'👍': 1.5,
	'👎': -1.5,
	'🙌': 2.0,
	'💪': 1.5,
	'💩': -1.5,
	'🙄': -1.5,
	'😡': -2.5,
	'🤬': -3.0,
	'😤': -1.0,
	'🤮': -2.5,
	'🤢': -2.0,
	'🖕': -3.0,
	'🤡': -1.5,
	'💔': -2.5,
}

// applyEmojiOverrides rewires sa so that every emoji in emojiValences scores
// its curated valence.
//
// govader expands an emoji to a description string and then looks the
// resulting words up in the lexicon, so the override is two-sided: the emoji
// dictionary maps the rune to a synthetic token and the lexicon gives that
// token the valence. The token is built from lowercase letters and hex digits
// only, so neither lowercasing nor govader's ASCII punctuation stripping can
// alter it, and it cannot collide with a real English word.
func applyEmojiOverrides(sa *govader.SentimentIntensityAnalyzer) {
	for r, valence := range emojiValences {
		token := fmt.Sprintf("hsemoji%x", r)
		sa.EmojiDict[string(r)] = token
		sa.Lexicon[token] = valence
	}
}

// emojiJoiners removes the code points that turn a base emoji into a
// presentation variant or a sequence. govader copies any rune it does not
// recognise straight into its buffer, so a variation selector or skin-tone
// modifier would glue itself onto the substituted token and make the lexicon
// lookup miss.
var emojiJoiners = strings.NewReplacer(
	"\uFE0E", "", // text presentation selector
	"\uFE0F", "", // emoji presentation selector
	"\u200D", "", // zero-width joiner
	"\U0001F3FB", "", // skin tone modifiers, Fitzpatrick types 1-2 through 6
	"\U0001F3FC", "",
	"\U0001F3FD", "",
	"\U0001F3FE", "",
	"\U0001F3FF", "",
)

// stripEmojiModifiers removes variation selectors, zero-width joiners and
// skin-tone modifiers so the base emoji reaches the emoji dictionary intact.
func stripEmojiModifiers(s string) string {
	return emojiJoiners.Replace(s)
}
