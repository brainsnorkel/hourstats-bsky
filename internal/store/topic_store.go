package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type TopicTokenRow struct {
	PostURI   string
	Tokens    string
	CreatedAt string
	AuthorDID string
}

type TopicSnapshotRow struct {
	ID             int64  `json:"id"`
	SnapshotTime   string `json:"snapshot_time"`
	Rank           int    `json:"rank"`
	TopicID        string `json:"topic_id"`
	Label          string `json:"label"`
	Description    string `json:"description"`
	PostCount      int    `json:"post_count"`
	Keywords       string `json:"keywords"`
	ExemplarURI    string `json:"exemplar_uri"`
	ExemplarHandle string `json:"exemplar_handle"`
	IsMeme         bool   `json:"is_meme"`
	Justification  string `json:"justification"`
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
	_, err := s.writeDB.ExecContext(ctx,
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
// most recent `limit` rows. Only includes posts that have been hydrated with
// engagement > 0 (filters spam and unhydrated posts). A limit of 0 returns all rows.
func (s *Store) GetTopicTokensSinceLimit(ctx context.Context, cutoff string, limit int) ([]TopicTokenRow, error) {
	var query string
	var args []any
	if limit > 0 {
		query = `SELECT tt.post_uri, tt.tokens, tt.created_at, tt.author_did FROM (
			SELECT tt.post_uri, tt.tokens, tt.created_at, pb.author_did
			FROM topic_tokens tt
			JOIN post_buffer pb ON tt.post_uri = pb.uri
			WHERE tt.created_at >= ?
			  AND (pb.likes + pb.reposts + pb.replies) > 0
			ORDER BY tt.created_at DESC LIMIT ?
		) tt ORDER BY tt.created_at ASC`
		args = []any{cutoff, limit}
	} else {
		query = `SELECT tt.post_uri, tt.tokens, tt.created_at, pb.author_did
			FROM topic_tokens tt
			JOIN post_buffer pb ON tt.post_uri = pb.uri
			WHERE tt.created_at >= ?
			  AND (pb.likes + pb.reposts + pb.replies) > 0
			ORDER BY tt.created_at ASC`
		args = []any{cutoff}
	}
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query topic_tokens: %w", err)
	}
	defer rows.Close()

	var result []TopicTokenRow
	for rows.Next() {
		var r TopicTokenRow
		if err := rows.Scan(&r.PostURI, &r.Tokens, &r.CreatedAt, &r.AuthorDID); err != nil {
			return nil, fmt.Errorf("scan topic_tokens: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) CountTopicTokensSince(ctx context.Context, cutoff string) (int64, error) {
	var count int64
	err := s.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM topic_tokens WHERE created_at >= ?`, cutoff,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count topic_tokens: %w", err)
	}
	return count, nil
}

func (s *Store) PurgeTopicTokens(ctx context.Context, cutoff string) (int64, error) {
	total, err := purgeInChunks(ctx, s.writeDB, `DELETE FROM topic_tokens WHERE rowid IN (SELECT rowid FROM topic_tokens WHERE created_at < ? LIMIT 1000)`, cutoff)
	if err != nil {
		return total, fmt.Errorf("purge topic_tokens: %w", err)
	}
	return total, nil
}

// ExemplarCandidate is a post matched by keyword with engagement from post_buffer.
type ExemplarCandidate struct {
	URI        string
	Handle     string
	Engagement int
}

func (s *Store) GetExemplarCandidates(ctx context.Context, keywords []string, cutoff string, limit int) ([]ExemplarCandidate, error) {
	if len(keywords) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(keywords))
	args := make([]any, 0, len(keywords)+2)
	for i, kw := range keywords {
		placeholders[i] = "?"
		args = append(args, kw)
	}
	args = append(args, cutoff)
	args = append(args, limit)

	q := fmt.Sprintf(
		`SELECT pb.uri, pb.author_handle, (pb.likes + pb.reposts + pb.replies) AS eng
		 FROM topic_tokens tt, json_each(tt.tokens) je
		 JOIN post_buffer pb ON tt.post_uri = pb.uri
		 WHERE je.value IN (%s)
		   AND tt.created_at >= ?
		   AND pb.author_handle != ''
		 GROUP BY pb.uri
		 ORDER BY COUNT(DISTINCT je.value) DESC, eng DESC
		 LIMIT ?`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query exemplar candidates: %w", err)
	}
	defer rows.Close()

	var result []ExemplarCandidate
	for rows.Next() {
		var c ExemplarCandidate
		if err := rows.Scan(&c.URI, &c.Handle, &c.Engagement); err != nil {
			return nil, fmt.Errorf("scan exemplar candidate: %w", err)
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) InsertTopicSnapshot(ctx context.Context, snapshotTime string, rank int, topicID, label, description string, postCount int, keywordsJSON, exemplarURI, exemplarHandle string, isMeme bool, justification string) error {
	isMemeInt := 0
	if isMeme {
		isMemeInt = 1
	}
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO topic_snapshots (snapshot_time, rank, topic_id, label, description, post_count, keywords, exemplar_uri, exemplar_handle, is_meme, justification)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshotTime, rank, topicID, label, description, postCount, keywordsJSON, exemplarURI, exemplarHandle, isMemeInt, justification,
	)
	if err != nil {
		return fmt.Errorf("insert topic_snapshot: %w", err)
	}
	return nil
}

func (s *Store) GetTopicSnapshotsSince(ctx context.Context, cutoff string) ([]TopicSnapshotRow, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, snapshot_time, rank, topic_id, label, description, post_count, keywords, exemplar_uri, exemplar_handle, is_meme, COALESCE(justification, '') as justification
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
		var isMeme int
		if err := rows.Scan(&r.ID, &r.SnapshotTime, &r.Rank, &r.TopicID, &r.Label, &r.Description, &r.PostCount, &r.Keywords, &r.ExemplarURI, &r.ExemplarHandle, &isMeme, &r.Justification); err != nil {
			return nil, fmt.Errorf("scan topic_snapshot: %w", err)
		}
		r.IsMeme = isMeme != 0
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) GetRecentTopicSnapshots(ctx context.Context, since string, limit int) ([]TopicSnapshotRow, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, snapshot_time, rank, topic_id, label, description, post_count, keywords, exemplar_uri, exemplar_handle, is_meme, COALESCE(justification, '') as justification
		 FROM topic_snapshots WHERE snapshot_time >= ? ORDER BY snapshot_time DESC, rank ASC LIMIT ?`,
		since, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query recent topic_snapshots: %w", err)
	}
	defer rows.Close()

	var result []TopicSnapshotRow
	for rows.Next() {
		var r TopicSnapshotRow
		var isMeme int
		if err := rows.Scan(&r.ID, &r.SnapshotTime, &r.Rank, &r.TopicID, &r.Label, &r.Description, &r.PostCount, &r.Keywords, &r.ExemplarURI, &r.ExemplarHandle, &isMeme, &r.Justification); err != nil {
			return nil, fmt.Errorf("scan recent topic_snapshot: %w", err)
		}
		r.IsMeme = isMeme != 0
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) PurgeTopicSnapshots(ctx context.Context, cutoff string) (int64, error) {
	result, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM topic_snapshots WHERE snapshot_time < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("purge topic_snapshots: %w", err)
	}
	return result.RowsAffected()
}

func (s *Store) UpdateSnapshotExemplar(ctx context.Context, snapshotID int64, exemplarURI, exemplarHandle string) error {
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE topic_snapshots SET exemplar_uri=?, exemplar_handle=? WHERE id=?`,
		exemplarURI, exemplarHandle, snapshotID,
	)
	if err != nil {
		return fmt.Errorf("update snapshot exemplar: %w", err)
	}
	return nil
}

func (s *Store) UpsertTopicIdentity(ctx context.Context, topicID, label, keywordsJSON, firstSeen, lastSeen string, peakRank int) error {
	_, err := s.writeDB.ExecContext(ctx,
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
	rows, err := s.readDB.QueryContext(ctx,
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

// GetLatestTopicSnapshotTime returns the most recent snapshot_time and the count
// of topics in that snapshot. Returns ("", 0, nil) if no snapshots exist.
func (s *Store) GetLatestTopicSnapshotTime(ctx context.Context) (string, int, error) {
	var snapshotTime string
	var count int
	err := s.readDB.QueryRowContext(ctx,
		`SELECT snapshot_time, COUNT(*) FROM topic_snapshots
		 GROUP BY snapshot_time ORDER BY snapshot_time DESC LIMIT 1`,
	).Scan(&snapshotTime, &count)
	if err == sql.ErrNoRows {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("get latest topic snapshot time: %w", err)
	}
	return snapshotTime, count, nil
}

func (s *Store) PurgeTopicIdentities(ctx context.Context, cutoff string) (int64, error) {
	result, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM topic_identity WHERE last_seen < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("purge topic_identity: %w", err)
	}
	return result.RowsAffected()
}
