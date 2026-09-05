package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bluesky-social/indigo/api/bsky"
)

func TestQuoteUnavailable(t *testing.T) {
	yes, no, blocker := true, false, "at://did:plc:me/app.bsky.graph.block/1"
	cases := []struct {
		name string
		pv   *bsky.FeedDefs_PostView
		want bool
	}{
		{"plain post", &bsky.FeedDefs_PostView{Author: &bsky.ActorDefs_ProfileViewBasic{}}, false},
		{"no viewer state at all", &bsky.FeedDefs_PostView{}, false},
		{"embedding disabled", &bsky.FeedDefs_PostView{Viewer: &bsky.FeedDefs_ViewerState{EmbeddingDisabled: &yes}}, true},
		{"embedding explicitly allowed", &bsky.FeedDefs_PostView{Viewer: &bsky.FeedDefs_ViewerState{EmbeddingDisabled: &no}, Author: &bsky.ActorDefs_ProfileViewBasic{Viewer: &bsky.ActorDefs_ViewerState{BlockedBy: &no}}}, false},
		{"author blocks us", &bsky.FeedDefs_PostView{Author: &bsky.ActorDefs_ProfileViewBasic{Viewer: &bsky.ActorDefs_ViewerState{BlockedBy: &yes}}}, true},
		{"we block author", &bsky.FeedDefs_PostView{Author: &bsky.ActorDefs_ProfileViewBasic{Viewer: &bsky.ActorDefs_ViewerState{Blocking: &blocker}}}, true},
		{"blocked via list", &bsky.FeedDefs_PostView{Author: &bsky.ActorDefs_ProfileViewBasic{Viewer: &bsky.ActorDefs_ViewerState{BlockingByList: &bsky.GraphDefs_ListViewBasic{}}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := quoteUnavailable(tc.pv)
			if got != tc.want {
				t.Errorf("quoteUnavailable = %v (%q), want %v", got, reason, tc.want)
			}
			if got && reason == "" {
				t.Error("unavailable without a reason")
			}
		})
	}
}

func TestEmbeddingDisabled_BlockedAuthor(t *testing.T) {
	blockedURI := "at://did:plc:blocker/app.bsky.feed.post/1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"posts":[{"uri":%q,"cid":"c1","indexedAt":"2026-01-01T00:00:00Z",
			"author":{"did":"did:plc:blocker","handle":"blocker.bsky.social","viewer":{"blockedBy":true}},
			"viewer":{"embeddingDisabled":false}}]}`, blockedURI)
	}))
	defer srv.Close()

	got, err := newTestClient(srv.URL).EmbeddingDisabled(context.Background(), []string{blockedURI})
	if err != nil {
		t.Fatalf("EmbeddingDisabled() error = %v", err)
	}
	if !got[blockedURI] {
		t.Errorf("a post whose author blocks this account should not be quoted")
	}
}
