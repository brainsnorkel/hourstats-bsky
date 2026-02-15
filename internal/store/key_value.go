package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// KeyValueEntry holds a key-value pair with its last-updated timestamp.
type KeyValueEntry struct {
	Key       string
	Value     string
	UpdatedAt string
}

// GetKeyValueWithTimestamp returns a key-value entry including its updated_at timestamp.
// Returns nil, nil if the key does not exist.
func (s *Store) GetKeyValueWithTimestamp(ctx context.Context, key string) (*KeyValueEntry, error) {
	var entry KeyValueEntry
	err := s.readDB.QueryRowContext(ctx,
		`SELECT key, value, updated_at FROM key_value WHERE key = ?`, key,
	).Scan(&entry.Key, &entry.Value, &entry.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get key_value with timestamp %q: %w", key, err)
	}
	return &entry, nil
}

func (s *Store) SetKeyValue(ctx context.Context, key, value string) error {
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO key_value (key, value, updated_at) VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set key_value %q: %w", key, err)
	}
	return nil
}

// DeleteKeyValues removes the given keys and returns how many rows were affected.
func (s *Store) DeleteKeyValues(ctx context.Context, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(keys))
	args := make([]interface{}, len(keys))
	for i, k := range keys {
		placeholders[i] = "?"
		args[i] = k
	}
	query := fmt.Sprintf(`DELETE FROM key_value WHERE key IN (%s)`, strings.Join(placeholders, ", "))
	res, err := s.writeDB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("delete key_values: %w", err)
	}
	return res.RowsAffected()
}

func (s *Store) GetKeyValue(ctx context.Context, key string) (string, error) {
	var value string
	err := s.readDB.QueryRowContext(ctx, `SELECT value FROM key_value WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("key not found: %s: %w", key, err)
	}
	if err != nil {
		return "", fmt.Errorf("get key_value %q: %w", key, err)
	}
	return value, nil
}
