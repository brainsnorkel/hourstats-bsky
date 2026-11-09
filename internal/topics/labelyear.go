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

// labelYearMatch is one past year in a label: the byte span to cut when it
// is stripped (the match minus its prefix character, the whole tail included
// even when it is not a range, plus a following "-NN" date part), the year
// text as written, and the four-digit years it carries, for the backing test.
type labelYearMatch struct {
	start, end int
	shown      string
	years      []string
}

// labelDatePartPattern is the day of a date after a year-month match:
// "2024-11" matched, "-05" follows.
var labelDatePartPattern = regexp.MustCompile(`^[-–/]\d{1,2}\b`)

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
		isRange := false
		if end, _ := strconv.Atoi(tail); len(tail) == 4 {
			// A range running backwards ("2026-1999") is not a range.
			if end >= year {
				year, shown, isRange = end, label[idx[4]:idx[1]], true
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
				year, shown, isRange = end, label[idx[4]:idx[1]], true
			}
		}
		if year < floor {
			// The cut covers the whole match, so a tail that is not a range
			// ("2024-12", "2020-51", "2024-2020") goes with its year rather
			// than being left behind as "-12 Recap", and so does a date's day.
			end := idx[1]
			if tail != "" && !isRange {
				if d := labelDatePartPattern.FindStringIndex(label[end:]); d != nil {
					end += d[1]
				}
			}
			out = append(out, labelYearMatch{start: idx[4], end: end, shown: shown, years: years})
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

// labelStartWords and labelEndWords are the words a label may not start or
// end on once a year is stripped: "2024 and Beyond" without its year is "and
// Beyond", "Class of 2024" is "Class of". Such a label is dropped, not
// repaired: what it was about went with the year.
var (
	labelStartWords = map[string]bool{
		"and": true, "or": true, "vs": true, "&": true, "of": true, "in": true,
		"from": true, "to": true, "for": true,
	}
	labelEndWords = map[string]bool{
		"and": true, "of": true, "in": true, "for": true, "the": true, "to": true, "with": true,
		"at": true, "on": true, "from": true, "by": true, "a": true, "an": true,
		"vs": true, "&": true, "versus": true,
	}
	// labelGenericNouns are what "2024/25 Season" or "Election (2024)" leave
	// behind: a label naming no subject once its year is gone.
	labelGenericNouns = map[string]bool{
		"season": true, "year": true, "election": true, "elections": true,
		"recap": true, "review": true, "results": true,
	}
	// labelEmptyPairs are the brackets and quotes a stripped year can leave
	// empty: "Taylor Swift (1989)" becomes "Taylor Swift ()".
	labelEmptyPairs = regexp.MustCompile(`\(\s*\)|\[\s*\]|"\s*"|“\s*”|'\s*'`)
)

// applyLabelYearRule strips the past years a cluster's top terms do not back
// from every label, and drops a cluster whose label is unusable without them.
// It runs both before and after MergeSimilarClusters (it is idempotent: a
// kept label names only backed years): before, so the merge picks among
// labels already judged, and after, because a merged cluster's keywords are
// a union whose top term can differ from either member's.
//
// For each cluster, in order:
//  1. A label naming no past year (pastLabelYearMatches) is kept as is.
//  2. Each past year is judged on its own (yearBacked). It is backed only
//     when a keyword carrying that exact year is the cluster's top-scoring
//     keyword, ties allowed; a keyword with no score backs nothing. When
//     every past year is backed the cluster is kept unchanged: "1989
//     (Taylor's Version)" with 1989_taylor on top is about that year, while
//     a 2024_election under midterms is a comparison.
//  3. Only the unbacked years are cut out, each with one attached separator,
//     and emptied brackets and quotes go with them (stripLabelYears).
//  4. The result is dropped, not repaired, when it still names an unbacked
//     past year, starts on a conjunction or preposition, ends on a function
//     word, has fewer than two words of three letters or more, or is only a
//     generic noun such as "Season" (strippedLabelProblem).
//  5. Otherwise it is re-validated (validateCluster) and kept if that passes.
//
// The current year is never substituted: a date the terms do not carry is a
// guess.
func applyLabelYearRule(clusters []TopicCluster, terms []TermScore, now time.Time) (kept []TopicCluster, stripped, dropped int) {
	scores := make(map[string]float64, len(terms))
	for _, t := range terms {
		scores[strings.ToLower(t.Term)] = t.Score
	}
	kept = make([]TopicCluster, 0, len(clusters))
	for _, c := range clusters {
		var unbacked []labelYearMatch
		for _, m := range pastLabelYearMatches(c.Label, now) {
			if !yearBacked(m, c.Keywords, scores) {
				unbacked = append(unbacked, m)
			}
		}
		if len(unbacked) == 0 {
			kept = append(kept, c)
			continue
		}
		original := c.Label
		c.Label = stripLabelYears(c.Label, unbacked)
		validated := c
		reason := strippedLabelProblem(c.Label, c.Keywords, scores, now)
		if reason == "" {
			validated, reason = validateCluster(c)
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
	// Logged on every call, 0/0 included: the zero line is a deliberate
	// heartbeat, so the strip and drop rates can be tracked per cycle. Each
	// grouping path calls this twice (before and after the merge), so a cycle
	// logs two lines; the second normally reads 0/0 unless a merge changed a
	// cluster's top keyword.
	slog.Info("topics: label year rule", "label_year_stripped", stripped, "label_year_dropped", dropped)
	return kept, stripped, dropped
}

// strippedLabelProblem names why a label left by stripLabelYears is
// unusable, or returns "" (step 4 of applyLabelYearRule).
func strippedLabelProblem(label string, keywords []string, scores map[string]float64, now time.Time) string {
	for _, m := range pastLabelYearMatches(label, now) {
		if !yearBacked(m, keywords, scores) {
			return "past_year_remains"
		}
	}
	words := strings.Fields(label)
	if len(words) == 0 {
		return "empty"
	}
	if labelStartWords[strings.ToLower(words[0])] {
		return "function_word_start"
	}
	if labelEndWords[strings.ToLower(words[len(words)-1])] {
		return "function_word_end"
	}
	long := 0
	for _, w := range words {
		if countLetters(w) >= 3 {
			long++
		}
	}
	if long < 2 {
		return "too_few_words"
	}
	if labelGenericNouns[strings.ToLower(label)] {
		return "generic_noun"
	}
	return ""
}

// yearBacked reports whether a keyword carrying one of m's four-digit years
// is the cluster's top-scoring keyword (a tie with another keyword counts).
// A keyword with no score backs nothing, and with no scored keyword at all
// no year is backed. Anything less than the top is a comparison or a side
// mention: "2024 US Presidential Election" over midterms, election,
// 2024_election and vote is about the midterms.
func yearBacked(m labelYearMatch, keywords []string, scores map[string]float64) bool {
	top, scored := 0.0, false
	for _, k := range keywords {
		if s, ok := scores[strings.ToLower(k)]; ok && (!scored || s > top) {
			top, scored = s, true
		}
	}
	if !scored {
		return false
	}
	for _, k := range keywords {
		s, ok := scores[strings.ToLower(k)]
		if !ok || s < top {
			continue
		}
		for _, y := range keywordYears(k) {
			if slices.Contains(m.years, strconv.Itoa(y)) {
				return true
			}
		}
	}
	return false
}

// keywordYears returns the four-digit years (19xx, 20xx) among a keyword's
// "_"-separated parts.
func keywordYears(keyword string) []int {
	var years []int
	for _, part := range strings.Split(keyword, "_") {
		if len(part) == 4 && (strings.HasPrefix(part, "19") || strings.HasPrefix(part, "20")) {
			if y, err := strconv.Atoi(part); err == nil {
				years = append(years, y)
			}
		}
	}
	return years
}

// stripLabelYears removes each matched year with one attached separator: a
// following ": ", " - ", " – ", " / " or "'s ", else a following space, else
// a preceding one. Matches are cut last to first so earlier spans stay valid.
// Emptied brackets and quotes are then removed, whitespace collapsed, and the
// ends trimmed of spaces and of a separator left dangling ("Election -").
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
	label = labelEmptyPairs.ReplaceAllString(label, " ")
	return strings.Trim(strings.Join(strings.Fields(label), " "), " :-–/,")
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
