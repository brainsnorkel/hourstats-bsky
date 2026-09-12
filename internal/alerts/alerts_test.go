package alerts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// testThresholds is DefaultThresholds with a known machine size, so the RSS
// condition does not depend on the environment the test runs in.
func testThresholds() Thresholds {
	cfg := DefaultThresholds()
	cfg.TotalMemoryMB = 1000
	return cfg
}

// names returns the condition names in order, which is what the table tests
// assert on; the messages carry numbers and are checked separately.
func names(conds []Condition) []string {
	out := make([]string, len(conds))
	for i, c := range conds {
		out[i] = c.Name
	}
	return out
}

func severityOf(conds []Condition, name string) string {
	for _, c := range conds {
		if c.Name == name {
			return c.Severity
		}
	}
	return ""
}

func TestEvaluate_Conditions(t *testing.T) {
	mb := int64(1024 * 1024)

	tests := []struct {
		name         string
		latest       *store.StatsSnapshot
		previous     *store.StatsSnapshot
		events       []store.StatsEvent
		report       ConsumerReport
		wantNames    []string
		wantSeverity string // severity expected for wantNames[0], when set
	}{
		{
			name:      "quiet snapshot raises nothing",
			latest:    &store.StatsSnapshot{},
			previous:  &store.StatsSnapshot{},
			wantNames: nil,
		},
		{
			name:      "no snapshots at all raises nothing",
			wantNames: nil,
		},
		{
			name:         "dropped posts in two consecutive snapshots",
			latest:       &store.StatsSnapshot{DroppedPosts: 3},
			previous:     &store.StatsSnapshot{DroppedPosts: 1},
			wantNames:    []string{"dropped_posts"},
			wantSeverity: SeverityError,
		},
		{
			name:      "dropped posts in only the latest snapshot is a spike",
			latest:    &store.StatsSnapshot{DroppedPosts: 3},
			previous:  &store.StatsSnapshot{},
			wantNames: nil,
		},
		{
			name:      "dropped posts with no previous snapshot cannot be sustained",
			latest:    &store.StatsSnapshot{DroppedPosts: 3},
			wantNames: nil,
		},
		{
			name:         "capped posts over the threshold",
			latest:       &store.StatsSnapshot{CappedPosts: defaultCappedPostsPerSnapshot + 1},
			wantNames:    []string{"capped_posts"},
			wantSeverity: SeverityWarn,
		},
		{
			name:      "capped posts at the threshold",
			latest:    &store.StatsSnapshot{CappedPosts: defaultCappedPostsPerSnapshot},
			wantNames: nil,
		},
		{
			name:         "any denied posts are informational",
			latest:       &store.StatsSnapshot{DeniedPosts: 1},
			wantNames:    []string{"denied_posts"},
			wantSeverity: SeverityInfo,
		},
		{
			name:         "denied posts past the warn threshold",
			latest:       &store.StatsSnapshot{DeniedPosts: defaultDeniedPostsWarn + 1},
			wantNames:    []string{"denied_posts"},
			wantSeverity: SeverityWarn,
		},
		{
			name:         "stale posts over the threshold",
			latest:       &store.StatsSnapshot{StalePosts: defaultStalePostsPerSnapshot + 1},
			wantNames:    []string{"stale_posts"},
			wantSeverity: SeverityWarn,
		},
		{
			name:      "stale posts at prod's routine rate",
			latest:    &store.StatsSnapshot{StalePosts: 200000},
			wantNames: nil,
		},
		{
			name:         "oversized posts over the threshold",
			latest:       &store.StatsSnapshot{OversizedPosts: defaultOversizedPosts + 1},
			wantNames:    []string{"oversized_posts"},
			wantSeverity: SeverityWarn,
		},
		{
			name:         "reconnects summed across both snapshots",
			latest:       &store.StatsSnapshot{ReconnectCount: 2},
			previous:     &store.StatsSnapshot{ReconnectCount: 2},
			wantNames:    []string{"consumer_reconnects"},
			wantSeverity: SeverityWarn,
		},
		{
			name:      "reconnects at the threshold",
			latest:    &store.StatsSnapshot{ReconnectCount: 2},
			previous:  &store.StatsSnapshot{ReconnectCount: 1},
			wantNames: nil,
		},
		{
			name:         "reconnects fall back to the live report with no snapshots",
			report:       ConsumerReport{Reconnects: 9},
			wantNames:    []string{"consumer_reconnects"},
			wantSeverity: SeverityWarn,
		},
		{
			name:      "the live report is ignored once a snapshot exists",
			latest:    &store.StatsSnapshot{},
			report:    ConsumerReport{Reconnects: 9},
			wantNames: nil,
		},
		{
			name:         "cycle duration over the threshold on an analysis snapshot",
			latest:       &store.StatsSnapshot{AnalysisRan: 1, CycleDurationMs: (defaultCycleSeconds + 1) * 1000},
			wantNames:    []string{"cycle_duration"},
			wantSeverity: SeverityWarn,
		},
		{
			name:      "cycle duration on a snapshot that saw no cycle",
			latest:    &store.StatsSnapshot{AnalysisRan: 0, CycleDurationMs: (defaultCycleSeconds + 1) * 1000},
			wantNames: nil,
		},
		{
			name:         "rss over the configured share of the machine",
			latest:       &store.StatsSnapshot{RSSBytes: 601 * mb},
			wantNames:    []string{"rss_high"},
			wantSeverity: SeverityWarn,
		},
		{
			name:      "rss at the configured share of the machine",
			latest:    &store.StatsSnapshot{RSSBytes: 600 * mb},
			wantNames: nil,
		},
		{
			name:         "a memory guard trip is an error",
			latest:       &store.StatsSnapshot{},
			events:       []store.StatsEvent{{EventType: "memory_guard_trip", Details: "rss=1400MB"}},
			wantNames:    []string{"memory_guard_trip"},
			wantSeverity: SeverityError,
		},
		{
			name:   "each alerting event type is reported once",
			latest: &store.StatsSnapshot{},
			events: []store.StatsEvent{
				{EventType: "hydration_timeout", Details: "second"},
				{EventType: "hydration_timeout", Details: "first"},
				{EventType: "window_capped"},
				{EventType: "app_start"},
				{EventType: "cycle_memory_tick"},
			},
			wantNames: []string{"window_capped", "hydration_timeout"},
		},
		{
			name:         "a feature gate outage is visible to alerts",
			latest:       &store.StatsSnapshot{},
			events:       []store.StatsEvent{{EventType: "feature_gate_unavailable", Details: "run_id=run-1 candidates=6"}},
			wantNames:    []string{"feature_gate_unavailable"},
			wantSeverity: SeverityWarn,
		},
		{
			name:      "events that are not alerting events are ignored",
			latest:    &store.StatsSnapshot{},
			events:    []store.StatsEvent{{EventType: "app_start"}, {EventType: "wal_pressure_checkpoint"}},
			wantNames: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.latest, tt.previous, tt.events, tt.report, testThresholds())
			gotNames := names(got)
			if len(gotNames) != len(tt.wantNames) {
				t.Fatalf("conditions = %v, want %v", gotNames, tt.wantNames)
			}
			for i, want := range tt.wantNames {
				if gotNames[i] != want {
					t.Fatalf("conditions = %v, want %v", gotNames, tt.wantNames)
				}
			}
			if tt.wantSeverity != "" {
				if sev := severityOf(got, tt.wantNames[0]); sev != tt.wantSeverity {
					t.Errorf("%s severity = %q, want %q", tt.wantNames[0], sev, tt.wantSeverity)
				}
			}
		})
	}
}

// TestEvaluate_EventMessageCountsAndDetails pins what an operator actually
// reads: how many of the event happened, and the newest one's details.
func TestEvaluate_EventMessageCountsAndDetails(t *testing.T) {
	events := []store.StatsEvent{
		{EventType: "hydration_timeout", Details: "newest"},
		{EventType: "hydration_timeout", Details: "older"},
		{EventType: "hydration_timeout", Details: "oldest"},
	}
	conds := Evaluate(&store.StatsSnapshot{}, nil, events, ConsumerReport{}, testThresholds())
	if len(conds) != 1 {
		t.Fatalf("conditions = %v, want one", names(conds))
	}
	want := "3 hydration_timeout event(s) since the previous snapshot: newest"
	if conds[0].Message != want {
		t.Errorf("message = %q, want %q", conds[0].Message, want)
	}
}

// TestEvaluate_RSSDisabledWithoutAMachineSize covers the case where nothing in
// the environment describes the machine: a percentage of an unknown total is
// not a threshold, so the condition must not fire.
func TestEvaluate_RSSDisabledWithoutAMachineSize(t *testing.T) {
	cfg := testThresholds()
	cfg.TotalMemoryMB = 0
	conds := Evaluate(&store.StatsSnapshot{RSSBytes: 4 << 30}, nil, nil, ConsumerReport{}, cfg)
	if len(conds) != 0 {
		t.Fatalf("conditions = %v, want none", names(conds))
	}
}

func TestStatus(t *testing.T) {
	tests := []struct {
		name  string
		conds []Condition
		want  string
	}{
		{name: "nothing", want: StatusOK},
		{name: "info only", conds: []Condition{{Severity: SeverityInfo}}, want: StatusOK},
		{name: "a warn", conds: []Condition{{Severity: SeverityInfo}, {Severity: SeverityWarn}}, want: StatusWarn},
		{name: "an error wins", conds: []Condition{{Severity: SeverityWarn}, {Severity: SeverityError}}, want: StatusError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Status(tt.conds); got != tt.want {
				t.Errorf("Status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestThresholdsFromEnv(t *testing.T) {
	t.Setenv("ALERT_CAPPED_POSTS_PER_SNAPSHOT", "7")
	t.Setenv("ALERT_STALE_POSTS_PER_SNAPSHOT", "1")
	t.Setenv("ALERT_RECONNECTS_PER_HOUR", "0")
	t.Setenv("ALERT_CYCLE_SECONDS", "60")
	t.Setenv("ALERT_RSS_PCT", "80")
	t.Setenv("FLY_VM_MEMORY_MB", "2048")
	t.Setenv("MEMORY_GUARD_TOTAL_MB", "512")

	cfg := ThresholdsFromEnv()
	if cfg.CappedPostsPerSnapshot != 7 || cfg.StalePostsPerSnapshot != 1 {
		t.Errorf("drop thresholds = %d/%d, want 7/1", cfg.CappedPostsPerSnapshot, cfg.StalePostsPerSnapshot)
	}
	if cfg.ReconnectsPerHour != 0 {
		t.Errorf("ReconnectsPerHour = %d, want 0 (zero is a valid threshold, not an unset value)", cfg.ReconnectsPerHour)
	}
	if cfg.CycleSeconds != 60 || cfg.RSSPct != 80 {
		t.Errorf("cycle/rss = %d/%d, want 60/80", cfg.CycleSeconds, cfg.RSSPct)
	}
	// FLY_VM_MEMORY_MB wins: it is the one source that follows a resize.
	if cfg.TotalMemoryMB != 2048 {
		t.Errorf("TotalMemoryMB = %d, want 2048", cfg.TotalMemoryMB)
	}
}

func TestThresholdsFromEnv_GarbageFallsBackToDefaults(t *testing.T) {
	t.Setenv("ALERT_CYCLE_SECONDS", "soon")
	t.Setenv("ALERT_RSS_PCT", "-5")
	cfg := ThresholdsFromEnv()
	if cfg.CycleSeconds != defaultCycleSeconds {
		t.Errorf("CycleSeconds = %d, want %d", cfg.CycleSeconds, defaultCycleSeconds)
	}
	if cfg.RSSPct != defaultRSSPct {
		t.Errorf("RSSPct = %d, want %d", cfg.RSSPct, defaultRSSPct)
	}
}

// fakeSource serves the two reads Run makes.
type fakeSource struct {
	snaps     []store.StatsSnapshot
	snapsErr  error
	events    []store.StatsEvent
	eventsErr error

	snapsSince  time.Time
	eventsSince time.Time
}

func (f *fakeSource) GetSnapshotHistory(_ context.Context, since time.Time, _ int) ([]store.StatsSnapshot, error) {
	f.snapsSince = since
	return f.snaps, f.snapsErr
}

func (f *fakeSource) GetEvents(_ context.Context, since time.Time, _ string, _ int) ([]store.StatsEvent, error) {
	f.eventsSince = since
	return f.events, f.eventsErr
}

func TestRun_EvaluatesAndRecords(t *testing.T) {
	previousAt := time.Now().UTC().Add(-30 * time.Minute)
	src := &fakeSource{
		snaps: []store.StatsSnapshot{
			{SnapshotTime: time.Now().UTC(), DroppedPosts: 5},
			{SnapshotTime: previousAt, DroppedPosts: 2},
		},
		events: []store.StatsEvent{{EventType: "window_capped", Details: "kept=40000"}},
	}
	state := NewState()
	n := NewNotifier("staging", "", nil)

	conds := Run(context.Background(), src, ConsumerReport{}, testThresholds(), state, n)
	if got := names(conds); len(got) != 2 || got[0] != "dropped_posts" || got[1] != "window_capped" {
		t.Fatalf("conditions = %v, want [dropped_posts window_capped]", got)
	}
	// Events are read from the previous snapshot's time, which is the window
	// the latest snapshot covers.
	if !src.eventsSince.Equal(previousAt) {
		t.Errorf("events read since %v, want the previous snapshot time %v", src.eventsSince, previousAt)
	}
	status, active := state.Snapshot()
	if status != StatusError {
		t.Errorf("status = %q, want %q", status, StatusError)
	}
	if len(active) != 2 {
		t.Fatalf("active = %v, want two", active)
	}
}

func TestRun_SnapshotReadFailureIsSwallowed(t *testing.T) {
	src := &fakeSource{snapsErr: errors.New("database is locked")}
	state := NewState()
	if conds := Run(context.Background(), src, ConsumerReport{}, testThresholds(), state, NewNotifier("staging", "", nil)); conds != nil {
		t.Fatalf("conditions = %v, want none", names(conds))
	}
	if status, _ := state.Snapshot(); status != StatusOK {
		t.Errorf("status = %q, want %q (a failed read must not latch an alert)", status, StatusOK)
	}
}

func TestRun_EventReadFailureStillEvaluatesSnapshots(t *testing.T) {
	src := &fakeSource{
		snaps: []store.StatsSnapshot{
			{SnapshotTime: time.Now().UTC(), CappedPosts: defaultCappedPostsPerSnapshot + 1},
		},
		eventsErr: errors.New("database is locked"),
	}
	conds := Run(context.Background(), src, ConsumerReport{}, testThresholds(), NewState(), NewNotifier("staging", "", nil))
	if got := names(conds); len(got) != 1 || got[0] != "capped_posts" {
		t.Fatalf("conditions = %v, want [capped_posts]", got)
	}
}

func TestState_SinceIsStableAcrossEvaluationsAndResetsOnRecurrence(t *testing.T) {
	state := NewState()
	first := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	cond := Condition{Name: "capped_posts", Severity: SeverityWarn, Message: "6000"}

	state.Set([]Condition{cond}, first)
	state.Set([]Condition{{Name: "capped_posts", Severity: SeverityWarn, Message: "7000"}}, first.Add(30*time.Minute))
	_, active := state.Snapshot()
	if len(active) != 1 || !active[0].Since.Equal(first) {
		t.Fatalf("since = %v, want the first evaluation %v", active, first)
	}
	if active[0].Message != "7000" {
		t.Errorf("message = %q, want the latest evaluation's", active[0].Message)
	}

	// Clearing and recurring restarts the clock: a fresh occurrence is news.
	state.Set(nil, first.Add(time.Hour))
	if status, got := state.Snapshot(); status != StatusOK || len(got) != 0 {
		t.Fatalf("status/active = %q/%v, want ok and empty", status, got)
	}
	again := first.Add(2 * time.Hour)
	state.Set([]Condition{cond}, again)
	_, active = state.Snapshot()
	if len(active) != 1 || !active[0].Since.Equal(again) {
		t.Fatalf("since = %v, want the recurrence %v", active, again)
	}
}

func TestState_NilIsUsable(t *testing.T) {
	var state *State
	state.Set([]Condition{{Name: "x", Severity: SeverityError}}, time.Now())
	status, active := state.Snapshot()
	if status != StatusOK || active != nil {
		t.Errorf("nil state = %q/%v, want ok/nil", status, active)
	}
}
