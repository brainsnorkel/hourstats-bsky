package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/hydrator"
)

const (
	defaultHydrationTimeoutMinutes = 12
	defaultHydrationRatePerMinute  = 500

	// maxHydrationRatePerMinute is the appview's own limit: public.api.bsky.app
	// documents 3000 requests per 5 minutes per IP. Past it hydration would be
	// throttled by 429s instead of by the bot's limiter.
	maxHydrationRatePerMinute = 600
	// warnHydrationRatePerMinute leaves headroom under that limit for retries
	// and anything else on the same IP.
	warnHydrationRatePerMinute = 550

	// hydrationCycleReserve is what the rest of a cycle needs inside its
	// interval besides hydration: the window read, the 60s topic wait, the
	// posting block and a margin.
	hydrationCycleReserve = 10 * time.Minute
)

// hydrationBudget is the hydrator's time and rate budget for one cycle, and
// the number of posts it can hydrate at most (rate x batch x minutes).
type hydrationBudget struct {
	Timeout       time.Duration
	RatePerMinute int
	CeilingPosts  int
	Notes         []string
}

// resolveHydrationBudget applies the defaults and clamps to the configured
// timeout and rate. The timeout is clamped so hydration leaves room for the
// rest of the cycle inside the analysis interval.
func resolveHydrationBudget(intervalMinutes, timeoutMinutes, ratePerMinute int) hydrationBudget {
	var b hydrationBudget
	if timeoutMinutes <= 0 {
		timeoutMinutes = defaultHydrationTimeoutMinutes
	}
	if ratePerMinute <= 0 {
		ratePerMinute = defaultHydrationRatePerMinute
	}

	if ratePerMinute > maxHydrationRatePerMinute {
		b.Notes = append(b.Notes, fmt.Sprintf("rate %d/min clamped to %d/min: public.api.bsky.app allows 3000 req / 5 min per IP", ratePerMinute, maxHydrationRatePerMinute))
		ratePerMinute = maxHydrationRatePerMinute
	}
	if ratePerMinute > warnHydrationRatePerMinute {
		b.Notes = append(b.Notes, "above 550/min leaves little headroom under the appview's 600/min")
	}

	maxTimeout := time.Duration(intervalMinutes)*time.Minute - hydrationCycleReserve
	if intervalMinutes <= 20 {
		maxTimeout = time.Duration(max(intervalMinutes/2, 1)) * time.Minute
	}
	timeout := time.Duration(timeoutMinutes) * time.Minute
	if timeout > maxTimeout {
		b.Notes = append(b.Notes, fmt.Sprintf("timeout %s clamped to %s to fit the %dm analysis interval", timeout, maxTimeout, intervalMinutes))
		timeout = maxTimeout
	}

	b.Timeout = timeout
	b.RatePerMinute = ratePerMinute
	b.CeilingPosts = ratePerMinute * hydrator.BatchSize * int(timeout/time.Minute)
	return b
}

// logHydrationBudget records the resolved budget and its implied ceiling, at
// Warn when anything was clamped or is close to the appview's limit.
func logHydrationBudget(b hydrationBudget) {
	level := slog.LevelInfo
	if len(b.Notes) > 0 {
		level = slog.LevelWarn
	}
	slog.Log(context.Background(), level, "hydration budget",
		"timeout", b.Timeout.String(),
		"rate_per_minute", b.RatePerMinute,
		"ceiling_posts", b.CeilingPosts,
		"notes", b.Notes,
	)
}

var (
	hydrationBudgetOnce sync.Once
	hydrationBudgetVal  hydrationBudget
)

// sharedHydrationBudget resolves the budget from HYDRATION_TIMEOUT_MINUTES and
// HYDRATION_RATE_PER_MINUTE once per process and logs it. The interval of the
// first call wins; main calls it at startup with ANALYSIS_INTERVAL_MINUTES.
func sharedHydrationBudget(intervalMinutes int) hydrationBudget {
	hydrationBudgetOnce.Do(func() {
		hydrationBudgetVal = resolveHydrationBudget(intervalMinutes,
			envInt("HYDRATION_TIMEOUT_MINUTES", defaultHydrationTimeoutMinutes),
			envInt("HYDRATION_RATE_PER_MINUTE", defaultHydrationRatePerMinute))
		logHydrationBudget(hydrationBudgetVal)
	})
	return hydrationBudgetVal
}
