package topics

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// subjectKeyYear matches a token that is only a 19xx or 20xx year.
var subjectKeyYear = regexp.MustCompile(`^(19|20)\d\d$`)

// subjectKeyStop are tokens dropped from a subject key: the country prefix
// the grouper adds or leaves out from hour to hour, and filler words. "and"
// is a stop token because "&", its other spelling, is not a token character
// and vanishes: "Donald Trump & US Elections" and "Donald Trump and US
// Elections" must fold together.
var subjectKeyStop = map[string]bool{
	"us": true, "u.s": true, "u.s.": true,
	"the": true, "a": true, "of": true, "in": true, "on": true, "and": true,
}

// SubjectKey folds a topic label to the key the weekly report counts rank-1
// hours under, so that the grouper's spellings of one subject from hour to
// hour ("2024 US Presidential Election", "US Presidential Elections") land in
// one key while different subjects ("Brazilian Presidential Election") do
// not. The label is lowercased and split into tokens of Unicode letters,
// digits, apostrophes, hyphens and periods; a token's leading and trailing
// periods are trimmed after the stop check, so "U.S." is matched as a stop
// token while a sentence-final period is not part of a word. A typographic
// apostrophe is read as "'", and a possessive is then removed: a trailing
// "'s" or bare "'" is stripped from a token ("Swift's" folds to "swift",
// "voters'" to "voter"). Tokens that are a bare year, a stop token or hold no
// letter or digit are dropped, and a trailing "s" is stripped from a token longer than four runes that does not
// end in "ss" ("elections" folds to "election", "congress" stays). The
// tokens are joined with single spaces. There is deliberately no prefix or
// subset joining: it would chain unrelated subjects through a shared generic
// word.
func SubjectKey(label string) string {
	fields := strings.FieldsFunc(strings.ReplaceAll(strings.ToLower(label), "\u2019", "'"), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '-' && r != '.'
	})
	keep := fields[:0]
	for _, tok := range fields {
		if subjectKeyStop[tok] {
			continue
		}
		tok = strings.Trim(tok, ".")
		tok = strings.TrimSuffix(tok, "'s")
		tok = strings.TrimSuffix(tok, "'")
		if tok == "" || subjectKeyStop[tok] || subjectKeyYear.MatchString(tok) || !hasLetterOrDigit(tok) {
			continue
		}
		if utf8.RuneCountInString(tok) > 4 && strings.HasSuffix(tok, "s") && !strings.HasSuffix(tok, "ss") {
			tok = tok[:len(tok)-1]
		}
		keep = append(keep, tok)
	}
	return strings.Join(keep, " ")
}

func hasLetterOrDigit(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}
