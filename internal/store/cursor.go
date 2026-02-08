package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetCursor returns the stored Jetstream cursor value.
// Returns 0 if no cursor has been saved yet.
func (s *Store) GetCursor(ctx context.Context) (int64, error) {
	var cursor int64
	err := s.db.QueryRowContext(ctx, `SELECT cursor_value FROM cursor WHERE id = 1`).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get cursor: %w", err)
	}
	return cursor, nil
}

// SaveCursor upserts the Jetstream cursor value.
func (s *Store) SaveCursor(ctx context.Context, cursor int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cursor (id, cursor_value, updated_at) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET cursor_value=excluded.cursor_value, updated_at=excluded.updated_at`,
		cursor, nowUTC(),
	)
	if err != nil {
		return fmt.Errorf("save cursor: %w", err)
	}
	return nil
}
