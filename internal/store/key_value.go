package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) SetKeyValue(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO key_value (key, value, updated_at) VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set key_value %q: %w", key, err)
	}
	return nil
}

func (s *Store) GetKeyValue(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM key_value WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("key not found: %s: %w", key, err)
	}
	if err != nil {
		return "", fmt.Errorf("get key_value %q: %w", key, err)
	}
	return value, nil
}
