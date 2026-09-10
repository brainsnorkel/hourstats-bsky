package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// The v2 cursor lives in key_value rather than the single-row cursor table:
// the two protocols' cursors are not interchangeable (a seq is not a
// microsecond timestamp), so a v1 fallback must find its own row untouched.
const (
	kvV2Cursor       = "jetstream_v2_cursor"
	kvV2CursorTimeUS = "jetstream_v2_cursor_time_us"
)

// GetV2Cursor returns the stored Jetstream v2 cursor: the seq to resume from
// and the event time it was witnessed at, which is what the consumer's
// staleness gate reads. Both are 0 when no v2 cursor has been saved yet.
func (s *Store) GetV2Cursor(ctx context.Context) (seq, timeUS int64, err error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT key, value FROM key_value WHERE key IN (?, ?)`, kvV2Cursor, kvV2CursorTimeUS)
	if err != nil {
		return 0, 0, fmt.Errorf("get v2 cursor: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return 0, 0, fmt.Errorf("scan v2 cursor: %w", err)
		}
		n, convErr := strconv.ParseInt(value, 10, 64)
		if convErr != nil {
			return 0, 0, fmt.Errorf("parse v2 cursor %q=%q: %w", key, value, convErr)
		}
		switch key {
		case kvV2Cursor:
			seq = n
		case kvV2CursorTimeUS:
			timeUS = n
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("get v2 cursor: %w", err)
	}
	return seq, timeUS, nil
}

// SaveV2Cursor upserts the Jetstream v2 cursor with SQLITE_BUSY retry. Both
// keys are written in one statement so a crash cannot leave the seq paired
// with a stale witnessed time.
func (s *Store) SaveV2Cursor(ctx context.Context, seq, timeUS int64) error {
	return withRetry(ctx, func() error {
		_, err := s.writeDB.ExecContext(ctx,
			`INSERT INTO key_value (key, value, updated_at) VALUES (?, ?, ?), (?, ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
			kvV2Cursor, strconv.FormatInt(seq, 10), nowUTC(),
			kvV2CursorTimeUS, strconv.FormatInt(timeUS, 10), nowUTC(),
		)
		if err != nil {
			return fmt.Errorf("save v2 cursor: %w", err)
		}
		return nil
	})
}

// GetCursor returns the stored Jetstream cursor value.
// Returns 0 if no cursor has been saved yet.
func (s *Store) GetCursor(ctx context.Context) (int64, error) {
	var cursor int64
	err := s.readDB.QueryRowContext(ctx, `SELECT cursor_value FROM cursor WHERE id = 1`).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get cursor: %w", err)
	}
	return cursor, nil
}

// SaveCursor upserts the Jetstream cursor value with SQLITE_BUSY retry.
func (s *Store) SaveCursor(ctx context.Context, cursor int64) error {
	return withRetry(ctx, func() error {
		_, err := s.writeDB.ExecContext(ctx,
			`INSERT INTO cursor (id, cursor_value, updated_at) VALUES (1, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET cursor_value=excluded.cursor_value, updated_at=excluded.updated_at`,
			cursor, nowUTC(),
		)
		if err != nil {
			return fmt.Errorf("save cursor: %w", err)
		}
		return nil
	})
}
