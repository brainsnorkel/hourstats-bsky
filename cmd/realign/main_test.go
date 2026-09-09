package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/formatter"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// The seeded history: 40 cycles with no shadow score, then 35 with one, then a
// single row written by the post-switch binary (stock already set). The shadow
// deltas cycle through 1.7/1.8/1.9, so the expected S is 1.80.
const (
	preShadowRows   = 40
	shadowRows      = 35
	dailyRows       = 10
	expectedShift   = 1.80
	postSwitchRun   = "run-post-switch"
	postSwitchNet   = 12.34
	postSwitchStock = 10.5
)

// seed builds a database that looks like prod on the night of the switch and
// returns its path. Percentages are kept in the 8–14 range the bot actually
// produces: that is the range the +S/−S round trip of the root and reply
// columns is exact over.
func seed(t *testing.T) (string, time.Time) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "hourstats-test.db")
	db, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	base := time.Now().UTC().Add(-100 * time.Hour).Truncate(time.Second)
	write := func(dp store.SentimentDataPoint) {
		t.Helper()
		if err := db.StoreSentimentDataPoint(ctx, dp); err != nil {
			t.Fatalf("seed %s: %v", dp.RunID, err)
		}
	}

	for i := 0; i < preShadowRows; i++ {
		net := 8.5 + float64(i)*0.11
		write(store.SentimentDataPoint{
			RunID:                fmt.Sprintf("run-pre-%02d", i),
			Timestamp:            base.Add(time.Duration(i) * time.Hour),
			AverageCompoundScore: net / 100,
			NetSentimentPercent:  net,
			SentimentCategory:    "neutral",
			TotalPosts:           600 + i,
			RootSentimentPct:     net + 0.4,
			ReplySentimentPct:    net + 1.1,
		})
	}
	for i := 0; i < shadowRows; i++ {
		net := 9.0 + float64(i)*0.09
		emoji := net + []float64{1.7, 1.8, 1.9}[i%3]
		// Two of the shadow cycles are too small to count as a pair, and one
		// has a zero reply split (the "not computed" marker on old rows).
		posts := 700 + i
		reply := net + 1.1
		if i < 2 {
			posts = 120
		}
		if i == 5 {
			reply = 0
		}
		write(store.SentimentDataPoint{
			RunID:                fmt.Sprintf("run-shadow-%02d", i),
			Timestamp:            base.Add(time.Duration(preShadowRows+i) * time.Hour),
			AverageCompoundScore: net / 100,
			NetSentimentPercent:  net,
			SentimentCategory:    "neutral",
			TotalPosts:           posts,
			RootSentimentPct:     net + 0.4,
			ReplySentimentPct:    reply,
			NetSentimentPctEmoji: &emoji,
		})
	}

	stock := postSwitchStock
	emoji := postSwitchNet
	postSwitchTS := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Second)
	write(store.SentimentDataPoint{
		RunID:                postSwitchRun,
		Timestamp:            postSwitchTS,
		AverageCompoundScore: postSwitchNet / 100,
		NetSentimentPercent:  postSwitchNet,
		SentimentCategory:    "neutral",
		TotalPosts:           900,
		RootSentimentPct:     12.7,
		ReplySentimentPct:    13.9,
		NetSentimentPctEmoji: &emoji,
		NetSentimentPctStock: &stock,
	})

	day := time.Now().UTC().AddDate(0, 0, -dailyRows)
	for i := 0; i < dailyRows; i++ {
		avg := 9.6 + float64(i)*0.13
		if err := db.StoreDailySentiment(ctx, store.DailySentimentDataPoint{
			Date:             day.AddDate(0, 0, i).Format("2006-01-02"),
			RunID:            fmt.Sprintf("run-daily-%02d", i),
			AverageSentiment: avg,
			MinSentiment:     avg - 1.2,
			MaxSentiment:     avg + 1.4,
			Q1Sentiment:      avg - 0.6,
			MedianSentiment:  avg + 0.1,
			Q3Sentiment:      avg + 0.7,
			TotalRuns:        24,
			TotalPosts:       120000 + i,
		}); err != nil {
			t.Fatalf("seed daily %d: %v", i, err)
		}
	}
	return dbPath, postSwitchTS
}

func realign(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	var out strings.Builder
	full := append([]string{"-db", dbPath, "-backup-dir", filepath.Join(t.TempDir(), "backups")}, args...)
	err := run(context.Background(), full, &out, io.Discard)
	return out.String(), err
}

// dump renders a whole table as text, one line per row, using the shortest
// exact representation of every value. Two dumps are equal only if every
// stored value is bit-for-bit identical.
func dump(t *testing.T, dbPath, table, orderBy string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=query_only(ON)")
	if err != nil {
		t.Fatalf("open dump db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY ` + orderBy)
	if err != nil {
		t.Fatalf("dump %s: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("dump columns: %v", err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan dump row: %v", err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = fmt.Sprintf("%s=%T(%v)", cols[i], v, v)
		}
		out = append(out, strings.Join(parts, " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("dump rows: %v", err)
	}
	return out
}

func dumpAll(t *testing.T, dbPath string) []string {
	t.Helper()
	all := dump(t, dbPath, "sentiment_history", "timestamp, run_id")
	all = append(all, dump(t, dbPath, "daily_sentiment", "date")...)
	return append(all, dump(t, dbPath, "key_value", "key")...)
}

type historyRow struct {
	net      float64
	compound float64
	root     float64
	reply    float64
	emoji    sql.NullFloat64
	stock    sql.NullFloat64
}

func readHistory(t *testing.T, dbPath string) map[string]historyRow {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=query_only(ON)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT run_id, net_sentiment_percent, average_compound_score,
		root_sentiment_pct, reply_sentiment_pct, net_sentiment_pct_emoji, net_sentiment_pct_stock
		FROM sentiment_history`)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	defer rows.Close()
	out := map[string]historyRow{}
	for rows.Next() {
		var id string
		var h historyRow
		if err := rows.Scan(&id, &h.net, &h.compound, &h.root, &h.reply, &h.emoji, &h.stock); err != nil {
			t.Fatalf("scan history: %v", err)
		}
		out[id] = h
	}
	return out
}

func keyValues(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=query_only(ON)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT key, value FROM key_value`)
	if err != nil {
		t.Fatalf("read key_value: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan key_value: %v", err)
		}
		out[k] = v
	}
	return out
}

func TestDryRunReportsShiftAndTouchesNothing(t *testing.T) {
	dbPath, _ := seed(t)
	before := dumpAll(t, dbPath)

	out, err := realign(t, dbPath)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out, "+1.80 pt") {
		t.Errorf("dry run did not report S = +1.80:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d rows to realign", preShadowRows+shadowRows)) {
		t.Errorf("dry run did not report %d rows to realign:\n%s", preShadowRows+shadowRows, out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d with a shadow score", shadowRows)) {
		t.Errorf("dry run did not report %d shadow rows:\n%s", shadowRows, out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d rows before", dailyRows)) {
		t.Errorf("dry run did not report %d daily rows:\n%s", dailyRows, out)
	}
	if !strings.Contains(out, fmt.Sprintf("%+.2f pt (formatter.RealignShift)", formatter.RealignShift)) {
		t.Errorf("dry run did not report the expected shift:\n%s", out)
	}
	if !strings.Contains(out, "post-switch rows     1") {
		t.Errorf("dry run did not report the post-switch row:\n%s", out)
	}
	if !strings.Contains(out, "compound mismatches  0") {
		t.Errorf("dry run did not report the compound-score check:\n%s", out)
	}
	// Ten samples plus the header line.
	if got := strings.Count(out, "→"); got < 10 {
		t.Errorf("dry run printed %d before/after cells, want at least 10:\n%s", got, out)
	}
	if diff := diffDumps(before, dumpAll(t, dbPath)); diff != "" {
		t.Errorf("dry run modified the database: %s", diff)
	}
}

func TestApplyExactThenRevertRestoresEverything(t *testing.T) {
	dbPath, postSwitchTS := seed(t)
	before := dumpAll(t, dbPath)
	beforeHistory := readHistory(t, dbPath)

	if _, err := realign(t, dbPath, "-apply"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	after := readHistory(t, dbPath)
	for id, was := range beforeHistory {
		got := after[id]
		switch {
		case id == postSwitchRun:
			continue
		case was.emoji.Valid: // -shadow-rows exact: the emoji score becomes the headline
			assertFloat(t, id+" net", got.net, was.emoji.Float64)
			assertFloat(t, id+" compound", got.compound, was.emoji.Float64/100)
		default:
			assertFloat(t, id+" net", got.net, was.net+expectedShift)
			assertFloat(t, id+" compound", got.compound, (was.net+expectedShift)/100)
		}
		if !got.stock.Valid || got.stock.Float64 != was.net {
			t.Errorf("%s stock = %v, want the old headline %v", id, got.stock, was.net)
		}
		assertFloat(t, id+" root", got.root, was.root+expectedShift)
		if was.reply == 0 {
			// Zero means "not computed" on old rows and must stay zero.
			assertFloat(t, id+" reply", got.reply, 0)
		} else {
			assertFloat(t, id+" reply", got.reply, was.reply+expectedShift)
		}
	}

	// The row the post-switch binary wrote is outside the realigned set.
	if got, was := after[postSwitchRun], beforeHistory[postSwitchRun]; got != was {
		t.Errorf("post-switch row changed: %+v, want %+v", got, was)
	}

	kv := keyValues(t, dbPath)
	if kv[keyShift] != "1.80" {
		t.Errorf("%s = %q, want \"1.80\"", keyShift, kv[keyShift])
	}
	if kv[keyShadowRows] != shadowExact {
		t.Errorf("%s = %q, want %q", keyShadowRows, kv[keyShadowRows], shadowExact)
	}
	if _, err := time.Parse(time.RFC3339, kv[keyAppliedAt]); err != nil {
		t.Errorf("%s = %q, want an RFC3339 time: %v", keyAppliedAt, kv[keyAppliedAt], err)
	}
	if kv[keyRowCount] != fmt.Sprint(preShadowRows+shadowRows) {
		t.Errorf("%s = %q, want %d", keyRowCount, kv[keyRowCount], preShadowRows+shadowRows)
	}
	if _, ok := kv["sentiment_scorer_v2_since"]; ok {
		t.Error("realign wrote sentiment_scorer_v2_since; the bot records that at startup")
	}
	if kv[keyMaxTimestamp] == "" || kv[keyMaxTimestamp] >= postSwitchTS.Format(time.RFC3339) {
		t.Errorf("%s = %q, want the newest realigned row (before the post-switch row at %s)",
			keyMaxTimestamp, kv[keyMaxTimestamp], postSwitchTS.Format(time.RFC3339))
	}

	// daily_sentiment moved by S and the originals are preserved.
	assertDailyShifted(t, dbPath, expectedShift)

	// Idempotency: a second apply is refused, and refuses without writing.
	dumpAfterApply := dumpAll(t, dbPath)
	if _, err := realign(t, dbPath, "-apply"); err == nil {
		t.Fatal("second apply succeeded, want a refusal")
	} else if !strings.Contains(err.Error(), keyAppliedAt) {
		t.Errorf("second apply error = %v, want it to name %s", err, keyAppliedAt)
	}
	if diff := diffDumps(dumpAfterApply, dumpAll(t, dbPath)); diff != "" {
		t.Errorf("refused apply still modified the database: %s", diff)
	}

	if _, err := realign(t, dbPath, "-revert"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if diff := diffDumps(before, dumpAll(t, dbPath)); diff != "" {
		t.Errorf("revert did not restore the database exactly: %s", diff)
	}

	// And a revert with nothing applied is refused.
	if _, err := realign(t, dbPath, "-revert"); err == nil {
		t.Fatal("revert with no marker succeeded, want a refusal")
	}
}

func TestApplyShiftModeMovesShadowRowsByS(t *testing.T) {
	dbPath, _ := seed(t)
	beforeHistory := readHistory(t, dbPath)

	if _, err := realign(t, dbPath, "-apply", "-shadow-rows", shadowShift); err != nil {
		t.Fatalf("apply: %v", err)
	}

	after := readHistory(t, dbPath)
	shadowSeen := 0
	for id, was := range beforeHistory {
		if id == postSwitchRun {
			continue
		}
		if was.emoji.Valid {
			shadowSeen++
			if got := after[id]; got.net == was.emoji.Float64 && was.emoji.Float64 != was.net+expectedShift {
				t.Errorf("%s took the exact emoji value %v in shift mode", id, got.net)
			}
		}
		assertFloat(t, id+" net", after[id].net, was.net+expectedShift)
		assertFloat(t, id+" compound", after[id].compound, (was.net+expectedShift)/100)
	}
	if shadowSeen != shadowRows {
		t.Fatalf("checked %d shadow rows, want %d", shadowSeen, shadowRows)
	}
	if kv := keyValues(t, dbPath); kv[keyShadowRows] != shadowShift {
		t.Errorf("%s = %q, want %q", keyShadowRows, kv[keyShadowRows], shadowShift)
	}

	if _, err := realign(t, dbPath, "-revert"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	reverted := readHistory(t, dbPath)
	for id, was := range beforeHistory {
		if id == postSwitchRun {
			continue
		}
		assertFloat(t, id+" reverted net", reverted[id].net, was.net)
	}
}

func TestApplyRefusesWithoutEnoughPairs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sparse.db")
	db, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	ctx := context.Background()
	base := time.Now().UTC().Add(-40 * time.Hour)
	for i := 0; i < 10; i++ {
		emoji := 11.0 + float64(i)*0.1
		if err := db.StoreSentimentDataPoint(ctx, store.SentimentDataPoint{
			RunID:                fmt.Sprintf("run-%02d", i),
			Timestamp:            base.Add(time.Duration(i) * time.Hour),
			NetSentimentPercent:  9.5,
			TotalPosts:           800,
			NetSentimentPctEmoji: &emoji,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	db.Close()

	if _, err := realign(t, dbPath, "-apply"); err == nil {
		t.Fatal("apply with 10 pairs succeeded, want a refusal")
	} else if !strings.Contains(err.Error(), "paired cycles") {
		t.Errorf("error = %v, want it to mention the pair count", err)
	}
	if kv := keyValues(t, dbPath); len(kv) != 0 {
		t.Errorf("refused apply wrote markers: %v", kv)
	}
}

func TestParseArgsFlagSemantics(t *testing.T) {
	if _, err := parseArgs([]string{"-apply", "-revert"}, io.Discard); err == nil {
		t.Error("-apply -revert was accepted, want a refusal")
	}
	if _, err := parseArgs([]string{"-apply", "-dry-run=true"}, io.Discard); err == nil {
		t.Error("-apply with an explicit -dry-run was accepted, want a refusal")
	}
	if _, err := parseArgs([]string{"-shadow-rows", "average"}, io.Discard); err == nil {
		t.Error("an unknown -shadow-rows mode was accepted")
	}
	opt, err := parseArgs([]string{"-apply"}, io.Discard)
	if err != nil {
		t.Fatalf("-apply: %v", err)
	}
	if opt.mode != modeApply || opt.shadowRows != shadowExact {
		t.Errorf("options = %+v, want apply mode with exact shadow rows", opt)
	}
	if opt, err := parseArgs(nil, io.Discard); err != nil || opt.mode != modeDryRun {
		t.Errorf("default mode = %v (err %v), want dry-run", opt.mode, err)
	}
}

func assertFloat(t *testing.T, what string, got, want float64) {
	t.Helper()
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func assertDailyShifted(t *testing.T, dbPath string, shift float64) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=query_only(ON)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT d.date, d.average_sentiment, d.min_sentiment, d.max_sentiment,
		d.q1_sentiment, d.median_sentiment, d.q3_sentiment,
		p.average_sentiment, p.min_sentiment, p.max_sentiment, p.q1_sentiment, p.median_sentiment, p.q3_sentiment
		FROM daily_sentiment d JOIN ` + dailyPrealign + ` p ON p.date = d.date`)
	if err != nil {
		t.Fatalf("join prealign: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var date string
		var now, was [6]float64
		if err := rows.Scan(&date, &now[0], &now[1], &now[2], &now[3], &now[4], &now[5],
			&was[0], &was[1], &was[2], &was[3], &was[4], &was[5]); err != nil {
			t.Fatalf("scan daily: %v", err)
		}
		for i := range now {
			assertFloat(t, fmt.Sprintf("daily %s col %d", date, i), now[i], was[i]+shift)
		}
		seen++
	}
	if seen != dailyRows {
		t.Errorf("compared %d daily rows, want %d", seen, dailyRows)
	}
}

func diffDumps(want, got []string) string {
	if len(want) != len(got) {
		return fmt.Sprintf("row count %d, want %d", len(got), len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			return fmt.Sprintf("row %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
	return ""
}

// execSQL runs a statement against the database outside the tool, for tests
// that need to bend the fixture into a state the tool must refuse.
func execSQL(t *testing.T, dbPath, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func TestApplyRefusesWhenShiftDriftsFromThresholds(t *testing.T) {
	dbPath, _ := seed(t)
	before := dumpAll(t, dbPath)

	// The seeded S is 1.80; an expectation of 1.00 is well outside 0.125.
	_, err := realign(t, dbPath, "-apply", "-expect-shift", "1.0")
	if err == nil {
		t.Fatal("apply succeeded despite the shift drift, want a refusal")
	}
	if !strings.Contains(err.Error(), "RealignShift") {
		t.Errorf("error = %v, want it to name the threshold constant", err)
	}
	if diff := diffDumps(before, dumpAll(t, dbPath)); diff != "" {
		t.Errorf("refused apply modified the database: %s", diff)
	}

	// The dry run reports the same problem without failing.
	out, err := realign(t, dbPath, "-expect-shift", "1.0")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out, "-apply would refuse") {
		t.Errorf("dry run did not flag the drift:\n%s", out)
	}

	// -force proceeds anyway and says so.
	out, err = realign(t, dbPath, "-apply", "-expect-shift", "1.0", "-force")
	if err != nil {
		t.Fatalf("forced apply: %v", err)
	}
	if !strings.Contains(out, "WARNING (-force)") {
		t.Errorf("forced apply did not warn:\n%s", out)
	}
}

func TestApplyRefusesUntilTheNewBinaryHasWritten(t *testing.T) {
	dbPath, _ := seed(t)
	execSQL(t, dbPath, `DELETE FROM sentiment_history WHERE run_id = ?`, postSwitchRun)

	_, err := realign(t, dbPath, "-apply")
	if err == nil {
		t.Fatal("apply succeeded with no post-switch row, want a refusal")
	}
	if !strings.Contains(err.Error(), "analysis cycle") {
		t.Errorf("error = %v, want it to name the deploy step", err)
	}
	if kv := keyValues(t, dbPath); len(kv) != 0 {
		t.Errorf("refused apply wrote markers: %v", kv)
	}

	if _, err := realign(t, dbPath, "-apply", "-force"); err != nil {
		t.Fatalf("forced apply: %v", err)
	}
}

func TestApplyRefusesOnCompoundScoreDrift(t *testing.T) {
	dbPath, _ := seed(t)
	execSQL(t, dbPath, `UPDATE sentiment_history SET average_compound_score = 0.5 WHERE run_id = 'run-pre-00'`)

	_, err := realign(t, dbPath, "-apply")
	if err == nil {
		t.Fatal("apply succeeded with a compound-score mismatch, want a refusal")
	}
	if !strings.Contains(err.Error(), "average_compound_score") {
		t.Errorf("error = %v, want it to name the column", err)
	}

	// Forced, the snapshot still restores the odd value exactly.
	before := dumpAll(t, dbPath)
	if _, err := realign(t, dbPath, "-apply", "-force"); err != nil {
		t.Fatalf("forced apply: %v", err)
	}
	if _, err := realign(t, dbPath, "-revert"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if diff := diffDumps(before, dumpAll(t, dbPath)); diff != "" {
		t.Errorf("revert did not restore the odd compound score: %s", diff)
	}
}

func TestRevertUnshiftsDailyRowsWrittenAfterApply(t *testing.T) {
	dbPath, _ := seed(t)
	before := dumpAll(t, dbPath)
	if _, err := realign(t, dbPath, "-apply"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The daily job runs after the apply and writes a fresh row from already
	// realigned history, so it carries the shift and has no snapshot row.
	today := time.Now().UTC().Format("2006-01-02")
	execSQL(t, dbPath, `INSERT INTO daily_sentiment
		(date, run_id, average_sentiment, min_sentiment, max_sentiment, q1_sentiment, median_sentiment, q3_sentiment, total_runs, total_posts, created_at, ttl)
		VALUES (?, 'run-after', 12.5, 11.5, 13.5, 12.0, 12.4, 13.0, 24, 130000, '2026-09-11T00:00:00Z', 0)`, today)

	out, err := realign(t, dbPath, "-revert")
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if !strings.Contains(out, "1 written since the apply shifted back") {
		t.Errorf("revert did not report the post-apply daily row:\n%s", out)
	}

	var avg, min float64
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=query_only(ON)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT average_sentiment, min_sentiment FROM daily_sentiment WHERE run_id = 'run-after'`).
		Scan(&avg, &min); err != nil {
		t.Fatalf("read post-apply row: %v", err)
	}
	assertFloat(t, "post-apply average", avg, 12.5-expectedShift)
	assertFloat(t, "post-apply min", min, 11.5-expectedShift)

	// Every row that existed before the apply is still byte-identical.
	execSQL(t, dbPath, `DELETE FROM daily_sentiment WHERE run_id = 'run-after'`)
	if diff := diffDumps(before, dumpAll(t, dbPath)); diff != "" {
		t.Errorf("revert did not restore the pre-apply rows: %s", diff)
	}
}

func TestRevertRefusesOnIncompleteSnapshot(t *testing.T) {
	dbPath, _ := seed(t)
	if _, err := realign(t, dbPath, "-apply"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	execSQL(t, dbPath, `DELETE FROM `+historyPrealign+` WHERE run_id = 'run-pre-00'`)

	_, err := realign(t, dbPath, "-revert")
	if err == nil {
		t.Fatal("revert succeeded with a truncated snapshot, want a refusal")
	}
	if !strings.Contains(err.Error(), keyRowCount) {
		t.Errorf("error = %v, want it to name %s", err, keyRowCount)
	}
	// The refusal rolled back: the snapshot tables and markers are still there.
	if kv := keyValues(t, dbPath); kv[keyAppliedAt] == "" {
		t.Error("refused revert removed the markers")
	}
}

func TestSnapshotTablesGoneAfterRevert(t *testing.T) {
	dbPath, _ := seed(t)
	if _, err := realign(t, dbPath, "-apply"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, table := range []string{historyPrealign, dailyPrealign} {
		if !tableExists(t, dbPath, table) {
			t.Errorf("%s missing after apply", table)
		}
	}
	if _, err := realign(t, dbPath, "-revert"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	for _, table := range []string{historyPrealign, dailyPrealign} {
		if tableExists(t, dbPath, table) {
			t.Errorf("%s still present after revert", table)
		}
	}
}

func TestRunRefusesMissingDatabase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.db")
	if _, err := realign(t, missing); err == nil {
		t.Fatal("dry run on a missing database succeeded, want a refusal")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("the run created the database it was pointed at")
	}
}

func tableExists(t *testing.T, dbPath, table string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=query_only(ON)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
		t.Fatalf("look up %s: %v", table, err)
	}
	return n > 0
}
