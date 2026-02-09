package store

import (
	"context"
	"fmt"
	"strings"
)

type TopicTokenRow struct {
	PostURI   string
	Tokens    string
	CreatedAt string
}

type TopicSnapshotRow struct {
	ID             int64
	SnapshotTime   string
	Rank           int
	TopicID        string
	Label          string
	Description    string
	PostCount      int
	Keywords       string
	ExemplarURI    string
	ExemplarHandle string
}

type TopicIdentityRow struct {
	TopicID        string
	CanonicalLabel string
	Keywords       string
	FirstSeen      string
	LastSeen       string
	PeakRank       int
}

func (s *Store) InsertTopicTokens(ctx context.Context, postURI, tokensJSON, createdAt string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO topic_tokens (post_uri, tokens, created_at) VALUES (?, ?, ?)`,
		postURI, tokensJSON, createdAt,
	)
	if err != nil {
		return fmt.Errorf("insert topic_tokens: %w", err)
	}
	return nil
}

func (s *Store) GetTopicTokensSince(ctx context.Context, cutoff string) ([]TopicTokenRow, error) {
	return s.GetTopicTokensSinceLimit(ctx, cutoff, 0)
}

// GetTopicTokensSinceLimit returns topic tokens since cutoff, limited to the
// most recent `limit` rows. A limit of 0 returns all rows.
func (s *Store) GetTopicTokensSinceLimit(ctx context.Context, cutoff string, limit int) ([]TopicTokenRow, error) {
	var query string
	var args []any
	if limit > 0 {
		// Use a subquery to get the most recent `limit` rows, then re-order ASC.
		query = `SELECT post_uri, tokens, created_at FROM (
			SELECT post_uri, tokens, created_at FROM topic_tokens
			WHERE created_at >= ? ORDER BY created_at DESC LIMIT ?
		) sub ORDER BY created_at ASC`
		args = []any{cutoff, limit}
	} else {
		query = `SELECT post_uri, tokens, created_at FROM topic_tokens WHERE created_at >= ? ORDER BY created_at ASC`
		args = []any{cutoff}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query topic_tokens: %w", err)
	}
	defer rows.Close()

	var result []TopicTokenRow
	for rows.Next() {
		var r TopicTokenRow
		if err := rows.Scan(&r.PostURI, &r.Tokens, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan topic_tokens: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) CountTopicTokensSince(ctx context.Context, cutoff string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM topic_tokens WHERE created_at >= ?`, cutoff,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count topic_tokens: %w", err)
	}
	return count, nil
}

func (s *Store) PurgeTopicTokens(ctx context.Context, cutoff string) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM topic_tokens WHERE created_at < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("purge topic_tokens: %w", err)
	}
	return result.RowsAffected()
}

func (s *Store) GetTopicTokenURIsByKeywords(ctx context.Context, keywords []string, cutoff string, limit int) ([]string, error) {
	if len(keywords) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(keywords))
	args := make([]any, 0, len(keywords)+2)
	args = append(args, cutoff)
	for i, kw := range keywords {
		placeholders[i] = "?"
		args = append(args, kw)
	}
	args = append(args, limit)

	q := fmt.Sprintf(
		`SELECT DISTINCT t.post_uri FROM topic_tokens t, json_each(t.tokens) AS je
		 WHERE t.created_at >= ? AND je.value IN (%s)
		 ORDER BY t.created_at DESC LIMIT ?`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query topic_token URIs: %w", err)
	}
	defer rows.Close()

	var uris []string
	for rows.Next() {
		var uri string
		if err := rows.Scan(&uri); err != nil {
			return nil, fmt.Errorf("scan topic_token URI: %w", err)
		}
		uris = append(uris, uri)
	}
	return uris, rows.Err()
}

func (s *Store) InsertTopicSnapshot(ctx context.Context, snapshotTime string, rank int, topicID, label, description string, postCount int, keywordsJSON, exemplarURI, exemplarHandle string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO topic_snapshots (snapshot_time, rank, topic_id, label, description, post_count, keywords, exemplar_uri, exemplar_handle)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshotTime, rank, topicID, label, description, postCount, keywordsJSON, exemplarURI, exemplarHandle,
	)
	if err != nil {
		return fmt.Errorf("insert topic_snapshot: %w", err)
	}
	return nil
}

func (s *Store) GetTopicSnapshotsSince(ctx context.Context, cutoff string) ([]TopicSnapshotRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, snapshot_time, rank, topic_id, label, description, post_count, keywords, exemplar_uri, exemplar_handle
		 FROM topic_snapshots WHERE snapshot_time >= ? ORDER BY snapshot_time ASC, rank ASC`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query topic_snapshots: %w", err)
	}
	defer rows.Close()

	var result []TopicSnapshotRow
	for rows.Next() {
		var r TopicSnapshotRow
		if err := rows.Scan(&r.ID, &r.SnapshotTime, &r.Rank, &r.TopicID, &r.Label, &r.Description, &r.PostCount, &r.Keywords, &r.ExemplarURI, &r.ExemplarHandle); err != nil {
			return nil, fmt.Errorf("scan topic_snapshot: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) PurgeTopicSnapshots(ctx context.Context, cutoff string) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM topic_snapshots WHERE snapshot_time < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("purge topic_snapshots: %w", err)
	}
	return result.RowsAffected()
}

func (s *Store) UpdateSnapshotExemplar(ctx context.Context, snapshotID int64, exemplarURI, exemplarHandle string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE topic_snapshots SET exemplar_uri=?, exemplar_handle=? WHERE id=?`,
		exemplarURI, exemplarHandle, snapshotID,
	)
	if err != nil {
		return fmt.Errorf("update snapshot exemplar: %w", err)
	}
	return nil
}

func (s *Store) UpsertTopicIdentity(ctx context.Context, topicID, label, keywordsJSON, firstSeen, lastSeen string, peakRank int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO topic_identity (topic_id, canonical_label, keywords, first_seen, last_seen, peak_rank)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(topic_id) DO UPDATE SET
			canonical_label=excluded.canonical_label,
			keywords=excluded.keywords,
			last_seen=excluded.last_seen,
			peak_rank=CASE WHEN excluded.peak_rank < topic_identity.peak_rank THEN excluded.peak_rank ELSE topic_identity.peak_rank END`,
		topicID, label, keywordsJSON, firstSeen, lastSeen, peakRank,
	)
	if err != nil {
		return fmt.Errorf("upsert topic_identity: %w", err)
	}
	return nil
}

func (s *Store) GetRecentTopicIdentities(ctx context.Context, cutoff string) ([]TopicIdentityRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT topic_id, canonical_label, keywords, first_seen, last_seen, peak_rank
		 FROM topic_identity WHERE last_seen >= ? ORDER BY last_seen DESC`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query topic_identity: %w", err)
	}
	defer rows.Close()

	var result []TopicIdentityRow
	for rows.Next() {
		var r TopicIdentityRow
		if err := rows.Scan(&r.TopicID, &r.CanonicalLabel, &r.Keywords, &r.FirstSeen, &r.LastSeen, &r.PeakRank); err != nil {
			return nil, fmt.Errorf("scan topic_identity: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) PurgeTopicIdentities(ctx context.Context, cutoff string) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM topic_identity WHERE last_seen < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("purge topic_identity: %w", err)
	}
	return result.RowsAffected()
}
