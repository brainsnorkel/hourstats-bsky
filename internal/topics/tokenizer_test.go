package topics

import (
	"strings"
	"testing"
)

func TestTokenize_PlainText(t *testing.T) {
	got := Tokenize("Breaking earthquake hits coastal regions")
	if !contains(got, "breaking") || !contains(got, "earthquake") || !contains(got, "coastal") || !contains(got, "regions") {
		t.Errorf("content words should be present, got %v", got)
	}
	if !contains(got, "breaking_earthquake") {
		t.Errorf("bigram breaking_earthquake should be present, got %v", got)
	}
}

func TestTokenize_URLs(t *testing.T) {
	got := Tokenize("Volcano this https://example.com/foo and http://bar.org important earthquake")
	if contains(got, "https") || contains(got, "example") || contains(got, "http") {
		t.Errorf("URLs should be stripped, got %v", got)
	}
	if !contains(got, "volcano") || !contains(got, "important") || !contains(got, "earthquake") {
		t.Errorf("non-URL words should remain, got %v", got)
	}
}

func TestTokenize_Mentions(t *testing.T) {
	got := Tokenize("Hey @user.bsky.social posted about climate policy")
	if contains(got, "user") || contains(got, "bsky") {
		t.Errorf("mentions should be stripped, got %v", got)
	}
	if !contains(got, "climate") || !contains(got, "policy") {
		t.Errorf("non-mention words should remain, got %v", got)
	}
}

func TestTokenize_Emoji(t *testing.T) {
	got := Tokenize("🔥🔥🔥 incredible content 🎉 stellar")
	if contains(got, "🔥") || contains(got, "🎉") {
		t.Errorf("emoji should be stripped, got %v", got)
	}
	if !contains(got, "incredible") || !contains(got, "content") || !contains(got, "stellar") {
		t.Errorf("words should remain, got %v", got)
	}
}

func TestTokenize_Stopwords(t *testing.T) {
	got := Tokenize("the quick brown fox and the lazy dog")
	if contains(got, "the") || contains(got, "and") {
		t.Errorf("stopwords should be removed, got %v", got)
	}
	if !contains(got, "quick") || !contains(got, "brown") || !contains(got, "fox") {
		t.Errorf("content words should remain, got %v", got)
	}
}

func TestTokenize_ShortWords(t *testing.T) {
	got := Tokenize("AI is an ok ML to go on by")
	if len(got) != 0 {
		t.Errorf("all words < 3 chars, expected empty, got %v", got)
	}
}

func TestTokenize_AllStopwords(t *testing.T) {
	got := Tokenize("the and for are but not you all")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestTokenize_Empty(t *testing.T) {
	got := Tokenize("")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestTokenize_MixedContent(t *testing.T) {
	got := Tokenize("🇺🇸 @president.bsky.social announces https://whitehouse.gov policy on climate 🌍")
	if contains(got, "president") || contains(got, "whitehouse") {
		t.Errorf("mentions/URLs should be stripped, got %v", got)
	}
	if !contains(got, "announces") || !contains(got, "policy") || !contains(got, "climate") {
		t.Errorf("content words should remain, got %v", got)
	}
}

func TestTokenize_Punctuation(t *testing.T) {
	got := Tokenize("wow!!! incredible... stellar??? remarkable---stuff")
	if !contains(got, "wow") || !contains(got, "incredible") || !contains(got, "stellar") {
		t.Errorf("punctuation should be trimmed, got %v", got)
	}
}

func TestTokenize_SocialMediaStopwords(t *testing.T) {
	got := Tokenize("lol omg wtf bruh literally the best thing ever")
	if contains(got, "lol") || contains(got, "omg") || contains(got, "bruh") || contains(got, "literally") || contains(got, "best") {
		t.Errorf("social media stopwords should be removed, got %v", got)
	}
}

func TestTokenize_ContractionFragments(t *testing.T) {
	got := Tokenize("I don't think he didn't know")
	if contains(got, "don") || contains(got, "didn") {
		t.Errorf("contraction fragments should be stopwords, got %v", got)
	}
}

func TestTokenize_GenericSentiment(t *testing.T) {
	got := Tokenize("I love this amazing happy beautiful day")
	if contains(got, "love") || contains(got, "amazing") || contains(got, "happy") || contains(got, "beautiful") || contains(got, "day") {
		t.Errorf("generic sentiment words should be stopwords, got %v", got)
	}
}

func TestTokenize_Profanity(t *testing.T) {
	got := Tokenize("this fucking shit sucks damn")
	if contains(got, "fucking") || contains(got, "shit") || contains(got, "sucks") || contains(got, "damn") {
		t.Errorf("profanity should be stopwords, got %v", got)
	}
}

func TestTokenize_PureNumbers(t *testing.T) {
	got := Tokenize("in 2026 there were 500 incidents")
	if contains(got, "2026") || contains(got, "500") {
		t.Errorf("pure numbers should not be unigrams, got %v", got)
	}
	for _, tok := range got {
		if strings.Contains(tok, "500") || strings.Contains(tok, "2026") {
			t.Errorf("no bigram should carry 500, nor 2026 next to function words, got %v", got)
		}
	}
	if !contains(got, "incidents") {
		t.Errorf("content word should remain, got %v", got)
	}
}

func TestTokenize_BareDomains(t *testing.T) {
	got := Tokenize("volcano example.com and foo.org earthquake")
	if contains(got, "example") || contains(got, "foo") {
		t.Errorf("bare domains should be stripped, got %v", got)
	}
	if !contains(got, "volcano") || !contains(got, "earthquake") {
		t.Errorf("non-domain words should remain, got %v", got)
	}
}

func TestTokenize_PlatformNames(t *testing.T) {
	got := Tokenize("posted on youtube and twitter and reddit")
	if contains(got, "youtube") || contains(got, "twitter") || contains(got, "reddit") {
		t.Errorf("platform names should be stopwords, got %v", got)
	}
}

func TestTokenize_Bigrams(t *testing.T) {
	got := Tokenize("super bowl halftime performance")
	if !contains(got, "super_bowl") || !contains(got, "bowl_halftime") || !contains(got, "halftime_performance") {
		t.Errorf("bigrams should be generated, got %v", got)
	}
}

func TestTokenize_BigramBridgesStopword(t *testing.T) {
	got := Tokenize("halftime show tonight")
	if !contains(got, "halftime_show") {
		t.Errorf("a plain word should pair with a content stopword, got %v", got)
	}
	if contains(got, "show") {
		t.Errorf("content stopword 'show' should not appear as unigram, got %v", got)
	}
	if contains(got, "show_tonight") {
		t.Errorf("bigram of two content stopwords should be excluded, got %v", got)
	}
}

func TestIsRepetitive_KeywordStuffing(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"trump_epstein_spam", "TRUMP EPSTEIN TRUMP EPSTEIN TRUMP EPSTEIN TRUMP EPSTEIN TRUMP EPSTEIN TRUMP EPSTEIN TRUMP EPSTEIN TRUMP EPSTEIN", true},
		{"single_word_spam", "Epstein Epstein Epstein Epstein Epstein Epstein Epstein Epstein Epstein Epstein Epstein Epstein", true},
		{"normal_post", "The earthquake in Turkey has caused widespread damage and rescue efforts are underway across the region", false},
		{"short_post", "hello hello hello", false},
		{"empty", "", false},
		{"moderate_repetition", "trump trump election vote trump election rally crowd trump supporters rally", false},
		{"three_word_spam", "buy now buy now buy now buy now buy now buy now", true},
		{
			"preamble_then_spam",
			"KASH PATEL IS NOW TALKING ABOUT \u201cANTIFA FUNDERS\u201d INSTEAD OF THE EPSTEIN FILES JUSTICE FOR THE SURVIVORS Trump-Epstein, Trump-Epstein, Trump-Epstein, Trump-Epstein, Trump-Epstein, Trump-Epstein, Trump-Epstein, Trump-Epstein, Trump-Epstein, Trump-Epstein, Trump-Epstein, Trump-Epstein.....",
			true,
		},
		{
			"hyphenated_spam",
			"TRUMP-EPSTEIN FILES TRUMP-EPSTEIN FILES TRUMP-EPSTEIN FILES TRUMP-EPSTEIN FILES TRUMP-EPSTEIN FILES TRUMP-EPSTEIN FILES TRUMP-EPSTEIN FILES TRUMP-EPSTEIN FILES",
			true,
		},
		{
			"punctuation_disguised_spam",
			"Epstein! Epstein? Epstein... Epstein, Epstein; Epstein: Epstein! Epstein? Epstein...",
			true,
		},
		{"four_repetitions_ok", "trump mentioned trump again said trump today trump statement was clear and direct", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRepetitive(tt.text)
			if got != tt.want {
				t.Errorf("IsRepetitive(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestStripWordPunctuation(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"trump-epstein,", "trump-epstein"},
		{"trump-epstein.....", "trump-epstein"},
		{"survivors!!!!", "survivors"},
		{"\u201cantifa", "antifa"},
		{"funders\u201d", "funders"},
		{"hello", "hello"},
		{"it's", "it's"},
		{"...", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripWordPunctuation(tt.input)
			if got != tt.want {
				t.Errorf("stripWordPunctuation(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestTokenize_CompoundsSurvive(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"the White House said", "white_house"},
		{"New York City", "new_york"},
		{"World Cup final", "world_cup"},
		{"Fox News host", "fox_news"},
		{"First Amendment case", "first_amendment"},
		{"Real Madrid won", "real_madrid"},
		{"on election day", "election_day"},
		{"the halftime show", "halftime_show"},
		{"Game of Thrones", "game_thrones"},
		{"Call of Duty", "call_duty"},
		{"State of the Union", "state_union"},
		{"post a banger", "post_banger"},
		{"Gov Newsom signs the bill", "gov_newsom"},
		{"Black Friday deals", "black_friday"},
		{"happy new year everyone", "new_year"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := Tokenize(tt.text); !contains(got, tt.want) {
				t.Errorf("Tokenize(%q) = %v, want %s", tt.text, got, tt.want)
			}
		})
	}
}

func TestTokenize_NoFunctionWordBigrams(t *testing.T) {
	tests := []struct {
		text   string
		reject []string
	}{
		{"and vote today", []string{"and_vote"}},
		{"Get out and vote today", []string{"and_vote", "out_and"}},
		{"the cybertruck is getting canned", []string{"the_cybertruck", "cybertruck_is"}},
		{"monitoring the results", []string{"monitoring_the", "the_results"}},
		{"what a banger that was", []string{"banger_that", "that_was"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := Tokenize(tt.text)
			for _, r := range tt.reject {
				if contains(got, r) {
					t.Errorf("Tokenize(%q) = %v, must not contain %s", tt.text, got, r)
				}
			}
			for _, tok := range got {
				a, b, ok := strings.Cut(tok, "_")
				if !ok {
					if functionWords[tok] || contentStopwords[tok] {
						t.Errorf("stopword unigram %s in %v", tok, got)
					}
					continue
				}
				if functionWords[a] || functionWords[b] {
					t.Errorf("bigram %s has a function-word side, got %v", tok, got)
				}
			}
		})
	}
}

func TestTokenize_SegmentBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		reject string
		keep   []string
	}{
		{"full_stop", "truck canned. Post vehicle", "canned_post", []string{"canned", "vehicle", "truck_canned", "post_vehicle"}},
		{"end_start", "the season is over at the end. Start again", "end_start", nil},
		{"colon_space", "breaking: volcano erupts", "breaking_volcano", []string{"volcano_erupts"}},
		{"ellipsis_unicode", "Taylor Swift… Travis Kelce", "swift_travis", []string{"taylor_swift", "travis_kelce"}},
		{"ellipsis_ascii", "Taylor Swift...Travis Kelce", "swift_travis", []string{"taylor_swift", "travis_kelce"}},
		{"em_dash", "Big Story — NYT", "story_nyt", nil},
		{"bullet", "volcano • tsunami", "volcano_tsunami", nil},
		{"pipe", "volcano | tsunami", "volcano_tsunami", nil},
		{"ideographic_stop", "volcano。tsunami", "volcano_tsunami", nil},
		{"fullwidth_exclamation", "volcano！tsunami", "volcano_tsunami", nil},
		{"fullwidth_question", "volcano？tsunami", "volcano_tsunami", nil},
		{"exclamation", "volcano erupts! Tsunami warning", "erupts_tsunami", []string{"volcano_erupts", "tsunami_warning"}},
		{"newline", "volcano eruption\ntsunami warning", "eruption_tsunami", []string{"tsunami_warning"}},
		{"semicolon", "volcano eruption; tsunami warning", "eruption_tsunami", nil},
		{"url", "volcano https://example.com/a eruption", "volcano_eruption", []string{"volcano", "eruption"}},
		{"bare_domain", "volcano example.com eruption", "volcano_eruption", []string{"volcano", "eruption"}},
		{"mention", "volcano @user.bsky.social eruption", "volcano_eruption", []string{"volcano", "eruption"}},
		{"emoji", "volcano 🌋 eruption", "volcano_eruption", []string{"volcano", "eruption"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.text)
			if contains(got, tt.reject) {
				t.Errorf("Tokenize(%q) = %v, must not contain %s", tt.text, got, tt.reject)
			}
			for _, k := range tt.keep {
				if !contains(got, k) {
					t.Errorf("Tokenize(%q) = %v, want %s", tt.text, got, k)
				}
			}
		})
	}
}

func TestTokenize_Years(t *testing.T) {
	tests := []struct {
		text   string
		want   []string
		reject []string
	}{
		{"the 2026 midterms", []string{"2026_midterms", "midterms"}, []string{"2026"}},
		{"midterms 2026 results", []string{"midterms_2026", "2026_results"}, []string{"2026"}},
		{"World Cup 2026", []string{"world_cup", "cup_2026"}, []string{"2026"}},
		{"in 2026 there were 500 people", nil, []string{"2026", "500", "2026_there", "there_were", "were_people", "2026_people"}},
		{"worse than 2024", nil, []string{"2024", "worse_than", "than_2024", "worse_2024"}},
		{"2024 election", []string{"2024_election"}, []string{"2024"}},
		{"new 2026 season", nil, []string{"new_2026", "2026_season", "2026"}},
		{"1999 2000 election", []string{"2000_election"}, []string{"1999_2000", "1999", "2000"}},
		{"route 2150 closed", nil, []string{"2150", "route_2150", "2150_closed"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := Tokenize(tt.text)
			for _, w := range tt.want {
				if !contains(got, w) {
					t.Errorf("Tokenize(%q) = %v, want %s", tt.text, got, w)
				}
			}
			for _, r := range tt.reject {
				if contains(got, r) {
					t.Errorf("Tokenize(%q) = %v, must not contain %s", tt.text, got, r)
				}
			}
		})
	}
}

func TestTokenize_NoBreakInsideSentence(t *testing.T) {
	tests := []struct {
		text   string
		want   []string
		reject []string
	}{
		{"Lakers vs. Celtics tonight", []string{"lakers_celtics"}, nil},
		{"Sen. Warren grills Hegseth", []string{"sen_warren", "warren_grills", "grills_hegseth"}, nil},
		{"Gov. Newsom signs", []string{"gov_newsom"}, nil},
		{"Dr. Fauci testifies", []string{"fauci_testifies"}, nil},
		{"iOS 26.1 update", []string{"ios_update"}, nil},
		{"rally at 8:00pm downtown", []string{"rally_00pm", "00pm_downtown"}, nil},
		{"rally at 8:00 downtown", []string{"rally_downtown"}, nil},
		{"senators, e.g. Warren, object", []string{"senators_warren"}, nil},
		{"the U.S. economy slows", []string{"economy_slows"}, nil},
		{"breaking:volcano erupts", []string{"breaking_volcano"}, nil},
		{"tired of it. Lakers lost", []string{"lakers_lost"}, []string{"tired_lakers"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := Tokenize(tt.text)
			for _, w := range tt.want {
				if !contains(got, w) {
					t.Errorf("Tokenize(%q) = %v, want %s", tt.text, got, w)
				}
			}
			for _, r := range tt.reject {
				if contains(got, r) {
					t.Errorf("Tokenize(%q) = %v, must not contain %s", tt.text, got, r)
				}
			}
		})
	}
}

func TestTokenize_Contractions(t *testing.T) {
	tests := []struct {
		text   string
		want   []string
		reject []string
	}{
		{"Democrats won't fund ICE", []string{"democrats", "fund"}, []string{"democrats_won", "won_fund", "democrats_wont", "wont_fund", "wont", "won"}},
		{"Democrats won’t fund ICE", nil, []string{"democrats_won", "won_fund"}},
		{"voters don't care", nil, []string{"voters_don", "voters_dont", "dont"}},
		{"senators couldn't agree", nil, []string{"senators_couldnt", "couldnt_agree", "couldnt"}},
		{"Mamdani won the race", []string{"mamdani_won", "won_race"}, []string{"won"}},
		{"Mamdani lost the race", []string{"mamdani_lost", "lost_race"}, nil},
		{"Don Lemon interview", []string{"don_lemon"}, []string{"don"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := Tokenize(tt.text)
			for _, w := range tt.want {
				if !contains(got, w) {
					t.Errorf("Tokenize(%q) = %v, want %s", tt.text, got, w)
				}
			}
			for _, r := range tt.reject {
				if contains(got, r) {
					t.Errorf("Tokenize(%q) = %v, must not contain %s", tt.text, got, r)
				}
			}
		})
	}
}

func TestTokenize_StopwordCompoundsFirst(t *testing.T) {
	for pair := range stopwordCompounds {
		a, b, _ := strings.Cut(pair, "_")
		t.Run(pair, func(t *testing.T) {
			if got := Tokenize(a + " " + b + " again"); !contains(got, pair) {
				t.Errorf("Tokenize(%q) = %v, want %s", a+" "+b, got, pair)
			}
			if got := Tokenize(a + ". " + b); contains(got, pair) {
				t.Errorf("Tokenize(%q) = %v, must not contain %s", a+". "+b, got, pair)
			}
		})
	}
	if got := Tokenize("white. house"); contains(got, "white_house") {
		t.Errorf("white_house must not form across a boundary, got %v", got)
	}
}

func TestTokenize_ClassMoves(t *testing.T) {
	tests := []struct {
		text   string
		reject []string
	}{
		{"he admitted having the hall pass", []string{"admitted_having", "having_hall"}},
		{"getting groceries", []string{"getting_groceries"}},
		{"doing laundry", []string{"doing_laundry"}},
		{"making pasta", []string{"making_pasta"}},
		{"rain today", []string{"rain_today"}},
		{"Celtics tonight", []string{"celtics_tonight"}},
		{"storm tomorrow", []string{"storm_tomorrow"}},
		{"earthquake yesterday", []string{"earthquake_yesterday"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := Tokenize(tt.text)
			for _, r := range tt.reject {
				if contains(got, r) {
					t.Errorf("Tokenize(%q) = %v, must not contain %s", tt.text, got, r)
				}
			}
		})
	}
}

func TestTokenize_SkipThe(t *testing.T) {
	tests := []struct {
		text   string
		want   []string
		reject []string
	}{
		{"monitoring the results", []string{"monitoring_results"}, []string{"monitoring_the", "the_results"}},
		{"beat the Lakers", []string{"beat_lakers"}, []string{"beat_the", "the_lakers"}},
		{"The Lakers won", []string{"lakers_won"}, []string{"the_lakers", "the"}},
		{"great game. The Lakers won", []string{"lakers_won"}, []string{"game_lakers", "the_lakers"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := Tokenize(tt.text)
			for _, w := range tt.want {
				if !contains(got, w) {
					t.Errorf("Tokenize(%q) = %v, want %s", tt.text, got, w)
				}
			}
			for _, r := range tt.reject {
				if contains(got, r) {
					t.Errorf("Tokenize(%q) = %v, must not contain %s", tt.text, got, r)
				}
			}
		})
	}
}

func TestEligible(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"volcano erupts near the coast", true},
		{"volcano erupts #breaking", true},
		{"#3dart #3drender #pinup", false},
		{"#aiart #nsfw", false},
		{"TRUMP EPSTEIN TRUMP EPSTEIN TRUMP EPSTEIN TRUMP EPSTEIN TRUMP EPSTEIN", false},
	}
	for _, tt := range tests {
		if got := Eligible(tt.text); got != tt.want {
			t.Errorf("Eligible(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

// v2ContractionWords are the joined "n't" forms tokenizer v2 produces that
// the legacy tokenizer never saw (it split them at the apostrophe).
var v2ContractionWords = map[string]bool{
	"couldnt": true, "wouldnt": true, "shouldnt": true, "hasnt": true, "hadnt": true,
	"aint": true, "mustnt": true,
}

// TestStopwordClasses_CoverLegacy checks that the two classes partition the
// pre-v2 stopword list exactly: no word was dropped by the split, none was
// added beyond the v2ContractionWords, and none is in both classes.
func TestStopwordClasses_CoverLegacy(t *testing.T) {
	for w := range functionWords {
		if contentStopwords[w] {
			t.Errorf("%q is both a function word and a content stopword", w)
		}
		if !legacyStopwords[w] && !v2ContractionWords[w] {
			t.Errorf("function word %q was not a legacy stopword", w)
		}
	}
	for w := range contentStopwords {
		if !legacyStopwords[w] {
			t.Errorf("content stopword %q was not a legacy stopword", w)
		}
	}
	for w := range legacyStopwords {
		if !functionWords[w] && !contentStopwords[w] {
			t.Errorf("legacy stopword %q is in neither class", w)
		}
	}
}
