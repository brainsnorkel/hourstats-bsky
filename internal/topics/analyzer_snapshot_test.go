package topics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// twoWindowStore holds snapshots from two different cycles, the situation that
// used to let a failed cycle republish the previous window's topics.
func twoWindowStore() *mockAnalyzerStore {
	return &mockAnalyzerStore{
		snapshots: []store.TopicSnapshotRow{
			{SnapshotTime: "2026-01-01T00:00:00Z", Rank: 1, TopicID: "old", Label: "Yesterday News", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 10},
			{SnapshotTime: "2026-01-01T06:00:00Z", Rank: 1, TopicID: "new", Label: "Current News", Keywords: "[]", Synonyms: "[]", UniqueAuthorCount: 15},
		},
	}
}

func analyzerFor(ms *mockAnalyzerStore) *Analyzer {
	return &Analyzer{
		store:    ms,
		grouper:  NewGrouper("test", "", ""),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}
}

// TestRunTrendingPost_EmptySnapshotTimeSuppresses is the regression case: the
// analysis step produced nothing this cycle, so nothing may be posted. Before,
// RunTrendingPost re-read the newest snapshot within 24h and republished it.
func TestRunTrendingPost_EmptySnapshotTimeSuppresses(t *testing.T) {
	ms := twoWindowStore()
	poster := &mockPoster{}

	err := analyzerFor(ms).RunTrendingPost(context.Background(), poster, false, "", "", "", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if poster.posted || poster.postedReply {
		t.Error("published a post with no snapshot from this cycle, want suppression")
	}
}

// TestRunTrendingPost_UsesGivenSnapshotNotLatest pins publication to the
// snapshot the caller names, even when a newer one exists in the table.
func TestRunTrendingPost_UsesGivenSnapshotNotLatest(t *testing.T) {
	ms := twoWindowStore()
	poster := &mockPoster{}

	err := analyzerFor(ms).RunTrendingPost(context.Background(), poster, false, "2026-01-01T00:00:00Z", "", "", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !poster.posted {
		t.Fatal("expected the named snapshot to be published")
	}
	if !strings.Contains(poster.text, "Yesterday News") {
		t.Errorf("published text = %q, want the named snapshot's topic", poster.text)
	}
	if strings.Contains(poster.text, "Current News") {
		t.Errorf("published text = %q, want only the named snapshot's topics", poster.text)
	}
}

// TestRunTrendingPost_UnknownSnapshotTimeSuppresses covers a snapshot time that
// matches no rows (e.g. it was purged): publish nothing rather than fall back.
func TestRunTrendingPost_UnknownSnapshotTimeSuppresses(t *testing.T) {
	ms := twoWindowStore()
	poster := &mockPoster{}

	err := analyzerFor(ms).RunTrendingPost(context.Background(), poster, false, "2026-01-01T12:00:00Z", "", "", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if poster.posted || poster.postedReply {
		t.Error("published a post for a snapshot time with no rows, want suppression")
	}
}

// TestRunAnalysisCycle_NoTermsIsError checks that a genuine pipeline failure
// surfaces as ErrTopicsUnavailable rather than a silent nil, which used to let
// the caller go on and republish the previous window.
func TestRunAnalysisCycle_NoTermsIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Every token row is identical, so TF-IDF finds nothing that distinguishes
	// one document from another and yields no significant terms.
	b, _ := json.Marshal([]string{"same"})
	var rows []store.TopicTokenRow
	for i := 0; i < 200; i++ {
		rows = append(rows, store.TopicTokenRow{
			PostURI: "at://a/x", Tokens: string(b), CreatedAt: "2026-01-01T00:00:00Z", AuthorDID: "did:plc:a",
		})
	}
	ms := &mockAnalyzerStore{tokens: rows, tokenCount: int64(len(rows))}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouperWithEndpoint("test-key", srv.URL),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}

	snapshotTime, err := a.RunAnalysisCycle(context.Background())
	if err == nil {
		t.Fatal("expected an error when no topics could be produced, got nil")
	}
	if !errors.Is(err, ErrTopicsUnavailable) {
		t.Errorf("err = %v, want it to wrap ErrTopicsUnavailable", err)
	}
	if snapshotTime != "" {
		t.Errorf("snapshot time = %q, want empty", snapshotTime)
	}
	if len(ms.insertedSnapshots) != 0 {
		t.Error("expected no snapshots to be inserted on failure")
	}
}

// TestRunAnalysisCycle_OfflineFallbackStillProducesSnapshot guards the opt-in
// TOPICS_OFFLINE_FALLBACK path: with it on, Gemini being down is not, on its
// own, a reason to suppress. The offline co-occurrence fallback must still
// yield a snapshot the caller can publish as current.
func TestRunAnalysisCycle_OfflineFallbackStillProducesSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tokens := buildTestTokens()
	ms := &mockAnalyzerStore{tokens: tokens, tokenCount: int64(len(tokens))}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouperWithEndpoint("test-key", srv.URL),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
		// TOPICS_OFFLINE_FALLBACK=true: the pre-2026-09-28 behaviour.
		offlineFallback: true,
	}

	snapshotTime, err := a.RunAnalysisCycle(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshotTime == "" {
		t.Fatal("expected a snapshot time from the offline fallback")
	}
	if len(ms.insertedSnapshots) == 0 {
		t.Fatal("expected snapshots to be inserted by the offline fallback")
	}
	if got := ms.insertedSnapshots[0].SnapshotTime; got != snapshotTime {
		t.Errorf("returned snapshot time = %q, want the inserted one %q", snapshotTime, got)
	}
}

type recordedEvent struct{ eventType, details string }

// TestRunAnalysisCycle_GroupingFailureSuppressesByDefault is hs-9uj: with the
// offline fallback off (the default), a grouping failure writes no snapshot,
// so no trending reply and no top topic, and records grouping_failed.
func TestRunAnalysisCycle_GroupingFailureSuppressesByDefault(t *testing.T) {
	fail := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	primary := httptest.NewServer(fail)
	defer primary.Close()
	fallback := httptest.NewServer(fail)
	defer fallback.Close()

	tokens := buildTestTokens()
	ms := &mockAnalyzerStore{tokens: tokens, tokenCount: int64(len(tokens))}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouperWithEndpoints("test-key", primary.URL, fallback.URL),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}
	var events []recordedEvent
	a.SetEventLogger(func(_ context.Context, eventType, details string) {
		events = append(events, recordedEvent{eventType, details})
	})

	snapshotTime, err := a.RunAnalysisCycle(context.Background())
	if !errors.Is(err, ErrTopicsUnavailable) {
		t.Fatalf("err = %v, want ErrTopicsUnavailable", err)
	}
	if snapshotTime != "" || len(ms.insertedSnapshots) != 0 {
		t.Errorf("snapshot time = %q, inserted = %d, want no snapshot", snapshotTime, len(ms.insertedSnapshots))
	}
	if len(events) != 1 || events[0].eventType != "grouping_failed" {
		t.Fatalf("events = %+v, want one grouping_failed", events)
	}
	d := events[0].details
	if !strings.HasPrefix(d, "attempts=2 primary=") || !strings.Contains(d, ":status_5xx:") || !strings.Contains(d, " fallback=") {
		t.Errorf("details = %q, want both attempts classed status_5xx", d)
	}
}

// TestRunAnalysisCycle_FallbackModelSuccessIsRecorded covers the
// grouping_fallback_model event: the hour was served, but by the second tier.
func TestRunAnalysisCycle_FallbackModelSuccessIsRecorded(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(geminiMockHandler([]TopicCluster{
		{Label: "US Election", Description: "d", Keywords: []string{"trump", "election"}, Justification: "j"},
	}))
	defer fallback.Close()

	tokens := buildTestTokens()
	ms := &mockAnalyzerStore{tokens: tokens, tokenCount: int64(len(tokens))}
	a := &Analyzer{
		store:    ms,
		grouper:  NewGrouperWithEndpoints("test-key", primary.URL, fallback.URL),
		tracker:  NewTracker(ms),
		hydrator: NewExemplarHydrator(ms),
	}
	var events []recordedEvent
	a.SetEventLogger(func(_ context.Context, eventType, details string) {
		events = append(events, recordedEvent{eventType, details})
	})

	if _, err := a.RunAnalysisCycle(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].eventType != "grouping_fallback_model" {
		t.Fatalf("events = %+v, want one grouping_fallback_model", events)
	}
	if d := events[0].details; !strings.Contains(d, ":status_429:") || !strings.Contains(d, ":ok:") || strings.Contains(d, "US Election") {
		t.Errorf("details = %q, want a 429 primary, an ok fallback and no label", d)
	}
}
