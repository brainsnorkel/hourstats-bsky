package main

import (
	"log/slog"
	"os"
	"sync"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// defaultHydrationMaxErrorPct is the share of attempted posts hydration may
// lose before the cycle is marked low confidence.
const defaultHydrationMaxErrorPct = 10

var hydrationMaxErrorPctOnce sync.Once

// hydrationMaxErrorPct reads HYDRATION_MAX_ERROR_PCT. HYDRATION_MAX_UNHYDRATED_PCT,
// its name before deadline truncation stopped counting as a loss, is honoured
// when the new name is unset, with one deprecation warning per process.
func hydrationMaxErrorPct() int {
	if os.Getenv("HYDRATION_MAX_ERROR_PCT") == "" && os.Getenv("HYDRATION_MAX_UNHYDRATED_PCT") != "" {
		hydrationMaxErrorPctOnce.Do(func() {
			slog.Warn("HYDRATION_MAX_UNHYDRATED_PCT is deprecated, use HYDRATION_MAX_ERROR_PCT (it now counts attempted losses only)")
		})
		return envInt("HYDRATION_MAX_UNHYDRATED_PCT", defaultHydrationMaxErrorPct)
	}
	return envInt("HYDRATION_MAX_ERROR_PCT", defaultHydrationMaxErrorPct)
}

// windowPopulation splits one analysis window into the posts sentiment is
// scored over and the posts the top posts are ranked from.
type windowPopulation struct {
	// Scored is the sentiment population: every post except those hydration
	// attempted and could not return (deleted, taken down, adult-filtered, or
	// a failed getPosts). Hydrated posts come first, in window order, so
	// Ranked is a prefix of Scored and costs no allocation of its own.
	Scored []store.Post
	// Ranked is the hydrated posts, the only ones carrying engagement counts.
	Ranked []store.Post

	WindowPosts int
	Hydrated    int
	// NeverAttempted is the unhydrated posts hydration never reached because
	// its deadline fired first. They are scored: firehose deletes and account
	// purges are applied to post_buffer live, so they are almost all alive.
	NeverAttempted int
	// AttemptedLost is the unhydrated posts hydration did reach.
	AttemptedLost int
	// AttemptedLostPct is AttemptedLost as a percentage of the attempted
	// posts; a deadline truncation does not move it.
	AttemptedLostPct float64
}

// selectPopulation applies the population rule to a hydrated window.
// attempted is HydrateResult.Attempted, aligned with posts; a missing entry
// (nil or short slice) counts as attempted, which is the pre-hs-thk.4 rule of
// scoring hydrated posts only. A post with an author handle counts as
// hydrated whether or not this cycle attempted it.
//
// When every post was attempted, Scored and Ranked are the hydrated posts in
// window order, exactly what the old AuthorHandle filter produced.
func selectPopulation(posts []store.Post, attempted []bool) windowPopulation {
	wasAttempted := func(i int) bool { return i >= len(attempted) || attempted[i] }

	pop := windowPopulation{WindowPosts: len(posts)}
	attemptedCount := 0
	for i, p := range posts {
		tried := wasAttempted(i)
		if tried {
			attemptedCount++
		}
		switch {
		case p.AuthorHandle != "":
			pop.Hydrated++
		case tried:
			pop.AttemptedLost++
		default:
			pop.NeverAttempted++
		}
	}

	scored := make([]store.Post, pop.Hydrated+pop.NeverAttempted)
	h, n := 0, pop.Hydrated
	for i, p := range posts {
		switch {
		case p.AuthorHandle != "":
			scored[h] = p
			h++
		case !wasAttempted(i):
			scored[n] = p
			n++
		}
	}
	pop.Scored = scored
	pop.Ranked = scored[:pop.Hydrated:pop.Hydrated]

	if attemptedCount > 0 {
		pop.AttemptedLostPct = float64(pop.AttemptedLost) * 100 / float64(attemptedCount)
	}
	return pop
}
