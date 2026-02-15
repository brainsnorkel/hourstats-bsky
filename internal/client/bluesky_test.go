package client

import (
	"context"
	"testing"

	"github.com/bluesky-social/indigo/api/atproto"
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
