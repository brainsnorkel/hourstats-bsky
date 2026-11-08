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
	"strings"
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
	// Accounts are the busiest accounts behind a drop counter, highest first,
	// for the conditions that have one. The message already names them, but
	// /stats/health carries them structurally as well so a script does not
	// have to read English. It is nil for every other condition.
	Accounts []DIDCount `json:"accounts,omitempty"`
}

// DIDCount is one account and the number of drops it accounts for. It mirrors
// jetstream.DIDCount rather than importing it, for the same reason
// ConsumerReport mirrors StatsReport.
type DIDCount struct {
	DID   string `json:"did"`
	Count int64  `json:"count"`
}

// Offenders is the per-account breakdown of the three drop counters the
// consumer accumulated since Since. The lists are the busiest accounts only,
// highest first; an empty list means the consumer reported none, which is what
// a restart between snapshots looks like.
type Offenders struct {
	Since  time.Time
	Stale  []DIDCount
	Capped []DIDCount
	Denied []DIDCount
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
func ThresholdsFromEnv() Thresholds {
	return ThresholdsFromEnvWith(DefaultThresholds())
}

// ThresholdsFromEnvWith returns base with the ALERT_* overrides applied, so a
// caller can supply derived defaults (cmd/hourstats derives CycleSeconds from
// the hydration budget).
//
// TotalMemoryMB follows the memory guard's resolution order — FLY_VM_MEMORY_MB
// first, since Fly sets it on every machine and it follows a resize, then the
// manual MEMORY_GUARD_TOTAL_MB — so the RSS condition and the guard are always
// talking about the same machine.
func ThresholdsFromEnvWith(base Thresholds) Thresholds {
	cfg := base
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

	// Offenders names the accounts behind the stale, capped and denied
	// counters of the window this evaluation covers. It is the zero value
	// when there is no consumer to ask, in which case the three drop
	// conditions read exactly as they did before it existed.
	Offenders Offenders
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
	"cycle_failed":             SeverityError,
	"memory_guard_trip":        SeverityError,
	"memory_guard_warn":        SeverityWarn,
	"window_capped":            SeverityWarn,
	"hydration_timeout":        SeverityWarn,
	"consumer_restart":         SeverityWarn,
	"feature_gate_unavailable": SeverityWarn,
	"seq_floor_reset":          SeverityWarn,
	"grouping_failed":          SeverityWarn,
	"stall_detected":           SeverityError,
	// hydration_ceiling_projected is written at the start of a cycle whose
	// window is larger than hydration can cover; posting_skipped at its end
	// when the hour published nothing. Alerts evaluate after each 30-minute
	// snapshot, so either lands up to ~35 minutes after the cycle started.
	"hydration_ceiling_projected": SeverityWarn,
	"posting_skipped":             SeverityWarn,
	// weekly_report_preview_failed is written by the Sunday preview when its
	// Discord post failed; raising it here is the retry, since the alert
	// path posts to the same webhook at the next snapshot.
	"weekly_report_preview_failed": SeverityWarn,
	// weekly_report_topic_dropped is written by the Monday weekly report when
	// its post went out without the top-topic line because the label
	// named a year before the week. The Sunday preview shows most of these in
	// advance; this covers a topic that changed once Sunday's rows landed.
	"weekly_report_topic_dropped": SeverityWarn,
}

// actionableEvents are the events where a person should look now rather
// than at the next routine check: an hour was lost or is about to be.
// posting_skipped is not listed: whether it pages depends on its reason, see
// postingSkippedActionable.
var actionableEvents = map[string]bool{
	"cycle_failed":      true,
	"memory_guard_trip": true,
	"window_capped":     true,
	"consumer_restart":  true,
	"stall_detected":    true,
}

// eventMeaning explains each event type for the person reading the alert.
var eventMeaning = map[string]string{
	"cycle_failed":                 "The hour's cycle stopped before recording a run: no summary, no sentiment point. Stage and error are on the event. The next hour runs regardless.",
	"memory_guard_trip":            "Memory crossed 70% of the machine; the cycle was cancelled and the hour published nothing. Heap profile written to /data.",
	"memory_guard_warn":            "Memory crossed 55% of the machine; the cycle continued and a heap profile was written. Check the next snapshot's RSS.",
	"window_capped":                "The hour had more posts than ANALYSIS_MAX_WINDOW_POSTS; marked low confidence, nothing posted. Busy live hours can now exceed the cap, so this is not necessarily a flood.",
	"hydration_timeout":            "Engagement hydration hit HYDRATION_TIMEOUT_MINUTES; posts it never reached are still scored, ranking uses the hydrated ones, and only attempted posts lost past HYDRATION_MAX_ERROR_PCT stop posting. rate_limited=0 on the event means the bot's own HYDRATION_RATE_PER_MINUTE limiter was the ceiling (rate x 25 x minutes), not the appview; otherwise check public.api.bsky.app errors in the hydrator lines.",
	"consumer_restart":             "The firehose consumer exited outside its reconnect loop and was restarted. See the error on the event.",
	"feature_gate_unavailable":     "The privacy gate could not reach Bluesky twice, so the hour's summary went out with no top posts, by design. Check Bluesky API status.",
	"seq_floor_reset":              "A reconnect landed on a different Jetstream instance; the dedup floor was reset and ingest continued. The half hour's counts may be duplicated.",
	"grouping_failed":              "Topic grouping produced nothing for the hour, so the trending reply was skipped and the sparkline footer shows no topic. Written when every Gemini tier failed, or when output validation or the label year rule dropped every cluster; the attempt class says which (invalid, label_year, or a call failure such as timeout). Two or more call failures in a window means Gemini is slow or down.",
	"hydration_ceiling_projected":  "The window is larger than hydration can cover in HYDRATION_TIMEOUT_MINUTES; the cycle will run out of budget about 25 minutes from now. Raise the timeout if the interval allows.",
	"posting_skipped":              "The hour's cycle finished but published nothing: no summary, sparkline or trending reply. reason= on the event says why; window_posts vs hydrated vs ceiling_posts shows whether hydration ran out of budget (raise HYDRATION_TIMEOUT_MINUTES) or the hour was too small or flooded. Evaluated after each 30-minute snapshot, so this lands up to ~35 minutes after the cycle started.",
	"weekly_report_preview_failed": "The Sunday preview of Monday's weekly report could not be posted to Discord, so the week is unreviewed. The preview text is in the process log (msg \"weekly report preview\"); seed weekly_report_last_week before 00:00 UTC Monday to skip the post.",
	"weekly_report_topic_dropped":  "Monday's weekly report went out without its top-topic line: the label named a year before the week, which the date-blind grouper gets wrong (years and label are on the event). Nothing to do for the post; if the label was right after all, that is the cost of the rule in cmd/hourstats/weekly.go.",
	"stall_detected":               "No posts have reached the bot for over five minutes. connected=false with reconnects means Jetstream itself is unreachable or refusing us; connected=true means a silent connection that has now been dropped. The hour's counts and sentiment will be short. Check the event's last_error and bsky.network status; JETSTREAM_LEGACY=true falls back to the v1 hosts.",
}

// eventOrder fixes the order alerting events are reported in, so a set of
// conditions is stable across evaluations and across map iterations.
var eventOrder = []string{
	"cycle_failed",
	"memory_guard_trip",
	"memory_guard_warn",
	"window_capped",
	"hydration_ceiling_projected",
	"hydration_timeout",
	"posting_skipped",
	"consumer_restart",
	"feature_gate_unavailable",
	"seq_floor_reset",
	"grouping_failed",
	"weekly_report_preview_failed",
	"weekly_report_topic_dropped",
	"stall_detected",
}

// defaultOffenderWindowMinutes is the window the per-account rates are
// computed over when the consumer's own window cannot be measured — the
// snapshot cadence, which is what that window almost always is.
const defaultOffenderWindowMinutes = 30.0

// offenderWindowMinutes is how long the breakdown accumulated for: from the
// consumer's last read to this snapshot. The snapshot's time is used rather
// than the wall clock because Evaluate has no clock, and because the counts
// stop at the snapshot that took them.
func offenderWindowMinutes(since time.Time, latest *store.StatsSnapshot) float64 {
	if since.IsZero() || latest == nil || latest.SnapshotTime.IsZero() {
		return defaultOffenderWindowMinutes
	}
	minutes := latest.SnapshotTime.Sub(since).Minutes()
	if minutes <= 0 {
		return defaultOffenderWindowMinutes
	}
	return minutes
}

// accountsPrefix introduces the per-account breakdown. It is the last thing in
// a flood condition's message, so the notifier can rewrite the DIDs after it
// as handles without disturbing the prose before it.
const accountsPrefix = "Top: "

// accountsText names the busiest accounts behind a drop counter, as
// "Top: <did> <n> (~<rate>/min); ...". It
// returns "" when there is no breakdown to report.
func accountsText(accounts []DIDCount, minutes float64) string {
	if len(accounts) == 0 {
		return ""
	}
	if minutes <= 0 {
		minutes = defaultOffenderWindowMinutes
	}
	entries := make([]string, 0, len(accounts))
	for _, a := range accounts {
		entries = append(entries, fmt.Sprintf("%s %d (~%.0f/min)",
			a.DID, a.Count, float64(a.Count)/minutes))
	}
	return accountsPrefix + strings.Join(entries, "; ")
}

// floodCondition builds one of the three drop-counter conditions, naming the
// accounts behind it when the consumer reported any. The DIDs go in the
// message because the channel this reaches is the operator's own.
func floodCondition(name, severity, message string, accounts []DIDCount, minutes float64) Condition {
	if text := accountsText(accounts, minutes); text != "" {
		message += " " + text
	}
	return Condition{
		Name:     name,
		Severity: severity,
		Message:  message,
		Accounts: accounts,
	}
}

// floodSeverity decides whether a clamp's volume is information (the clamp
// doing its job) or a warning (the flood is large enough to distort the
// hour's counts or load the machine). dropped is the clamp's count; live is
// the live figure it is compared with; ratioPct is the share of live above
// which it warns.
func floodSeverity(dropped, live int64, ratioPct int64) string {
	if live <= 0 {
		return SeverityInfo
	}
	if dropped*100 > live*ratioPct {
		return SeverityWarn
	}
	return SeverityInfo
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
		offenderMinutes := offenderWindowMinutes(report.Offenders.Since, latest)

		// Dropped posts mean the write buffer was full and firehose posts
		// were thrown away. One window of it is a spike; two in a row is
		// sustained loss, which is why the previous snapshot is required.
		if previous != nil && latest.DroppedPosts > 0 && previous.DroppedPosts > 0 {
			conds = append(conds, Condition{
				Name:       "dropped_posts",
				Actionable: true,
				Severity:   SeverityError,
				Message: fmt.Sprintf("Write buffer overflowed: %d posts lost this half hour, %d the half hour before (normal 0). Ingest is outrunning SQLite; this hour's counts and sentiment are incomplete. Check slow-flush warnings and WAL size.",
					latest.DroppedPosts, previous.DroppedPosts),
			})
		}

		if latest.CappedPosts > cfg.CappedPostsPerSnapshot {
			// The cap absorbing a spam farm is routine. It is a warning only
			// when the capped volume is a quarter or more of the English posts
			// actually stored, because then the hour's counts are being
			// shaped by the flood rather than merely protected from it.
			conds = append(conds, floodCondition("capped_posts",
				floodSeverity(int64(latest.CappedPosts), int64(latest.EnglishPostsStored), 25),
				fmt.Sprintf("Per-account cap dropped %d posts this half hour (%s) against %d English posts stored. Accounts posting faster than 60/min; excess never counted. Warns only above a quarter of English stored.",
					latest.CappedPosts, versus(previous != nil, prevInt(previous, func(p *store.StatsSnapshot) int { return p.CappedPosts }), int64(cfg.CappedPostsPerSnapshot)), latest.EnglishPostsStored),
				report.Offenders.Capped, offenderMinutes))
		}

		// The denylist doing its job is information, not a problem; it only
		// becomes one at a volume that says the list is matching far more
		// than it was written for.
		if latest.DeniedPosts > cfg.DeniedPostsWarn {
			conds = append(conds, floodCondition("denied_posts",
				floodSeverity(int64(latest.DeniedPosts), int64(latest.TotalFirehosePosts), 300),
				fmt.Sprintf("Denylist dropped %d posts this half hour (%s) against a live firehose of %d. A listed account is flooding hard; warns only above three times the firehose. Check firehose_deny_dids.",
					latest.DeniedPosts, versus(previous != nil, prevInt(previous, func(p *store.StatsSnapshot) int { return p.DeniedPosts }), int64(cfg.DeniedPostsWarn)), latest.TotalFirehosePosts),
				report.Offenders.Denied, offenderMinutes))
		} else if latest.DeniedPosts > 0 {
			conds = append(conds, Condition{
				Name:     "denied_posts",
				Severity: SeverityInfo,
				Message:  fmt.Sprintf("Denylist dropped %d posts this half hour. Working as intended.", latest.DeniedPosts),
			})
		}

		if latest.StalePosts > cfg.StalePostsPerSnapshot {
			conds = append(conds, floodCondition("stale_posts",
				floodSeverity(int64(latest.StalePosts), int64(latest.TotalFirehosePosts), 300),
				fmt.Sprintf("Age filter dropped %d backdated or future-dated posts this half hour (%s) against a live firehose of %d. Imports, replays or Jetstream re-delivery; none entered the window. Normal 60k to 250k; warns only above three times the firehose. If English counts also jumped it was a live flood.",
					latest.StalePosts, versus(previous != nil, prevInt(previous, func(p *store.StatsSnapshot) int { return p.StalePosts }), int64(cfg.StalePostsPerSnapshot)), latest.TotalFirehosePosts),
				report.Offenders.Stale, offenderMinutes))
		}

		if latest.OversizedPosts > cfg.OversizedPosts {
			conds = append(conds, Condition{
				Name:       "oversized_posts",
				Actionable: true,
				Severity:   SeverityWarn,
				Message: fmt.Sprintf("Text-length cap dropped %d posts this half hour (%s; normal 0). A PDS is emitting abnormally large posts, broken or hostile. Check memory and cycle time on this snapshot.",
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
				Message: fmt.Sprintf("Analysis cycle took %ds (threshold %ds; typical 250s at 40k posts, 650s at 105k) on %d posts. Past the analysis interval the next tick is skipped (cycle_overlap_skipped). Check hydration timings and Gemini errors.",
					latest.CycleDurationMs/1000, cfg.CycleSeconds, latest.PostsConsidered),
			})
		}

		if cfg.TotalMemoryMB > 0 && cfg.RSSPct > 0 && latest.RSSBytes > 0 {
			totalBytes := int64(cfg.TotalMemoryMB) * 1024 * 1024
			if latest.RSSBytes*100 > totalBytes*int64(cfg.RSSPct) {
				conds = append(conds, Condition{
					Name:     "rss_high",
					Severity: SeverityWarn,
					Message: fmt.Sprintf("Memory at %dMB, %d%% of the machine's %dMB (threshold %d%%; typical 200 to 370MB). The guard warns at 55%% and cancels the cycle at 70%%. Check cycle_memory_peak events and the window size.",
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
			Message: fmt.Sprintf("Firehose reconnected %d times in the last hour (%s; threshold %d, normal 0). Each resume is by timestamp so little is lost, but churn means gaps. Check 'connection lost' log lines.",
				n, source, cfg.ReconnectsPerHour),
		})
	}

	conds = append(conds, eventConditions(events)...)

	// The snapshot flag covers every path that skips publication, including
	// any that writes no posting_skipped event, so it is the fallback: when
	// the event is in the window its condition (which carries the reason)
	// already stands for the hour. AnalysisRan is reset after every snapshot
	// while PostingSkipped is not, hence both. DRY_RUN does not set the flag:
	// it is the cycle's lowConfidence, and a dry run suppresses posting in
	// the client, not through lowConfidence.
	if latest != nil && latest.AnalysisRan == 1 && latest.PostingSkipped == 1 && !hasEvent(events, "posting_skipped") {
		conds = append(conds, Condition{
			Name: "posting_skipped",
			// window_capped and memory_guard_trip already page for the skip
			// they cause.
			Actionable: !hasEvent(events, "window_capped") && !hasEvent(events, "memory_guard_trip"),
			Severity:   SeverityWarn,
			Message: fmt.Sprintf("The analysis cycle on this snapshot published nothing: %d posts considered, %d hydrated, and no posting_skipped event says why. %s",
				latest.PostsConsidered, latest.PostsHydrated, eventMeaning["posting_skipped"]),
		})
	}
	return conds
}

// hasEvent reports whether an event of eventType is among events.
func hasEvent(events []store.StatsEvent, eventType string) bool {
	for _, e := range events {
		if e.EventType == eventType {
			return true
		}
	}
	return false
}

// detailValue returns the value of key in a space-separated key=value details
// string, or "" when key is absent.
func detailValue(details, key string) string {
	for _, field := range strings.Fields(details) {
		if v, ok := strings.CutPrefix(field, key+"="); ok {
			return v
		}
	}
	return ""
}

// postingSkippedActionable decides whether a skipped publication pages. A
// hydration shortfall pages: only a person can raise the budget. A low post
// count pages unless a stall_detected event in the same window already has.
// window_capped and memory_guard reasons do not, since their own events page
// for the same hour and a second mention would be noise.
func postingSkippedActionable(details string, events []store.StatsEvent) bool {
	switch detailValue(details, "reason") {
	case "hydration_shortfall":
		return true
	case "low_post_count":
		return !hasEvent(events, "stall_detected")
	}
	return false
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
	actionable := make(map[string]bool, len(alertingEvents))
	for _, e := range events {
		if _, ok := alertingEvents[e.EventType]; !ok {
			continue
		}
		counts[e.EventType]++
		// Any one of the window's skips with a paging reason pages.
		if e.EventType == "posting_skipped" && postingSkippedActionable(e.Details, events) {
			actionable[e.EventType] = true
		}
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
			Actionable: actionableEvents[eventType] || actionable[eventType],
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
