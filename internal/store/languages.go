package store

import (
	"context"
	"fmt"
	"time"
)

// StoreLanguageCounts records one analysis cycle's per-language firehose
// counts against the cycle's timestamp, in a single transaction.
func (s *Store) StoreLanguageCounts(ctx context.Context, at time.Time, counts map[string]int64) error {
	if len(counts) == 0 {
		return nil
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store language counts: begin: %w", err)
	}
	defer tx.Rollback()
	ts := timeToStr(at)
	for lang, n := range counts {
		if lang == "" || n <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO language_counts (timestamp, lang, count) VALUES (?, ?, ?)
			 ON CONFLICT(timestamp, lang) DO UPDATE SET count = count + excluded.count`,
			ts, lang, n); err != nil {
			return fmt.Errorf("store language count %s: %w", lang, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store language counts: commit: %w", err)
	}
	return nil
}

// RollupLanguageDaily sums the day's language_counts into language_daily and
// returns how many languages were written. Like the other rollups it merges
// with MAX so a re-run over a purged window never lowers a stored count.
// The SELECT keeps its WHERE clause on purpose (SQLite upsert grammar).
func (s *Store) RollupLanguageDaily(ctx context.Context, date string) (int64, error) {
	start, end, err := dayBounds(date)
	if err != nil {
		return 0, err
	}
	result, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO language_daily (date, lang, count)
		 SELECT ?, lang, SUM(count) FROM language_counts
		 WHERE timestamp >= ? AND timestamp < ?
		 GROUP BY lang
		 ON CONFLICT(date, lang) DO UPDATE SET count = MAX(language_daily.count, excluded.count)`,
		date, start, end)
	if err != nil {
		return 0, fmt.Errorf("rollup language_daily %s: %w", date, err)
	}
	return result.RowsAffected()
}

// GetLanguageDailyRange returns every language row from startDate to endDate
// inclusive, ordered by date then count descending.
func (s *Store) GetLanguageDailyRange(ctx context.Context, startDate, endDate string) ([]LanguageDailyRow, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT date, lang, count FROM language_daily
		 WHERE date >= ? AND date <= ?
		 ORDER BY date ASC, count DESC, lang ASC`, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("query language_daily %s..%s: %w", startDate, endDate, err)
	}
	defer rows.Close()
	var out []LanguageDailyRow
	for rows.Next() {
		var r LanguageDailyRow
		if err := rows.Scan(&r.Date, &r.Lang, &r.Count); err != nil {
			return nil, fmt.Errorf("scan language_daily: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PurgeLanguageCounts deletes per-cycle language rows older than olderThan.
func (s *Store) PurgeLanguageCounts(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := timeToStr(time.Now().UTC().Add(-olderThan))
	result, err := s.writeDB.ExecContext(ctx, `DELETE FROM language_counts WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge language_counts: %w", err)
	}
	return result.RowsAffected()
}
