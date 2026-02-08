package topics

import (
	"reflect"
	"testing"
)

func TestTokenize_PlainText(t *testing.T) {
	got := Tokenize("Breaking news about the economy today")
	want := []string{"breaking", "news", "economy", "today"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTokenize_URLs(t *testing.T) {
	got := Tokenize("Check this out https://example.com/foo and http://bar.org important stuff")
	if contains(got, "https") || contains(got, "example") || contains(got, "http") {
		t.Errorf("URLs should be stripped, got %v", got)
	}
	if !contains(got, "check") || !contains(got, "important") || !contains(got, "stuff") {
		t.Errorf("non-URL words should remain, got %v", got)
	}
}

func TestTokenize_Mentions(t *testing.T) {
	got := Tokenize("Hey @user.bsky.social great post about climate change")
	if contains(got, "user") || contains(got, "bsky") {
		t.Errorf("mentions should be stripped, got %v", got)
	}
	if !contains(got, "climate") || !contains(got, "change") {
		t.Errorf("non-mention words should remain, got %v", got)
	}
}

func TestTokenize_Emoji(t *testing.T) {
	got := Tokenize("Love this 🔥🔥🔥 amazing content 🎉")
	if contains(got, "🔥") || contains(got, "🎉") {
		t.Errorf("emoji should be stripped, got %v", got)
	}
	if !contains(got, "love") || !contains(got, "amazing") || !contains(got, "content") {
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
	got := Tokenize("wow!!! amazing... really??? incredible---stuff")
	if !contains(got, "wow") || !contains(got, "amazing") || !contains(got, "incredible") {
		t.Errorf("punctuation should be trimmed, got %v", got)
	}
}

func TestTokenize_SocialMediaStopwords(t *testing.T) {
	got := Tokenize("lol omg wtf bruh literally the best thing ever")
	if contains(got, "lol") || contains(got, "omg") || contains(got, "bruh") || contains(got, "literally") {
		t.Errorf("social media stopwords should be removed, got %v", got)
	}
	if !contains(got, "best") {
		t.Errorf("non-stopword should remain, got %v", got)
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
