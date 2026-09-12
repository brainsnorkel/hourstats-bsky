// Package alerts turns the stats the bot already records — snapshots, the
// stats_events log and the live consumer report — into a small set of named
// conditions, and sends each one down a single notification path.
//
// The design constraint is that nothing here may block the scheduler loop or
// depend on an external service being reachable: Evaluate is pure, and the
// Notifier's only required sink is the process log.
package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// Severity levels. Only warn and error move the reported health status;
// info exists for conditions that are worth naming on /stats/health but are
// routine enough that paging on them would train an operator to ignore the
// channel (a handful of denylist hits every window, for instance).
const (
	SeverityInfo  = "info"
	SeverityWarn  = "warn"
	SeverityError = "error"
)

// Reported health statuses.
const (
	StatusOK    = "ok"
	StatusWarn  = "warn"
	StatusError = "error"
)

// Condition is one thing that is wrong, named so it can be suppressed and
// looked up. Name is stable across evaluations — it is the suppression key —
// while Message carries the numbers of this particular evaluation.
// Condition is one thing the evaluation found worth saying. Actionable marks
// the ones where a person should do something now; the notifier only pings
// the channel for those and for errors.
type Condition struct {
	Actionable bool
	Name       string `json:"name"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
}

// Thresholds holds every number Evaluate compares against, so a test can set
// them directly and production can read them from the environment.
type Thresholds struct {
	// CappedPostsPerSnapshot is the per-snapshot rate-cap drop count above
	// which the intake is considered to be shedding real traffic.
	CappedPostsPerSnapshot int
	// StalePostsPerSnapshot is the per-snapshot backfill drop count above
	// which the stream is considered to be replaying a repo rather than
	// following the live tail.
	StalePostsPerSnapshot int
	// OversizedPosts is the per-snapshot oversized-text drop count above
	// which the rune cap is suspected of being set too low. Not env-tunable:
	// prod sits at single digits, so there is nothing to tune yet.
	OversizedPosts int
	// DeniedPostsWarn is the per-snapshot denylist count above which the
	// denylist is doing more than its intended trickle
	// (ALERT_DENIED_POSTS_PER_SNAPSHOT).
	DeniedPostsWarn int
	// ReconnectsPerHour is the reconnect count, summed over the snapshots
	// covering roughly the last hour, above which the connection is churning.
	ReconnectsPerHour int
	// CycleSeconds is the analysis cycle wall time above which the cycle is
	// at risk of overlapping the next tick.
	CycleSeconds int
	// RSSPct is the share of TotalMemoryMB the process RSS may reach before
	// the machine is considered close to the memory guard's territory.
	RSSPct int
	// TotalMemoryMB is what RSSPct is a percentage of. Zero disables the RSS
	// condition, since a percentage of an unknown total means nothing.
	TotalMemoryMB int
}

// Default thresholds. The three per-snapshot drop counts are deliberately well
// above prod's routine rates: prod sheds a few thousand capped posts and a few
// hundred thousand backfill posts a window as a matter of course, so alerting
// at zero would alert every window.
const (
	defaultCappedPostsPerSnapshot = 5000
	defaultStalePostsPerSnapshot  = 300000
	defaultOversizedPosts         = 100
	defaultDeniedPostsWarn        = 250000
	defaultReconnectsPerHour      = 3
	defaultCycleSeconds           = 900
	defaultRSSPct                 = 60
	// defaultTotalMemoryMB is the VM size this bot has always run on, used
	// only when the environment describes no machine at all. It matches
	// memGuardDefaultTotalMB in cmd/hourstats.
	defaultTotalMemoryMB = 1024
)

// eventWindowFallback is how far back events are read when there is only one
// snapshot to work from, so the first evaluation after a restart still sees
// the events it should.
const eventWindowFallback = 30 * time.Minute

// maxEvents bounds the event read. A window that produced more alerting
// events than this is already described by the first few.
const maxEvents = 200

// DefaultThresholds returns the thresholds with no environment applied.
func DefaultThresholds() Thresholds {
	return Thresholds{
		CappedPostsPerSnapshot: defaultCappedPostsPerSnapshot,
		StalePostsPerSnapshot:  defaultStalePostsPerSnapshot,
		OversizedPosts:         defaultOversizedPosts,
		DeniedPostsWarn:        defaultDeniedPostsWarn,
		ReconnectsPerHour:      defaultReconnectsPerHour,
		CycleSeconds:           defaultCycleSeconds,
		RSSPct:                 defaultRSSPct,
		TotalMemoryMB:          defaultTotalMemoryMB,
	}
}

// ThresholdsFromEnv returns the defaults with the ALERT_* overrides applied.
// TotalMemoryMB follows the memory guard's resolution order — FLY_VM_MEMORY_MB
// first, since Fly sets it on every machine and it follows a resize, then the
// manual MEMORY_GUARD_TOTAL_MB — so the RSS condition and the guard are always
// talking about the same machine.
func ThresholdsFromEnv() Thresholds {
	cfg := DefaultThresholds()
	cfg.CappedPostsPerSnapshot = envInt("ALERT_CAPPED_POSTS_PER_SNAPSHOT", cfg.CappedPostsPerSnapshot)
	cfg.DeniedPostsWarn = envInt("ALERT_DENIED_POSTS_PER_SNAPSHOT", cfg.DeniedPostsWarn)
	cfg.StalePostsPerSnapshot = envInt("ALERT_STALE_POSTS_PER_SNAPSHOT", cfg.StalePostsPerSnapshot)
	cfg.ReconnectsPerHour = envInt("ALERT_RECONNECTS_PER_HOUR", cfg.ReconnectsPerHour)
	cfg.CycleSeconds = envInt("ALERT_CYCLE_SECONDS", cfg.CycleSeconds)
	cfg.RSSPct = envInt("ALERT_RSS_PCT", cfg.RSSPct)
	for _, key := range []string{"FLY_VM_MEMORY_MB", "MEMORY_GUARD_TOTAL_MB"} {
		if mb := envInt(key, 0); mb > 0 {
			cfg.TotalMemoryMB = mb
			break
		}
	}
	return cfg
}

// envInt reads a non-negative integer, falling back (with a warning) on
// anything it cannot parse.
func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		slog.Warn("alert threshold is not a non-negative integer, using the default",
			"env", key, "value", raw, "default", fallback)
		return fallback
	}
	return n
}

// ConsumerReport is the slice of jetstream.StatsReport Evaluate needs. It is a
// plain struct rather than the real report so this package does not depend on
// the consumer, and so a test can state a reconnect count in one line.
type ConsumerReport struct {
	// Reconnects is the consumer's lifetime reconnect count. It is only a
	// fallback: snapshots store per-snapshot reconnect deltas, which is the
	// measure an hourly threshold wants.
	Reconnects int64
}

// alertingEvents maps the stats_events types worth alerting on to a severity.
// An event type absent from this map is ignored, so adding a new event to the
// bot does not silently start paging.
//
// seq_floor_reset is listed in hope: the reset it would describe happens in
// internal/jetstream, which holds no collector, so nothing writes that event
// today. Listing it costs nothing and means the condition exists the day a
// hook does.
var alertingEvents = map[string]string{
	"memory_guard_trip":        SeverityError,
	"memory_guard_warn":        SeverityWarn,
	"window_capped":            SeverityWarn,
	"hydration_timeout":        SeverityWarn,
	"consumer_restart":         SeverityWarn,
	"feature_gate_unavailable": SeverityWarn,
	"seq_floor_reset":          SeverityWarn,
}

// actionableEvents are the events where a person should look now rather
// than at the next routine check: an hour was lost or is about to be.
var actionableEvents = map[string]bool{
	"memory_guard_trip": true,
	"window_capped":     true,
	"consumer_restart":  true,
}

// eventMeaning explains each event type for the person reading the alert.
var eventMeaning = map[string]string{
	"memory_guard_trip":        "Meaning: memory crossed the trip line (70% of the machine) and the running cycle was cancelled, so that hour published nothing and its sentiment is marked low confidence. A heap profile was written to /data. Normal is never. Check: window size, stale/capped floods, and the profile.",
	"memory_guard_warn":        "Meaning: memory crossed the warn line (55% of the machine); the cycle continued and a heap profile was written. Normal is never. Check: whether the next snapshot's RSS came back down.",
	"window_capped":            "Meaning: more posts were in the hour's window than ANALYSIS_MAX_WINDOW_POSTS allows, so the hour was marked low confidence and nothing was posted. Normal is never (the cap is ~3x the busiest hour). Check: whether it was a flood the per-account cap should have caught.",
	"hydration_timeout":        "Meaning: engagement hydration did not finish inside its budget; posts left unhydrated were excluded and, above the allowed share, the hour was marked low confidence. Check: public.api.bsky.app errors and rate limiting in the hydrator log lines.",
	"consumer_restart":         "Meaning: the firehose consumer exited outside its own reconnect loop and was restarted; ingest paused briefly. Normal is never. Check: the error on the consumer_restart event and the lines before it.",
	"feature_gate_unavailable": "Meaning: the privacy gate could not reach Bluesky twice in a row, so the summary went out with no top posts and no exemplars for that hour by design. Check: Bluesky API status and auth.",
	"seq_floor_reset":          "Meaning: Jetstream delivered 10,000 frames in a row that looked already-seen, which happens when a reconnect lands on a different Jetstream instance; the dedup floor was reset and ingest continued. Check: whether counts for the half hour look duplicated.",
}

// eventOrder fixes the order alerting events are reported in, so a set of
// conditions is stable across evaluations and across map iterations.
var eventOrder = []string{
	"memory_guard_trip",
	"memory_guard_warn",
	"window_capped",
	"hydration_timeout",
	"consumer_restart",
	"feature_gate_unavailable",
	"seq_floor_reset",
}

// prevInt reads one counter from the previous snapshot, or 0 when absent.
func prevInt(p *store.StatsSnapshot, get func(*store.StatsSnapshot) int) int64 {
	if p == nil {
		return 0
	}
	return int64(get(p))
}

// versus phrases the comparison every alert carries: the same counter in the
// previous half-hour snapshot (when there is one) and the threshold.
func versus(prevKnown bool, prev, threshold int64) string {
	if prevKnown {
		return fmt.Sprintf("previous half hour %d, threshold %d", prev, threshold)
	}
	return fmt.Sprintf("threshold %d", threshold)
}

// Evaluate compares the two most recent snapshots, the events since the
// previous one, and the live consumer report against cfg, and returns every
// condition that holds. It is pure: no I/O, no clock, no logging, so the
// table tests are the specification.
//
// latest may be nil (no snapshots yet), in which case only the event and
// reconnect-fallback conditions can fire. previous may be nil on the first
// snapshot after a restart.
func Evaluate(latest, previous *store.StatsSnapshot, events []store.StatsEvent, report ConsumerReport, cfg Thresholds) []Condition {
	var conds []Condition

	if latest != nil {
		// Dropped posts mean the write buffer was full and firehose posts
		// were thrown away. One window of it is a spike; two in a row is
		// sustained loss, which is why the previous snapshot is required.
		if previous != nil && latest.DroppedPosts > 0 && previous.DroppedPosts > 0 {
			conds = append(conds, Condition{
				Name:       "dropped_posts",
				Actionable: true,
				Severity:   SeverityError,
				Message: fmt.Sprintf("%d posts were thrown away this half hour after %d in the previous one because the SQLite write buffer stayed full. Meaning: the firehose is arriving faster than the database can absorb it, so this hour's counts and sentiment are missing posts. Normal is 0. Check: slow write flush warnings, WAL size, and whether a cycle or purge is holding the write connection.",
					latest.DroppedPosts, previous.DroppedPosts),
			})
		}

		if latest.CappedPosts > cfg.CappedPostsPerSnapshot {
			conds = append(conds, Condition{
				Name:     "capped_posts",
				Severity: SeverityWarn,
				Message: fmt.Sprintf("%d posts were dropped this half hour by the per-account rate cap (%s). Meaning: some accounts posted faster than the cap allows, which is what a spam or import flood looks like; their excess never reached the buffer or the counts. Normal on prod is a few hundred. Check: the 'capped posts hourly summary' log line for the accounts, and whether the firehose total looks inflated.",
					latest.CappedPosts, versus(previous != nil, prevInt(previous, func(p *store.StatsSnapshot) int { return p.CappedPosts }), int64(cfg.CappedPostsPerSnapshot))),
			})
		}

		// The denylist doing its job is information, not a problem; it only
		// becomes one at a volume that says the list is matching far more
		// than it was written for.
		if latest.DeniedPosts > cfg.DeniedPostsWarn {
			conds = append(conds, Condition{
				Name:     "denied_posts",
				Severity: SeverityWarn,
				Message: fmt.Sprintf("%d posts from denylisted accounts were dropped this half hour (%s). Meaning: the operator denylist is matching far more than it was written for; either a listed account is flooding or the list has grown. Check: key_value firehose_deny_dids and FIREHOSE_DENY_DIDS, and the stale/capped counters for the same accounts.",
					latest.DeniedPosts, versus(previous != nil, prevInt(previous, func(p *store.StatsSnapshot) int { return p.DeniedPosts }), int64(cfg.DeniedPostsWarn))),
			})
		} else if latest.DeniedPosts > 0 {
			conds = append(conds, Condition{
				Name:     "denied_posts",
				Severity: SeverityInfo,
				Message:  fmt.Sprintf("%d posts from denylisted accounts were dropped this half hour. Meaning: the denylist is doing its job; nothing to do.", latest.DeniedPosts),
			})
		}

		if latest.StalePosts > cfg.StalePostsPerSnapshot {
			conds = append(conds, Condition{
				Name:     "stale_posts",
				Severity: SeverityWarn,
				Message: fmt.Sprintf("%d posts were dropped this half hour because their createdAt was more than the allowed age before (or ahead of) the time Jetstream delivered them (%s). Meaning: accounts are importing or replaying old posts, or Jetstream re-delivered history; none of it entered the analysis window. Normal on prod is 60k to 250k per half hour from a few importing accounts. Check: the 'stale create hourly summary' line if diagnostics are on, and whether English counts also jumped (that would mean a live flood, not backfill).",
					latest.StalePosts, versus(previous != nil, prevInt(previous, func(p *store.StatsSnapshot) int { return p.StalePosts }), int64(cfg.StalePostsPerSnapshot))),
			})
		}

		if latest.OversizedPosts > cfg.OversizedPosts {
			conds = append(conds, Condition{
				Name:       "oversized_posts",
				Actionable: true,
				Severity:   SeverityWarn,
				Message: fmt.Sprintf("%d posts were dropped this half hour for exceeding the text length cap (%s). Meaning: a PDS is emitting posts far larger than the app allows, which is either broken software or an attempt to exhaust memory. Normal is 0. Check: memory and cycle time on the same snapshot.",
					latest.OversizedPosts, versus(previous != nil, prevInt(previous, func(p *store.StatsSnapshot) int { return p.OversizedPosts }), int64(cfg.OversizedPosts))),
			})
		}

		// CycleDurationMs is only meaningful on a snapshot that saw a cycle;
		// it is reset to zero afterwards, so a non-analysis snapshot carries
		// no duration at all.
		if latest.AnalysisRan != 0 && latest.CycleDurationMs > int64(cfg.CycleSeconds)*1000 {
			conds = append(conds, Condition{
				Name:       "cycle_duration",
				Actionable: true,
				Severity:   SeverityWarn,
				Message: fmt.Sprintf("the hourly analysis cycle took %ds against a threshold of %ds; typical is 250s at 40k posts and 650s at 105k. Meaning: hydration, Gemini or the database is slow, or the window is unusually large. If it passes 900s it can overlap the next hour, which is then skipped. Check: 'hydration complete' timings, Gemini errors, and posts_considered on this snapshot (%d).",
					latest.CycleDurationMs/1000, cfg.CycleSeconds, latest.PostsConsidered),
			})
		}

		if cfg.TotalMemoryMB > 0 && cfg.RSSPct > 0 && latest.RSSBytes > 0 {
			totalBytes := int64(cfg.TotalMemoryMB) * 1024 * 1024
			if latest.RSSBytes*100 > totalBytes*int64(cfg.RSSPct) {
				conds = append(conds, Condition{
					Name:     "rss_high",
					Severity: SeverityWarn,
					Message: fmt.Sprintf("the process is using %dMB of memory, %d%% of the machine's %dMB (threshold %d%%); typical is 200 to 370MB between and during cycles. Meaning: the memory guard warns at 55%% and cancels the cycle at 70%%, so this is the last stop before an hour is lost. Check: cycle_memory_peak events and whether a cycle is running with an unusually large window.",
						latest.RSSBytes/(1024*1024), latest.RSSBytes*100/totalBytes,
						cfg.TotalMemoryMB, cfg.RSSPct),
				})
			}
		}
	}

	if n, source := reconnects(latest, previous, report); n > int64(cfg.ReconnectsPerHour) {
		conds = append(conds, Condition{
			Name:       "consumer_reconnects",
			Actionable: true,
			Severity:   SeverityWarn,
			Message: fmt.Sprintf("the firehose connection reconnected %d times in the last hour (source: %s; threshold %d, normal 0). Meaning: Jetstream or the network is unstable; each reconnect resumes by timestamp so little is lost, but frequent churn means gaps. Check: 'connection lost' and 'rotating jetstream endpoint' log lines.",
				n, source, cfg.ReconnectsPerHour),
		})
	}

	conds = append(conds, eventConditions(events)...)
	return conds
}

// reconnects returns the reconnect count for roughly the last hour and names
// where it came from. Snapshots store per-snapshot deltas and are taken every
// 30 minutes, so the two of them together are the hour; the consumer's own
// lifetime counter is the fallback for the window before the first snapshot
// exists, where it is the only number available.
func reconnects(latest, previous *store.StatsSnapshot, report ConsumerReport) (int64, string) {
	if latest == nil {
		return report.Reconnects, "the live consumer report"
	}
	n := int64(latest.ReconnectCount)
	if previous != nil {
		n += int64(previous.ReconnectCount)
	}
	return n, "the last two snapshots"
}

// eventConditions collapses the events since the previous evaluation into one
// condition per alerting event type, so ten hydration timeouts in a window are
// one alert that says ten rather than ten alerts.
func eventConditions(events []store.StatsEvent) []Condition {
	if len(events) == 0 {
		return nil
	}
	counts := make(map[string]int, len(alertingEvents))
	latest := make(map[string]string, len(alertingEvents))
	for _, e := range events {
		if _, ok := alertingEvents[e.EventType]; !ok {
			continue
		}
		counts[e.EventType]++
		// GetEvents returns newest first, so the first one seen for a type is
		// the one whose details are worth carrying.
		if _, ok := latest[e.EventType]; !ok {
			latest[e.EventType] = e.Details
		}
	}

	var conds []Condition
	for _, eventType := range eventOrder {
		n := counts[eventType]
		if n == 0 {
			continue
		}
		msg := fmt.Sprintf("%d %s event(s) since the previous snapshot", n, eventType)
		if d := latest[eventType]; d != "" {
			msg += ": " + d
		}
		if why, ok := eventMeaning[eventType]; ok {
			msg += ". " + why
		}
		conds = append(conds, Condition{
			Name:       eventType,
			Actionable: actionableEvents[eventType],
			Severity:   alertingEvents[eventType],
			Message:    msg,
		})
	}
	return conds
}

// Status reduces a set of conditions to the health status the API reports.
func Status(conds []Condition) string {
	status := StatusOK
	for _, c := range conds {
		switch c.Severity {
		case SeverityError:
			return StatusError
		case SeverityWarn:
			status = StatusWarn
		}
	}
	return status
}

// SnapshotSource is the subset of *store.Store Run reads.
type SnapshotSource interface {
	GetSnapshotHistory(ctx context.Context, since time.Time, limit int) ([]store.StatsSnapshot, error)
	GetEvents(ctx context.Context, since time.Time, eventType string, limit int) ([]store.StatsEvent, error)
}

// Run reads the two most recent snapshots and the events since the older of
// them, evaluates the conditions, records them in state and notifies on them.
// It is the whole alert path in one call so cmd/hourstats only has to wire it
// to the snapshot ticker.
//
// Every failure is logged and swallowed: an alert path that can take down the
// scheduler is worse than no alert path.
func Run(ctx context.Context, src SnapshotSource, report ConsumerReport, cfg Thresholds, state *State, n *Notifier) []Condition {
	// Two snapshots is all Evaluate reads; a 3-hour floor is wide enough to
	// find them after a gap and narrow enough to stay off an index scan.
	snaps, err := src.GetSnapshotHistory(ctx, time.Now().UTC().Add(-3*time.Hour), 2)
	if err != nil {
		slog.Warn("alert evaluation could not read snapshots", "error", err)
		return nil
	}

	var latest, previous *store.StatsSnapshot
	if len(snaps) > 0 {
		latest = &snaps[0]
	}
	if len(snaps) > 1 {
		previous = &snaps[1]
	}

	since := time.Now().UTC().Add(-eventWindowFallback)
	switch {
	case previous != nil:
		since = previous.SnapshotTime
	case latest != nil:
		since = latest.SnapshotTime.Add(-eventWindowFallback)
	}

	events, err := src.GetEvents(ctx, since, "", maxEvents)
	if err != nil {
		// The snapshot conditions are still worth evaluating without events.
		slog.Warn("alert evaluation could not read events", "error", err)
	}

	conds := Evaluate(latest, previous, events, report, cfg)
	state.Set(conds, time.Now().UTC())
	n.Notify(ctx, conds)
	return conds
}
