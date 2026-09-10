// Command realign is the one-off admin tool for the 2026-09-11 switch of the
// headline sentiment scorer from stock VADER to the emoji-aware analyzer.
//
// The new binary writes net_sentiment_pct_stock on every cycle, so a row with
// that column NULL is by definition a row written before the switch. Those are
// the rows realign moves onto the new scale: their old headline is preserved in
// net_sentiment_pct_stock and their net sentiment becomes either the exact
// emoji-aware score they already carry (-shadow-rows exact) or the old value
// plus the constant shift S (-shadow-rows shift). S is computed here, from the
// cycles that carry both scores, checked against formatter.RealignShift (the
// value the mood-word thresholds were derived from) and recorded in key_value.
//
// Every value the rewrite overwrites is copied first into
// sentiment_history_prealign and daily_sentiment_prealign, so -revert restores
// the originals rather than reversing arithmetic.
//
// Intended invocation on prod:
//
//	fly ssh console -a hourstats-prod -C "realign -dry-run"
//	fly ssh console -a hourstats-prod -C "realign -apply"
//
// See docs/SENTIMENT_REALIGNMENT_PLAN.md.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/formatter"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// key_value markers written by -apply and removed by -revert.
const (
	keyShift      = "sentiment_realign_shift"
	keyAppliedAt  = "sentiment_realign_applied_at"
	keyShadowRows = "sentiment_realign_shadow_rows"
	// keyRowCount is the number of sentiment_history rows realigned. -revert
	// refuses to run unless the snapshot table still holds exactly this many
	// rows, so a truncated or partially restored snapshot cannot be applied.
	keyRowCount = "sentiment_realign_row_count"
	// keyMaxTimestamp is informational: the newest cycle the rewrite touched,
	// which is the boundary between the old and new scale in the series.
	keyMaxTimestamp = "sentiment_realign_max_timestamp"
)

var realignKeys = []string{keyShift, keyAppliedAt, keyShadowRows, keyRowCount, keyMaxTimestamp}

const (
	shadowExact = "exact"
	shadowShift = "shift"

	// pairMinPosts is the window size below which a cycle is too small to
	// contribute a trustworthy pair; it matches the analysis cycle's own
	// low-confidence floor.
	pairMinPosts = 500
	// minPairs is the smallest number of paired cycles the shift may be
	// averaged over.
	minPairs = 30
	// maxShiftDrift is how far the computed S may sit from the shift the mood
	// word thresholds were built on before -apply refuses: half of the 0.25
	// grid those thresholds are rounded to.
	maxShiftDrift = 0.125

	historyPrealign = "sentiment_history_prealign"
	dailyPrealign   = "daily_sentiment_prealign"
)

// prealignColumns are the sentiment_history columns the rewrite overwrites,
// snapshotted so -revert can put them back verbatim.
const prealignColumns = `run_id, timestamp, net_sentiment_percent, average_compound_score, root_sentiment_pct, reply_sentiment_pct`

type mode string

const (
	modeDryRun mode = "dry-run"
	modeApply  mode = "apply"
	modeRevert mode = "revert"
)

type options struct {
	dbPath      string
	mode        mode
	shadowRows  string
	backupDir   string
	profile     string
	expectShift float64
	force       bool
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.Error("realign failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	opt, err := parseArgs(args, errOut)
	if err != nil {
		return err
	}
	// store.New would happily create an empty database, which on the Fly
	// volume looks exactly like a wiped history.
	if _, err := os.Stat(opt.dbPath); err != nil {
		return fmt.Errorf("database %s: %w", opt.dbPath, err)
	}

	db, err := store.New(opt.dbPath)
	if err != nil {
		return fmt.Errorf("open database %s: %w", opt.dbPath, err)
	}
	defer db.Close()
	slog.Info("database opened", "path", opt.dbPath, "mode", string(opt.mode), "shadow_rows", opt.shadowRows)

	r := &realigner{db: db, out: out, opt: opt}
	switch opt.mode {
	case modeApply:
		return r.apply(ctx)
	case modeRevert:
		return r.revert(ctx)
	default:
		return r.dryRun(ctx)
	}
}

func parseArgs(args []string, errOut io.Writer) (options, error) {
	profile := envOr("HOURSTATS_PROFILE", "staging")
	dataDir := envOr("DATA_DIR", "/data")

	fs := flag.NewFlagSet("realign", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dbPath := fs.String("db", fmt.Sprintf("%s/hourstats-%s.db", dataDir, profile), "path to the SQLite database")
	dryRun := fs.Bool("dry-run", true, "print what would change and exit (the default)")
	apply := fs.Bool("apply", false, "realign the history (overrides -dry-run)")
	revert := fs.Bool("revert", false, "undo a previous -apply (overrides -dry-run)")
	shadowRows := fs.String("shadow-rows", shadowExact,
		"how to move rows that already carry an emoji-aware score: exact (use it) or shift (add S like every other row)")
	backupDir := fs.String("backup-dir", filepath.Join(dataDir, "backups"), "directory for the backup taken before -apply and -revert")
	expectShift := fs.Float64("expect-shift", formatter.RealignShift,
		"the shift the mood-word thresholds were built on; -apply refuses if the computed S differs by more than 0.125")
	force := fs.Bool("force", false, "proceed despite the shift, deploy and compound-score preflight checks (never bypasses the applied-at marker)")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	explicitDryRun := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "dry-run" {
			explicitDryRun = true
		}
	})

	switch {
	case *apply && *revert:
		return options{}, errors.New("-apply and -revert are mutually exclusive")
	case explicitDryRun && *dryRun && (*apply || *revert):
		return options{}, errors.New("-dry-run was passed explicitly alongside -apply/-revert; refusing to write")
	}
	if *shadowRows != shadowExact && *shadowRows != shadowShift {
		return options{}, fmt.Errorf("-shadow-rows must be %q or %q, got %q", shadowExact, shadowShift, *shadowRows)
	}

	opt := options{
		dbPath:      *dbPath,
		mode:        modeDryRun,
		shadowRows:  *shadowRows,
		backupDir:   *backupDir,
		profile:     profile,
		expectShift: *expectShift,
		force:       *force,
	}
	if *apply {
		opt.mode = modeApply
	}
	if *revert {
		opt.mode = modeRevert
	}
	return opt, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type realigner struct {
	db  *store.Store
	out io.Writer
	opt options
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// shift returns the mean of (emoji-aware minus stock) over the paired cycles
// that are still pre-switch, rounded to two decimals, and the pair count.
func (r *realigner) shift(ctx context.Context) (float64, int, error) {
	var pairs int
	var mean sql.NullFloat64
	err := r.db.DB().QueryRowContext(ctx,
		`SELECT COUNT(*), AVG(net_sentiment_pct_emoji - net_sentiment_percent)
		 FROM sentiment_history
		 WHERE net_sentiment_pct_emoji IS NOT NULL
		   AND net_sentiment_pct_stock IS NULL
		   AND total_posts >= ?`, pairMinPosts).Scan(&pairs, &mean)
	if err != nil {
		return 0, 0, fmt.Errorf("compute shift: %w", err)
	}
	if pairs < minPairs {
		return 0, pairs, fmt.Errorf("only %d paired cycles (>= %d posts, no stock score); need at least %d",
			pairs, pairMinPosts, minPairs)
	}
	return math.Round(mean.Float64*100) / 100, pairs, nil
}

type counts struct {
	rows        int // pre-switch sentiment_history rows
	withShadow  int // ... of which already carry an emoji-aware score
	dailyRows   int
	postSwitch  int // rows the emoji-headline build has already written
	compoundOff int // rows where average_compound_score != net/100
}

func (r *realigner) counts(ctx context.Context, today string) (counts, error) {
	var c counts
	err := r.db.DB().QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(net_sentiment_pct_emoji IS NOT NULL), 0)
		 FROM sentiment_history WHERE net_sentiment_pct_stock IS NULL`).Scan(&c.rows, &c.withShadow)
	if err != nil {
		return c, fmt.Errorf("count sentiment rows: %w", err)
	}
	if err := r.db.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM daily_sentiment WHERE date < ?`, today).Scan(&c.dailyRows); err != nil {
		return c, fmt.Errorf("count daily rows: %w", err)
	}
	if err := r.db.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sentiment_history WHERE net_sentiment_pct_stock IS NOT NULL`).Scan(&c.postSwitch); err != nil {
		return c, fmt.Errorf("count post-switch rows: %w", err)
	}
	if err := r.db.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sentiment_history
		 WHERE ABS(average_compound_score - net_sentiment_percent / 100.0) > 1e-9`).Scan(&c.compoundOff); err != nil {
		return c, fmt.Errorf("count compound mismatches: %w", err)
	}
	return c, nil
}

// sampleRow is one sentiment_history row as the dry run prints it.
type sampleRow struct {
	runID     string
	timestamp string
	net       float64
	compound  float64
	root      float64
	reply     float64
	emoji     sql.NullFloat64
}

const sampleColumns = `run_id, timestamp, net_sentiment_percent, average_compound_score, root_sentiment_pct, reply_sentiment_pct, net_sentiment_pct_emoji`

func (r *realigner) querySamples(ctx context.Context, query string, args ...any) ([]sampleRow, error) {
	rows, err := r.db.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query samples: %w", err)
	}
	defer rows.Close()
	var out []sampleRow
	for rows.Next() {
		var s sampleRow
		if err := rows.Scan(&s.runID, &s.timestamp, &s.net, &s.compound, &s.root, &s.reply, &s.emoji); err != nil {
			return nil, fmt.Errorf("scan sample: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// samples picks ten pre-switch rows: the three oldest, the three newest, and
// four straddling the point where the emoji-aware shadow score starts.
func (r *realigner) samples(ctx context.Context) ([]sampleRow, error) {
	const base = `SELECT ` + sampleColumns + ` FROM sentiment_history WHERE net_sentiment_pct_stock IS NULL`

	oldest, err := r.querySamples(ctx, base+` ORDER BY timestamp ASC LIMIT 3`)
	if err != nil {
		return nil, err
	}
	newest, err := r.querySamples(ctx, base+` ORDER BY timestamp DESC LIMIT 3`)
	if err != nil {
		return nil, err
	}
	picked := append(oldest, newest...)

	var boundary sql.NullString
	if err := r.db.DB().QueryRowContext(ctx,
		`SELECT MIN(timestamp) FROM sentiment_history
		 WHERE net_sentiment_pct_stock IS NULL AND net_sentiment_pct_emoji IS NOT NULL`).Scan(&boundary); err != nil {
		return nil, fmt.Errorf("find shadow boundary: %w", err)
	}
	if boundary.Valid {
		before, err := r.querySamples(ctx, base+` AND timestamp < ? ORDER BY timestamp DESC LIMIT 2`, boundary.String)
		if err != nil {
			return nil, err
		}
		after, err := r.querySamples(ctx, base+` AND timestamp >= ? ORDER BY timestamp ASC LIMIT 2`, boundary.String)
		if err != nil {
			return nil, err
		}
		picked = append(picked, before...)
		picked = append(picked, after...)
	}

	seen := make(map[string]bool, len(picked))
	var unique []sampleRow
	for _, s := range picked {
		key := s.runID + "\x00" + s.timestamp
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, s)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].timestamp < unique[j].timestamp })
	if len(unique) > 10 {
		unique = unique[:10]
	}
	return unique, nil
}

// after returns the values the row would carry once realigned.
func (s sampleRow) after(shift float64, shadowRows string) (net, compound, root, reply float64) {
	net = s.net + shift
	if shadowRows == shadowExact && s.emoji.Valid {
		net = s.emoji.Float64
	}
	compound = net / 100
	root, reply = s.root, s.reply
	if root != 0 {
		root += shift
	}
	if reply != 0 {
		reply += shift
	}
	return net, compound, root, reply
}

func (r *realigner) marker(ctx context.Context, key string) (string, bool, error) {
	entry, err := r.db.GetKeyValueWithTimestamp(ctx, key)
	if err != nil {
		return "", false, err
	}
	if entry == nil {
		return "", false, nil
	}
	return entry.Value, true, nil
}

// preflight is the set of conditions -apply insists on. Each one is a warning
// under -force and a refusal without it; the applied-at marker is checked
// separately and -force never bypasses it.
func (r *realigner) preflight(shift float64, c counts) error {
	var problems []error
	if drift := math.Abs(shift - r.opt.expectShift); drift > maxShiftDrift {
		problems = append(problems, fmt.Errorf(
			"computed S %+.2f is %.2f away from the expected %+.2f (formatter.RealignShift); the mood-word thresholds would no longer match the realigned series",
			shift, drift, r.opt.expectShift))
	}
	if c.postSwitch == 0 {
		problems = append(problems, errors.New(
			"no sentiment_history row carries net_sentiment_pct_stock: the emoji-headline build has not completed a cycle yet. Deploy it and wait for one analysis cycle (prod: the :55 run) before applying"))
	}
	if c.compoundOff > 0 {
		problems = append(problems, fmt.Errorf(
			"%d rows have average_compound_score out of step with net_sentiment_percent/100; the rewrite normalises them and the snapshot restores them, but check them first", c.compoundOff))
	}
	if len(problems) == 0 {
		return nil
	}
	if r.opt.force {
		for _, p := range problems {
			slog.Warn("preflight check overridden by -force", "problem", p.Error())
			fmt.Fprintf(r.out, "WARNING (-force): %v\n", p)
		}
		return nil
	}
	return fmt.Errorf("preflight failed (pass -force to proceed anyway): %w", errors.Join(problems...))
}

// ---------------------------------------------------------------------------
// Dry run
// ---------------------------------------------------------------------------

func (r *realigner) dryRun(ctx context.Context) error {
	appliedAt, applied, err := r.marker(ctx, keyAppliedAt)
	if err != nil {
		return err
	}
	if applied {
		fmt.Fprintf(r.out, "NOTE: %s = %s — the history is already realigned; -apply would refuse.\n\n", keyAppliedAt, appliedAt)
	}

	shift, pairs, err := r.shift(ctx)
	if err != nil {
		return err
	}
	today := time.Now().UTC().Format("2006-01-02")
	c, err := r.counts(ctx, today)
	if err != nil {
		return err
	}

	fmt.Fprintf(r.out, "realign dry run (%s)\n", r.opt.dbPath)
	fmt.Fprintf(r.out, "  shift S              %+.2f pt (mean emoji-aware minus stock over %d paired cycles)\n", shift, pairs)
	fmt.Fprintf(r.out, "  expected S           %+.2f pt (formatter.RealignShift), difference %.2f, tolerance %.3f\n",
		r.opt.expectShift, math.Abs(shift-r.opt.expectShift), maxShiftDrift)
	fmt.Fprintf(r.out, "  shadow rows mode     %s\n", r.opt.shadowRows)
	fmt.Fprintf(r.out, "  sentiment_history    %d rows to realign (%d with a shadow score, %d without)\n",
		c.rows, c.withShadow, c.rows-c.withShadow)
	fmt.Fprintf(r.out, "  post-switch rows     %d (written by the emoji-headline build, left alone)\n", c.postSwitch)
	fmt.Fprintf(r.out, "  compound mismatches  %d rows where average_compound_score != net/100\n", c.compoundOff)
	fmt.Fprintf(r.out, "  daily_sentiment      %d rows before %s\n\n", c.dailyRows, today)

	if err := r.preflight(shift, c); err != nil {
		fmt.Fprintf(r.out, "\n-apply would refuse: %v\n", err)
	}

	samples, err := r.samples(ctx)
	if err != nil {
		return err
	}
	// The arrow is three bytes wide, so the data cells below are padded to a
	// fixed 13 visible columns rather than by %-width.
	fmt.Fprintf(r.out, "%-22s %-8s %13s %13s %13s %13s\n", "timestamp", "shadow", "net", "compound", "root", "reply")
	for _, s := range samples {
		net, compound, root, reply := s.after(shift, r.opt.shadowRows)
		shadow := "no"
		if s.emoji.Valid {
			shadow = "yes"
		}
		fmt.Fprintf(r.out, "%-22s %-8s %6.2f→%6.2f %6.3f→%6.3f %6.2f→%6.2f %6.2f→%6.2f\n",
			s.timestamp, shadow, s.net, net, s.compound, compound, s.root, root, s.reply, reply)
	}
	fmt.Fprintf(r.out, "\nnothing was written. Re-run with -apply to realign.\n")
	return nil
}

// ---------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------

func (r *realigner) apply(ctx context.Context) error {
	if appliedAt, applied, err := r.marker(ctx, keyAppliedAt); err != nil {
		return err
	} else if applied {
		return fmt.Errorf("%s is already set (%s); the history has been realigned already", keyAppliedAt, appliedAt)
	}

	shift, pairs, err := r.shift(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	appliedAt := now.Format(time.RFC3339)

	c, err := r.counts(ctx, today)
	if err != nil {
		return err
	}
	if err := r.preflight(shift, c); err != nil {
		return err
	}

	// The backup is the fallback if anything below is wrong, so a failure to
	// take it stops the run before a single row moves.
	backupPath, err := r.backup(ctx, "prerealign")
	if err != nil {
		return err
	}
	slog.Info("pre-realign backup written", "path", backupPath)

	var rowCount int
	var beforeMean, afterMean float64
	var maxTimestamp string
	var dailyUpdated int64

	err = r.db.WriteTx(ctx, func(tx store.TxExecer) error {
		// Snapshot first: it is both the restore source for -revert and the
		// definition of the row set everything below operates on.
		if err := snapshotHistory(ctx, tx); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*), COALESCE(AVG(net_sentiment_percent), 0), COALESCE(MAX(timestamp), '')
			 FROM `+historyPrealign).Scan(&rowCount, &beforeMean, &maxTimestamp); err != nil {
			return fmt.Errorf("snapshot stats: %w", err)
		}
		if rowCount == 0 {
			return errors.New("no pre-switch rows found (every sentiment_history row already has a stock score)")
		}
		if err := snapshotDaily(ctx, tx); err != nil {
			return err
		}

		netExpr := `(net_sentiment_percent + ?)`
		if r.opt.shadowRows == shadowExact {
			netExpr = `COALESCE(net_sentiment_pct_emoji, net_sentiment_percent + ?)`
		}
		// One statement: SQLite evaluates every right-hand side against the
		// original row, so the stock column keeps the pre-realignment headline
		// even though the same statement overwrites it.
		if _, err := tx.ExecContext(ctx,
			`UPDATE sentiment_history SET
				net_sentiment_pct_stock = net_sentiment_percent,
				net_sentiment_percent = `+netExpr+`,
				average_compound_score = (`+netExpr+`) / 100.0,
				root_sentiment_pct = CASE WHEN root_sentiment_pct != 0 THEN root_sentiment_pct + ? ELSE root_sentiment_pct END,
				reply_sentiment_pct = CASE WHEN reply_sentiment_pct != 0 THEN reply_sentiment_pct + ? ELSE reply_sentiment_pct END
			 WHERE `+snapshotPredicate,
			shift, shift, shift, shift); err != nil {
			return fmt.Errorf("realign sentiment_history: %w", err)
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE daily_sentiment SET
				average_sentiment = average_sentiment + ?,
				min_sentiment = min_sentiment + ?,
				max_sentiment = max_sentiment + ?,
				q1_sentiment = q1_sentiment + ?,
				median_sentiment = median_sentiment + ?,
				q3_sentiment = q3_sentiment + ?
			 WHERE date < ?`,
			shift, shift, shift, shift, shift, shift, today)
		if err != nil {
			return fmt.Errorf("realign daily_sentiment: %w", err)
		}
		dailyUpdated, _ = res.RowsAffected()

		for _, kv := range [][2]string{
			{keyShift, strconv.FormatFloat(shift, 'f', 2, 64)},
			{keyAppliedAt, appliedAt},
			{keyShadowRows, r.opt.shadowRows},
			{keyRowCount, strconv.Itoa(rowCount)},
			{keyMaxTimestamp, maxTimestamp},
		} {
			if err := setKeyValue(ctx, tx, kv[0], kv[1]); err != nil {
				return err
			}
		}

		afterMean, err = snapshotJoinedMean(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}

	slog.Info("realign applied",
		"shift", shift,
		"pairs", pairs,
		"shadow_rows", r.opt.shadowRows,
		"sentiment_rows", rowCount,
		"daily_rows", dailyUpdated,
		"applied_at", appliedAt,
	)
	fmt.Fprintf(r.out, "realign applied (%s)\n", r.opt.dbPath)
	fmt.Fprintf(r.out, "  shift S              %+.2f pt over %d paired cycles, shadow rows %s\n", shift, pairs, r.opt.shadowRows)
	fmt.Fprintf(r.out, "  backup               %s\n", backupPath)
	fmt.Fprintf(r.out, "  sentiment_history    %d rows, mean net %.2f → %.2f (originals in %s)\n",
		rowCount, beforeMean, afterMean, historyPrealign)
	fmt.Fprintf(r.out, "  daily_sentiment      %d rows before %s (originals in %s)\n", dailyUpdated, today, dailyPrealign)
	fmt.Fprintf(r.out, "  %s   %s\n", keyAppliedAt, appliedAt)
	return nil
}

// backup writes a copy of the essential tables — which include both prealign
// tables — before a mutating run. store.Backup places the file in a "backups"
// subdirectory of the directory it is given, so a -backup-dir that already ends
// in "backups" is passed as its parent to keep the file exactly where the flag
// says.
func (r *realigner) backup(ctx context.Context, label string) (string, error) {
	dir := r.opt.backupDir
	if filepath.Base(dir) == "backups" {
		dir = filepath.Dir(dir)
	}
	// store.Backup creates missing directories, so a mistyped -backup-dir
	// would silently land the only pre-write copy on the container's
	// ephemeral root filesystem (seen in the staging rehearsal). Insist that
	// the parent already exists, which on Fly means it is on the volume.
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("backup dir %s must already exist (refusing to create it): %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("backup dir %s is not a directory", dir)
	}
	// A large retention keeps this admin run from pruning the daily backups.
	path, err := r.db.Backup(ctx, dir, r.opt.profile+"-"+label, 3650)
	if err != nil {
		return "", fmt.Errorf("backup before %s: %w", label, err)
	}
	return path, nil
}

// snapshotPredicate matches the rows captured in the history snapshot.
const snapshotPredicate = `EXISTS (SELECT 1 FROM ` + historyPrealign + ` p
	WHERE p.run_id = sentiment_history.run_id AND p.timestamp = sentiment_history.timestamp)`

func snapshotHistory(ctx context.Context, tx store.TxExecer) error {
	if _, err := tx.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS `+historyPrealign+` (
			run_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			net_sentiment_percent REAL NOT NULL,
			average_compound_score REAL NOT NULL,
			root_sentiment_pct REAL NOT NULL,
			reply_sentiment_pct REAL NOT NULL,
			PRIMARY KEY (run_id, timestamp)
		)`); err != nil {
		return fmt.Errorf("create %s: %w", historyPrealign, err)
	}
	if err := requireEmpty(ctx, tx, historyPrealign); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+historyPrealign+` (`+prealignColumns+`)
		 SELECT `+prealignColumns+` FROM sentiment_history WHERE net_sentiment_pct_stock IS NULL`); err != nil {
		return fmt.Errorf("snapshot sentiment_history: %w", err)
	}
	return nil
}

func snapshotDaily(ctx context.Context, tx store.TxExecer) error {
	if _, err := tx.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS `+dailyPrealign+` AS SELECT * FROM daily_sentiment WHERE 0`); err != nil {
		return fmt.Errorf("create %s: %w", dailyPrealign, err)
	}
	if err := requireEmpty(ctx, tx, dailyPrealign); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+dailyPrealign+` SELECT * FROM daily_sentiment`); err != nil {
		return fmt.Errorf("snapshot daily_sentiment: %w", err)
	}
	return nil
}

func requireEmpty(ctx context.Context, tx store.TxExecer, table string) error {
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&existing); err != nil {
		return fmt.Errorf("count %s: %w", table, err)
	}
	if existing > 0 {
		return fmt.Errorf("%s already holds %d rows; a previous apply was not reverted", table, existing)
	}
	return nil
}

// snapshotJoinedMean is the mean net sentiment of the live rows the snapshot
// covers — the same row set before and after the rewrite.
func snapshotJoinedMean(ctx context.Context, tx store.TxExecer) (float64, error) {
	var mean sql.NullFloat64
	err := tx.QueryRowContext(ctx,
		`SELECT AVG(h.net_sentiment_percent) FROM sentiment_history h
		 JOIN `+historyPrealign+` p ON p.run_id = h.run_id AND p.timestamp = h.timestamp`).Scan(&mean)
	if err != nil {
		return 0, fmt.Errorf("joined mean: %w", err)
	}
	return mean.Float64, nil
}

// ---------------------------------------------------------------------------
// Revert
// ---------------------------------------------------------------------------

func (r *realigner) revert(ctx context.Context) error {
	appliedAt, applied, err := r.marker(ctx, keyAppliedAt)
	if err != nil {
		return err
	}
	if !applied {
		return fmt.Errorf("%s is not set; there is nothing to revert", keyAppliedAt)
	}
	shiftStr, ok, err := r.marker(ctx, keyShift)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s is missing; cannot undo the shift on daily rows written since the apply", keyShift)
	}
	shift, err := strconv.ParseFloat(shiftStr, 64)
	if err != nil {
		return fmt.Errorf("%s = %q: %w", keyShift, shiftStr, err)
	}
	countStr, ok, err := r.marker(ctx, keyRowCount)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s is missing; cannot verify the snapshot is complete", keyRowCount)
	}
	wantRows, err := strconv.Atoi(countStr)
	if err != nil {
		return fmt.Errorf("%s = %q: %w", keyRowCount, countStr, err)
	}

	backupPath, err := r.backup(ctx, "prerevert")
	if err != nil {
		return err
	}
	slog.Info("pre-revert backup written", "path", backupPath)

	var rowsReverted, dailyRestored, dailyUnshifted int64
	var beforeMean, afterMean float64

	err = r.db.WriteTx(ctx, func(tx store.TxExecer) error {
		var haveRows int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+historyPrealign).Scan(&haveRows); err != nil {
			return fmt.Errorf("count %s: %w", historyPrealign, err)
		}
		if haveRows != wantRows {
			return fmt.Errorf("%s holds %d rows but %s says %d were realigned; refusing a partial restore",
				historyPrealign, haveRows, keyRowCount, wantRows)
		}

		var err error
		if beforeMean, err = snapshotJoinedMean(ctx, tx); err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE sentiment_history SET
				net_sentiment_percent = (SELECT p.net_sentiment_percent FROM `+historyPrealign+` p
					WHERE p.run_id = sentiment_history.run_id AND p.timestamp = sentiment_history.timestamp),
				average_compound_score = (SELECT p.average_compound_score FROM `+historyPrealign+` p
					WHERE p.run_id = sentiment_history.run_id AND p.timestamp = sentiment_history.timestamp),
				root_sentiment_pct = (SELECT p.root_sentiment_pct FROM `+historyPrealign+` p
					WHERE p.run_id = sentiment_history.run_id AND p.timestamp = sentiment_history.timestamp),
				reply_sentiment_pct = (SELECT p.reply_sentiment_pct FROM `+historyPrealign+` p
					WHERE p.run_id = sentiment_history.run_id AND p.timestamp = sentiment_history.timestamp),
				net_sentiment_pct_stock = NULL
			 WHERE `+snapshotPredicate)
		if err != nil {
			return fmt.Errorf("restore sentiment_history: %w", err)
		}
		rowsReverted, _ = res.RowsAffected()
		if afterMean, err = snapshotJoinedMean(ctx, tx); err != nil {
			return err
		}

		// Daily rows written since the apply were aggregated from realigned
		// history, so they carry the shift and have no snapshot row of their
		// own. The daily job only ever rebuilds yesterday, so nothing else
		// would ever take it back off them.
		res, err = tx.ExecContext(ctx,
			`UPDATE daily_sentiment SET
				average_sentiment = average_sentiment - ?,
				min_sentiment = min_sentiment - ?,
				max_sentiment = max_sentiment - ?,
				q1_sentiment = q1_sentiment - ?,
				median_sentiment = median_sentiment - ?,
				q3_sentiment = q3_sentiment - ?
			 WHERE date NOT IN (SELECT date FROM `+dailyPrealign+`)`,
			shift, shift, shift, shift, shift, shift)
		if err != nil {
			return fmt.Errorf("unshift post-apply daily rows: %w", err)
		}
		dailyUnshifted, _ = res.RowsAffected()

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM daily_sentiment WHERE date IN (SELECT date FROM `+dailyPrealign+`)`); err != nil {
			return fmt.Errorf("clear realigned daily rows: %w", err)
		}
		res, err = tx.ExecContext(ctx, `INSERT INTO daily_sentiment SELECT * FROM `+dailyPrealign)
		if err != nil {
			return fmt.Errorf("restore daily_sentiment: %w", err)
		}
		dailyRestored, _ = res.RowsAffected()

		for _, table := range []string{historyPrealign, dailyPrealign} {
			if _, err := tx.ExecContext(ctx, `DROP TABLE `+table); err != nil {
				return fmt.Errorf("drop %s: %w", table, err)
			}
		}
		for _, key := range realignKeys {
			if _, err := tx.ExecContext(ctx, `DELETE FROM key_value WHERE key = ?`, key); err != nil {
				return fmt.Errorf("delete %s: %w", key, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	slog.Info("realign reverted",
		"shift", shift,
		"sentiment_rows", rowsReverted,
		"daily_rows_restored", dailyRestored,
		"daily_rows_unshifted", dailyUnshifted,
		"applied_at", appliedAt,
	)
	fmt.Fprintf(r.out, "realign reverted (%s)\n", r.opt.dbPath)
	fmt.Fprintf(r.out, "  backup               %s\n", backupPath)
	fmt.Fprintf(r.out, "  sentiment_history    %d rows restored from %s, mean net %.2f → %.2f\n",
		rowsReverted, historyPrealign, beforeMean, afterMean)
	fmt.Fprintf(r.out, "  daily_sentiment      %d rows restored from %s, %d written since the apply shifted back by %+.2f\n",
		dailyRestored, dailyPrealign, dailyUnshifted, -shift)
	fmt.Fprintf(r.out, "  markers removed      %d\n", len(realignKeys))
	return nil
}

func setKeyValue(ctx context.Context, tx store.TxExecer, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO key_value (key, value, updated_at) VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value)
	if err != nil {
		return fmt.Errorf("set key_value %q: %w", key, err)
	}
	return nil
}
