# Handoff: weekly and monthly report posts

Beads epic **hs-dqu** (tasks hs-dqu.1 to hs-dqu.5). Branch `feat/periodic-reports`,
based on `main` at 07b1d5e (what prod runs). The approved mockups, post
texts and chosen chart variants are in `README.md` beside this file.
Read that first; this file is the implementation context.

## What already exists on this branch

- `internal/sparkline/monthly_candle_generator.go`: `NewMonthlyCandleGenerator().GenerateMonthlyCandleChart(days []DailyCandle, meta MonthlyCandleMeta)`. `DailyCandle` maps one-to-one onto a `daily_sentiment` row (min, q1, median, q3, max, average, runs). `MonthlyCandleMeta{MonthLabel, PrevMonthAvg (NaN if unknown), PrevLabel}`.
- `internal/sparkline/monthly_volume_generator.go`: `NewMonthlyVolumeGenerator().GenerateMonthlyVolumeChart(days []DailyVolumePoint, meta MonthlyVolumeMeta)`. `DailyVolumePoint{Date, ENPosts, TotalPosts}`; `TotalPosts == 0` on every day degrades to English-only. `MonthlyVolumeMeta{MonthLabel, PrevMonthEN (0 if unknown), PrevLabel}`.
- `cmd/graph-lab/monthly.go`: synthetic scenarios, `go run ./cmd/graph-lab -type monthly` writes PNGs to `test-results/graph-lab/`.
- Both generators reuse the package-private furniture in `series_chart.go` (`drawHeader`, `drawYGrid`, `drawNeutralBand`, `drawAverage`, `drawHaloText`, `fitRange`, `plotArea`). Do not fork these; extend them if a new need appears.

No store, scheduler, posting or text-builder code exists yet. That is the five tasks.

## Repo facts the tasks depend on

**Schema** lives in `internal/store/store.go` as an ordered list of `CREATE TABLE IF NOT EXISTS` / `ALTER TABLE ADD COLUMN` statements (around line 420). ALTER statements that fail because the column exists are tolerated; follow the existing pattern (see `total_firehose_posts` on `sentiment_history`).

**Existing rows to build on**
- `daily_sentiment` (date PK, average/min/max/q1/median/q3, total_runs, total_posts). Written once a day by `runDailyAggregation` in `cmd/hourstats/daily.go` from `sentiment_history` filtered through `filterHighConfidence`. It has no firehose total yet; `store.SentimentDataPoint.TotalFirehosePosts` is available per cycle to sum.
- `sentiment_history` has every hourly cycle (total_posts, total_firehose_posts, root/reply pct). `GetDailyPostCounts(ctx, duration)` and `GetWeeklyPostTotals(ctx)` already aggregate it by day and week. `GetWeeklyPostTotals` has no caller today.
- `topic_snapshots` (48h retention, purged by `PurgeTopicSnapshots`): one row per topic per hourly cycle with `snapshot_time`, `rank`, `topic_id`, `label`, `unique_author_count`. `topic_identity` keeps `first_seen`/`last_seen`/`peak_rank` per topic_id but is also purged.
- `runs` (48h retention) holds `top_posts` JSON per cycle. `GetTopPostForDate(ctx, "YYYY-MM-DD")` in `internal/store/daily_top_post.go` already returns the best `store.Post` (URI, CID, AuthorHandle, Likes, Reposts, Replies, EngagementScore) for a date; the daily quote reply uses it.
- `key_value` table: `db.GetKeyValue` / `db.SetKeyValue`. Existing guard keys: `daily_quote_last_date`, `yearly_post_uri`, `yearly_post_cid`.

**Scheduling** is in `cmd/hourstats/main.go` (select loop, around line 225 to 270).
- `backupCh := newWallClockTicker(24*time.Hour, 0)` fires at 00:00 UTC; the branch runs `runBackup`, `runDailyAggregation`, `runDailyTopPostQuote`, and a Sunday `RunVacuum`, all inside `startAfterCycle(ctx, jobs, cycles, "daily", jobCycleWait, func(){...})`.
- `yearlyPostCh := newDailyTickerAtHour(1)` fires at 01:00 UTC every day and `runYearlyPosting` has no day-of-month check, so the yearly chart is re-posted and re-pinned daily (CLAUDE.md's "1st of month" description is stale). The monthly job must check `Day() == 1` itself; do not assume the branch only runs monthly.
- `startAfterCycle` is the overlap guard: it waits up to `jobCycleWait` for an in-flight analysis cycle, and skips with `job_overlap_skipped` if another job is running. Weekly and monthly work must run inside these bodies, never as new goroutines.
- Weekly job goes at the end of the daily branch when `time.Now().UTC().Weekday() == time.Monday` (the report covers the previous Monday to Sunday; the Sunday aggregate has just been written by `runDailyAggregation`).
- Monthly job goes after `runYearlyPosting` in the yearly branch when `time.Now().UTC().Day() == 1`.

**Posting** via `internal/client` (`client.New(handle, password)` then `Authenticate()`):
- `PostWithImage(ctx, text, png, altText, facets...) (uri, cid, err)`
- `PostWithImageAsReply(ctx, text, png, altText, rootURI, rootCID, parentURI, parentCID)`
- `PostWithFacetsAsReply(ctx, text, facets, rootURI, rootCID, parentURI, parentCID)` (pass nil facets for plain text)
- `PostReplyWithQuote(ctx, text, rootURI, rootCID, parentURI, parentCID, quoteURI, quoteCID)`
- `EmbeddingDisabled(ctx, uris) map[string]bool` for the quote-control check; the hourly summary fails open on error. Reuse that for post of the week: if quoting is disabled, post the reply as text with the author handle and counts, no embed.
- There is no standalone root-post-with-facets helper that returns URI/CID other than `PostWithImage`; check `PostWithFacets` (returns only error) and add a URI-returning variant if the weekly root needs one.
- Every job takes `handle, password string, dryRun bool`; `DRY_RUN` logs "would post" with the payload sizes and returns before authenticating. Follow `runYearlyPosting` in `cmd/hourstats/yearly.go` as the template.

**Alt text** helpers are in `cmd/hourstats/alttext.go` (`signedPct`, `zonePhrase`, `deltaPhrase`, `extremes`). Match the style of `buildYearlyAltText`.

**Text limits**: 300 graphemes, count with `utf8.RuneCountInString`. The mockup texts are 242, 101, 252 and 186. Facet byte offsets are on the UTF-8 string.

**Config**: env helpers `envOr`, `envBool`, `envInt` in `cmd/hourstats/config.go`. Add rows to the env table in `CLAUDE.md`.

**Tests**: stdlib only. Store tests open a temp SQLite file (see `internal/store/store_test.go`); scheduling tests are in `cmd/hourstats/scheduler_jobs_test.go`; chart generator tests just assert PNG bytes and no error.

## Task-by-task design (matches the beads)

**hs-dqu.1 store**
- Tables (400-day retention):
  - `topic_daily(date TEXT, topic_id TEXT, label TEXT, appearances INTEGER, best_rank INTEGER, max_authors INTEGER, PRIMARY KEY(date, topic_id))`
  - `daily_top_post(date TEXT PRIMARY KEY, uri, cid, author_handle TEXT, likes, reposts, replies INTEGER, engagement_score REAL)`
- `ALTER TABLE daily_sentiment ADD COLUMN total_firehose_posts INTEGER NOT NULL DEFAULT 0`; add `TotalFirehosePosts` to `DailySentimentDataPoint`, and read/write it in `StoreDailySentiment`, `GetDailySentimentHistory`, `GetDailySentimentForDate`.
- New queries: `GetDailySentimentRange(ctx, startDate, endDate string)` (inclusive, ordered), `RollupTopicDaily(ctx, date string)` (GROUP BY topic_id over that day's `topic_snapshots`: count(*) appearances, min(rank), max(unique_author_count), label from the latest snapshot), `GetTopTopicForRange(ctx, start, end) (label string, appearances int, err)` (sum appearances by topic_id, highest wins, tie on best_rank), `StoreDailyTopPost(ctx, date, store.Post)`, `GetTopPostForRange(ctx, start, end) (*Post, error)` (max engagement_score), `PurgeReportRollups(ctx, olderThan time.Duration)`.

**hs-dqu.2 daily cycle** (`cmd/hourstats/daily.go`)
- Sum `TotalFirehosePosts` into the daily aggregate.
- After aggregation: `RollupTopicDaily(yesterday)`, then `GetTopPostForDate(yesterday)` and `StoreDailyTopPost`. Both idempotent (INSERT OR REPLACE). Then `PurgeReportRollups(400 days)`.
- Run these before `runDailyTopPostQuote` so a quote failure does not skip the rollup.

**hs-dqu.3 weekly job** (`cmd/hourstats/weekly.go`)
- Week = previous Monday 00:00 to Sunday 23:59 UTC. Guard key `weekly_report_last_week` = that Monday's date.
- Data: `GetDailySentimentRange` for the 7 days and the 7 before (delta); happiest and unhappiest by `AverageSentiment`; `GetTopTopicForRange`; posts analysed = sum of `TotalPosts`; `GetTopPostForRange` for the quote reply.
- Skip with an info log when fewer than 5 of the 7 days exist. Omit the topic line when there is no topic data rather than failing.
- Root text and reply text exactly as the mockup; `buildWeeklyReportText` and `buildPostOfWeekText` should be pure functions with tests covering the 300 limit and a missing-topic case.

**hs-dqu.4 monthly job** (`cmd/hourstats/monthly.go`)
- Month = previous calendar month. Guard key `monthly_report_last_month` = "YYYY-MM".
- Data: `GetDailySentimentRange` for the month (candles and English counts) and for the prior month (deltas); firehose totals from the new column, falling back to `GetDailyPostCounts` only if the column is all zeros for the month.
- Root: candlestick PNG plus mockup text. Reply: volume PNG plus mockup text. Alt text for both. Skip when fewer than 20 days exist.
- The month thread is standalone, not a reply to the pinned yearly post.

**hs-dqu.5 scheduling and config**
- `REPORTS_ENABLED` (default `false`) gates both jobs so prod is unchanged until it is switched on.
- `REPORTS_RUN_AT_STARTUP` (comma list, `weekly`, `monthly`) runs the named jobs once about 30 seconds after startup through `startAfterCycle`, ignoring the weekday and day-of-month checks but still honouring the key_value guards and `DRY_RUN`. This is how staging gets tested without waiting for a Monday.
- Document both in the `CLAUDE.md` env table and add the two jobs to the component table.

## Staging test plan

Staging (`hourstats-staging`) is currently stopped; `make sync-staging` copies the prod database so it has real `daily_sentiment` and `sentiment_history` rows, but no `topic_daily` or `daily_top_post` rows until the daily cycle has run there. For a first run, backfill `topic_daily` from whatever `topic_snapshots` exist (48h) or accept the topic line being omitted.

1. `make sync-staging`, then `fly secrets set REPORTS_ENABLED=true REPORTS_RUN_AT_STARTUP=weekly,monthly DRY_RUN=true -a hourstats-staging` and `make deploy-staging`. Confirm the "would post" log lines carry sane numbers and image byte counts.
2. Flip `DRY_RUN=false` on staging, redeploy, and inspect the two threads on the staging account. Delete the guard keys from `key_value` between runs to re-fire.
3. Unset `REPORTS_RUN_AT_STARTUP` before leaving staging running, or every restart re-checks the guards (harmless, but noisy).
4. Prod rollout: merge to main, deploy with `REPORTS_ENABLED=false`, let the daily cycle populate `topic_daily` and `daily_top_post` for a week, then enable.

Fly apps are under chris@flatmapit.com; `fly ssh console -a hourstats-staging` and `sqlite3 /data/hourstats-staging.db` for the key_value edits.

## Decisions already made (do not reopen)

- Volume chart shows English and firehose lines (variant B).
- Weekly post attaches no image; the hourly sparkline already covers the week.
- Wording "Stickiest topic ... trending N of 168 hours".
- Rollups are written by the daily cycle only; nothing new touches the hourly analysis path.
- Firehose totals get a real column on `daily_sentiment` rather than depending on `sentiment_history` retention.
