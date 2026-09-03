package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bluesky-social/indigo/api/atproto"
	indigoclient "github.com/bluesky-social/indigo/atproto/client"
)

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		maxLength int
		want      string
	}{
		{
			name:      "short text unchanged",
			text:      "hello",
			maxLength: 10,
			want:      "hello",
		},
		{
			name:      "exact limit unchanged",
			text:      "hello",
			maxLength: 5,
			want:      "hello",
		},
		{
			name:      "over limit truncated with ellipsis",
			text:      "hello world this is a long text",
			maxLength: 10,
			want:      "hello w...",
		},
		{
			name:      "empty string",
			text:      "",
			maxLength: 10,
			want:      "",
		},
		{
			name:      "multi-byte chars (emoji)",
			text:      "Hello 🌍🌎🌏 world",
			maxLength: 9,
			want:      "Hello ...",
		},
		{
			name:      "CJK characters",
			text:      "日本語のテキストです",
			maxLength: 6,
			want:      "日本語...",
		},
		{
			name:      "maxLength 3 gives just ellipsis",
			text:      "abcdef",
			maxLength: 3,
			want:      "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateText(tt.text, tt.maxLength)
			if got != tt.want {
				t.Errorf("truncateText(%q, %d) = %q, want %q", tt.text, tt.maxLength, got, tt.want)
			}
		})
	}
}

func TestConvertATURItoWebURL(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "valid AT URI converts to web URL",
			uri:  "at://did:plc:abc123/app.bsky.feed.post/xyz789",
			want: "https://bsky.app/profile/did:plc:abc123/post/xyz789",
		},
		{
			name: "non-post collection strips prefix",
			uri:  "at://did:plc:abc123/app.bsky.feed.like/xyz789",
			want: "did:plc:abc123/app.bsky.feed.like/xyz789",
		},
		{
			name: "web URL passthrough",
			uri:  "https://bsky.app/profile/user.bsky.social/post/abc",
			want: "https://bsky.app/profile/user.bsky.social/post/abc",
		},
		{
			name: "empty string passthrough",
			uri:  "",
			want: "",
		},
		{
			name: "at:// with too few parts strips prefix",
			uri:  "at://did:plc:abc123",
			want: "did:plc:abc123",
		},
		{
			name: "handle-based AT URI",
			uri:  "at://user.bsky.social/app.bsky.feed.post/abc123",
			want: "https://bsky.app/profile/user.bsky.social/post/abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertATURItoWebURL(tt.uri)
			if got != tt.want {
				t.Errorf("convertATURItoWebURL(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestCreateUserHandleFacets(t *testing.T) {
	t.Run("single handle with link", func(t *testing.T) {
		text := "Bluesky is #happy\n\n1. @alice.bsky.social +"
		posts := []Post{
			{URI: "at://did:plc:aaa/app.bsky.feed.post/111", Author: "alice.bsky.social"},
		}
		facets := createUserHandleFacets(text, posts)
		if len(facets) < 2 {
			t.Fatalf("got %d facets, want at least 2 (hashtag + handle)", len(facets))
		}

		hashtagFacet := facets[0]
		if hashtagFacet.Features[0].RichtextFacet_Tag == nil {
			t.Error("first facet should be a hashtag")
		}
		if hashtagFacet.Features[0].RichtextFacet_Tag.Tag != "happy" {
			t.Errorf("hashtag tag = %q, want %q", hashtagFacet.Features[0].RichtextFacet_Tag.Tag, "happy")
		}

		handleFacet := facets[1]
		if handleFacet.Features[0].RichtextFacet_Link == nil {
			t.Error("second facet should be a link")
		}
		wantURL := "https://bsky.app/profile/did:plc:aaa/post/111"
		if handleFacet.Features[0].RichtextFacet_Link.Uri != wantURL {
			t.Errorf("link URI = %q, want %q", handleFacet.Features[0].RichtextFacet_Link.Uri, wantURL)
		}
	})

	t.Run("multiple handles", func(t *testing.T) {
		text := "Bluesky is #ok\n\n1. @alice.bsky.social +\n2. @bob.bsky.social -"
		posts := []Post{
			{URI: "at://did:plc:aaa/app.bsky.feed.post/111", Author: "alice.bsky.social"},
			{URI: "at://did:plc:bbb/app.bsky.feed.post/222", Author: "bob.bsky.social"},
		}
		facets := createUserHandleFacets(text, posts)
		if len(facets) != 3 {
			t.Fatalf("got %d facets, want 3", len(facets))
		}
	})

	t.Run("no matching handle", func(t *testing.T) {
		text := "Bluesky is #ok\n\nNo handles here"
		posts := []Post{
			{URI: "at://did:plc:aaa/app.bsky.feed.post/111", Author: "alice.bsky.social"},
		}
		facets := createUserHandleFacets(text, posts)
		if len(facets) != 1 {
			t.Fatalf("got %d facets, want 1 (hashtag only)", len(facets))
		}
	})

	t.Run("empty posts", func(t *testing.T) {
		text := "Bluesky is #ok"
		facets := createUserHandleFacets(text, nil)
		if len(facets) != 1 {
			t.Fatalf("got %d facets, want 1", len(facets))
		}
	})

	t.Run("no hashtag prefix", func(t *testing.T) {
		text := "Some other text @alice.bsky.social"
		posts := []Post{
			{URI: "at://did:plc:aaa/app.bsky.feed.post/111", Author: "alice.bsky.social"},
		}
		facets := createUserHandleFacets(text, posts)
		if len(facets) != 1 {
			t.Fatalf("got %d facets, want 1", len(facets))
		}
		if facets[0].Features[0].RichtextFacet_Link == nil {
			t.Error("facet should be a link")
		}
	})

	t.Run("post with empty URI skipped", func(t *testing.T) {
		text := "Bluesky is #ok\n\n1. @alice.bsky.social +"
		posts := []Post{
			{URI: "", Author: "alice.bsky.social"},
		}
		facets := createUserHandleFacets(text, posts)
		if len(facets) != 1 {
			t.Fatalf("got %d facets, want 1 (hashtag only, handle skipped)", len(facets))
		}
	})

	t.Run("duplicate handles get correct positions", func(t *testing.T) {
		text := "1. @alice.bsky.social +\n2. @alice.bsky.social -"
		posts := []Post{
			{URI: "at://did:plc:aaa/app.bsky.feed.post/111", Author: "alice.bsky.social"},
			{URI: "at://did:plc:aaa/app.bsky.feed.post/222", Author: "alice.bsky.social"},
		}
		facets := createUserHandleFacets(text, posts)
		if len(facets) != 2 {
			t.Fatalf("got %d facets, want 2", len(facets))
		}
		if facets[0].Index.ByteStart == facets[1].Index.ByteStart {
			t.Error("duplicate handles should have different byte positions")
		}
	})
}

func TestHasAdultContentLabel(t *testing.T) {
	c := &BlueskyClient{}

	tests := []struct {
		name   string
		labels []*atproto.LabelDefs_Label
		want   bool
	}{
		{
			name:   "nil labels",
			labels: nil,
			want:   false,
		},
		{
			name:   "empty labels",
			labels: []*atproto.LabelDefs_Label{},
			want:   false,
		},
		{
			name:   "porn label",
			labels: []*atproto.LabelDefs_Label{{Val: "porn"}},
			want:   true,
		},
		{
			name:   "sexual label",
			labels: []*atproto.LabelDefs_Label{{Val: "sexual"}},
			want:   true,
		},
		{
			name:   "nudity label",
			labels: []*atproto.LabelDefs_Label{{Val: "nudity"}},
			want:   true,
		},
		{
			name:   "graphic-media label",
			labels: []*atproto.LabelDefs_Label{{Val: "graphic-media"}},
			want:   true,
		},
		{
			name:   "non-adult label",
			labels: []*atproto.LabelDefs_Label{{Val: "politics"}},
			want:   false,
		},
		{
			name: "mixed labels with adult",
			labels: []*atproto.LabelDefs_Label{
				{Val: "politics"},
				{Val: "nudity"},
			},
			want: true,
		},
		{
			name:   "nil label in slice",
			labels: []*atproto.LabelDefs_Label{nil, {Val: "politics"}},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.hasAdultContentLabel(tt.labels)
			if got != tt.want {
				t.Errorf("hasAdultContentLabel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateEmbedCard(t *testing.T) {
	c := &BlueskyClient{}
	ctx := context.Background()

	t.Run("valid post returns embed", func(t *testing.T) {
		post := Post{URI: "at://did:plc:abc/app.bsky.feed.post/123", CID: "bafyabc"}
		embed := c.createEmbedCard(ctx, post)
		if embed == nil {
			t.Fatal("expected embed, got nil")
		}
		if embed.EmbedRecord == nil {
			t.Fatal("expected EmbedRecord, got nil")
		}
		if embed.EmbedRecord.Record.Uri != post.URI {
			t.Errorf("embed URI = %q, want %q", embed.EmbedRecord.Record.Uri, post.URI)
		}
		if embed.EmbedRecord.Record.Cid != post.CID {
			t.Errorf("embed CID = %q, want %q", embed.EmbedRecord.Record.Cid, post.CID)
		}
	})

	t.Run("empty URI returns nil", func(t *testing.T) {
		post := Post{URI: "", CID: "bafyabc"}
		embed := c.createEmbedCard(ctx, post)
		if embed != nil {
			t.Errorf("expected nil embed for empty URI, got %v", embed)
		}
	})

	t.Run("empty CID returns nil", func(t *testing.T) {
		post := Post{URI: "at://did:plc:abc/app.bsky.feed.post/123", CID: ""}
		embed := c.createEmbedCard(ctx, post)
		if embed != nil {
			t.Errorf("expected nil embed for empty CID, got %v", embed)
		}
	})

	t.Run("both empty returns nil", func(t *testing.T) {
		post := Post{URI: "", CID: ""}
		embed := c.createEmbedCard(ctx, post)
		if embed != nil {
			t.Errorf("expected nil embed for empty post, got %v", embed)
		}
	})
}

func TestNew(t *testing.T) {
	t.Run("creates client with handle and password", func(t *testing.T) {
		c := New("test.bsky.social", "secret")
		if c == nil {
			t.Fatal("expected non-nil client")
		}
		if c.handle != "test.bsky.social" {
			t.Errorf("handle = %q, want %q", c.handle, "test.bsky.social")
		}
		if c.password != "secret" {
			t.Errorf("password = %q, want %q", c.password, "secret")
		}
		if c.client == nil {
			t.Error("expected non-nil API client")
		}
	})

	t.Run("APIClient returns internal client", func(t *testing.T) {
		c := New("test.bsky.social", "secret")
		api := c.APIClient()
		if api == nil {
			t.Error("expected non-nil API client from APIClient()")
		}
		if api != c.client {
			t.Error("APIClient() should return the same client instance")
		}
	})
}

// newTestClient points a BlueskyClient at a local XRPC server so the request
// path, query params and response decoding are all exercised for real.
func newTestClient(host string) *BlueskyClient {
	return &BlueskyClient{
		client: indigoclient.NewAPIClient(host),
		handle: "hourstats.bsky.social",
	}
}

func TestEmbeddingDisabled(t *testing.T) {
	const (
		disabledURI = "at://did:plc:aaa/app.bsky.feed.post/111"
		allowedURI  = "at://did:plc:bbb/app.bsky.feed.post/222"
		silentURI   = "at://did:plc:ccc/app.bsky.feed.post/333"
		missingURI  = "at://did:plc:ddd/app.bsky.feed.post/444"
	)

	var gotPath string
	var gotURIs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotURIs = r.URL.Query()["uris"]
		w.Header().Set("Content-Type", "application/json")
		// silentURI has a viewer state with no embeddingDisabled field, which
		// is what the AppView returns for a post with no postgate.
		fmt.Fprintf(w, `{"posts":[
			{"uri":%q,"cid":"c1","indexedAt":"2026-01-01T00:00:00Z","viewer":{"embeddingDisabled":true}},
			{"uri":%q,"cid":"c2","indexedAt":"2026-01-01T00:00:00Z","viewer":{"embeddingDisabled":false}},
			{"uri":%q,"cid":"c3","indexedAt":"2026-01-01T00:00:00Z","viewer":{}}
		]}`, disabledURI, allowedURI, silentURI)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	got, err := c.EmbeddingDisabled(context.Background(), []string{disabledURI, allowedURI, silentURI, missingURI})
	if err != nil {
		t.Fatalf("EmbeddingDisabled() error = %v", err)
	}

	if gotPath != "/xrpc/app.bsky.feed.getPosts" {
		t.Errorf("request path = %q, want %q", gotPath, "/xrpc/app.bsky.feed.getPosts")
	}
	if len(gotURIs) != 4 {
		t.Errorf("sent %d uris params, want 4: %v", len(gotURIs), gotURIs)
	}

	want := map[string]bool{
		disabledURI: true,
		allowedURI:  false,
		silentURI:   false,
		// A post the AppView did not return is not a post we know to be
		// quote-controlled, so it must not suppress the embed.
		missingURI: false,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for uri, wantDisabled := range want {
		if got[uri] != wantDisabled {
			t.Errorf("EmbeddingDisabled()[%q] = %v, want %v", uri, got[uri], wantDisabled)
		}
	}
}

func TestEmbeddingDisabledEmptyURIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s for an empty URI list", r.URL.Path)
	}))
	defer srv.Close()

	got, err := newTestClient(srv.URL).EmbeddingDisabled(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbeddingDisabled() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want an empty map", got)
	}
}

func TestEmbeddingDisabledRejectsOversizedBatch(t *testing.T) {
	uris := make([]string, maxGetPostsURIs+1)
	for i := range uris {
		uris[i] = fmt.Sprintf("at://did:plc:aaa/app.bsky.feed.post/%d", i)
	}

	if _, err := newTestClient("https://example.invalid").EmbeddingDisabled(context.Background(), uris); err == nil {
		t.Fatal("expected an error for a batch over the getPosts limit, got nil")
	}
}

func TestEmbeddingDisabledPropagatesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"InternalServerError","message":"boom"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).EmbeddingDisabled(context.Background(), []string{"at://did:plc:aaa/app.bsky.feed.post/111"})
	if err == nil {
		t.Fatal("expected an error from a 500 response, got nil")
	}
	// The caller fails open on error, so the message has to say what failed.
	if !strings.Contains(err.Error(), "quote-control check") {
		t.Errorf("error = %q, want it to name the quote-control check", err)
	}
}

func TestEmbeddingDisabledUnauthenticatedClient(t *testing.T) {
	c := &BlueskyClient{handle: "hourstats.bsky.social"}
	if _, err := c.EmbeddingDisabled(context.Background(), []string{"at://did:plc:aaa/app.bsky.feed.post/111"}); err == nil {
		t.Fatal("expected an error when the client is not authenticated, got nil")
	}
}

// captureCreateRecord serves com.atproto.repo.createRecord and hands the
// decoded post record back, so embed and text decisions can be asserted on the
// record that would actually reach Bluesky.
func captureCreateRecord(t *testing.T, record *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Record map[string]any `json:"record"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decoding createRecord input: %v", err)
		}
		*record = input.Record
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"uri":"at://did:plc:bot/app.bsky.feed.post/summary","cid":"summarycid"}`)
	}))
}

func TestPostTrendingSummaryQuoteControlledTopPost(t *testing.T) {
	var record map[string]any
	srv := captureCreateRecord(t, &record)
	defer srv.Close()

	posts := []Post{
		{URI: "at://did:plc:aaa/app.bsky.feed.post/111", CID: "c1", Author: "alice.bsky.social", QuoteControlled: true},
		{URI: "at://did:plc:bbb/app.bsky.feed.post/222", CID: "c2", Author: "bob.bsky.social"},
	}

	uri, cid, err := newTestClient(srv.URL).PostTrendingSummary(posts, "positive", 30, 1000, 11.0)
	if err != nil {
		t.Fatalf("PostTrendingSummary() error = %v", err)
	}
	if uri == "" || cid == "" {
		t.Errorf("PostTrendingSummary() = (%q, %q), want the posted URI and CID", uri, cid)
	}

	if embed, ok := record["embed"]; ok && embed != nil {
		t.Errorf("record carries embed %v, want none for a quote-controlled top post", embed)
	}
	text, _ := record["text"].(string)
	if !strings.Contains(text, "1. @alice.bsky.social · no embed, post is quote controlled\n") {
		t.Errorf("text = %q, want the quote-control note on the #1 line", text)
	}
}

// The #2 post must not silently take the embed slot: the text says there is no
// embed, so there must be no embed.
func TestPostTrendingSummaryQuoteControlledDoesNotFallThrough(t *testing.T) {
	var record map[string]any
	srv := captureCreateRecord(t, &record)
	defer srv.Close()

	posts := []Post{
		{URI: "at://did:plc:aaa/app.bsky.feed.post/111", CID: "c1", Author: "alice.bsky.social", QuoteControlled: true},
	}
	if _, _, err := newTestClient(srv.URL).PostTrendingSummary(posts, "positive", 30, 1000, 11.0); err != nil {
		t.Fatalf("PostTrendingSummary() error = %v", err)
	}
	if _, ok := record["embed"]; ok {
		t.Error("record should have no embed field at all")
	}
}

func TestPostTrendingSummaryEmbedsWhenNotQuoteControlled(t *testing.T) {
	var record map[string]any
	srv := captureCreateRecord(t, &record)
	defer srv.Close()

	posts := []Post{
		{URI: "at://did:plc:aaa/app.bsky.feed.post/111", CID: "c1", Author: "alice.bsky.social"},
	}
	if _, _, err := newTestClient(srv.URL).PostTrendingSummary(posts, "positive", 30, 1000, 11.0); err != nil {
		t.Fatalf("PostTrendingSummary() error = %v", err)
	}

	embed, ok := record["embed"].(map[string]any)
	if !ok {
		t.Fatalf("record embed = %v, want a record embed", record["embed"])
	}
	rec, ok := embed["record"].(map[string]any)
	if !ok {
		t.Fatalf("embed record = %v, want a strong ref", embed["record"])
	}
	if rec["uri"] != posts[0].URI {
		t.Errorf("embed uri = %v, want %q", rec["uri"], posts[0].URI)
	}
	text, _ := record["text"].(string)
	if strings.Contains(text, "quote controlled") {
		t.Errorf("text = %q, want no quote-control note", text)
	}
}

// The note sits after the handle, so the handle facet must still cover exactly
// "@alice.bsky.social" and stop before the separator.
func TestCreateUserHandleFacetsWithQuoteControlNote(t *testing.T) {
	text := "Bluesky is #happy +1.0% sentiment\n\nTop recent posts\n" +
		"1. @alice.bsky.social · no embed, post is quote controlled\n2. @bob.bsky.social\n"
	posts := []Post{
		{URI: "at://did:plc:aaa/app.bsky.feed.post/111", Author: "alice.bsky.social", QuoteControlled: true},
		{URI: "at://did:plc:bbb/app.bsky.feed.post/222", Author: "bob.bsky.social"},
	}

	facets := createUserHandleFacets(text, posts)
	if len(facets) != 3 {
		t.Fatalf("got %d facets, want 3 (hashtag + two handles)", len(facets))
	}

	for i, want := range []string{"@alice.bsky.social", "@bob.bsky.social"} {
		f := facets[i+1]
		got := text[f.Index.ByteStart:f.Index.ByteEnd]
		if got != want {
			t.Errorf("facet %d covers %q, want %q", i+1, got, want)
		}
	}
}

// The note is dropped rather than truncated when it would push the summary
// past 300 graphemes: truncation would cut into a handle and strand its facet.
// The embed stays suppressed regardless, since that is the actual bug fix.
func TestPostTrendingSummaryDropsQuoteControlNoteOnOverflow(t *testing.T) {
	var record map[string]any
	srv := captureCreateRecord(t, &record)
	defer srv.Close()

	// Handles sized so the summary fits without the note and overflows with it.
	longHandle := strings.Repeat("a", 63) + ".bsky.social"
	posts := []Post{
		{URI: "at://did:plc:aaa/app.bsky.feed.post/111", CID: "c1", Author: "1" + longHandle, QuoteControlled: true},
		{URI: "at://did:plc:bbb/app.bsky.feed.post/222", CID: "c2", Author: "2" + longHandle},
		{URI: "at://did:plc:ccc/app.bsky.feed.post/333", CID: "c3", Author: "3" + longHandle},
	}
	if _, _, err := newTestClient(srv.URL).PostTrendingSummary(posts, "positive", 30, 1000, 11.0); err != nil {
		t.Fatalf("PostTrendingSummary() error = %v", err)
	}

	text, _ := record["text"].(string)
	if n := len([]rune(text)); n > 300 {
		t.Errorf("text is %d graphemes, want <= 300: %q", n, text)
	}
	if strings.Contains(text, "quote controlled") {
		t.Errorf("note should have been dropped to fit, got %q", text)
	}
	if strings.Contains(text, "...") {
		t.Errorf("text was truncated instead of dropping the note, got %q", text)
	}
	if _, ok := record["embed"]; ok {
		t.Error("embed must stay suppressed even when the note does not fit")
	}
}
