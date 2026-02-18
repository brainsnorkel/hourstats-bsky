package statsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// StatsStore defines the interface for accessing stats data.
// This decouples the API server from the concrete store implementation.
type StatsStore interface {
	GetLatestSnapshot(ctx context.Context) (*store.StatsSnapshot, error)
	GetSnapshotHistory(ctx context.Context, since time.Time, limit int) ([]store.StatsSnapshot, error)
	GetHealthHistory(ctx context.Context, since time.Time, limit int) ([]store.StatsSnapshot, error)
	GetEvents(ctx context.Context, since time.Time, eventType string, limit int) ([]store.StatsEvent, error)
	GetRecentTopicSnapshots(ctx context.Context, since string, limit int) ([]store.TopicSnapshotRow, error)
	GetPostingActivity(ctx context.Context) (*store.PostingActivity, error)
	GetDatabaseHealth(ctx context.Context) (*store.DatabaseHealth, error)
}

// HealthChartConfig holds configuration for the health chart endpoint.
type HealthChartConfig struct {
	Hours         int
	MemoryLimitMB int
}

// Server provides an HTTP API for querying stats.
type Server struct {
	store       StatsStore
	port        int
	server      *http.Server
	healthChart HealthChartConfig
}

// New creates a new stats API server.
func New(store StatsStore, port int, healthCfg HealthChartConfig) *Server {
	s := &Server{store: store, port: port, healthChart: healthCfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stats/latest", s.handleLatest)
	mux.HandleFunc("GET /stats/history", s.handleHistory)
	mux.HandleFunc("GET /stats/events", s.handleEvents)
	mux.HandleFunc("GET /stats/topics", s.handleTopics)
	mux.HandleFunc("GET /stats/posting", s.handlePosting)
	mux.HandleFunc("GET /stats/health", s.handleHealth)
	mux.HandleFunc("GET /stats/health/history", s.handleHealthHistory)
	mux.HandleFunc("GET /stats/health/chart", s.handleHealthChart)
	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}
	return s
}

// Start begins serving HTTP requests in a background goroutine.
func (s *Server) Start() error {
	slog.Info("starting stats API server", "port", s.port)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("stats API server error", "error", err)
		}
	}()
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("shutting down stats API server")
	return s.server.Shutdown(ctx)
}

// handleLatest returns the most recent stats snapshot.
// GET /stats/latest
func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snapshot, err := s.store.GetLatestSnapshot(ctx)
	if err != nil {
		slog.Error("failed to get latest snapshot", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if snapshot == nil {
		writeError(w, http.StatusNotFound, "no snapshots yet")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// handleHistory returns historical snapshots.
// GET /stats/history?hours=24&limit=48
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse hours parameter (default: 24)
	hoursStr := r.URL.Query().Get("hours")
	hours := 24
	if hoursStr != "" {
		var err error
		hours, err = strconv.Atoi(hoursStr)
		if err != nil || hours <= 0 {
			writeError(w, http.StatusBadRequest, "hours must be a positive integer")
			return
		}
	}

	// Parse limit parameter (default: 48, max: 1000)
	limitStr := r.URL.Query().Get("limit")
	limit := 48
	if limitStr != "" {
		var err error
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 || limit > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer <= 1000")
			return
		}
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	snapshots, err := s.store.GetSnapshotHistory(ctx, since, limit)
	if err != nil {
		slog.Error("failed to get snapshot history", "error", err, "hours", hours, "limit", limit)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Return empty array if no results (never null)
	if snapshots == nil {
		snapshots = []store.StatsSnapshot{}
	}
	writeJSON(w, http.StatusOK, snapshots)
}

// handleEvents returns recent events.
// GET /stats/events?hours=24&type=&limit=100
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse hours parameter (default: 24)
	hoursStr := r.URL.Query().Get("hours")
	hours := 24
	if hoursStr != "" {
		var err error
		hours, err = strconv.Atoi(hoursStr)
		if err != nil || hours <= 0 {
			writeError(w, http.StatusBadRequest, "hours must be a positive integer")
			return
		}
	}

	// Parse type parameter (default: empty = all types)
	eventType := r.URL.Query().Get("type")

	// Parse limit parameter (default: 100, max: 1000)
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		var err error
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 || limit > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer <= 1000")
			return
		}
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	events, err := s.store.GetEvents(ctx, since, eventType, limit)
	if err != nil {
		slog.Error("failed to get events", "error", err, "hours", hours, "type", eventType, "limit", limit)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Return empty array if no results (never null)
	if events == nil {
		events = []store.StatsEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

// handleTopics returns recent topic snapshots.
// GET /stats/topics?hours=24&limit=50
func (s *Server) handleTopics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse hours parameter (default: 24)
	hoursStr := r.URL.Query().Get("hours")
	hours := 24
	if hoursStr != "" {
		var err error
		hours, err = strconv.Atoi(hoursStr)
		if err != nil || hours <= 0 {
			writeError(w, http.StatusBadRequest, "hours must be a positive integer")
			return
		}
	}

	// Parse limit parameter (default: 50, max: 500)
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		var err error
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 || limit > 500 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer <= 500")
			return
		}
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour).Format(time.RFC3339)
	topics, err := s.store.GetRecentTopicSnapshots(ctx, since, limit)
	if err != nil {
		slog.Error("failed to get topic snapshots", "error", err, "hours", hours, "limit", limit)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Return empty array if no results (never null)
	if topics == nil {
		topics = []store.TopicSnapshotRow{}
	}

	// Convert TopicSnapshotRow to response format where Keywords is a proper
	// JSON array rather than a double-encoded string.
	type topicResponse struct {
		ID                int64           `json:"id"`
		SnapshotTime      string          `json:"snapshot_time"`
		Rank              int             `json:"rank"`
		TopicID           string          `json:"topic_id"`
		Label             string          `json:"label"`
		Description       string          `json:"description"`
		UniqueAuthorCount int             `json:"unique_author_count"`
		Keywords          json.RawMessage `json:"keywords"`
		ExemplarURI       string          `json:"exemplar_uri"`
		ExemplarHandle    string          `json:"exemplar_handle"`
		IsMeme            bool            `json:"is_meme"`
		Justification     string          `json:"justification"`
	}
	resp := make([]topicResponse, len(topics))
	for i, t := range topics {
		kw := json.RawMessage(t.Keywords)
		if !json.Valid(kw) {
			kw = json.RawMessage("[]")
		}
		resp[i] = topicResponse{
			ID: t.ID, SnapshotTime: t.SnapshotTime, Rank: t.Rank,
			TopicID: t.TopicID, Label: t.Label, Description: t.Description,
			UniqueAuthorCount: t.UniqueAuthorCount, Keywords: kw,
			ExemplarURI: t.ExemplarURI, ExemplarHandle: t.ExemplarHandle,
			IsMeme: t.IsMeme, Justification: t.Justification,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handlePosting returns recent posting activity for each post type.
// GET /stats/posting
func (s *Server) handlePosting(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	activity, err := s.store.GetPostingActivity(ctx)
	if err != nil {
		slog.Error("failed to get posting activity", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	health, err := s.store.GetDatabaseHealth(ctx)
	if err != nil {
		slog.Error("failed to get database health", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, health)
}

func (s *Server) handleHealthHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	hoursStr := r.URL.Query().Get("hours")
	hours := s.healthChart.Hours
	if hours <= 0 {
		hours = 6
	}
	if hoursStr != "" {
		var err error
		hours, err = strconv.Atoi(hoursStr)
		if err != nil || hours <= 0 {
			writeError(w, http.StatusBadRequest, "hours must be a positive integer")
			return
		}
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 1000
	if limitStr != "" {
		var err error
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 || limit > 5000 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer <= 5000")
			return
		}
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	snapshots, err := s.store.GetHealthHistory(ctx, since, limit)
	if err != nil {
		slog.Error("failed to get health history", "error", err, "hours", hours, "limit", limit)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if snapshots == nil {
		snapshots = []store.StatsSnapshot{}
	}
	writeJSON(w, http.StatusOK, snapshots)
}

func (s *Server) handleHealthChart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	hoursStr := r.URL.Query().Get("hours")
	hours := s.healthChart.Hours
	if hours <= 0 {
		hours = 6
	}
	if hoursStr != "" {
		var err error
		hours, err = strconv.Atoi(hoursStr)
		if err != nil || hours <= 0 {
			writeError(w, http.StatusBadRequest, "hours must be a positive integer")
			return
		}
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	snapshots, err := s.store.GetHealthHistory(ctx, since, 1000)
	if err != nil {
		slog.Error("failed to get health history for chart", "error", err, "hours", hours)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if len(snapshots) < 2 {
		writeError(w, http.StatusNotFound, "not enough data points for chart (need at least 2)")
		return
	}

	memLimitMB := s.healthChart.MemoryLimitMB
	if memLimitMB <= 0 {
		memLimitMB = 512
	}

	png, err := sparkline.GenerateHealthChart(snapshots, memLimitMB)
	if err != nil {
		slog.Error("failed to generate health chart", "error", err)
		writeError(w, http.StatusInternalServerError, "chart generation failed")
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	w.WriteHeader(http.StatusOK)
	w.Write(png)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
