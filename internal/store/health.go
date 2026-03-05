package store

import (
	"context"
	"fmt"
	"os"
	"time"
)

// DatabaseHealth contains comprehensive health metrics for the SQLite database.
type DatabaseHealth struct {
	DBSizeBytes   int64 `json:"db_size_bytes"`
	WALSizeBytes  int64 `json:"wal_size_bytes"`
	FreelistCount int64 `json:"freelist_count"`
	PageSize      int64 `json:"page_size"`
	PageCount     int64 `json:"page_count"`

	Tables    []TableHealth `json:"tables"`
	CheckedAt time.Time     `json:"checked_at"`
}

// TableHealth describes a single table's row count and how many rows exceed retention.
type TableHealth struct {
	Name      string `json:"name"`
	RowCount  int64  `json:"row_count"`
	StaleRows int64  `json:"stale_rows,omitempty"`
	Retention string `json:"retention,omitempty"`
}

type tableSpec struct {
	name      string
	staleSQL  string
	retention string
	cutoff    func() string
}

// GetDatabaseHealth gathers comprehensive database health metrics.
func (s *Store) GetDatabaseHealth(ctx context.Context) (*DatabaseHealth, error) {
	h := &DatabaseHealth{
		CheckedAt: time.Now().UTC(),
	}

	if info, err := os.Stat(s.dbPath); err == nil {
		h.DBSizeBytes = info.Size()
	}
	if info, err := os.Stat(s.dbPath + "-wal"); err == nil {
		h.WALSizeBytes = info.Size()
	}

	if err := s.readDB.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&h.FreelistCount); err != nil {
		return nil, fmt.Errorf("freelist_count: %w", err)
	}
	if err := s.readDB.QueryRowContext(ctx, "PRAGMA page_size").Scan(&h.PageSize); err != nil {
		return nil, fmt.Errorf("page_size: %w", err)
	}
	if err := s.readDB.QueryRowContext(ctx, "PRAGMA page_count").Scan(&h.PageCount); err != nil {
		return nil, fmt.Errorf("page_count: %w", err)
	}

	now := time.Now().UTC()
	specs := []tableSpec{
		{
			name:      "post_buffer",
			retention: "3h",
			staleSQL:  "SELECT COUNT(*) FROM post_buffer WHERE inserted_at < ?",
			cutoff:    func() string { return now.Add(-3 * time.Hour).Format(time.RFC3339) },
		},
		{
			name:      "topic_tokens",
			retention: "26h",
			staleSQL:  "SELECT COUNT(*) FROM topic_tokens WHERE created_at < ?",
			cutoff:    func() string { return now.Add(-26 * time.Hour).Format(time.RFC3339) },
		},
		{
			name: "token_postings",
		},
		{
			name: "runs",
		},
		{
			name: "sentiment_history",
		},
		{
			name: "daily_sentiment",
		},
		{
			name: "topic_snapshots",
		},
		{
			name: "topic_identity",
		},
		{
			name: "stats_snapshots",
		},
		{
			name: "stats_events",
		},
		{
			name: "key_value",
		},
		{
			name: "cursor",
		},
	}

	for _, spec := range specs {
		th := TableHealth{
			Name:      spec.name,
			Retention: spec.retention,
		}

		query := "SELECT COUNT(*) FROM " + spec.name
		if err := s.readDB.QueryRowContext(ctx, query).Scan(&th.RowCount); err != nil {
			continue
		}

		if spec.staleSQL != "" && spec.cutoff != nil {
			_ = s.readDB.QueryRowContext(ctx, spec.staleSQL, spec.cutoff()).Scan(&th.StaleRows)
		}

		h.Tables = append(h.Tables, th)
	}

	return h, nil
}
