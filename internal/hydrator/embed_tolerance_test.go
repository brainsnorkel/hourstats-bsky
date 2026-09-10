package hydrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// embedToleranceResponseJSON is a synthetic app.bsky.feed.getPosts response
// body containing three posts:
//
//  1. A "known-but-new-to-us" embed, app.bsky.embed.gallery#view, whose Items
//     union element is itself typed (app.bsky.embed.gallery#viewImage). This
//     indigo version (v0.0.0-20260903211445-41278964ec8e) already has a case
//     for gallery#view in FeedDefs_PostView_Embed.UnmarshalJSON
//     (api/bsky/feeddefs.go), so this exercises the "recognised" branch.
//  2. A genuinely unknown embed, app.bsky.embed.future#view, standing in for
//     any lexicon Bluesky ships after this indigo pin. UnmarshalJSON's
//     `default: return nil` branch leaves every field of the
//     FeedDefs_PostView_Embed union nil for this post — no error, no data.
//  3. No embed at all, the common case.
//
// All three carry likeCount/repostCount/replyCount and an author so
// counters() and a full Hydrate pass can be exercised against them.
const embedToleranceResponseJSON = `{
  "posts": [
    {
      "$type": "app.bsky.feed.defs#postView",
      "uri": "at://did:plc:test/app.bsky.feed.post/gallery",
      "cid": "bafygallery",
      "author": {"did": "did:plc:gallery", "handle": "gallery.bsky.social"},
      "indexedAt": "2026-09-11T00:00:00.000Z",
      "likeCount": 10,
      "repostCount": 2,
      "replyCount": 1,
      "embed": {
        "$type": "app.bsky.embed.gallery#view",
        "items": [
          {
            "$type": "app.bsky.embed.gallery#viewImage",
            "alt": "a photo",
            "aspectRatio": {"width": 4, "height": 3},
            "fullsize": "https://cdn.example/full.jpg",
            "thumbnail": "https://cdn.example/thumb.jpg"
          }
        ]
      }
    },
    {
      "$type": "app.bsky.feed.defs#postView",
      "uri": "at://did:plc:test/app.bsky.feed.post/future",
      "cid": "bafyfuture",
      "author": {"did": "did:plc:future", "handle": "future.bsky.social"},
      "indexedAt": "2026-09-11T00:00:00.000Z",
      "likeCount": 20,
      "repostCount": 3,
      "replyCount": 2,
      "embed": {
        "$type": "app.bsky.embed.future#view",
        "foo": 1
      }
    },
    {
      "$type": "app.bsky.feed.defs#postView",
      "uri": "at://did:plc:test/app.bsky.feed.post/noembed",
      "cid": "bafynoembed",
      "author": {"did": "did:plc:noembed", "handle": "noembed.bsky.social"},
      "indexedAt": "2026-09-11T00:00:00.000Z",
      "likeCount": 30,
      "repostCount": 4,
      "replyCount": 3
    }
  ]
}`

// TestEmbedToleranceUnmarshal confirms the synthetic response body decodes
// cleanly into *bsky.FeedGetPosts_Output: a known-but-newly-added embed
// (gallery#view), a wholly unknown future embed, and no embed at all must
// all unmarshal without error.
func TestEmbedToleranceUnmarshal(t *testing.T) {
	var out bsky.FeedGetPosts_Output
	if err := json.Unmarshal([]byte(embedToleranceResponseJSON), &out); err != nil {
		t.Fatalf("unmarshal FeedGetPosts_Output: %v", err)
	}
	if len(out.Posts) != 3 {
		t.Fatalf("Posts = %d, want 3", len(out.Posts))
	}
	for i, p := range out.Posts {
		if p == nil {
			t.Fatalf("Posts[%d] is nil", i)
		}
	}
}

// TestEmbedToleranceCountersSurvive runs the hydrator's counters() helper
// (hydrator.go) against each of the three views and asserts the engagement
// counts survive regardless of what shape (or non-shape) the embed took.
// counters() never touches PostView.Embed, but this pins that contract down
// so a future change to counters() that starts reading Embed gets caught
// here instead of in production.
func TestEmbedToleranceCountersSurvive(t *testing.T) {
	var out bsky.FeedGetPosts_Output
	if err := json.Unmarshal([]byte(embedToleranceResponseJSON), &out); err != nil {
		t.Fatalf("unmarshal FeedGetPosts_Output: %v", err)
	}

	tests := []struct {
		name            string
		uri             string
		wantLikes       int
		wantReposts     int
		wantReplies     int
		wantEmbedNil    bool // true if the whole Embed union should be untyped (nil pointer, or every member nil)
		wantGalleryView bool // true if EmbedGallery_View specifically should be populated
	}{
		{
			name:            "gallery view embed (known, newly added type)",
			uri:             "at://did:plc:test/app.bsky.feed.post/gallery",
			wantLikes:       10,
			wantReposts:     2,
			wantReplies:     1,
			wantGalleryView: true,
		},
		{
			name:         "future view embed (unknown $type)",
			uri:          "at://did:plc:test/app.bsky.feed.post/future",
			wantLikes:    20,
			wantReposts:  3,
			wantReplies:  2,
			wantEmbedNil: true,
		},
		{
			name:         "no embed at all",
			uri:          "at://did:plc:test/app.bsky.feed.post/noembed",
			wantLikes:    30,
			wantReposts:  4,
			wantReplies:  3,
			wantEmbedNil: true,
		},
	}

	byURI := make(map[string]*bsky.FeedDefs_PostView, len(out.Posts))
	for _, p := range out.Posts {
		byURI[p.Uri] = p
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := byURI[tt.uri]
			if !ok {
				t.Fatalf("no post found for uri %q", tt.uri)
			}

			likes, reposts, replies := counters(v)
			if likes != tt.wantLikes {
				t.Errorf("likes = %d, want %d", likes, tt.wantLikes)
			}
			if reposts != tt.wantReposts {
				t.Errorf("reposts = %d, want %d", reposts, tt.wantReposts)
			}
			if replies != tt.wantReplies {
				t.Errorf("replies = %d, want %d", replies, tt.wantReplies)
			}

			if tt.wantEmbedNil {
				if v.Embed != nil {
					// The union struct itself may be non-nil (an empty
					// FeedDefs_PostView_Embed{} for the unknown-type case),
					// but every member must be nil - nothing should have
					// been populated from a $type it didn't recognise.
					if v.Embed.EmbedImages_View != nil ||
						v.Embed.EmbedVideo_View != nil ||
						v.Embed.EmbedGallery_View != nil ||
						v.Embed.EmbedExternal_View != nil ||
						v.Embed.EmbedRecord_View != nil ||
						v.Embed.EmbedRecordWithMedia_View != nil {
						t.Errorf("Embed = %+v, want every union member nil", v.Embed)
					}
				}
			}

			if tt.wantGalleryView {
				if v.Embed == nil || v.Embed.EmbedGallery_View == nil {
					t.Fatalf("Embed.EmbedGallery_View is nil, want populated")
				}
				if len(v.Embed.EmbedGallery_View.Items) != 1 {
					t.Errorf("gallery Items = %d, want 1", len(v.Embed.EmbedGallery_View.Items))
				}
			}
		})
	}
}

// TestEmbedToleranceFullHydratePass runs a full Hydrate pass with a fake
// PostFetcher returning the three synthetic views (gallery#view, an unknown
// future embed, and no embed) and asserts all three posts hydrate cleanly:
// hydrated == 3, errors == 0. This is the end-to-end version of
// TestEmbedToleranceCountersSurvive - it proves the worker loop in
// Hydrator.Hydrate (hydrator.go:298-362) never dereferences PostView.Embed on
// the way to calling counters() and the updater.
func TestEmbedToleranceFullHydratePass(t *testing.T) {
	var out bsky.FeedGetPosts_Output
	if err := json.Unmarshal([]byte(embedToleranceResponseJSON), &out); err != nil {
		t.Fatalf("unmarshal FeedGetPosts_Output: %v", err)
	}
	if len(out.Posts) != 3 {
		t.Fatalf("Posts = %d, want 3", len(out.Posts))
	}

	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			return out.Posts, nil
		},
	}
	updater := &mockUpdater{}
	h := New(fetcher, updater, Config{RateLimit: 1000})

	posts := make([]store.Post, len(out.Posts))
	for i, v := range out.Posts {
		posts[i] = store.Post{URI: v.Uri}
	}

	res, err := h.Hydrate(context.Background(), posts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Hydrated != 3 {
		t.Errorf("Hydrated = %d, want 3", res.Hydrated)
	}
	if res.Errors != 0 {
		t.Errorf("Errors = %d, want 0", res.Errors)
	}
}
