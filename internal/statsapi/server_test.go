package statsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/alerts"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

type mockStore struct {
	latestSnap    *store.StatsSnapshot
	latestSnapErr error
	history       []store.StatsSnapshot
	historyErr    error
	events        []store.StatsEvent
	eventsErr     error
	topics        []store.TopicSnapshotRow
	topicsErr     error
	postingAct    *store.PostingActivity
	postingErr    error
	dbHealth      *store.DatabaseHealth
	dbHealthErr   error
}

func (m *mockStore) GetLatestSnapshot(_ context.Context) (*store.StatsSnapshot, error) {
	return m.latestSnap, m.latestSnapErr
}
func (m *mockStore) GetSnapshotHistory(_ context.Context, _ time.Time, _ int) ([]store.StatsSnapshot, error) {
	return m.history, m.historyErr
}
func (m *mockStore) GetEvents(_ context.Context, _ time.Time, _ string, _ int) ([]store.StatsEvent, error) {
	return m.events, m.eventsErr
}
func (m *mockStore) GetRecentTopicSnapshots(_ context.Context, _ string, _ int) ([]store.TopicSnapshotRow, error) {
	return m.topics, m.topicsErr
}
func (m *mockStore) GetPostingActivity(_ context.Context) (*store.PostingActivity, error) {
	return m.postingAct, m.postingErr
}
func (m *mockStore) GetDatabaseHealth(_ context.Context) (*store.DatabaseHealth, error) {
	return m.dbHealth, m.dbHealthErr
}
func (m *mockStore) GetHealthHistory(_ context.Context, _ time.Time, _ int) ([]store.StatsSnapshot, error) {
	return m.history, m.historyErr
}

func newTestServer(ms *mockStore) *Server {
	return New(ms, 0, HealthChartConfig{})
}

func doRequest(s *Server, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rr, req)
	return rr
}

func TestLatest_OK(t *testing.T) {
	snap := &store.StatsSnapshot{
		ID:                 1,
		SnapshotTime:       time.Now().UTC(),
		ActiveEndpoint:     "wss://test",
		EnglishPostsStored: 42,
	}
	s := newTestServer(&mockStore{latestSnap: snap})
	rr := doRequest(s, "GET", "/stats/latest")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	var got store.StatsSnapshot
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.EnglishPostsStored != 42 {
		t.Errorf("EnglishPostsStored = %d, want 42", got.EnglishPostsStored)
	}
}

// TestLatest_ExposesFirehoseAndRSSFields pins the JSON keys the CLI and any
// external dashboard read for firehose reconstruction and RSS accounting.
func TestLatest_ExposesFirehoseAndRSSFields(t *testing.T) {
	snap := &store.StatsSnapshot{
		ID:                      1,
		SnapshotTime:            time.Now().UTC(),
		ActiveEndpoint:          "wss://test",
		TotalFirehosePosts:      3308,
		EarlyRejectedNonEnglish: 71000,
		RSSBytes:                1024 * 1024 * 588,
		HeapReleasedBytes:       1024 * 1024 * 37,
		StackInuseBytes:         1024 * 1024 * 3,
	}
	s := newTestServer(&mockStore{latestSnap: snap})
	rr := doRequest(s, "GET", "/stats/latest")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]float64{
		"total_firehose_posts":       3308,
		"early_rejected_non_english": 71000,
		"rss_bytes":                  1024 * 1024 * 588,
		"heap_released_bytes":        1024 * 1024 * 37,
		"stack_inuse_bytes":          1024 * 1024 * 3,
	}
	for key, wantVal := range want {
		got, ok := body[key]
		if !ok {
			t.Errorf("response is missing %q", key)
			continue
		}
		if got != wantVal {
			t.Errorf("%s = %v, want %v", key, got, wantVal)
		}
	}
}

func TestLatest_NotFound(t *testing.T) {
	s := newTestServer(&mockStore{latestSnap: nil})
	rr := doRequest(s, "GET", "/stats/latest")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestLatest_StoreError(t *testing.T) {
	s := newTestServer(&mockStore{latestSnapErr: errors.New("db error")})
	rr := doRequest(s, "GET", "/stats/latest")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestHistory_Defaults(t *testing.T) {
	s := newTestServer(&mockStore{history: []store.StatsSnapshot{{ID: 1}, {ID: 2}}})
	rr := doRequest(s, "GET", "/stats/history")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var got []store.StatsSnapshot
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestHistory_EmptyReturnsArray(t *testing.T) {
	s := newTestServer(&mockStore{history: nil})
	rr := doRequest(s, "GET", "/stats/history")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	body := rr.Body.String()
	if body == "null\n" {
		t.Error("empty history should return [], not null")
	}
}

func TestHistory_InvalidHours(t *testing.T) {
	s := newTestServer(&mockStore{})
	rr := doRequest(s, "GET", "/stats/history?hours=-1")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHistory_InvalidLimit(t *testing.T) {
	s := newTestServer(&mockStore{})
	rr := doRequest(s, "GET", "/stats/history?limit=9999")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHistory_NonIntHours(t *testing.T) {
	s := newTestServer(&mockStore{})
	rr := doRequest(s, "GET", "/stats/history?hours=abc")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestEvents_OK(t *testing.T) {
	evts := []store.StatsEvent{
		{ID: 1, EventType: "app_start", Details: "ok"},
		{ID: 2, EventType: "connection_drop", Details: "timeout"},
	}
	s := newTestServer(&mockStore{events: evts})
	rr := doRequest(s, "GET", "/stats/events")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var got []store.StatsEvent
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestEvents_EmptyReturnsArray(t *testing.T) {
	s := newTestServer(&mockStore{events: nil})
	rr := doRequest(s, "GET", "/stats/events")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if body == "null\n" {
		t.Error("empty events should return [], not null")
	}
}

func TestEvents_WithTypeFilter(t *testing.T) {
	s := newTestServer(&mockStore{events: []store.StatsEvent{{ID: 1}}})
	rr := doRequest(s, "GET", "/stats/events?type=app_start")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestEvents_InvalidLimit(t *testing.T) {
	s := newTestServer(&mockStore{})
	rr := doRequest(s, "GET", "/stats/events?limit=0")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestTopics_OK(t *testing.T) {
	topics := []store.TopicSnapshotRow{
		{
			ID: 1, Rank: 1, Label: "AI", Description: "Artificial Intelligence",
			UniqueAuthorCount: 100, Keywords: `["ai","ml","gpt"]`,
			ExemplarURI: "at://did:plc:abc/app.bsky.feed.post/123",
		},
	}
	s := newTestServer(&mockStore{topics: topics})
	rr := doRequest(s, "GET", "/stats/topics")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var got []map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0]["label"] != "AI" {
		t.Errorf("label = %v", got[0]["label"])
	}
}

func TestTopics_InvalidKeywordsReturnEmptyArray(t *testing.T) {
	topics := []store.TopicSnapshotRow{
		{ID: 1, Keywords: "not-valid-json"},
	}
	s := newTestServer(&mockStore{topics: topics})
	rr := doRequest(s, "GET", "/stats/topics")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var got []map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	kw, ok := got[0]["keywords"].([]interface{})
	if !ok {
		t.Fatalf("keywords is not array: %T", got[0]["keywords"])
	}
	if len(kw) != 0 {
		t.Errorf("invalid keywords should become empty array, got %v", kw)
	}
}

func TestTopics_LimitValidation(t *testing.T) {
	s := newTestServer(&mockStore{})
	rr := doRequest(s, "GET", "/stats/topics?limit=999")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d for limit > 500", rr.Code, http.StatusBadRequest)
	}
}

func TestPosting_OK(t *testing.T) {
	pa := &store.PostingActivity{
		SentimentSummary: &store.PostingEntry{
			LastPosted: "2026-02-16T00:00:00Z",
			Summary:    "positive (60.5%), 1500 posts",
		},
	}
	s := newTestServer(&mockStore{postingAct: pa})
	rr := doRequest(s, "GET", "/stats/posting")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var got store.PostingActivity
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SentimentSummary == nil {
		t.Fatal("SentimentSummary is nil")
	}
	if got.SentimentSummary.Summary != pa.SentimentSummary.Summary {
		t.Errorf("Summary = %q", got.SentimentSummary.Summary)
	}
}

func TestPosting_StoreError(t *testing.T) {
	s := newTestServer(&mockStore{postingErr: errors.New("db err")})
	rr := doRequest(s, "GET", "/stats/posting")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

func TestHealth_OK(t *testing.T) {
	health := &store.DatabaseHealth{
		DBSizeBytes:  1024 * 1024,
		WALSizeBytes: 512,
		CheckedAt:    time.Now().UTC(),
	}
	s := newTestServer(&mockStore{dbHealth: health})
	rr := doRequest(s, "GET", "/stats/health")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var got store.DatabaseHealth
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DBSizeBytes != 1024*1024 {
		t.Errorf("DBSizeBytes = %d", got.DBSizeBytes)
	}
}

// TestHealth_AlertsFromTheLastEvaluation covers the two fields the alert path
// adds, alongside the database fields that were there before it.
func TestHealth_AlertsFromTheLastEvaluation(t *testing.T) {
	s := newTestServer(&mockStore{dbHealth: &store.DatabaseHealth{DBSizeBytes: 4096}})

	state := alerts.NewState()
	since := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	state.Set([]alerts.Condition{
		{Name: "capped_posts", Severity: alerts.SeverityWarn, Message: "rate cap dropped 6000 posts"},
		{Name: "memory_guard_trip", Severity: alerts.SeverityError, Message: "1 memory_guard_trip event(s)"},
	}, since)
	s.SetAlertState(state)

	rr := doRequest(s, "GET", "/stats/health")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var got struct {
		DBSizeBytes int64           `json:"db_size_bytes"`
		Status      string          `json:"status"`
		Alerts      []alerts.Active `json:"alerts"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DBSizeBytes != 4096 {
		t.Errorf("db_size_bytes = %d, want the existing field to survive", got.DBSizeBytes)
	}
	if got.Status != alerts.StatusError {
		t.Errorf("status = %q, want %q", got.Status, alerts.StatusError)
	}
	if len(got.Alerts) != 2 {
		t.Fatalf("alerts = %v, want two", got.Alerts)
	}
	if got.Alerts[0].Name != "capped_posts" || got.Alerts[0].Severity != alerts.SeverityWarn {
		t.Errorf("alerts[0] = %+v", got.Alerts[0])
	}
	if !got.Alerts[0].Since.Equal(since) {
		t.Errorf("alerts[0].since = %v, want %v", got.Alerts[0].Since, since)
	}
	if got.Alerts[1].Message != "1 memory_guard_trip event(s)" {
		t.Errorf("alerts[1].message = %q", got.Alerts[1].Message)
	}
}

// TestHealth_NoEvaluationYet covers the window between startup and the first
// snapshot: status is ok and alerts is an empty array, never null.
func TestHealth_NoEvaluationYet(t *testing.T) {
	s := newTestServer(&mockStore{dbHealth: &store.DatabaseHealth{DBSizeBytes: 1}})
	rr := doRequest(s, "GET", "/stats/health")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("body = %s, want status ok", body)
	}
	if !strings.Contains(body, `"alerts":[]`) {
		t.Errorf("body = %s, want an empty alerts array", body)
	}
}

func TestHealth_StoreError(t *testing.T) {
	s := newTestServer(&mockStore{dbHealthErr: errors.New("db err")})
	rr := doRequest(s, "GET", "/stats/health")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

// TestBindAddrs pins the listener layout: loopback always, the Fly private
// address as a second listener, and never a wildcard bind.
func TestBindAddrs(t *testing.T) {
	t.Setenv("STATS_API_BIND", "")
	t.Setenv("FLY_PRIVATE_IP", "")

	local, private := bindAddrs(9111)
	if local != "127.0.0.1:9111" {
		t.Errorf("local = %q, want 127.0.0.1:9111", local)
	}
	if private != "" {
		t.Errorf("private = %q, want empty off Fly", private)
	}

	t.Setenv("FLY_PRIVATE_IP", "fdaa:0:1234::3")
	local, private = bindAddrs(9111)
	if local != "127.0.0.1:9111" {
		t.Errorf("local = %q, want loopback listener on Fly too", local)
	}
	if private != "[fdaa:0:1234::3]:9111" {
		t.Errorf("private = %q, want bracketed IPv6", private)
	}

	t.Setenv("STATS_API_BIND", "127.0.0.1:0")
	local, private = bindAddrs(9111)
	if local != "127.0.0.1:0" {
		t.Errorf("local = %q, STATS_API_BIND should win", local)
	}
	if private != "" {
		t.Errorf("private = %q, STATS_API_BIND should be the only listener", private)
	}
}

// A FLY_PRIVATE_IP that is not an IP address would be concatenated into an
// address that cannot be bound, which now fails Start outright. It is skipped
// instead, so a misconfigured environment still gets the loopback listener.
func TestBindAddrs_MalformedPrivateIP(t *testing.T) {
	t.Setenv("STATS_API_BIND", "")

	// "fdaa:0:1234::3:9111" is deliberately absent: it parses as an ordinary
	// IPv6 address, so there is nothing to reject.
	for _, bad := range []string{"not-an-ip", "127.0.0.1:9111", "fdaa:0:1234::3%eth0", " "} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("FLY_PRIVATE_IP", bad)
			local, private := bindAddrs(9111)
			if local != "127.0.0.1:9111" {
				t.Errorf("local = %q, want the loopback listener to survive", local)
			}
			if private != "" {
				t.Errorf("private = %q, want no second listener for %q", private, bad)
			}
		})
	}
}

// Start binds up front, so an address already in use is returned to the caller
// rather than logged from inside a goroutine and forgotten.
func TestStart_ReportsABindFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()

	t.Setenv("STATS_API_BIND", ln.Addr().String())
	t.Setenv("FLY_PRIVATE_IP", "")

	s := New(&mockStore{}, 9111, HealthChartConfig{})
	if err := s.Start(); err == nil {
		_ = s.Shutdown(context.Background())
		t.Fatal("Start() error = nil, want an error for an address already in use")
	}
}

func TestNew_TimeoutsAndLoopbackBind(t *testing.T) {
	t.Setenv("STATS_API_BIND", "")
	t.Setenv("FLY_PRIVATE_IP", "fdaa:0:1234::3")

	s := New(&mockStore{}, 9111, HealthChartConfig{})
	if s.privServer == nil {
		t.Fatal("privServer is nil with FLY_PRIVATE_IP set")
	}
	for _, srv := range []*http.Server{s.server, s.privServer} {
		if srv.ReadHeaderTimeout != 5*time.Second {
			t.Errorf("%s ReadHeaderTimeout = %v, want 5s", srv.Addr, srv.ReadHeaderTimeout)
		}
		if srv.ReadTimeout != 15*time.Second {
			t.Errorf("%s ReadTimeout = %v, want 15s", srv.Addr, srv.ReadTimeout)
		}
		if srv.WriteTimeout != 30*time.Second {
			t.Errorf("%s WriteTimeout = %v, want 30s", srv.Addr, srv.WriteTimeout)
		}
		if srv.IdleTimeout != 60*time.Second {
			t.Errorf("%s IdleTimeout = %v, want 60s", srv.Addr, srv.IdleTimeout)
		}
		if srv.MaxHeaderBytes != 64<<10 {
			t.Errorf("%s MaxHeaderBytes = %d, want %d", srv.Addr, srv.MaxHeaderBytes, 64<<10)
		}
	}
}

// TestStart_ServesOnLoopback starts the real listener and makes a request
// through it, so a broken Addr is caught rather than only the handler wiring.
func TestStart_ServesOnLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	t.Setenv("STATS_API_BIND", addr)
	t.Setenv("FLY_PRIVATE_IP", "")

	snap := &store.StatsSnapshot{ID: 1, EnglishPostsStored: 7}
	s := New(&mockStore{latestSnap: snap}, 9111, HealthChartConfig{})
	if s.server.Addr != addr {
		t.Fatalf("Addr = %q, want %q", s.server.Addr, addr)
	}
	if s.privServer != nil {
		t.Fatal("privServer should be nil when STATS_API_BIND is set")
	}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get("http://" + addr + "/stats/latest")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET /stats/latest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got store.StatsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.EnglishPostsStored != 7 {
		t.Errorf("EnglishPostsStored = %d, want 7", got.EnglishPostsStored)
	}
}
