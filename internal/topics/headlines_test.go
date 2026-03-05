package topics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanHeadlineTitle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Trump proposes tariffs - Reuters", "Trump proposes tariffs"},
		{"Olympic hockey final - BBC News", "Olympic hockey final"},
		{"Simple headline", "Simple headline"},
		{"Headline - with - multiple - dashes - Source", "Headline - with - multiple - dashes"},
		{"  spaced  ", "spaced"},
		{"", ""},
	}

	for _, tt := range tests {
		got := cleanHeadlineTitle(tt.input)
		if got != tt.want {
			t.Errorf("cleanHeadlineTitle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFetchFeed_ParsesRSS(t *testing.T) {
	rssXML := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <item><title>Finland wins Olympic gold - BBC</title></item>
    <item><title>Trump announces tariffs - Reuters</title></item>
    <item><title>Greenland deal proposed - CNN</title></item>
  </channel>
</rss>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(rssXML))
	}))
	defer srv.Close()

	client := &http.Client{}
	items, err := fetchFeed(context.Background(), client, srv.URL)
	if err != nil {
		t.Fatalf("fetchFeed error: %v", err)
	}

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].Title != "Finland wins Olympic gold - BBC" {
		t.Errorf("unexpected title: %q", items[0].Title)
	}
}

func TestFetchFeed_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &http.Client{}
	_, err := fetchFeed(context.Background(), client, srv.URL)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestFetchFeed_InvalidXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not xml at all"))
	}))
	defer srv.Close()

	client := &http.Client{}
	_, err := fetchFeed(context.Background(), client, srv.URL)
	if err == nil {
		t.Fatal("expected error for invalid XML")
	}
}

func TestBuildPrompt_WithHeadlines(t *testing.T) {
	terms := []TermScore{{Term: "hockey", Score: 5.0}, {Term: "finland", Score: 3.0}}
	headlines := []string{"Finland wins Olympic hockey gold", "Trump proposes tariffs"}

	prompt := buildPrompt(terms, headlines)

	if !strings.Contains(prompt, "CURRENT NEWS HEADLINES") {
		t.Error("expected headlines section in prompt")
	}
	if !strings.Contains(prompt, "Finland wins Olympic hockey gold") {
		t.Error("expected headline text in prompt")
	}
	if !strings.Contains(prompt, "disambiguation only") {
		t.Error("expected disambiguation instruction")
	}
	if !strings.Contains(prompt, "Bluesky-specific topics") {
		t.Error("expected Bluesky-native topic guidance")
	}
}

func TestBuildPrompt_WithoutHeadlines(t *testing.T) {
	terms := []TermScore{{Term: "hockey", Score: 5.0}}

	prompt := buildPrompt(terms, nil)

	if strings.Contains(prompt, "CURRENT NEWS HEADLINES") {
		t.Error("expected no headlines section when nil")
	}

	prompt2 := buildPrompt(terms, []string{})
	if strings.Contains(prompt2, "CURRENT NEWS HEADLINES") {
		t.Error("expected no headlines section when empty")
	}
}
