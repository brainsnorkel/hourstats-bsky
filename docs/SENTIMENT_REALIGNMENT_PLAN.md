# Plan: switch the headline to the emoji-aware scorer and realign history

Status: implemented, awaiting execution (code landed 2026-09-10; `cmd/realign -apply` not yet run on prod). Decisions taken from section 7: thresholds shift by S rounded to the nearest 0.25; shadow rows use their exact emoji value by default (`-shadow-rows exact`, `shift` available); `net_sentiment_pct_emoji` keeps being written. Proposed 2026-09-10. Tracks beads `hs-tcm`. Deviates from that issue's original schedule (two weeks of pairs, hour-of-day fit) by decision: the pre-switch history is rough enough that a constant shift is sufficient.

## 1. Inputs

From prod `sentiment_history`, cycles with the shadow column and at least 500 posts (2026-09-07 03:00 to 2026-09-09 22:55 UTC):

| | |
|---|---|
| Pairs | 67 |
| Mean shift, emoji minus stock | +1.77 pt |
| Shift range | +1.42 to +2.02 |
| Shift by 4-hour bucket | 1.70, 1.82, 1.84, 1.75, 1.72, 1.77 |
| Correlation | 0.98 |
| Linear fit | emoji = 0.80 + 1.09 × stock (headline range observed only 9.0 to 12.2, so the slope is an extrapolation and is not used) |

Decision: a **constant shift S**, computed at execution time from every valid pair (same filter), rounded to two decimals. Today S = 1.77.

Rows affected: 5,561 `sentiment_history` rows without a shadow score (2026-01-30 onward), 67 with one, 358 `daily_sentiment` rows.

## 2. What changes in code (one commit)

1. **Headline scorer.** In `cmd/hourstats/analysis.go` the headline (`net_sentiment_percent`, `average_compound_score`, `sentiment_category`, root and reply splits) is produced by `analyzer.NewEmojiAware`. The stock VADER score is still computed and stored in a new column `sentiment_history.net_sentiment_pct_stock` (NULL for rows before the switch), so the pair keeps accumulating with the roles swapped. `net_sentiment_pct_emoji` keeps being written and now equals the headline.
2. **Mood tiers shift by S.** `internal/formatter/sentiment_100_words.go` thresholds move up by S, rounded to the nearest 0.25 so they stay legible, and the interpolation clamps move with them:

   | Boundary | Now | After (S = 1.77) |
   |---|---|---|
   | Unusually Low | 8.5 | 10.25 |
   | Below Average | 9.75 | 11.5 |
   | Typical | 11.5 | 13.25 |
   | Above Average | 12.75 | 14.5 |
   | Unusually High | 15.0 | 16.75 |
   | Tier 2 lower clamp | 3.5 | 5.25 |
   | Tier 7 upper clamp | 20.0 | 21.75 |

   The percentile table and its comment are regenerated from the realigned series so the file still says where the boundaries come from. The weekly `moodPhrase` (±0.05 on the average) and the monthly delta text compare shifted values with shifted values and need no change. The charts carry no fixed reference line at 10%.
3. **Markers in `key_value`:** `sentiment_scorer_v2_since` (the first UTC day scored by the new headline) is written by the bot at startup, exactly like `firehose_count_v2_since`, so it records the deploy rather than the backfill. `cmd/realign` writes the backfill's own markers: `sentiment_realign_shift` (S), `sentiment_realign_applied_at`, `sentiment_realign_row_count`, `sentiment_realign_max_timestamp` and `sentiment_realign_shadow_rows`.
4. **Tests.** `TestMoodWordDistribution` runs against the hourly fixture CSV shifted by S and must reproduce the tier shares in `docs/SENTIMENT_CALIBRATION_REVIEW_2026-09.md` within one point (identical by construction). Threshold-pinning tests updated. A new test asserts the headline path uses the emoji analyzer and the stock column is populated.
5. **Docs.** CLAUDE.md (schema paragraph and analysis-cycle row), `docs/SENTIMENT_CALIBRATION_REVIEW_2026-09.md` gets a dated addendum with S and the switch run id, the architecture data view gains the new column.

## 3. The backfill (one-off command, run once on prod)

A small admin command, `cmd/realign`, using the store package. Flags: `-db`, `-dry-run` (the default: prints counts, S against the expected shift, and ten sample rows before and after), `-apply`, `-revert`, `-shadow-rows exact|shift`, `-expect-shift`, `-backup-dir`, `-force`. It refuses to run on a database path that does not exist, and refuses `-apply` if `sentiment_realign_applied_at` exists.

Preflight, before anything is written: refuse if `sentiment_realign_applied_at` exists (never overridable); refuse, unless `-force`, if the computed S is more than 0.125 from `formatter.RealignShift` (the shift the mood-word thresholds were built on), if no `sentiment_history` row yet carries `net_sentiment_pct_stock` (the new binary has not completed a cycle, so the deploy step was skipped), or if any row has `average_compound_score` out of step with `net_sentiment_percent / 100`.

Order of operations inside `-apply`, all in one `BEGIN IMMEDIATE` transaction after a fresh local backup:

1. Add `net_sentiment_pct_stock` if missing (idempotent migration, also shipped in the binary).
2. Copy `run_id, timestamp, net_sentiment_percent, average_compound_score, root_sentiment_pct, reply_sentiment_pct` of every pre-switch row into `sentiment_history_prealign` (refusing if it is not empty). That snapshot is both the restore source for `-revert` and the definition of the row set the rest of the run touches.
3. Compute S from the valid pairs and record it.
4. `sentiment_history`, rows before the switch:
   - rows **with** a shadow score: `net_sentiment_pct_stock = net_sentiment_percent`, then `net_sentiment_percent = net_sentiment_pct_emoji` (the exact value, not the shift), `average_compound_score = net_sentiment_percent / 100`, root and reply percentages += S;
   - rows **without** one: `net_sentiment_pct_stock = net_sentiment_percent`, then net += S, compound += S/100, root and reply += S.
   `sentiment_category` is left alone: it is a ±0.3 band on the compound score and never moves at these magnitudes. `top_topic`, counts and firehose totals are untouched.
5. `daily_sentiment`, dates before the switch date: copy the table to `daily_sentiment_prealign`, then average, min, max, quartiles and median += S. Days with a shadow score for every cycle could be recomputed exactly, but the difference to a shift is under 0.1 and not worth the code.
6. `runs` (7-day TTL, only feeds the daily top post) is left as is.
7. Write the markers.

Rollback: `-revert` takes its own backup first, then restores every overwritten
value **verbatim from the two `_prealign` snapshot tables** — no arithmetic is
reversed, so the affected rows come back bit-for-bit (a test compares full row
dumps of `sentiment_history`, `daily_sentiment` and `key_value` before the apply
and after the revert). It refuses unless the snapshot still holds exactly
`sentiment_realign_row_count` rows. `daily_sentiment` rows written *after* the
apply have no snapshot row and were aggregated from realigned history, so those
are shifted back by −S (the daily job only ever rebuilds yesterday, so nothing
else would). Both snapshot tables are in the essential-tables backup list while
they exist, so a backup taken inside the window carries the revert source; the
S3 backup taken by the daily job before the switch is the last resort.

## 4. Sequencing on prod

1. Deploy the code in the 00:05 to 00:50 UTC window on the chosen night, after the daily job has written the previous day's `daily_sentiment` row. The 00:55 cycle is the first on the new headline and gets recorded in `sentiment_scorer_v2_since`.
2. Run `cmd/realign -dry-run` over `fly ssh`, read the output (it names the computed S, the expected S, the post-switch row count and the compound-score check), then `-apply`. Runtime is seconds; the write transaction blocks the write flusher for well under its 30 s busy timeout.
3. The rest of that day's cycles and the next daily aggregation are consistent with the realigned history without further action, because the daily job reads `sentiment_history` after the fact.

## 5. Verification

- Mean of `net_sentiment_percent` over the pre-switch rows rises by exactly S; the 67 shadow rows equal their old emoji values.
- The 7-day sparkline reply after the switch shows no step at the boundary (compare with the previous hour's image).
- The first three posts on the new headline carry tier words consistent with a hour of that value under the new thresholds.
- `TestMoodWordDistribution` on the realigned export matches the calibration review table.
- The next weekly report's "vs prior week" delta is within the usual ±0.5 range, not shifted by S.
- The yearly chart posted at 01:00 the next morning is continuous.

## 5a. Staging rehearsal, 2026-09-10 00:25 to 00:40 UTC

Staging was rebuilt from a prod volume snapshot (`make sync-staging`) and ran the branch build. Its 00:25 cycle wrote the first row with a stock column (headline 10.66, stock 9.11). Then, with per-table `sha3sum` hashes of `sentiment_history`, `daily_sentiment` and `key_value` taken as the baseline:

- `-dry-run`: S = +1.77 over 68 pairs, difference from the compiled thresholds 0.00, 5,629 history rows and 359 daily rows in scope, no compound-score anomalies.
- `-apply`: mean net 10.55 → 12.32. Checked against the snapshots: 0 stock-column mismatches, 0 shadow rows differing from their emoji value, 0 non-shadow rows off by anything but 1.77, 0 root/reply mismatches, 0 daily mismatches across all six columns; the post-switch row untouched; five markers written.
- A synthetic daily row was inserted after apply. `-revert` took its own backup, restored 5,629 history rows and 359 daily rows, shifted the synthetic row back by −1.77, and removed the markers and both snapshot tables. After deleting the synthetic row, all three table hashes were **identical to the baseline**.
- `-apply` was run again and staging was left on the new scale as a soak until the prod run.

One defect found and fixed: a `-backup-dir` whose parent did not exist was created rather than refused, which would have put the pre-write copy on the container's ephemeral filesystem. The tool now refuses.

## 6. Effort

Code switch and threshold move about half a day; backfill command with dry run and revert about half a day; execution and verification about an hour in the deploy window.

## 7. Open choices for you

1. **S rounding for thresholds:** nearest 0.25 (above) or exact S. Nearest 0.25 keeps the tier table readable; the difference is at most 0.13 pt of tier placement.
2. **Shadow rows:** use the exact emoji value (proposed) or the shift, for uniformity. Exact is more faithful; the shift is simpler to explain.
3. **Keep or drop the emoji column** once it equals the headline. Proposed: keep writing it for now, drop in a later cleanup.
4. **Night to run it.** Any night works; avoid the 1st of the month (monthly report) and a Monday (weekly report) so the first reports on the new scale come from a full, realigned period.
