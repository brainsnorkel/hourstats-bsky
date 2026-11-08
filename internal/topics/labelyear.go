package topics

import (
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// labelYearPattern finds four-digit years (19xx or 20xx) in a topic label,
// with an optional range tail of two or four digits ("2026-27", "2024/25",
// "2025-2026"). The character before the year is captured, RE2 having no
// lookbehind, so a currency amount ("$2000") and a year glued to a letter
// ("Election2024") are not years; a bare count ("Over 2000 Flights") still
// is. validLabelPattern admits no currency sign and folds dashes to "-", so
// those two exclusions are defence in depth for a label stored under other
// rules.
var labelYearPattern = regexp.MustCompile(`(^|[^$£€¥\w])((?:19|20)\d{2})(?:[-–/]((?:19|20)\d{2}|\d{2}))?\b`)

// LabelYearFloor is the earliest year a label may name at now and still be
// taken as current: now's year, or the year just ended while now is in
// January ("2026 Year in Review" in the first week of 2027 is right, not a
// slip).
func LabelYearFloor(now time.Time) int {
	floor := now.Year()
	if now.Month() == time.January {
		floor--
	}
	return floor
}

// labelYearMatch is one past year in a label: the byte span of the year text
// (the match minus its prefix character, range tail included), the text as
// written, and the four-digit years it carries, for the keyword exemption.
type labelYearMatch struct {
	start, end int
	shown      string
	years      []string
}

// pastLabelYearMatches returns every year in label that ends before
// LabelYearFloor(now), in label order, repeats included.
func pastLabelYearMatches(label string, now time.Time) []labelYearMatch {
	floor := LabelYearFloor(now)
	var out []labelYearMatch
	for _, idx := range labelYearPattern.FindAllStringSubmatchIndex(label, -1) {
		start, tail := label[idx[4]:idx[5]], ""
		if idx[6] >= 0 {
			tail = label[idx[6]:idx[7]]
		}
		year, _ := strconv.Atoi(start)
		shown := start
		years := []string{start}
		if end, _ := strconv.Atoi(tail); len(tail) == 4 {
			// A range running backwards ("2026-1999") is not a range.
			if end >= year {
				year, shown = end, label[idx[4]:idx[1]]
				years = append(years, tail)
			}
		} else if tail != "" {
			// A two-digit tail is a range end only when it lands within
			// thirty years after the start, in the start's century or the
			// next ("1999-00"). A month ("2024-11-05", "2024-12") never does
			// for a start year past 2012, and the label is judged by its year
			// alone.
			if end += year / 100 * 100; end <= year {
				end += 100
			}
			if end <= year+30 {
				year, shown = end, label[idx[4]:idx[1]]
			}
		}
		if year < floor {
			out = append(out, labelYearMatch{start: idx[4], end: idx[4] + len(shown), shown: shown, years: years})
		}
	}
	return out
}

// PastLabelYears returns the years in label that end before
// LabelYearFloor(now), as written ("2024", "2024/25"), distinct and in label
// order. The grouper has labelled the 2026 midterms a 2024 election, and a
// label naming an earlier year is wrong far more often than it is about that
// year. Later years are kept, since talk of 2028 the week after the midterms
// is a real topic and the grouper's slips run backwards.
func PastLabelYears(label string, now time.Time) []string {
	var years []string
	for _, m := range pastLabelYearMatches(label, now) {
		if !slices.Contains(years, m.shown) {
			years = append(years, m.shown)
		}
	}
	return years
}

// labelYearSeparators are the separators removed along with a stripped year
// when they follow it, longest first.
var labelYearSeparators = []string{": ", " - ", " – ", " / ", "'s "}

// labelEndWords are function words a label may not end on once a year is
// stripped: "Class of 2024" without its year is "Class of".
var labelEndWords = map[string]bool{
	"and": true, "of": true, "in": true, "for": true, "the": true, "to": true, "with": true,
	"at": true, "on": true, "from": true, "by": true, "a": true, "an": true,
}

// applyLabelYearRule strips past years the cluster's keywords do not back
// from every label, and drops a cluster whose label is unusable without them.
//
// A label naming a past year (PastLabelYears) is kept unchanged when any
// keyword, split on "_", is one of those years written in four digits:
// people do post about "1989" or "2024 YR4". Otherwise each past year is cut
// out with one attached separator, and the cluster is re-validated; it is
// dropped when that fails, when the label now ends on a function word, or
// when fewer than three letters are left. The current year is never
// substituted: a date the terms do not carry is a guess.
func applyLabelYearRule(clusters []TopicCluster, now time.Time) (kept []TopicCluster, stripped, dropped int) {
	kept = make([]TopicCluster, 0, len(clusters))
	for _, c := range clusters {
		matches := pastLabelYearMatches(c.Label, now)
		if len(matches) == 0 || keywordsBackYear(c.Keywords, matches) {
			kept = append(kept, c)
			continue
		}
		original := c.Label
		c.Label = stripLabelYears(c.Label, matches)
		validated, reason := validateCluster(c)
		if reason == "" && endsOnFunctionWord(validated.Label) {
			reason = "function_word_end"
		}
		if reason == "" && countLetters(validated.Label) < 3 {
			reason = "too_few_letters"
		}
		if reason != "" {
			dropped++
			slog.Debug("topics: label year rule dropped cluster", "label", original, "stripped_label", c.Label, "reason", reason)
			continue
		}
		stripped++
		slog.Debug("topics: label year rule stripped label", "label", original, "stripped_label", validated.Label)
		kept = append(kept, validated)
	}
	slog.Info("topics: label year rule", "label_year_stripped", stripped, "label_year_dropped", dropped)
	return kept, stripped, dropped
}

// keywordsBackYear reports whether any keyword part is one of the matched
// four-digit years.
func keywordsBackYear(keywords []string, matches []labelYearMatch) bool {
	for _, k := range keywords {
		for _, part := range strings.Split(k, "_") {
			for _, m := range matches {
				if slices.Contains(m.years, part) {
					return true
				}
			}
		}
	}
	return false
}

// stripLabelYears removes each matched year with one attached separator: a
// following ": ", " - ", " – ", " / " or "'s ", else a following space, else
// a preceding one. Matches are cut last to first so earlier spans stay valid.
// Whitespace is then collapsed and the ends trimmed of spaces and of a
// separator left dangling ("Election -").
func stripLabelYears(label string, matches []labelYearMatch) string {
	for i := len(matches) - 1; i >= 0; i-- {
		start, end := matches[i].start, matches[i].end
		rest := label[end:]
		cut := false
		for _, sep := range labelYearSeparators {
			if strings.HasPrefix(rest, sep) {
				end += len(sep)
				cut = true
				break
			}
		}
		if !cut {
			if strings.HasPrefix(rest, " ") {
				end++
			} else if start > 0 && label[start-1] == ' ' {
				start--
			}
		}
		label = label[:start] + label[end:]
	}
	return strings.Trim(strings.Join(strings.Fields(label), " "), " :-–/,")
}

// endsOnFunctionWord reports whether the label's last word is in
// labelEndWords.
func endsOnFunctionWord(label string) bool {
	words := strings.Fields(label)
	return len(words) > 0 && labelEndWords[strings.ToLower(words[len(words)-1])]
}

func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}
