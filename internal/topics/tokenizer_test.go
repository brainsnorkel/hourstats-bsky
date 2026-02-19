package topics

import (
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
		t.Errorf("pure numbers should be stripped, got %v", got)
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
		t.Errorf("bigram should bridge across stopword, got %v", got)
	}
	if contains(got, "show") {
		t.Errorf("stopword 'show' should not appear as unigram, got %v", got)
	}
	if contains(got, "show_tonight") {
		t.Errorf("bigram of two stopwords should be excluded, got %v", got)
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
