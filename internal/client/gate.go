package client

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
)

// Reasons a post is not featured, or not quoted. Exactly one is recorded per
// URI, the first that matched in the order Check evaluates them.
const (
	// ReasonMissing: the authenticated view did not return the post at all —
	// deleted, taken down, or its author deactivated.
	ReasonMissing = "missing"
	// ReasonBlocked: a block exists in either direction between the author and
	// this account.
	ReasonBlocked = "blocked"
	// ReasonAdultLabel: the post carries an adult-content moderation label.
	ReasonAdultLabel = "adult_label"
	// ReasonHiddenFromRecommendations: the author's content visibility
	// declaration sets hideFromAlgorithmicRecommendations.
	ReasonHiddenFromRecommendations = "hide_from_recommendations"
	// ReasonVisibilityUnknown: the declaration could not be read twice running.
	// A user we cannot check is not featured.
	ReasonVisibilityUnknown = "visibility_unknown"
	// ReasonQuoteControl: an app.bsky.feed.postgate forbids quoting. The post
	// is still listed with a handle link; only the quote embed is dropped.
	ReasonQuoteControl = "quote_control"
)

// labelReason formats the reason for a system moderation label, whose values
// all begin with "!" (!hide, !warn, !no-unauthenticated, !takedown).
func labelReason(val string) string { return "label:" + val }

// Verdict is the gate's decision about one post.
type Verdict struct {
	// OK is false when the post must not be featured on any surface: no quote,
	// no link, no handle.
	OK bool
	// Quotable is false while OK is true when the post may be listed but not
	// quote-embedded.
	Quotable bool
	// Reason is empty when the post passed unconditionally, otherwise the
	// single reason recorded above.
	Reason string
	// AuthorDID is filled whenever the AppView returned an author, including
	// for rejected posts, so callers can log or aggregate by account.
	AuthorDID string
}

// visibilityLookup is the slice of *VisibilityResolver the gate uses, named so
// tests can substitute a resolver that never touches the network.
type visibilityLookup interface {
	Lookup(ctx context.Context, did string) Visibility
}

// FeatureGate is the single check every surface that amplifies an individual
// user's post must pass: the hourly summary, the trending exemplars, and the
// daily and weekly quote replies. It combines the post's authenticated viewer
// state and moderation labels with the author's content visibility
// declaration, and fails closed on anything it cannot establish.
type FeatureGate struct {
	client     *BlueskyClient
	visibility visibilityLookup
}

// NewFeatureGate binds the gate to this authenticated client and a shared
// visibility resolver. A nil resolver gets a default one rather than being
// skipped: a gate that cannot read declarations would silently stop honouring
// them.
func (c *BlueskyClient) NewFeatureGate(v *VisibilityResolver) *FeatureGate {
	g := &FeatureGate{client: c}
	if v != nil {
		g.visibility = v
	} else {
		g.visibility = NewVisibilityResolver(nil, nil, 0)
	}
	return g
}

// visibilityConcurrency bounds the per-author PDS lookups a single Check fans
// out. Each one is an identity resolution plus a getRecord against a PDS we do
// not control.
const visibilityConcurrency = 4

// Check evaluates up to maxGetPostsURIs posts and returns one Verdict per
// requested URI.
//
// It makes exactly one authenticated app.bsky.feed.getPosts call — viewer
// state (blocks, postgates) is only populated for authenticated requests, so
// the public hydration host cannot answer this — and then resolves each
// distinct author's content visibility declaration once.
//
// surface is a short label for the logs ("hourly", "exemplar", "daily",
// "weekly"). A transport error from getPosts is returned: the caller decides
// whether to retry or to publish without featuring anyone.
func (g *FeatureGate) Check(ctx context.Context, surface string, uris []string) (map[string]Verdict, error) {
	if g.client == nil || g.client.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}
	if len(uris) == 0 {
		return map[string]Verdict{}, nil
	}
	if len(uris) > maxGetPostsURIs {
		return nil, fmt.Errorf("too many URIs for getPosts: got %d, limit %d", len(uris), maxGetPostsURIs)
	}

	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	out, err := bsky.FeedGetPosts(callCtx, g.client.client, uris)
	if err != nil {
		return nil, fmt.Errorf("failed to get posts for the feature gate: %w", err)
	}

	// A post absent from the response is one we cannot show responsibly: it was
	// deleted, taken down, or its author left. That is the starting verdict for
	// every URI, overwritten only by a post view we actually received.
	verdicts := make(map[string]Verdict, len(uris))
	for _, uri := range uris {
		verdicts[uri] = Verdict{Reason: ReasonMissing}
	}

	// needVisibility holds the URIs that survived the post-level checks and so
	// still depend on their author's declaration.
	var needVisibility []string
	dids := make(map[string]bool)
	for _, pv := range out.Posts {
		if pv == nil {
			continue
		}
		if _, requested := verdicts[pv.Uri]; !requested {
			continue
		}
		v := Verdict{OK: true, Quotable: true}
		if pv.Author != nil {
			v.AuthorDID = pv.Author.Did
		}
		sysLabel, labelled := systemLabel(pv)
		switch {
		case blockedEitherWay(pv):
			v.OK, v.Quotable, v.Reason = false, false, ReasonBlocked
		case g.client.hasAdultContentLabel(pv.Labels):
			v.OK, v.Quotable, v.Reason = false, false, ReasonAdultLabel
		case labelled:
			v.OK, v.Quotable, v.Reason = false, false, labelReason(sysLabel)
		case v.AuthorDID == "":
			// An author-less post view cannot be attributed, so its
			// declaration cannot be read either: fail closed.
			v.OK, v.Quotable, v.Reason = false, false, ReasonVisibilityUnknown
		default:
			needVisibility = append(needVisibility, pv.Uri)
			dids[v.AuthorDID] = true
		}
		verdicts[pv.Uri] = v
	}

	if len(dids) > 0 {
		seen := g.resolveVisibility(ctx, dids)
		for _, uri := range needVisibility {
			v := verdicts[uri]
			switch seen[v.AuthorDID] {
			case VisibilityHidden:
				v.OK, v.Quotable, v.Reason = false, false, ReasonHiddenFromRecommendations
			case VisibilityUnknown:
				v.OK, v.Quotable, v.Reason = false, false, ReasonVisibilityUnknown
			}
			verdicts[uri] = v
		}
	}

	// A postgate only forbids quoting, so it is applied last: it must not mask
	// a reason that would have suppressed the post entirely.
	for uri, v := range verdicts {
		if v.OK && embeddingDisabled(uri, out.Posts) {
			v.Quotable, v.Reason = false, ReasonQuoteControl
			verdicts[uri] = v
		}
	}

	for _, uri := range uris {
		v := verdicts[uri]
		slog.Info("feature_gate", "surface", surface, "uri", uri, "ok", v.OK, "quotable", v.Quotable, "reason", v.Reason)
	}
	return verdicts, nil
}

// resolveVisibility looks each distinct author up once, concurrently. An
// Unknown is retried once inline, since the resolver caches only definitive
// answers and a single failed PDS read is often transient.
func (g *FeatureGate) resolveVisibility(ctx context.Context, dids map[string]bool) map[string]Visibility {
	var (
		mu   sync.Mutex
		seen = make(map[string]Visibility, len(dids))
		wg   sync.WaitGroup
		sem  = make(chan struct{}, visibilityConcurrency)
	)
	for did := range dids {
		wg.Add(1)
		go func(did string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			v := g.visibility.Lookup(ctx, did)
			if v == VisibilityUnknown {
				v = g.visibility.Lookup(ctx, did)
			}
			mu.Lock()
			seen[did] = v
			mu.Unlock()
		}(did)
	}
	wg.Wait()
	return seen
}

// blockedEitherWay reports whether a block exists between the author and this
// account, in either direction or via a list. Viewer state is only populated
// on an authenticated call.
func blockedEitherWay(pv *bsky.FeedDefs_PostView) bool {
	if pv.Author == nil || pv.Author.Viewer == nil {
		return false
	}
	vs := pv.Author.Viewer
	if vs.BlockedBy != nil && *vs.BlockedBy {
		return true
	}
	if vs.Blocking != nil && *vs.Blocking != "" {
		return true
	}
	return vs.BlockingByList != nil
}

// systemLabel returns the first moderation label on the post or its author
// whose value starts with "!", which covers !hide, !warn, !no-unauthenticated
// and !takedown. Those are instructions to applications, and featuring the
// post would ignore them.
func systemLabel(pv *bsky.FeedDefs_PostView) (string, bool) {
	if val, ok := firstSystemLabel(pv.Labels); ok {
		return val, true
	}
	if pv.Author != nil {
		return firstSystemLabel(pv.Author.Labels)
	}
	return "", false
}

func firstSystemLabel(labels []*atproto.LabelDefs_Label) (string, bool) {
	for _, l := range labels {
		if l != nil && strings.HasPrefix(l.Val, "!") {
			return l.Val, true
		}
	}
	return "", false
}

// embeddingDisabled reports whether the author attached a postgate with a
// disable rule to this post, which would render a quote as
// app.bsky.embed.record#viewDetached — "Removed by author".
func embeddingDisabled(uri string, posts []*bsky.FeedDefs_PostView) bool {
	for _, pv := range posts {
		if pv == nil || pv.Uri != uri {
			continue
		}
		return pv.Viewer != nil && pv.Viewer.EmbeddingDisabled != nil && *pv.Viewer.EmbeddingDisabled
	}
	return false
}
