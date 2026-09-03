package statsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestHealth_StoreError(t *testing.T) {
	s := newTestServer(&mockStore{dbHealthErr: errors.New("db err")})
	rr := doRequest(s, "GET", "/stats/health")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}
