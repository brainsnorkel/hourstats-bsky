package sparkline

import (
	"image/color"
	"sort"
	"strings"
)

// Language series for the monthly volume chart.

// MaxLanguageSeries is how many languages get their own band; the rest fold
// into "other". Six is the categorical palette's validated depth.
const MaxLanguageSeries = 6

// languageNames maps primary subtags to display names. Unknown codes fall
// back to the upper-cased code.
var languageNames = map[string]string{
	"en": "English", "pt": "Portuguese", "ja": "Japanese", "es": "Spanish", "de": "German",
	"fr": "French", "ko": "Korean", "it": "Italian", "nl": "Dutch", "ru": "Russian",
	"zh": "Chinese", "tr": "Turkish", "pl": "Polish", "sv": "Swedish", "id": "Indonesian",
	"th": "Thai", "ar": "Arabic", "fa": "Persian", "uk": "Ukrainian", "vi": "Vietnamese",
	"fi": "Finnish", "da": "Danish", "no": "Norwegian", "nb": "Norwegian", "cs": "Czech",
	"el": "Greek", "he": "Hebrew", "hi": "Hindi", "hu": "Hungarian", "ca": "Catalan",
	"gl": "Galician", "eu": "Basque", "ga": "Irish", "cy": "Welsh", "tl": "Filipino",
	"ms": "Malay", "ro": "Romanian", "bg": "Bulgarian", "sk": "Slovak", "sl": "Slovenian",
	"hr": "Croatian", "sr": "Serbian", "lt": "Lithuanian", "lv": "Latvian", "et": "Estonian",
	"is": "Icelandic", "af": "Afrikaans", "sw": "Swahili", "ta": "Tamil", "te": "Telugu",
	"bn": "Bengali", "ur": "Urdu", "und": "untagged", "other": "other",
}

// LanguageName returns the display name for a primary language subtag.
func LanguageName(code string) string {
	if n, ok := languageNames[code]; ok {
		return n
	}
	return strings.ToUpper(code)
}

// languagePalette is the validated categorical order (blue, orange, aqua,
// yellow, magenta, green). English always takes slot 1; the remaining
// languages take slots in order of monthly volume, largest first.
var languagePalette = []color.RGBA{
	{42, 120, 214, 255},  // #2a78d6 blue
	{235, 104, 52, 255},  // #eb6834 orange
	{27, 175, 122, 255},  // #1baf7a aqua
	{237, 161, 0, 255},   // #eda100 yellow
	{232, 123, 164, 255}, // #e87ba4 magenta
	{0, 131, 0, 255},     // #008300 green
}

// languageOther is the neutral fill for the folded remainder.
var languageOther = color.RGBA{195, 194, 183, 255} // #c3c2b7

// LanguageSeries is one band of the stacked chart.
type LanguageSeries struct {
	Code  string // "en", "pt", ... or "other"
	Name  string
	Total int
	Color color.RGBA
}

// LanguageBreakdown picks the series for a month: English first when present,
// then the largest languages up to MaxLanguageSeries, then "other" holding
// everything else. Empty when there is no language data at all.
func LanguageBreakdown(days []DailyVolumePoint) []LanguageSeries {
	totals := map[string]int{}
	for _, d := range days {
		for code, n := range d.Languages {
			totals[code] += n
		}
	}
	if len(totals) == 0 {
		return nil
	}
	codes := make([]string, 0, len(totals))
	for c := range totals {
		if c != "en" && c != "other" {
			codes = append(codes, c)
		}
	}
	sort.Slice(codes, func(i, j int) bool {
		if totals[codes[i]] != totals[codes[j]] {
			return totals[codes[i]] > totals[codes[j]]
		}
		return codes[i] < codes[j]
	})

	var series []LanguageSeries
	if totals["en"] > 0 {
		series = append(series, LanguageSeries{Code: "en", Name: LanguageName("en"), Total: totals["en"]})
	}
	for _, c := range codes {
		if len(series) >= MaxLanguageSeries {
			break
		}
		series = append(series, LanguageSeries{Code: c, Name: LanguageName(c), Total: totals[c]})
	}
	for i := range series {
		series[i].Color = languagePalette[i]
	}
	shown := map[string]bool{}
	for _, s := range series {
		shown[s.Code] = true
	}
	other := 0
	for c, n := range totals {
		if !shown[c] {
			other += n
		}
	}
	if other > 0 {
		series = append(series, LanguageSeries{Code: "other", Name: "other", Total: other, Color: languageOther})
	}
	return series
}

// languageValue is the count for a series on one day, folding the remainder
// into "other".
func languageValue(d DailyVolumePoint, s LanguageSeries, shown map[string]bool) int {
	if s.Code != "other" {
		return d.Languages[s.Code]
	}
	n := 0
	for c, v := range d.Languages {
		if !shown[c] {
			n += v
		}
	}
	return n
}
