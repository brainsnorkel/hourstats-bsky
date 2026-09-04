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
// yellow, magenta, green).
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

// fixedLanguageSlots pins the languages that recur at the top of the Bluesky
// firehose to a palette slot, so a language keeps its colour from month to
// month (colour follows the entity, not its rank). Languages without a pin
// take whatever slots are left, largest first.
var fixedLanguageSlots = map[string]int{"en": 0, "pt": 1, "ja": 2, "es": 3, "de": 4}

// OtherCode is the synthetic series holding every language without a band.
const OtherCode = "other"

// UndeterminedCode is the bucket for posts with no usable language tag.
const UndeterminedCode = "und"

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
		if c != "en" && c != OtherCode {
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
	assignLanguageColors(series)
	shown := shownCodes(series)
	other := 0
	for c, n := range totals {
		if !shown[c] {
			other += n
		}
	}
	if other > 0 {
		series = append(series, LanguageSeries{Code: OtherCode, Name: OtherCode, Total: other, Color: languageOther})
	}
	return series
}

// assignLanguageColors gives pinned languages their fixed slot and hands the
// remaining slots to the rest in series order.
func assignLanguageColors(series []LanguageSeries) {
	used := make([]bool, len(languagePalette))
	for i, s := range series {
		if slot, ok := fixedLanguageSlots[s.Code]; ok && slot < len(used) && !used[slot] {
			series[i].Color = languagePalette[slot]
			used[slot] = true
		}
	}
	next := 0
	for i, s := range series {
		if _, ok := fixedLanguageSlots[s.Code]; ok && series[i].Color != (color.RGBA{}) {
			continue
		}
		for next < len(used) && used[next] {
			next++
		}
		if next >= len(used) {
			series[i].Color = languageOther
			continue
		}
		series[i].Color = languagePalette[next]
		used[next] = true
	}
}

// shownCodes is the set of codes that have their own band; "other" is never
// in it, so a stored "other" key would fold into the remainder like any
// other unlisted code.
func shownCodes(series []LanguageSeries) map[string]bool {
	shown := map[string]bool{}
	for _, s := range series {
		if s.Code != OtherCode {
			shown[s.Code] = true
		}
	}
	return shown
}

// languageValue is the count for a series on one day, folding the remainder
// into "other".
func languageValue(d DailyVolumePoint, s LanguageSeries, shown map[string]bool) int {
	if s.Code != OtherCode {
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
