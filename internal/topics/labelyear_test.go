package topics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestPastLabelYears is the weekly report's year table
// (TestBuildWeeklyReportTextDropsWrongYearTopic in cmd/hourstats), judged
// at the Monday each week starts.
func TestPastLabelYears(t *testing.T) {
	week := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)       // Mon 2 – Sun 8 Nov 2026
	crossYear := time.Date(2025, time.December, 29, 0, 0, 0, 0, time.UTC) // Mon 29 Dec 2025 – Sun 4 Jan 2026
	january := time.Date(2027, time.January, 4, 0, 0, 0, 0, time.UTC)     // Mon 4 – Sun 10 Jan 2027
	february := time.Date(2027, time.February, 1, 0, 0, 0, 0, time.UTC)   // Mon 1 – Sun 7 Feb 2027
	cases := []struct {
		name      string
		label     string
		now       time.Time // zero: week
		wantYears []string
	}{
		{name: "a past year", label: "2024 US Presidential Election", wantYears: []string{"2024"}},
		{name: "two past years, each reported once", label: "2020 vs 2024: 2024 Turnout", wantYears: []string{"2020", "2024"}},
		{name: "the week's year", label: "2026 Midterm Elections"},
		{name: "a later year is kept", label: "2028 Presidential Race"},
		{name: "a far later year is kept", label: "Agenda 2030"},
		{name: "a past year beyond the 60-rune cut still counts", label: strings.Repeat("Midterms ", 7) + "2024", wantYears: []string{"2024"}},
		{name: "a currency amount is not a year", label: "$2000 Stimulus Checks"},
		{name: "a bare count reads as a year, the accepted cost", label: "Over 2000 Flights Cancelled", wantYears: []string{"2000"}},
		{name: "a year glued to a letter is not a year", label: "Election2024 Recap"},
		{name: "a longer number is not a year", label: "Flight 20245"},
		{name: "a two-digit number is not a year", label: "COVID-19 Inquiry"},
		{name: "a season range ending in the week's year", label: "2025-26 NBA Season"},
		{name: "a fiscal range ending before the week", label: "2024/25 Budget", wantYears: []string{"2024/25"}},
		{name: "a range starting in the week's year", label: "2026/27 Budget"},
		{name: "a range written in full is judged by its end", label: "2024-2026 Term"},
		{name: "a range written in full that ended before the week", label: "2023-2025 Term", wantYears: []string{"2023-2025"}},
		{name: "a range running backwards is judged by its first year", label: "2026-1999 Countdown"},
		{name: "a century roll is a range end", label: "1999-00 Y2K Scare", wantYears: []string{"1999-00"}},
		{name: "a decade range is a range", label: "2016-26 Decade"},
		{name: "a thirty-year range is a range", label: "2020-50 Plan"},
		{name: "a tail past thirty years is not a range", label: "2020-51 Plan", wantYears: []string{"2020"}},
		{name: "a date is judged by its year, not read as a range", label: "2024-11-05 Vote", wantYears: []string{"2024"}},
		{name: "a year-month is judged by its year", label: "2024-12 Recap", wantYears: []string{"2024"}},
		{name: "no label", label: ""},
		{name: "a cross-year week keeps its first year", label: "2025 Review and 2026 Preview", now: crossYear},
		{name: "a cross-year week still drops an earlier year", label: "2024 Review", now: crossYear, wantYears: []string{"2024"}},
		{name: "a season ending in the week's year is kept in January too", label: "2026-27 NBA Season", now: january},
		{name: "a January week keeps the year just ended", label: "2026 Year in Review", now: january},
		{name: "a January week keeps the season that ended last year", label: "2025-26 NBA Season", now: january},
		{name: "a January week still drops two years back", label: "2025 Recap", now: january, wantYears: []string{"2025"}},
		{name: "a February week drops the year just ended", label: "2026 Year in Review", now: february, wantYears: []string{"2026"}},
		{name: "a February week drops the season that ended last year", label: "2025-26 NBA Season", now: february, wantYears: []string{"2025-26"}},
		{name: "a February week drops a full range that ended last year", label: "2025-2026 Season", now: february, wantYears: []string{"2025-2026"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := tc.now
			if now.IsZero() {
				now = week
			}
			if got := PastLabelYears(tc.label, now); !slices.Equal(got, tc.wantYears) {
				t.Errorf("PastLabelYears(%q) = %v, want %v", tc.label, got, tc.wantYears)
			}
		})
	}
}

func TestLabelYearFloor(t *testing.T) {
	for _, tc := range []struct {
		now  time.Time
		want int
	}{
		{time.Date(2026, time.November, 9, 0, 0, 0, 0, time.UTC), 2026},
		{time.Date(2027, time.January, 31, 23, 59, 0, 0, time.UTC), 2026},
		{time.Date(2027, time.February, 1, 0, 0, 0, 0, time.UTC), 2027},
	} {
		if got := LabelYearFloor(tc.now); got != tc.want {
			t.Errorf("LabelYearFloor(%v) = %d, want %d", tc.now, got, tc.want)
		}
	}
}

// TestApplyLabelYearRule: an unbacked past year is cut out of the label, a
// cluster is dropped only when the label is unusable without it, and a year
// one of the cluster's keywords carries stands.
func TestApplyLabelYearRule(t *testing.T) {
	nov := time.Date(2026, time.November, 9, 0, 0, 0, 0, time.UTC)
	jan := time.Date(2027, time.January, 4, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		label    string
		keywords []string
		now      time.Time // zero: nov
		want     string    // empty: dropped
	}{
		{name: "an unbacked past year is stripped", label: "2024 US Presidential Election", keywords: []string{"election", "president"}, want: "US Presidential Election"},
		{name: "a keyword carrying the year keeps the label", label: "2024 US Presidential Election", keywords: []string{"2024_election", "president"}, want: "2024 US Presidential Election"},
		{name: "a later year is kept", label: "2028 Presidential Race", keywords: []string{"race"}, want: "2028 Presidential Race"},
		{name: "a season ending this year is kept", label: "2025-26 NBA Season", keywords: []string{"nba"}, want: "2025-26 NBA Season"},
		{name: "a past season range goes whole, leaving the weak but valid Season", label: "2024/25 Season", keywords: []string{"season"}, want: "Season"},
		{name: "a label invalid without its year is dropped", label: "1989 (Taylor's Version)", keywords: []string{"taylor", "version"}},
		{name: "a year keyword keeps an album title", label: "1989 (Taylor's Version)", keywords: []string{"1989_taylor"}, want: "1989 (Taylor's Version)"},
		{name: "a label ending on a function word is dropped", label: "Class of 2024", keywords: []string{"class"}},
		{name: "a year takes its colon separator with it", label: "Election 2024: Results", keywords: []string{"election", "results"}, want: "Election Results"},
		{name: "January keeps the year just ended", label: "2026 Year in Review", keywords: []string{"review"}, now: jan, want: "2026 Year in Review"},
		{name: "a currency amount is not a year", label: "$2000 Fine", keywords: []string{"fine"}, want: "$2000 Fine"},
		{name: "a compound label keeps its other subjects", label: "2024 US Election and Midterms", keywords: []string{"midterms"}, want: "US Election and Midterms"},
		{name: "a possessive year goes with its 's", label: "2024's Biggest Moments", keywords: []string{"moments"}, want: "Biggest Moments"},
		{name: "a label left with too few letters is dropped", label: "2024 UK", keywords: []string{"uk"}},
		{name: "a dangling dash is trimmed", label: "Midterms - 2024", keywords: []string{"midterms"}, want: "Midterms"},
		{name: "a keyword with a different year does not back it", label: "2024 Election", keywords: []string{"2026_midterms"}, want: "Election"},
		{name: "every unbacked past year goes, even when it leaves a weak label", label: "2020 vs 2024 Turnout", keywords: []string{"turnout"}, want: "vs Turnout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := tc.now
			if now.IsZero() {
				now = nov
			}
			in := []TopicCluster{{Label: tc.label, Keywords: tc.keywords, Description: "d", Justification: "j"}}
			kept, stripped, dropped := applyLabelYearRule(in, now)
			if tc.want == "" {
				if len(kept) != 0 || dropped != 1 || stripped != 0 {
					t.Fatalf("kept %+v (stripped %d, dropped %d), want the cluster dropped", kept, stripped, dropped)
				}
				return
			}
			if len(kept) != 1 || kept[0].Label != tc.want {
				t.Fatalf("kept %+v, want one cluster labelled %q", kept, tc.want)
			}
			wantStripped := 0
			if tc.want != tc.label {
				wantStripped = 1
			}
			if stripped != wantStripped || dropped != 0 {
				t.Errorf("stripped %d, dropped %d; want %d, 0", stripped, dropped, wantStripped)
			}
		})
	}
}

// TestBuildPrompt_DatesThePrompt: the date line follows the first line, and
// no example label carries a year the model could copy.
func TestBuildPrompt_DatesThePrompt(t *testing.T) {
	now := time.Date(2026, time.November, 9, 23, 30, 0, 0, time.UTC)
	prompt := buildPrompt([]TermScore{{Term: "midterms", Score: 5.0}}, nil, promptContext{Now: now})
	lines := strings.Split(prompt, "\n")
	want := "Today is 2026-11-09 (UTC). Do not put a year in a label unless one of the terms contains that year."
	if len(lines) < 2 || lines[1] != want {
		t.Fatalf("second line = %q, want %q", lines[1], want)
	}
	year := regexp.MustCompile(`(19|20)\d\d`)
	examples := 0
	for _, l := range lines {
		if strings.Contains(l, "Good labels") || strings.Contains(l, "Bad labels") || strings.Contains(l, "e.g.") {
			examples++
			if year.MatchString(l) {
				t.Errorf("example line carries a year: %q", l)
			}
		}
	}
	if examples < 3 {
		t.Errorf("found %d example lines, want the good, bad and e.g. lines", examples)
	}
	if strings.Contains(prompt, "Hurricane Milton") || strings.Contains(prompt, "Donald Trump") {
		t.Error("the dated and catch-all examples should be gone")
	}
}

// TestGroupAndLabel_DatesPromptAndStripsYear: the grouper's clock dates the
// prompt it sends, and a label naming an unbacked past year comes back
// without it.
func TestGroupAndLabel_DatesPromptAndStripsYear(t *testing.T) {
	var body []byte
	clusters := []TopicCluster{{
		Label: "2024 US Presidential Election", Description: "Election talk",
		Keywords: []string{"midterms", "election"}, Justification: "Election terms",
	}}
	mock := geminiMockHandler(clusters)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		mock(w, r)
	}))
	defer srv.Close()

	g := NewGrouperWithEndpoint("test-key", srv.URL)
	g.SetClock(func() time.Time { return time.Date(2026, time.November, 9, 0, 0, 0, 0, time.UTC) })
	got, err := g.GroupAndLabel(context.Background(), []TermScore{{Term: "midterms", Score: 9}, {Term: "election", Score: 8}})
	if err != nil {
		t.Fatalf("GroupAndLabel: %v", err)
	}
	var req geminiRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.Contents) == 0 || len(req.Contents[0].Parts) == 0 {
		t.Fatalf("request body unreadable (%v): %s", err, body)
	}
	if !strings.Contains(req.Contents[0].Parts[0].Text, "Today is 2026-11-09") {
		t.Error("prompt lacks the date line")
	}
	if len(got) != 1 || got[0].Label != "US Presidential Election" {
		t.Errorf("clusters = %+v, want one labelled %q", got, "US Presidential Election")
	}
}
