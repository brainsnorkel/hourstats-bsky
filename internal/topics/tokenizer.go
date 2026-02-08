package topics

import (
	"regexp"
	"strings"
)

var (
	urlPattern     = regexp.MustCompile(`https?://\S+`)
	mentionPattern = regexp.MustCompile(`@[\w.-]+`)
	nonAlphaNum    = regexp.MustCompile(`[^a-z0-9]+`)
)

func Tokenize(text string) []string {
	text = urlPattern.ReplaceAllString(text, "")
	text = mentionPattern.ReplaceAllString(text, "")
	text = stripEmoji(text)
	text = strings.ToLower(text)
	text = nonAlphaNum.ReplaceAllString(text, " ")

	var tokens []string
	for _, word := range strings.Fields(text) {
		if len(word) < 3 {
			continue
		}
		if stopwords[word] {
			continue
		}
		tokens = append(tokens, word)
	}
	return tokens
}

func stripEmoji(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isEmoji(r) {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isEmoji(r rune) bool {
	return (r >= 0x1F600 && r <= 0x1F64F) || // emoticons
		(r >= 0x1F300 && r <= 0x1F5FF) || // misc symbols & pictographs
		(r >= 0x1F680 && r <= 0x1F6FF) || // transport & map
		(r >= 0x1F1E0 && r <= 0x1F1FF) || // flags
		(r >= 0x2600 && r <= 0x26FF) || // misc symbols
		(r >= 0x2700 && r <= 0x27BF) || // dingbats
		(r >= 0xFE00 && r <= 0xFE0F) || // variation selectors
		(r >= 0x1F900 && r <= 0x1F9FF) || // supplemental symbols
		(r >= 0x1FA00 && r <= 0x1FA6F) || // chess symbols
		(r >= 0x1FA70 && r <= 0x1FAFF) || // symbols extended-A
		(r >= 0x200D && r <= 0x200D) || // zero width joiner
		(r >= 0x20E3 && r <= 0x20E3) || // combining enclosing keycap
		(r >= 0xE0020 && r <= 0xE007F) // tags
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "any": true, "can": true,
	"had": true, "her": true, "was": true, "one": true, "our": true,
	"out": true, "has": true, "his": true, "how": true, "its": true,
	"may": true, "new": true, "now": true, "old": true, "see": true,
	"way": true, "who": true, "did": true, "get": true, "got": true,
	"let": true, "say": true, "she": true, "too": true, "use": true,
	"that": true, "with": true, "have": true, "this": true, "will": true,
	"your": true, "from": true, "they": true, "been": true, "said": true,
	"each": true, "make": true, "like": true, "long": true, "look": true,
	"many": true, "then": true, "them": true, "very": true, "when": true,
	"come": true, "could": true, "would": true, "about": true, "which": true,
	"their": true, "there": true, "these": true, "other": true, "into": true,
	"more": true, "some": true, "than": true, "what": true, "just": true,
	"know": true, "also": true, "back": true, "after": true, "year": true,
	"even": true, "most": true, "only": true, "over": true, "such": true,
	"take": true, "want": true, "give": true, "good": true, "much": true,
	"time": true, "being": true, "those": true, "still": true,
	"should": true, "while": true, "where": true, "every": true, "never": true,
	"going": true, "really": true, "think": true, "thing": true, "things": true,
	"people": true, "because": true, "through": true, "before": true, "between": true,
	"does": true, "dont": true, "were": true, "here": true,
	"well": true, "same": true, "need": true, "down": true,
	"right": true, "keep": true, "work": true, "first": true, "made": true,
	"great": true, "yeah": true, "sure": true, "thank": true, "thanks": true,
	"please": true, "sorry": true, "yes": true, "gonna": true,
	"lol": true, "lmao": true, "omg": true, "wtf": true, "tbh": true,
	"imo": true, "smh": true, "idk": true, "ngl": true, "bruh": true,
	"literally": true, "actually": true,
}
