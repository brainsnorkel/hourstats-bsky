package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
// the number of posts it can hydrate at most (rate x batch x minutes), with
// the two cycle limits derived from it: the window cap and the cycle-duration
// alert threshold.
type hydrationBudget struct {
	Timeout           time.Duration
	RatePerMinute     int
	CeilingPosts      int
	WindowCap         int
	CycleAlertSeconds int
	Notes             []string
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

// applyCycleLimits derives the window cap and the cycle-duration alert from a
// resolved budget. Unset, the cap is max(defaultMaxWindowPosts, ceiling) so no
// hydratable window is capped, and the alert is the hydration timeout plus
// hydrationCycleReserve. Set values are kept as given (a cap of 0 or less
// disables it); a positive cap below the ceiling is noted, since hydration
// minutes above it are unreachable.
//
// The clamp in resolveHydrationBudget already defines a cycle's slot as
// timeout + reserve, so the alert fires exactly when a cycle overruns that
// slot, and since the clamp keeps timeout + reserve within the interval it
// still fires before an overlap. The measured overrun past the hydration
// deadline was ~145 s at 150k posts and grows with the window, so a 350k-post
// hour lands near 300 s: a 5-minute margin would page on exactly the
// ceiling-bound hours that now post.
func applyCycleLimits(b hydrationBudget, windowCap int, windowCapSet bool, cycleAlertSeconds int, cycleAlertSet bool) hydrationBudget {
	b.Notes = append([]string(nil), b.Notes...)
	if windowCapSet {
		b.WindowCap = windowCap
		if windowCap > 0 && windowCap < b.CeilingPosts {
			b.Notes = append(b.Notes, fmt.Sprintf("window cap %d is below the hydration ceiling %d: hydration minutes above the cap are unreachable", windowCap, b.CeilingPosts))
		}
	} else {
		b.WindowCap = max(defaultMaxWindowPosts, b.CeilingPosts)
	}
	if cycleAlertSet {
		b.CycleAlertSeconds = cycleAlertSeconds
	} else {
		b.CycleAlertSeconds = int((b.Timeout + hydrationCycleReserve).Seconds())
	}
	return b
}

// lookupEnvInt reports an integer env value and whether it was set to one.
// Unset, empty and unparseable values all report unset.
func lookupEnvInt(key string) (int, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("ignoring non-integer env value, using the derived default", "key", key, "value", raw)
		return 0, false
	}
	return n, true
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
		"window_cap", b.WindowCap,
		"cycle_alert_seconds", b.CycleAlertSeconds,
		"notes", b.Notes,
	)
}

var (
	hydrationBudgetOnce sync.Once
	hydrationBudgetVal  hydrationBudget
)

// cycleLimitsFromEnv applies ANALYSIS_MAX_WINDOW_POSTS and ALERT_CYCLE_SECONDS
// to a resolved budget. A negative ALERT_CYCLE_SECONDS counts as unset, as it
// does in internal/alerts, so the logged value and the threshold agree.
func cycleLimitsFromEnv(b hydrationBudget) hydrationBudget {
	windowCap, windowCapSet := lookupEnvInt("ANALYSIS_MAX_WINDOW_POSTS")
	cycleAlert, cycleAlertSet := lookupEnvInt("ALERT_CYCLE_SECONDS")
	cycleAlertSet = cycleAlertSet && cycleAlert >= 0
	return applyCycleLimits(b, windowCap, windowCapSet, cycleAlert, cycleAlertSet)
}

// sharedHydrationBudget resolves the budget from HYDRATION_TIMEOUT_MINUTES and
// HYDRATION_RATE_PER_MINUTE once per process, derives the cycle limits from it
// and ANALYSIS_MAX_WINDOW_POSTS / ALERT_CYCLE_SECONDS, and logs it. The
// interval of the first call wins; main calls it at startup with
// ANALYSIS_INTERVAL_MINUTES.
func sharedHydrationBudget(intervalMinutes int) hydrationBudget {
	hydrationBudgetOnce.Do(func() {
		hydrationBudgetVal = cycleLimitsFromEnv(resolveHydrationBudget(intervalMinutes,
			envInt("HYDRATION_TIMEOUT_MINUTES", defaultHydrationTimeoutMinutes),
			envInt("HYDRATION_RATE_PER_MINUTE", defaultHydrationRatePerMinute)))
		logHydrationBudget(hydrationBudgetVal)
	})
	return hydrationBudgetVal
}
