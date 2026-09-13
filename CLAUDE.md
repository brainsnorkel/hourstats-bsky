# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

HourStats is a Go-based Bluesky bot that monitors the firehose in real time via Jetstream, performs VADER sentiment analysis on English posts, and publishes 30-minute summaries with the top 3 most engaged posts, sparkline charts, trending topics, and yearly sentiment visualizations.

**Live bot:** [@hourstats.bsky.social](https://bsky.app/profile/hourstats.bsky.social)

## Key Commands

### Build and Run
```bash
go run ./cmd/hourstats                  # Run locally (set env vars first)
make build-hourstats                    # Build binary (CGO_ENABLED=0)
make deploy-prod                        # Deploy to Fly.io production
make deploy-staging                     # Deploy to Fly.io staging
make deploy-all                         # Deploy both
```

### Testing and Development
```bash
make test                               # Run all tests (go test ./...)
make fmt                                # Format code (go fmt ./...)
make lint                               # Lint (requires golangci-lint)
make deps                               # go mod download && go mod tidy
make graph-lab                          # Generate chart experiments (no API needed)
```

### Operations
```bash
make fly-status                         # Check both app statuses
make fly-logs-prod                      # Tail production logs
make fly-logs-staging                   # Tail staging logs
make sync-staging                       # Sync prod data to staging
fly ssh console -a hourstats-prod       # SSH into production
fly ssh console -a hourstats-prod -C "su-exec hourstats realign -dry-run"   # One-off sentiment realignment (see below)
```

### Configuration (Environment Variables)
| Variable | Default | Description |
|----------|---------|-------------|
| `BLUESKY_HANDLE` | (required) | Bluesky account handle |
| `BLUESKY_PASSWORD` | (required) | Bluesky app password |
| `HOURSTATS_PROFILE` | `staging` | Profile name (used in DB filename) |
| `DATA_DIR` | `/data` | Directory for SQLite database |
| `DRY_RUN` | `false` | Prevents all posting to Bluesky |
| `ANALYSIS_INTERVAL_MINUTES` | `30` | Sentiment analysis window |
| `ANALYSIS_OFFSET_MINUTES` | `0` | Wall-clock offset within interval (minutes) |
| `TRENDING_ENABLED` | `false` | Enable trending topics |
| `GOOGLE_AI_API_KEY` | (required if trending) | Gemini API key |
| `GEMINI_MODEL` | `gemini-2.5-pro` | Gemini model for topic grouping |
| `GROUP_FALLBACK_MODEL` | `gemini-2.5-flash` | Cheaper Gemini model tried when the primary grouping call fails (429/5xx/empty). Empty disables the fallback (post is suppressed instead) |
| `HYDRATION_HOST` | `https://public.api.bsky.app` | Host for `app.bsky.feed.getPosts` engagement hydration. `app.bsky.feed.getPosts` needs no auth, so the cached public appview keeps hydration off the authenticated PDS rate budget. The literal value `pds` routes hydration back through the authenticated client |
| `HYDRATION_MAX_UNHYDRATED_PCT` | `10` | If more than this share of the window is left unhydrated (timeout, 4xx storm, or retries exhausted), the run is marked `low_confidence` and nothing is posted. Prod's routine exclusion rate is ~3.6% |
| `ANALYSIS_MAX_WINDOW_POSTS` | `300000` | Most posts one analysis window may load. Past it the window read keeps only the newest `N` by `created_at`; the run is logged as `analysis window capped`, recorded with `window_capped=1` on its `runs` row and a `window_capped` stats event, and marked `low_confidence` so sentiment is still stored but nothing is posted. `0` or negative disables the cap |
| `S3_BACKUP_BUCKET` | (optional) | S3 bucket for daily backups |
| `WAL_CHECKPOINT_THRESHOLD_MB` | `50` | WAL size (MB) that triggers TRUNCATE escalation |
| `VACUUM_FREELIST_PCT` | `20` | Freelist share of total pages the weekly VACUUM must exceed to run; below it the rewrite is skipped with an info log |
| `REPORTS_ENABLED` | `false` | Enable the weekly (Monday) week-in-review thread and the monthly (1st) candlestick + volume thread |
| `REPORTS_RUN_AT_STARTUP` | (empty) | Comma list of `weekly`, `monthly`. Runs the named reports once, ~30s after startup, still honouring the `key_value` guards and `DRY_RUN`. Requires `REPORTS_ENABLED=true`. For staging tests; unset it before leaving an app running |
| `STATS_API_BIND` | (unset) | Override the stats API listen address (tests). Unset, the server listens on `127.0.0.1:9111` and, when `FLY_PRIVATE_IP` is set, on the Fly private IPv6 address; never on all interfaces and not on `::1`, so over `fly ssh console` use `wget -qO- http://127.0.0.1:9111/...` (busybox resolves `localhost` to `::1` first). Read/write/idle timeouts and a 64 KiB header cap apply |
| `HEALTH_CHART_HOURS` | `6` | Default hours for health chart generation |
| `HEALTH_CHART_MEMORY_LIMIT_MB` | `512` | Memory limit line on health charts |
| `MEMORY_GUARD_ENABLED` | `true` | In-process memory guard. Watches RSS every 500ms during a cycle and the daily job, writes a heap profile and goroutine dump past the warn threshold, cancels the cycle past the trip threshold |
| `MEMORY_GUARD_WARN_PCT` | `55` | Warn threshold, percent of total memory. Crossing it runs `runtime.GC()` and writes `<DATA_DIR>/memguard-<label>-<UTC 20060102T150405>-<elapsed>s-warn.pprof` plus `.goroutines.txt` and a `memory_guard_warn` event; the cycle continues. Must be below the trip, or both fall back to 55/70 |
| `MEMORY_GUARD_TRIP_PCT` | `70` | Trip threshold, percent of total memory. Cancels the cycle first, then records a `memory_guard_trip` event, then runs `debug.FreeOSMemory()` and writes the same evidence with a `-trip` suffix. Nothing is posted (if the summary already went out, the sparkline and trending replies are skipped) and an already-computed sentiment is stored as `low_confidence`. The daily job only warns, never trips |
| `MEMORY_GUARD_TOTAL_MB` | (see description) | Total memory the two percentages are a share of. Resolved from `FLY_VM_MEMORY_MB` (set by Fly on every machine, follows a resize), then this variable, else 1024 with a warning — nothing else in the environment describes the machine |
| `SQLITE_MMAP_MB` | `128` | Read pool mmap_size in MB; `0` disables mmap entirely |
| `SQLITE_READ_CONNS` | `4` | Read pool max/idle connections (clamped to 1-8) |
| `SQLITE_READ_CACHE_MB` | `20` | Read pool per-connection page cache size in MB |
| `SQLITE_TEMP_STORE` | `MEMORY` | Read pool temp_store mode: `MEMORY` or `FILE` |
| `JETSTREAM_CURSOR_REWIND_SECONDS` | `5` | Seconds subtracted from the cursor on every (re)connect so in-flight events are replayed rather than lost. Negative disables the rewind |
| `JETSTREAM_MAX_CURSOR_AGE_MINUTES` | `360` | Persisted cursors older than this are discarded at startup and the consumer starts from the live tail, avoiding a wire-speed backlog replay. Negative disables the age check |
| `JETSTREAM_MAX_POST_AGE_MINUTES` | `120` | Post creates whose record `createdAt` is older than this relative to the event's witness time are dropped at ingest and counted as `stale_posts`. Jetstream v2 delivers repo backfills through the live tail as ordinary creates with old `createdAt` (observed 2026-09-11: over half of one half-hour's English posts on staging were backfill, some from 2024); v1 did not. Negative disables |
| `JETSTREAM_LEGACY` | `false` | Speak the legacy Jetstream v1 `/subscribe` protocol (uncompressed text frames, `time_us` cursor in the `cursor` table, `wantedCollections`) instead of v2. v2 never touches the v1 cursor row, so switching back resumes where v1 left off |
| `JETSTREAM_COMPRESS` | `true` | Dictionary zstd framing on v2. The dictionary comes from `network.bsky.jetstream.getZstdDictionary` (10s timeout) before the first dial and again when the server refuses the pinned ID; a failed fetch connects uncompressed and retries on the next reconnect. No effect on v1 |
| `FIREHOSE_DELETES_ENABLED` | `true` | Apply post delete commits and account deactivations from the firehose to post_buffer (see Firehose transport). `false` restores the pre-2026-09-11 behaviour where deleted posts stayed until hydration or the 2h purge |
| `JETSTREAM_EXTRA_COLLECTIONS` | (empty) | Comma list of NSIDs (e.g. `app.bsky.feed.like,app.bsky.feed.repost`) added to the v2 `collections` params for measurement only (hs-wsp.7). A matching frame is counted in the consumer's `EventsByCollection` and `BytesReceived` and dropped after a `"collection":"<nsid>"` byte scan, before the language pre-filter. Volume is logged once a minute as `jetstream extra collection volume` |
| `JETSTREAM_EXTRA_KINDS` | (empty) | **Diagnostic, off by default (staging only).** Comma list of v2 event kinds (`identity`, `sync`) added to the `kinds` params on top of the `commit,account` the bot consumes. A matching frame is counted in the consumer's `EventsByKind` and dropped after a `"$type":"network.bsky.jetstream.subscribeEvents#<kind>"` byte scan, before any parse; `commit`/`account` are ignored (already requested) and anything else is dropped with a warning. Volume is logged once a minute as `jetstream extra kind volume` and again on the stale hourly summary, so sync traffic can be correlated with backfill creates |
| `FIREHOSE_MAX_POST_RUNES` | `3000` | A post create whose text is longer than this many runes is dropped in `OnPost` before it is tokenised or stored, and counted as `oversized_posts`. The lexicon caps a post at 300 graphemes, so this is an order of magnitude of headroom over anything legitimate |
| `FIREHOSE_MAX_POSTS_PER_DID_PER_MINUTE` | `60` | Per-DID token bucket applied in the consumer's dispatch after the stale/future guards (so a repo's backfill burst cannot spend the tokens its live posts need) and before `OnPost`: capacity equals the rate (so one minute of burst), refilled at the rate. Over-cap creates are dropped and counted as `capped_posts`, and still counted toward the firehose total and their language via `OnCapped`, exactly as the language pre-filter's rejects are via `OnEarlyReject`; deletes and account events are never capped. Buckets are keyed by DID in a map bounded at 100,000 entries, swept of entries idle over 3 minutes once a minute; while the map is full no new key is added and the create is admitted, counted as `map_overflow`. Once an hour a `capped posts hourly summary` line carries `total_capped`, `distinct_dids`, the top 10 as `<did>=<count>` and `map_overflow`. `0` disables the cap |
| `FIREHOSE_MAX_POST_FUTURE_MINUTES` | `10` | The other side of `JETSTREAM_MAX_POST_AGE_MINUTES`: a create whose record `createdAt` is more than this far AHEAD of the event's witness time is dropped at ingest and counted in both `stale_posts` and `future_posts`. A few minutes of client clock skew is ordinary and kept; an unparseable `createdAt` is still treated as fresh. Negative disables |
| `FIREHOSE_DENY_DIDS` | (empty) | Operator denylist: comma list of DIDs whose post creates are dropped in both read loops by a bounded `"did":"<did>"` byte scan before the language pre-filter, so they are never parsed and never counted under their language. Counted as `denied_posts`. Deletes and account events for a denied DID still apply, so its already-buffered posts are still removed. The union of this list and the `key_value` row `firehose_deny_dids` (same comma format, re-read every 5 minutes, so `fly ssh console -a hourstats-prod -C "..."` can add a DID without a deploy) is what applies; the set lives in `internal/denylist` so the posting feature gate reads the same decision. The DIDs are never logged at Info — only the count |
| `JETSTREAM_STALE_SAMPLE_PER_HOUR` | `0` | **Diagnostic, off by default (staging only).** When > 0, logs up to this many `stale create sample` lines an hour for the creates the `JETSTREAM_MAX_POST_AGE_MINUTES` guard drops (`did`, `created_at`, `witness_time`, `age_hours`, `rev`, `seq`, `collection`, `has_reply`, `text_len` — never the text; the non-English pre-filter path carries `did`, `created_at`, `witness_time`, `age_hours`, `collection`, `first_lang` instead, since it never parses the frame). Each hour also emits one `stale create hourly summary` with `total_stale`, `distinct_dids` (capped at 50,000 tracked DIDs), the top 10 as `<did>=<count>`, and the oldest and newest `createdAt` of the hour |
| `ALERT_DISCORD_WEBHOOK_URL` | (empty) | **Secret, optional.** Discord webhook the alert path POSTs `{"content": "..."}` to, once per condition per hour, with a 10s timeout and no retries. Unset means the process log is the only sink. Set it with `fly secrets set`, never in a `fly.*.toml`; it is never logged, not even inside an error |
| `ALERT_DISCORD_MENTION` | (unset) | Text prefixed to every Discord alert so someone is pinged: a numeric user mention `<@123456789012345678>` (Discord user ID, from Developer Mode → Copy User ID), `@here` or `@everyone`. A plain `@name` pings nobody |
| `ALERT_CAPPED_POSTS_PER_SNAPSHOT` | `5000` | Per-snapshot `capped_posts` count above which the per-DID rate cap is warned about |
| `ALERT_DENIED_POSTS_PER_SNAPSHOT` | `250000` | Denied-post count per half-hour snapshot above which the denylist alert is a warning rather than an info line. Set above the volume of any deliberately denylisted flood account |
| `ALERT_STALE_POSTS_PER_SNAPSHOT` | `300000` | Per-snapshot `stale_posts` count above which the backfill-age guard is warned about. Prod routinely drops hundreds of thousands, hence the height |
| `ALERT_RECONNECTS_PER_HOUR` | `3` | Jetstream reconnects, summed over the two snapshots covering the last hour, above which the connection is warned about. Before the first snapshot exists the consumer's lifetime count stands in |
| `ALERT_CYCLE_SECONDS` | `900` | Analysis cycle wall time above which the cycle is warned about, on snapshots that saw a cycle |
| `ALERT_RSS_PCT` | `60` | Share of the machine's memory the process RSS may reach before it is warned about. The total resolves as `FLY_VM_MEMORY_MB` then `MEMORY_GUARD_TOTAL_MB`, so this and the memory guard always describe the same machine |

## Architecture

### Single Binary, Multiple Goroutines

Everything runs inside `cmd/hourstats/main.go` on Fly.io:

| Component | Trigger | What It Does |
|-----------|---------|-------------|
| **Jetstream Consumer** | Always running | WebSocket firehose, filter English posts, write to SQLite |
| **Write Flusher** | 2s ticker / 1500 batch | Batches pending writes to reduce SQLite contention |
| **Analysis Cycle** | Wall-clock ticker (default 30m, configurable; prod runs 60m at :55 via `ANALYSIS_INTERVAL_MINUTES`/`ANALYSIS_OFFSET_MINUTES`) | Hydrate engagement, score sentiment, post summary. The window is scored twice: the headline (net percent, category, root/reply split) comes from the emoji-aware analyzer (`analyzer.NewEmojiAware`) since 2026-09-11, and stock VADER (`analyzer.New`) scores the same window into `net_sentiment_pct_stock`; the `sentiment scorers` log line reports both and their delta. Runs in its own goroutine so the other tickers keep firing; an overlapping tick is skipped and logged as `cycle_overlap_skipped`. A memory sampler runs alongside it (500ms, `cmd/hourstats/memsampler.go`), writing a `cycle_memory_tick` event at each of its 15 timeline checkpoints (1s to 900s) and a `cycle_memory_peak` event at the end, so a cycle killed by the OOM killer still leaves a trace; the memory guard (`cmd/hourstats/memguard.go`) rides on the same samples. The cycle's heavy work — the window read, hydration and the topic goroutine — runs on a child context the guard can cancel, while the run/sentiment/purge writes stay on the parent so a trip still records the hour |
| **Sparkline** | After analysis | 7-day sentiment chart posted as reply |
| **Trending Topics** | After sparkline | TF-IDF + grouping (Gemini primary → `GROUP_FALLBACK_MODEL` → offline co-occurrence clustering → suppress), reply to sparkline (if enabled). Analysis runs in parallel with hydration; the cycle collects its result (bounded 60s wait) before the single seven-day `sentiment_history` read that opens the posting block, so this cycle's rank-1 label is already on its `top_topic` row when that read happens. Those same points feed both the chart and the trending reply's footer. The trending reply is posted after the sparkline and ends with a three-line footer: a `7day high & low UTC` header, then one line per extreme of the form `+14.1% Mon 14:01: <rank-1 label>` (the label and its colon are omitted when unknown; no calendar date, no link). The footer is the first thing dropped when the post would exceed 300 graphemes: its topic labels shrink from 28 to 12 runes, then go, then the whole footer, all before any topic is dropped. After that, trailing topics are dropped whole rather than listed without their exemplar link; only the rank-1 topic gives up its link before its label is cut. It is also dropped when the sparkline failed and the trending post goes out standalone, since the figures only read as a caption on the chart above them |
| **Daily Cycle** | Midnight UTC | SQLite backup to S3, daily aggregation (now including the day's firehose total), report rollups (`topic_daily` for the last 3 days, `daily_top_post` for every day `runs` still covers, firehose backfill for daily rows that predate the column), top-post quote reply. Runs in its own goroutine, after waiting up to 15m for an in-flight analysis cycle so the aggregate includes the day's last cycle |
| **Weekly Report** | End of the daily cycle (`REPORTS_ENABLED`) | Week-in-review text root (mood, delta vs prior week, happiest/unhappiest day, stickiest topic, posts analysed) plus a reply quoting the post of the week. Covers the last complete Monday–Sunday week; the guard key `weekly_report_last_week` makes it a no-op until a new week exists, so it posts on Monday and catches up if that run was skipped. Skipped with fewer than 5 daily rows |
| **Monthly Report** | After yearly posting (`REPORTS_ENABLED`) | Candlestick chart root plus a volume chart reply (English and, when tracked for every day, the full firehose; when every day also has a language split, the firehose is stacked as English plus the top 5 languages plus "other"). Covers the previous calendar month; guard key `monthly_report_last_month` (posts on the 1st, catches up if skipped). Skipped with fewer than 20 daily rows |
| **Yearly Posting** | 1st of month 01:00 UTC | 365-day sentiment chart, pinned to profile. Same goroutine/guard as the daily cycle, so chart rendering never overlaps a cycle or a daily run; a skipped tick logs `job_overlap_skipped`. Its `<Mon> <D> events` link facets point at the per-day `Portal:Current_events/YYYY_Month_D` subpage (built by `internal/wikipedia`) rather than an anchor into the monthly page; it is the only post that links to Wikipedia |
| **Stall Detection** | 5m ticker | Warns if no posts received in 5m and force-closes the WebSocket so the consumer reconnects |
| **WAL Checkpoint** | 5m ticker | Pressure-based WAL checkpoint: PASSIVE under threshold, TRUNCATE over threshold (default 50MB) |

Wall-clock aligned scheduling: tickers fire at UTC clock boundaries so deploys don't shift the schedule. An optional offset (`ANALYSIS_OFFSET_MINUTES`) shifts the fire point within each interval.

### Data Flow

```
Bluesky Jetstream -> Consumer (filter English) -> SQLite post_buffer
                                                       |
30-min ticker -> Read posts -> Hydrate engagement (25 URIs/batch, 10 concurrent)
                                    |
                            VADER sentiment -> Top 3 by engagement -> Post summary
                                    |
                            Sparkline reply -> Trending topics reply
                            (trending reply ends with "7day high & low UTC" and one "+14.1% Mon 14:01: label" line per extreme)
```

Every surface that features an individual user's post passes one feature gate
(`internal/client/gate.go`, `(*BlueskyClient).NewFeatureGate`): the hourly
summary, the trending exemplars, and the daily and weekly quote replies. One
authenticated `app.bsky.feed.getPosts` (up to 25 URIs; viewer state is only
populated when authenticated) plus one `VisibilityResolver` lookup per distinct
author DID (4 at a time) yields a `Verdict{OK, Quotable, Reason, AuthorDID}` per
URI. `OK=false` means no quote, no link, no handle, with `Reason` one of
`missing` (absent from the authenticated view: deleted, taken down,
deactivated), `blocked` (`Viewer.BlockedBy`/`Blocking`/`BlockingByList`, either
direction), `adult_label`, `label:<val>` (any `!`-prefixed moderation label on
the post or the author, covering `!hide`, `!warn`, `!no-unauthenticated`,
`!takedown`), `hide_from_recommendations` (the author's content visibility
declaration) or `visibility_unknown` (the declaration could not be read twice
running — a user we cannot check is not featured). `quote_control`
(`Viewer.EmbeddingDisabled`) leaves `OK=true` with `Quotable=false`: the post is
still listed with its handle link, but the record embed is dropped and the
summary's first line gains `· no embed, post can't be quoted`. Each URI gets one
`feature_gate` log line (`surface`, `uri`, `ok`, `quotable`, `reason`). The
hourly cycle gates 10 ranked candidates and lists the first 3 that pass, before
the run row is written, so `daily_top_post` and the weekly report inherit the
decision; exemplars are gated before their text is sent to Gemini. Unlike the
old quote-control check, the gate fails closed: after one retry, an unreachable
gate costs the hourly summary its top posts entirely — it is posted with no top
posts at all, just the aggregate sentiment lines, and the run row records none
either so the daily and weekly reports cannot inherit one — drops every
exemplar for the cycle, and skips the daily and weekly replies (their guard keys
are still set).

### Internal Packages

| Package | Purpose |
|---------|---------|
| `internal/store` | SQLite storage layer (schema, queries, backup, WAL management) |
| `internal/jetstream` | Jetstream WebSocket consumer (event parsing, cursor management) |
| `internal/hydrator` | Engagement hydration via `app.bsky.feed.getPosts` (batch, rate-limited) |
| `internal/topics` | Trending topics (TF-IDF extraction, Gemini grouping, identity tracking) |
| `internal/analyzer` | VADER sentiment analysis (govader) |
| `internal/client` | Bluesky AT Protocol client (posting, image upload, facets, pinning) |
| `internal/formatter` | Post content formatting (character counting, Bluesky limits) |
| `internal/sparkline` | Chart generation (sparkline, trendline, volume, yearly, bump charts) |
| `internal/procmem` | Process RSS from `/proc/self/statm` (0 on non-Linux) |
| `internal/stats` | Runtime statistics collector |
| `internal/statsapi` | HTTP stats API server (port 9111) |
| `internal/state` | Type definitions for sentiment data points |
| `internal/wikipedia` | Wikipedia link building (`Portal:Current_events` per-day URLs) |

Binaries: `cmd/hourstats` (the Fly.io process), `cmd/hourstats-stats` (CLI for the stats API), `cmd/graph-lab` (chart experiments, no API access needed).

### SQLite Database

Single file on Fly.io persistent volume: `/data/hourstats-{profile}.db` (WAL mode).

Three connection pools: `writeDB` (1 conn, 30s timeout), `readDB` (4 conns, read-only), `maintDB` (1 conn, 1s timeout for WAL checkpoints).

Key tables: `post_buffer` (2h retention), `runs` (48h), `sentiment_history` (nominally 8 days, but the purge has no caller so every hourly cycle since Jan 2026 is retained), `daily_sentiment` (3 years), `topic_tokens` (26h), `topic_snapshots` (48h), `topic_daily` and `daily_top_post` (400 days, rolled up from `topic_snapshots` and `runs` by the daily cycle), `language_counts` (per cycle, 8 days) and `language_daily` (400 days), `key_value` (permanent). `sentiment_history` carries both scorers: `net_sentiment_percent` is the headline, which since 2026-09-11 is the emoji-aware score (`analyzer.NewEmojiAware`) and is mirrored into `net_sentiment_pct_emoji` for continuity with the shadow era, while `net_sentiment_pct_stock` holds the same window scored by stock VADER (`analyzer.New`), the pre-switch headline. Both are NULL for rows written before their column existed and for cycles where that scorer failed; a NULL `net_sentiment_pct_stock` is exactly what marks a row as pre-switch, which is how `cmd/realign` finds the rows to move. `cmd/realign` (shipped as `/usr/local/bin/realign`) is the one-off tool that put the pre-switch history on the new scale. Run it as the app user — `fly ssh console -a hourstats-prod -C "su-exec hourstats realign -dry-run"` — because an ssh session is root and every file it writes as root is a file the uid-1000 bot cannot then write; the entrypoint repairs that on the next boot (`find /data -maxdepth 2 ! -user hourstats -exec chown ... +`), but not before it. Its flags: `-dry-run` (default) reports the shift S against `formatter.RealignShift` and prints samples, `-apply` rewrites history inside one `BEGIN IMMEDIATE` transaction after a local backup, `-revert` restores the originals from the `sentiment_history_prealign` and `daily_sentiment_prealign` snapshot tables (both are in the essential-tables backup while they exist). It records `sentiment_realign_shift`, `sentiment_realign_applied_at`, `sentiment_realign_row_count`, `sentiment_realign_max_timestamp` and `sentiment_realign_shadow_rows` in `key_value`; `sentiment_scorer_v2_since` (the first UTC day on the new scorer) is written by the bot itself at startup, like `firehose_count_v2_since`. See docs/SENTIMENT_REALIGNMENT_PLAN.md.

Firehose transport: the consumer speaks Jetstream v2 by default (`wss://jetstream.us-{west,east}.bsky.network/xrpc/network.bsky.jetstream.subscribeEvents`, subprotocol `xrpc.v1.json`, `collections=app.bsky.feed.post`, `kinds=commit,account`, dictionary zstd; `internal/jetstream/v2.go` and `v2_session.go`). It resumes by unix-microsecond timestamp, rewound `JETSTREAM_CURSOR_REWIND_SECONDS` (5s by default), on every dial (an endpoint rotation is the exception: it follows repeated instability, so the new endpoint is dialled at its live tip): a cursor at or above 1e15 is read as a timestamp and translated to the first seq that instance witnessed at or after that instant, while a seq belongs only to the instance that issued it (two connections to the same hostname at the same moment read seqs 43 million apart). The witnessed time is persisted every 10s as `key_value` `jetstream_v2_cursor_time_us`, with the seq alongside it in `jetstream_v2_cursor` for diagnostics only, never read back for a resume. The seq still dedups within a single connection, with the floor reset at every dial; the replayed overlap is absorbed by the idempotent post upsert. Each dial logs `jetstream v2 dial` with `cursor_kind` (`timestamp` or `live`), `cursor_time` and `rewind`. The first v2 deploy finds no v2 cursor and starts from the live tip. Both protocols normalise frames into the same `jetstream.Event`, so the callbacks are protocol-agnostic: `OnPost` for creates, `OnDelete` for post delete commits (the row and its topic tokens are removed in order inside the write batch, and an in-memory tombstone with 15-minute retention stops a replayed create from resurrecting it), `OnAccountInactive` for `deactivated`/`deleted`/`suspended`/`takendown` accounts (every buffered post by that DID is purged; `desynchronized` and `throttled` are not purges). The counts are on the stats snapshot as `post_deletes`, `account_purges` and `tombstone_hits`. v2 also delivers repo backfill through the live tail as ordinary creates with fresh seq numbers and a current witness time but a record `createdAt` days to years old, which v1 never did, so a create lagging its witness time by more than `JETSTREAM_MAX_POST_AGE_MINUTES` is dropped at ingest — before `OnPost` and before `OnEarlyReject`, so it inflates neither the buffer nor the firehose and per-language totals — and counted as `stale_posts`.

Alerts: after each 30-minute stats snapshot, `internal/alerts` evaluates the two most recent snapshots, the `stats_events` rows since the older of them, and the live consumer report against the `ALERT_*` thresholds, and sends every condition down one notification path. The conditions are `dropped_posts` (error, only when the write buffer dropped posts in two consecutive snapshots), `capped_posts`, `stale_posts`, `oversized_posts` (over 100), `denied_posts` (info at any count, warn over `ALERT_DENIED_POSTS_PER_SNAPSHOT`), `consumer_reconnects`, `cycle_duration`, `rss_high`, and one condition per alerting `stats_events` type in the window: `cycle_failed` (error; written when the cycle exits before its run row, i.e. the window read or the Bluesky login failed — the login is retried once after 10s),  `memory_guard_trip` (error), `memory_guard_warn`, `window_capped`, `hydration_timeout`, `consumer_restart`, `feature_gate_unavailable` (written by the analysis cycle when the gate is unreachable and the hour publishes no top posts at all) and `seq_floor_reset` (written by `cmd/hourstats`'s `OnSeqFloorReset` callback when the v2 dedup floor has rejected 10,000 frames in a row and is reset to the live tip, carrying `dropped`, `floor` and `seq`). Every condition is logged as `alert` with its name, severity and message; info stays in the log and on `/stats/health`, while warn and error also go to `ALERT_DISCORD_WEBHOOK_URL` when it is set, at most once per condition name per hour. Messages carry the count, the previous half hour and the threshold, a plain-language meaning, what normal looks like and what to check; the flood conditions (`stale_posts`, `capped_posts`, `denied_posts`) also name the top five accounts with counts and per-minute rates, with handles resolved for Discord and the top account's profile URL, since the channel is the operator's own record (DIDs never appear at Info in the process log). `ALERT_DISCORD_MENTION` (e.g. `@here`) is prefixed only to actionable conditions and errors: `dropped_posts`, `memory_guard_trip`, `window_capped`, `consumer_restart`, `rss_high`, `cycle_duration`, `oversized_posts`, `consumer_reconnects`. The evaluation runs off the scheduler loop on a 30s budget, and its result is what `/stats/health`'s `status` (`ok`/`warn`/`error`) and `alerts` array report.

Firehose counting: the Jetstream consumer drops non-English post creates with a bytes-level pre-filter before parsing. Since the language-volume change those frames still count toward the firehose total and toward `language_counts` (via `OnEarlyReject`); before it, "firehose" meant English plus untagged posts only, so `total_firehose_posts` values from before that deploy are undercounts. A post is counted as `en` when the English filter accepts it, otherwise under its first tag's primary subtag, with `und` for untagged.

## Coding Conventions

### Logging
- **Use `log/slog`** with structured key-value pairs: `slog.Info("message", "key", value)`
- JSON handler configured in main.go: `slog.NewJSONHandler(os.Stdout, ...)`
- Do NOT use `log.Printf` -- the codebase is migrating away from it

### Error Handling
- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`
- Log and return early on error -- no deep nesting
- Use `context.WithTimeout` for all external API calls

### Bluesky API Patterns
- Posts limited to 300 graphemes (use `[]rune` for length checks, not `len(string)`)
- Rich text facets use byte offsets (not rune offsets) for `ByteStart`/`ByteEnd`
- AT URIs: `at://did:plc:xxx/app.bsky.feed.post/yyy`
- Image upload: blob reference then embed in post record. `UploadImage` decodes the PNG header and sets the embed's `aspectRatio`; without it clients draw the image in a square frame with blank bands. The sparkline and yearly charts render at 2000×1333 (`sparkline.PostCanvasWidth/Height`), the CDN's full-size cap; feed thumbnails are served at 1000px wide regardless

### Testing
- Standard `go test ./...`
- Hydrator uses interfaces (`PostFetcher`, `PostUpdater`) for testability
- No external test framework -- stdlib `testing` package only

## Tech Stack

- **Go 1.24** (CGO_ENABLED=0 for Alpine)
- **AT Protocol**: `github.com/bluesky-social/indigo`
- **Firehose**: Bluesky Jetstream (WebSocket via `gorilla/websocket`)
- **Sentiment**: `github.com/jonreiter/govader`
- **Charts**: `github.com/fogleman/gg`
- **Database**: `modernc.org/sqlite` (pure Go, no CGO)
- **Topic Grouping**: Google Gemini Pro API
- **Deployment**: Fly.io (Docker, Alpine 3.21, persistent volume)
- **Backups**: AWS S3

## Deployment

- **Production**: `hourstats-prod` -- shared-cpu-1x, 1024MB RAM, SJC region
- **Staging**: `hourstats-staging` -- shared-cpu-1x, 1024MB RAM, SJC region
- **Container**: Multi-stage Docker build (golang:1.24-alpine to alpine:3.21)
- **Secrets**: `fly secrets set KEY=value -a hourstats-prod`
- **Config files**: `fly.prod.toml`, `fly.staging.toml`

## Important Notes

- Bluesky post limit is 300 **graphemes** (runes), not bytes
- Facet byte positions must be calculated on the UTF-8 byte string
- The Jetstream consumer auto-restarts with exponential backoff (1s to 60s)
- Cursor is persisted in SQLite for resume-on-restart: v2 in `key_value` (`jetstream_v2_cursor`), v1 in the `cursor` table
- `DRY_RUN=true` prevents all posting but still runs analysis and stores data
- The AWS Lambda/DynamoDB code is gone from the repo (git history only); the only AWS dependency left is the daily S3 backup in `internal/store/backup.go`