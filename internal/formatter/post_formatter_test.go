package formatter

import (
	"strings"
	"testing"
)

func TestFormatPostContent_EmptyPosts(t *testing.T) {
	result := FormatPostContent(nil, "positive", 30, 1000, 12.0)
	// Should contain the mood hashtag and sentiment line but no numbered posts
	if !strings.Contains(result, "Bluesky is #") {
		t.Errorf("expected header, got %q", result)
	}
	if !strings.Contains(result, "% sentiment") {
		t.Errorf("expected sentiment percentage, got %q", result)
	}
	if strings.Contains(result, "1.") || strings.Contains(result, "Top recent posts") {
		t.Errorf("should have no numbered posts or list header, got %q", result)
	}
}

func TestFormatPostContent_SinglePost(t *testing.T) {
	posts := []Post{
		{Author: "alice.bsky.social", Sentiment: "positive"},
	}
	result := FormatPostContent(posts, "positive", 30, 1000, 12.0)
	if !strings.Contains(result, "\nTop recent posts\n1. @alice.bsky.social\n") {
		t.Errorf("expected list header and numbered post without sentiment symbol, got %q", result)
	}
}

func TestFormatPostContent_ThreePosts(t *testing.T) {
	posts := []Post{
		{Author: "a", Sentiment: "positive"},
		{Author: "b", Sentiment: "negative"},
		{Author: "c", Sentiment: "neutral"},
	}
	result := FormatPostContent(posts, "neutral", 30, 5000, 11.0)

	want := "Bluesky is #" + getMoodWord100(11.0) + " +11.0% sentiment\n\nTop recent posts\n1. @a\n2. @b\n3. @c\n"
	if result != want {
		t.Errorf("unexpected content:\n got %q\nwant %q", result, want)
	}
	for _, sym := range []string{" +\n", " -\n", " x\n"} {
		if strings.Contains(result, sym) {
			t.Errorf("sentiment indicator %q should be removed, got %q", sym, result)
		}
	}
}

func TestFormatPostContent_PositiveNetSentiment(t *testing.T) {
	result := FormatPostContent(nil, "positive", 30, 1000, 15.5)
	if !strings.Contains(result, "+15.5% sentiment") {
		t.Errorf("expected positive sign, got %q", result)
	}
}

func TestFormatPostContent_NegativeNetSentiment(t *testing.T) {
	result := FormatPostContent(nil, "negative", 30, 1000, -5.3)
	if !strings.Contains(result, "-5.3% sentiment") {
		t.Errorf("expected negative sign (no +), got %q", result)
	}
}

func TestFormatPostContent_ZeroNetSentiment(t *testing.T) {
	result := FormatPostContent(nil, "neutral", 30, 1000, 0.0)
	// Zero should not have + prefix
	if strings.Contains(result, "+0.0") {
		t.Errorf("zero sentiment should not have + prefix, got %q", result)
	}
	if !strings.Contains(result, "0.0% sentiment") {
		t.Errorf("expected 0.0%% sentiment, got %q", result)
	}
}

func TestFormatPostContent_HeaderFormat(t *testing.T) {
	result := FormatPostContent(nil, "positive", 30, 1000, 12.0)
	lines := strings.Split(result, "\n")
	// First line: "Bluesky is #<word> +X.X% sentiment"
	if !strings.HasPrefix(lines[0], "Bluesky is #") {
		t.Errorf("first line should start with 'Bluesky is #', got %q", lines[0])
	}
	if !strings.Contains(lines[0], "% sentiment") {
		t.Errorf("first line should contain sentiment percentage, got %q", lines[0])
	}
}

func TestFormatPostContent_QuoteControlledTopPost(t *testing.T) {
	posts := []Post{
		{Author: "alice.bsky.social", Sentiment: "positive", QuoteControlled: true},
		{Author: "bob.bsky.social", Sentiment: "negative"},
	}
	result := FormatPostContent(posts, "positive", 30, 1000, 11.0)

	want := "Bluesky is #" + getMoodWord100(11.0) + " +11.0% sentiment\n\nTop recent posts\n" +
		"1. @alice.bsky.social · no embed, post is quote controlled\n2. @bob.bsky.social\n"
	if result != want {
		t.Errorf("unexpected content:\n got %q\nwant %q", result, want)
	}
}

// The note must never break the handle it follows: facets are matched on
// "@handle", so a separator glued to the handle would shift the byte range.
func TestFormatPostContent_QuoteControlNoteFollowsHandleWithSeparator(t *testing.T) {
	posts := []Post{{Author: "alice.bsky.social", QuoteControlled: true}}
	result := FormatPostContent(posts, "positive", 30, 1000, 11.0)

	if !strings.Contains(result, "1. @alice.bsky.social · ") {
		t.Errorf("note should follow the bare handle after a separator, got %q", result)
	}
}

func TestFormatPostContent_QuoteControlledOnlyAnnotatesFirstPost(t *testing.T) {
	posts := []Post{
		{Author: "alice.bsky.social"},
		{Author: "bob.bsky.social", QuoteControlled: true},
	}
	result := FormatPostContent(posts, "positive", 30, 1000, 11.0)

	if strings.Contains(result, "quote controlled") {
		t.Errorf("only the quote-embedded #1 post carries the note, got %q", result)
	}
}
