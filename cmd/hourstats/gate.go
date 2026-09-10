package main

import (
	"context"
	"log/slog"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

// visibilityResolver is the process-wide cache of content visibility
// declarations. It is a package-level var because every surface that features
// a post builds its own short-lived BlueskyClient (the analysis cycle, the
// daily job, the weekly report), and threading a resolver through all of them
// would touch far more code than it is worth. main sets it once, before any
// ticker can fire; nothing else writes it.
var visibilityResolver *client.VisibilityResolver

// newFeatureGate builds the gate for one surface from its authenticated
// client and the shared resolver.
func newFeatureGate(bskyClient *client.BlueskyClient) *client.FeatureGate {
	return bskyClient.NewFeatureGate(visibilityResolver)
}

// featureGate is the slice of *client.FeatureGate the callers below need,
// narrowed to one method so each surface's decision can be tested without an
// AppView.
type featureGate interface {
	Check(ctx context.Context, surface string, uris []string) (map[string]client.Verdict, error)
}

// checkFeatureGate runs the gate with a single retry. Both the AppView call
// and the declaration reads can fail transiently, and every caller's fallback
// costs the post something, so one retry is worth the latency.
func checkFeatureGate(ctx context.Context, gate featureGate, surface string, uris []string) (map[string]client.Verdict, error) {
	verdicts, err := gate.Check(ctx, surface, uris)
	if err == nil {
		return verdicts, nil
	}
	slog.Warn("feature gate check failed, retrying", "surface", surface, "uris", len(uris), "error", err)

	verdicts, err = gate.Check(ctx, surface, uris)
	if err != nil {
		slog.Warn("feature gate check failed again", "surface", surface, "uris", len(uris), "error", err)
		return nil, err
	}
	return verdicts, nil
}

// gateTopPosts trims the ranked candidates to the first topPostCount the
// feature gate clears, in rank order. It reports whether the published rank-1
// post may be quote-embedded, and whether the gate answered at all.
//
// When the gate is unavailable no post is returned and ok is false: the
// caller still publishes the hour's aggregate summary, but with no top posts
// in it at all, so nothing amplifies an account the gate could not check —
// and the run row records no top post either, so the daily and weekly
// reports cannot inherit one.
func gateTopPosts(ctx context.Context, gate featureGate, candidates []analyzer.AnalyzedPost) (top []analyzer.AnalyzedPost, quoteControlled, ok bool) {
	if len(candidates) == 0 {
		return nil, false, true
	}

	uris := make([]string, 0, len(candidates))
	for _, ap := range candidates {
		uris = append(uris, ap.URI)
	}

	verdicts, err := checkFeatureGate(ctx, gate, "hourly", uris)
	if err != nil {
		return nil, false, false
	}

	for _, ap := range candidates {
		v := verdicts[ap.URI]
		if !v.OK {
			// The reason alone is what an operator needs; naming the post or
			// its author in a routine Info line republishes exactly the
			// association the gate just refused to publish.
			slog.Info("top post not featured", "reason", v.Reason)
			slog.Debug("top post not featured", "uri", ap.URI, "author", ap.Author, "reason", v.Reason)
			continue
		}
		if len(top) == 0 {
			quoteControlled = !v.Quotable
		}
		top = append(top, ap)
		if len(top) >= topPostCount {
			break
		}
	}
	if len(top) < topPostCount {
		slog.Warn("fewer top posts than usual cleared the feature gate",
			"kept", len(top), "candidates", len(candidates), "wanted", topPostCount)
	}
	return top, quoteControlled, true
}

// exemplarGate adapts the feature gate to topics.ExemplarGate, which is
// declared in the topics package so it need not import internal/client.
type exemplarGate struct {
	gate featureGate
}

// Check reduces each Verdict to whether the post may be featured at all: a
// trending exemplar is a handle link, never a quote embed, so quote control
// alone does not disqualify one.
func (g exemplarGate) Check(ctx context.Context, surface string, uris []string) (map[string]bool, error) {
	verdicts, err := g.gate.Check(ctx, surface, uris)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(verdicts))
	for uri, v := range verdicts {
		allowed[uri] = v.OK
	}
	return allowed, nil
}

// newExemplarGate builds the topics-facing adapter for an authenticated client.
func newExemplarGate(bskyClient *client.BlueskyClient) topics.ExemplarGate {
	return exemplarGate{gate: newFeatureGate(bskyClient)}
}
