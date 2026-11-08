package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Rollups behind the weekly and monthly report posts. topic_snapshots and
// runs are purged after 48h, so the daily cycle condenses each finished UTC
// day into topic_daily and daily_top_post, which keep 400 days.

// dayBounds returns the RFC3339 timestamps [start, end) covering a
// YYYY-MM-DD date in UTC.
func dayBounds(date string) (string, string, error) {
	start, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", "", fmt.Errorf("parse date %q: %w", date, err)
	}
	return start.Format(time.RFC3339), start.Add(24 * time.Hour).Format(time.RFC3339), nil
}

// The SELECT below keeps its WHERE clause on purpose: SQLite's grammar needs
// one to tell the upsert's ON CONFLICT apart from a join's ON.
//
// RollupTopicDaily condenses the day's topic_snapshots into topic_daily and
// returns how many topics were written. It merges with any existing rows for
// the date rather than replacing them, so a re-run over a partially purged
// window (appearances can only be undercounted, never overcounted) keeps the
// better figures: max appearances, min best_rank, max authors, latest label.
func (s *Store) RollupTopicDaily(ctx context.Context, date string) (int64, error) {
	start, end, err := dayBounds(date)
	if err != nil {
		return 0, err
	}
	result, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO topic_daily (date, topic_id, label, appearances, best_rank, max_authors)
		 SELECT ?, t.topic_id,
		        (SELECT label FROM topic_snapshots l
		          WHERE l.topic_id = t.topic_id AND l.snapshot_time >= ? AND l.snapshot_time < ?
		          ORDER BY l.snapshot_time DESC LIMIT 1),
		        COUNT(DISTINCT t.snapshot_time), MIN(t.rank), MAX(t.unique_author_count)
		 FROM topic_snapshots t
		 WHERE t.snapshot_time >= ? AND t.snapshot_time < ?
		 GROUP BY t.topic_id
		 ON CONFLICT(date, topic_id) DO UPDATE SET
			label=excluded.label,
			appearances=MAX(topic_daily.appearances, excluded.appearances),
			best_rank=MIN(topic_daily.best_rank, excluded.best_rank),
			max_authors=MAX(topic_daily.max_authors, excluded.max_authors)`,
		date, start, end, start, end,
	)
	if err != nil {
		return 0, fmt.Errorf("rollup topic_daily %s: %w", date, err)
	}
	return result.RowsAffected()
}

// GetTopicDaily returns the rolled-up topics for one date, best first.
func (s *Store) GetTopicDaily(ctx context.Context, date string) ([]TopicDailyRow, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT date, topic_id, label, appearances, best_rank, max_authors
		 FROM topic_daily WHERE date = ?
		 ORDER BY appearances DESC, best_rank ASC, topic_id ASC`, date)
	if err != nil {
		return nil, fmt.Errorf("query topic_daily %s: %w", date, err)
	}
	defer rows.Close()
	var out []TopicDailyRow
	for rows.Next() {
		var r TopicDailyRow
		if err := rows.Scan(&r.Date, &r.TopicID, &r.Label, &r.Appearances, &r.BestRank, &r.MaxAuthors); err != nil {
			return nil, fmt.Errorf("scan topic_daily: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetTopTopicForRange returns the topic with the most topic_daily
// appearances between startDate and endDate inclusive, and that count.
// label is "" when the range holds no topic data.
//
// topic_daily fragments one subject in two independent ways. The identity
// tracker splits a subject across topic IDs (Halloween in the week of
// 2026-10-26 summed to 85 appearances under its normalised label but only 54
// under its best single ID), while Gemini relabels one ID from hour to hour
// (the 2026 midterms ID of 2026-11-02 trended 37 hours but summed to 22 under
// its best label key). So the rows are aggregated twice, by topic_id and by
// the normalised label key lower(trim(label)), and the better result is
// returned: the larger SUM(appearances), then the smaller MIN(best_rank),
// then the by-id result. The winner is never smaller than either axis alone,
// so a subject fragmented either way is not understated.
//
// Only the by-id sum is a count of cycles. A topic_daily row keeps the label
// its ID carried in the day's last snapshot, so the by-label sum credits
// every cycle of an ID to that final label: on 2026-11-04 the midterms ID
// carried "2026 Midterm Elections" in 4 of its 22 snapshots yet contributes
// all 22 to that key. Summed across IDs this can exceed a day's cycles
// ("donald trump" summed to 28 over 9 IDs on 2026-09-11, a 24-cycle day), so
// each (date, label_key) day sum is capped at that day's cycle count, the
// number of sentiment_history rows whose timestamp falls on the date, before
// the days are summed; a date with no sentiment_history rows is left
// uncapped. The cap bounds the by-label figure at one appearance per cycle
// but it remains an estimate that can run high within that bound (the
// relabelled ID above still credits 22 to a label it carried for 4), and it
// does not touch the group's MIN(best_rank) or the label chosen. The by-id
// axis needs no cap: appearances per (date, topic_id) is already a count of
// distinct snapshot times. The weekly report no longer calls this (since
// hs-aqa.6 it reads GetTopRankOneSubjectForRange); it is retained until two
// Mondays after hs-aqa.6 deploys, as a comparison for the new figure.
//
// The label shown is the spelling on the group's latest-date row (by id, the
// (date, topic_id) key makes that row unique; by label, ties on one date go
// to the larger appearances, then the smaller topic_id). SQLite's lower()
// folds ASCII only, so labels that differ only in non-ASCII case stay
// separate groups.
func (s *Store) GetTopTopicForRange(ctx context.Context, startDate, endDate string) (label string, appearances int, err error) {
	byID, err := s.topTopicBy(ctx, topTopicByIDQuery, startDate, endDate)
	if err != nil {
		return "", 0, fmt.Errorf("top topic by id %s..%s: %w", startDate, endDate, err)
	}
	if !byID.found {
		return "", 0, nil
	}
	byLabel, err := s.topTopicBy(ctx, topTopicByLabelQuery, startDate, endDate)
	if err != nil {
		return "", 0, fmt.Errorf("top topic by label %s..%s: %w", startDate, endDate, err)
	}
	if byLabel.found && (byLabel.appearances > byID.appearances ||
		byLabel.appearances == byID.appearances && byLabel.bestRank < byID.bestRank) {
		return byLabel.label, byLabel.appearances, nil
	}
	return byID.label, byID.appearances, nil
}

// topTopicResult is one aggregation's winner over a date range.
type topTopicResult struct {
	label       string
	appearances int
	bestRank    int
	found       bool
}

// Both queries bind ?1 = startDate and ?2 = endDate. The best CTE picks the
// winning group; the final SELECT fetches that group's rows to choose the
// displayed label.
const (
	topTopicByIDQuery = `
		WITH best AS (
			SELECT topic_id, SUM(appearances) AS total, MIN(best_rank) AS best_rank
			FROM topic_daily
			WHERE date BETWEEN ?1 AND ?2
			GROUP BY topic_id
			ORDER BY total DESC, best_rank ASC, topic_id ASC
			LIMIT 1
		)
		SELECT d.label, best.total, best.best_rank
		FROM best JOIN topic_daily d ON d.topic_id = best.topic_id
		WHERE d.date BETWEEN ?1 AND ?2
		ORDER BY d.date DESC, d.appearances DESC, d.label ASC
		LIMIT 1`

	// cycles counts the sentiment_history rows on each UTC date of the range;
	// the timestamps are RFC3339 'YYYY-MM-DDTHH:MM:SSZ', so a date string
	// sorts before every timestamp of that day and the range test can use the
	// timestamp index. days sums one label key's rows per date and capped
	// bounds that sum at the day's cycle count, leaving a date with no
	// cycles row as it is.
	topTopicByLabelQuery = `
		WITH cycles AS (
			SELECT substr(timestamp, 1, 10) AS date, COUNT(*) AS n
			FROM sentiment_history
			WHERE timestamp >= ?1 AND timestamp < date(?2, '+1 day')
			GROUP BY date
		),
		days AS (
			SELECT date, lower(trim(label)) AS label_key, SUM(appearances) AS total, MIN(best_rank) AS best_rank
			FROM topic_daily
			WHERE date BETWEEN ?1 AND ?2
			GROUP BY date, label_key
		),
		capped AS (
			SELECT days.label_key, MIN(days.total, COALESCE(cycles.n, days.total)) AS total, days.best_rank
			FROM days LEFT JOIN cycles ON cycles.date = days.date
		),
		best AS (
			SELECT label_key, SUM(total) AS total, MIN(best_rank) AS best_rank
			FROM capped
			GROUP BY label_key
			ORDER BY total DESC, best_rank ASC, label_key ASC
			LIMIT 1
		)
		SELECT d.label, best.total, best.best_rank
		FROM best JOIN topic_daily d ON lower(trim(d.label)) = best.label_key
		WHERE d.date BETWEEN ?1 AND ?2
		ORDER BY d.date DESC, d.appearances DESC, d.topic_id ASC
		LIMIT 1`
)

// topTopicBy runs one of the two aggregation queries over the range. A range
// with no rows is not an error: found is false.
func (s *Store) topTopicBy(ctx context.Context, query, startDate, endDate string) (topTopicResult, error) {
	var r topTopicResult
	err := s.readDB.QueryRowContext(ctx, query, startDate, endDate).
		Scan(&r.label, &r.appearances, &r.bestRank)
	if errors.Is(err, sql.ErrNoRows) {
		return topTopicResult{}, nil
	}
	if err != nil {
		return topTopicResult{}, err
	}
	r.found = true
	return r, nil
}

// GetTopRankOneSubjectForRange returns the subject that held rank 1 in the
// most clock hours between startDate and endDate inclusive (YYYY-MM-DD, so
// the range runs from startDate 00:00 to endDate 24:00 UTC), and that number
// of hours. It reads sentiment_history.top_topic, one row per cycle with that
// cycle's rank-1 label, and counts distinct UTC clock hours of the cycles'
// start, so the figure does not depend on the analysis interval: two
// 30-minute cycles in one hour count that hour once. topic_daily cannot give
// that figure: it counts appearances at ranks 1-5 per fragmented topic ID or
// exact label.
//
// The hour is read from the run ID, "run-YYYYMMDD-HHMMSS" at the cycle's
// start, not from timestamp, which is stamped when the sentiment is computed
// after hydration and so drifts past the hour: on 2026-11-04 the 08:55 and
// 09:55 cycles were stamped 09:00:13 and 09:59:35, one clock hour between
// them. Rows with an older run ID format (unix nanoseconds, before February
// 2026) fall back to the timestamp's hour.
//
// Only cycles with total_posts >= minPosts are counted. Callers pass the
// posting floor (cmd/hourstats minPostsRequired), so the figure counts
// analysed cycles that met the floor, which is close to but not literally
// the cycles that published: a DRY_RUN cycle above the floor is counted, and
// so is one skipped for another reason (a capped window, a memory guard
// trip). A label that is empty or only whitespace is ignored.
//
// Labels are grouped by subjectKey (topics.SubjectKey in production, passed
// in because internal/topics imports this package); a label whose key is
// empty is grouped under its own lowercased spelling. The winner has the
// most hours, ties going to the key whose latest cycle is later, then to the
// lexically smaller key. The label returned is the exact spelling inside the
// winning key that led in the most hours, ties going to the spelling seen
// most recently. label is "" and hours 0 when the range holds no labelled
// cycle.
func (s *Store) GetTopRankOneSubjectForRange(ctx context.Context, startDate, endDate string, minPosts int, subjectKey func(string) string) (label string, hours int, err error) {
	// One row per (label, start hour 'YYYY-MM-DDTHH'), with the hour's latest
	// timestamp.
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT top_topic,
		        CASE WHEN run_id GLOB 'run-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]'
		             THEN substr(run_id, 5, 4) || '-' || substr(run_id, 9, 2) || '-' || substr(run_id, 11, 2) || 'T' || substr(run_id, 14, 2)
		             ELSE substr(timestamp, 1, 13) END AS hour,
		        MAX(timestamp)
		 FROM sentiment_history
		 WHERE timestamp >= ?1 AND timestamp < date(?2, '+1 day')
		   AND top_topic IS NOT NULL AND trim(top_topic) != ''
		   AND total_posts >= ?3
		 GROUP BY top_topic, hour`, startDate, endDate, minPosts)
	if err != nil {
		return "", 0, fmt.Errorf("rank-1 topics %s..%s: %w", startDate, endDate, err)
	}
	defer rows.Close()

	type tally struct {
		n      int    // distinct clock hours
		latest string // RFC3339 timestamps sort as strings
	}
	type subject struct {
		tally
		hours  map[string]bool
		labels map[string]*tally
	}
	subjects := map[string]*subject{}
	for rows.Next() {
		var l, hour, ts string
		if err := rows.Scan(&l, &hour, &ts); err != nil {
			return "", 0, fmt.Errorf("scan rank-1 topic: %w", err)
		}
		key := subjectKey(l)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(l))
		}
		sub := subjects[key]
		if sub == nil {
			sub = &subject{hours: map[string]bool{}, labels: map[string]*tally{}}
			subjects[key] = sub
		}
		// Two spellings of one subject in one hour count that hour once.
		if !sub.hours[hour] {
			sub.hours[hour] = true
			sub.n++
		}
		sub.latest = max(sub.latest, ts)
		lt := sub.labels[l]
		if lt == nil {
			lt = &tally{}
			sub.labels[l] = lt
		}
		lt.n++ // the rows are already distinct per (label, hour)
		lt.latest = max(lt.latest, ts)
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("rank-1 topics %s..%s: %w", startDate, endDate, err)
	}

	// beats reports whether a outranks b: more hours, then a later latest
	// cycle, then the lexically smaller name.
	beats := func(a, b tally, aName, bName string) bool {
		if a.n != b.n {
			return a.n > b.n
		}
		if a.latest != b.latest {
			return a.latest > b.latest
		}
		return aName < bName
	}
	var bestKey string
	var best *subject
	for key, sub := range subjects {
		if best == nil || beats(sub.tally, best.tally, key, bestKey) {
			bestKey, best = key, sub
		}
	}
	if best == nil {
		return "", 0, nil
	}
	var bestLabel *tally
	for l, lt := range best.labels {
		if bestLabel == nil || beats(*lt, *bestLabel, l, label) {
			label, bestLabel = l, lt
		}
	}
	return label, best.n, nil
}

// StoreDailyTopPost records the most engaged post of a date. A re-run only
// replaces the stored row when it found a higher engagement score, so a
// partial window never demotes a complete one.
func (s *Store) StoreDailyTopPost(ctx context.Context, date string, p Post) error {
	if _, _, err := dayBounds(date); err != nil {
		return err
	}
	if p.URI == "" || p.CID == "" {
		return fmt.Errorf("store daily top post %s: post has no URI/CID", date)
	}
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO daily_top_post (date, uri, cid, author_handle, likes, reposts, replies, engagement_score)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(date) DO UPDATE SET
			uri=excluded.uri, cid=excluded.cid, author_handle=excluded.author_handle,
			likes=excluded.likes, reposts=excluded.reposts, replies=excluded.replies,
			engagement_score=excluded.engagement_score
		 WHERE excluded.engagement_score > daily_top_post.engagement_score`,
		date, p.URI, p.CID, p.AuthorHandle, p.Likes, p.Reposts, p.Replies, p.EngagementScore,
	)
	if err != nil {
		return fmt.Errorf("store daily top post %s: %w", date, err)
	}
	return nil
}

// GetTopPostForRange returns the highest-scoring daily top post between
// startDate and endDate inclusive, or nil when none is stored.
func (s *Store) GetTopPostForRange(ctx context.Context, startDate, endDate string) (*Post, error) {
	var p Post
	err := s.readDB.QueryRowContext(ctx,
		`SELECT uri, cid, author_handle, likes, reposts, replies, engagement_score
		 FROM daily_top_post
		 WHERE date >= ? AND date <= ?
		 ORDER BY engagement_score DESC, date DESC LIMIT 1`, startDate, endDate,
	).Scan(&p.URI, &p.CID, &p.AuthorHandle, &p.Likes, &p.Reposts, &p.Replies, &p.EngagementScore)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query top post %s..%s: %w", startDate, endDate, err)
	}
	return &p, nil
}

// HasDailyTopPost reports whether a top post is stored for the date.
func (s *Store) HasDailyTopPost(ctx context.Context, date string) (bool, error) {
	var n int
	if err := s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM daily_top_post WHERE date = ?`, date).Scan(&n); err != nil {
		return false, fmt.Errorf("count daily_top_post %s: %w", date, err)
	}
	return n > 0, nil
}

// UpdateDailyFirehoseTotal sets the firehose volume on an existing
// daily_sentiment row, used to backfill rows written before the column
// existed. It reports whether a row was updated.
func (s *Store) UpdateDailyFirehoseTotal(ctx context.Context, date string, total int) (bool, error) {
	result, err := s.writeDB.ExecContext(ctx,
		`UPDATE daily_sentiment SET total_firehose_posts = ? WHERE date = ?`, total, date)
	if err != nil {
		return false, fmt.Errorf("update daily firehose total %s: %w", date, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetDailySentimentMissingFirehose returns daily rows on or after startDate
// whose firehose total is still zero, oldest first.
func (s *Store) GetDailySentimentMissingFirehose(ctx context.Context, startDate string) ([]DailySentimentDataPoint, error) {
	return s.queryDailySentiment(ctx,
		`SELECT `+dailySentimentColumns+`
		 FROM daily_sentiment
		 WHERE date >= ? AND COALESCE(total_firehose_posts, 0) = 0
		 ORDER BY date ASC`, startDate)
}

// DayCycleTotals describes one UTC day of sentiment_history: how many
// cycles met the minimum post count and their English posts (matching these
// against the daily row proves the day is fully covered), plus the firehose
// posts summed over every cycle of the day regardless of confidence.
type DayCycleTotals struct {
	Cycles        int
	EnglishPosts  int
	FirehosePosts int
}

func (s *Store) GetDayCycleTotals(ctx context.Context, date string, minPosts int) (DayCycleTotals, error) {
	start, end, err := dayBounds(date)
	if err != nil {
		return DayCycleTotals{}, err
	}
	var t DayCycleTotals
	err = s.readDB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(total_posts >= ?), 0),
		        COALESCE(SUM(CASE WHEN total_posts >= ? THEN total_posts ELSE 0 END), 0),
		        COALESCE(SUM(total_firehose_posts), 0)
		 FROM sentiment_history
		 WHERE timestamp >= ? AND timestamp < ?`,
		minPosts, minPosts, start, end,
	).Scan(&t.Cycles, &t.EnglishPosts, &t.FirehosePosts)
	if err != nil {
		return DayCycleTotals{}, fmt.Errorf("day cycle totals %s: %w", date, err)
	}
	return t, nil
}

// GetDatesMissingTopPost returns daily_sentiment dates on or after startDate
// that have no daily_top_post row, oldest first.
func (s *Store) GetDatesMissingTopPost(ctx context.Context, startDate string) ([]string, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT date FROM daily_sentiment
		 WHERE date >= ? AND date NOT IN (SELECT date FROM daily_top_post)
		 ORDER BY date ASC`, startDate)
	if err != nil {
		return nil, fmt.Errorf("query dates missing top post: %w", err)
	}
	defer rows.Close()
	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("scan date: %w", err)
		}
		dates = append(dates, d)
	}
	return dates, rows.Err()
}

// GetEarliestRunDate returns the YYYY-MM-DD of the oldest runs row, or ""
// when there are none.
func (s *Store) GetEarliestRunDate(ctx context.Context) (string, error) {
	var earliest sql.NullString
	if err := s.readDB.QueryRowContext(ctx, `SELECT MIN(created_at) FROM runs`).Scan(&earliest); err != nil {
		return "", fmt.Errorf("query earliest run: %w", err)
	}
	if !earliest.Valid || len(earliest.String) < 10 {
		return "", nil
	}
	return earliest.String[:10], nil
}

// PurgeReportRollups deletes topic_daily and daily_top_post rows older than
// olderThan and returns the total removed.
func (s *Store) PurgeReportRollups(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format("2006-01-02")
	var total int64
	for _, table := range []string{"topic_daily", "daily_top_post", "language_daily"} {
		result, err := s.writeDB.ExecContext(ctx, `DELETE FROM `+table+` WHERE date < ?`, cutoff)
		if err != nil {
			return total, fmt.Errorf("purge %s: %w", table, err)
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return total, nil
}
