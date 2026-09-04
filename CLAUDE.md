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
| `S3_BACKUP_BUCKET` | (optional) | S3 bucket for daily backups |
| `WAL_CHECKPOINT_THRESHOLD_MB` | `50` | WAL size (MB) that triggers TRUNCATE escalation |
| `VACUUM_FREELIST_PCT` | `20` | Freelist share of total pages the weekly VACUUM must exceed to run; below it the rewrite is skipped with an info log |
| `REPORTS_ENABLED` | `false` | Enable the weekly (Monday) week-in-review thread and the monthly (1st) candlestick + volume thread |
| `REPORTS_RUN_AT_STARTUP` | (empty) | Comma list of `weekly`, `monthly`. Runs the named reports once, ~30s after startup, still honouring the `key_value` guards and `DRY_RUN`. Requires `REPORTS_ENABLED=true`. For staging tests; unset it before leaving an app running |
| `HEALTH_CHART_HOURS` | `6` | Default hours for health chart generation |
| `HEALTH_CHART_MEMORY_LIMIT_MB` | `512` | Memory limit line on health charts |
| `SQLITE_MMAP_MB` | `128` | Read pool mmap_size in MB; `0` disables mmap entirely |
| `SQLITE_READ_CONNS` | `4` | Read pool max/idle connections (clamped to 1-8) |
| `SQLITE_READ_CACHE_MB` | `20` | Read pool per-connection page cache size in MB |
| `SQLITE_TEMP_STORE` | `MEMORY` | Read pool temp_store mode: `MEMORY` or `FILE` |
| `JETSTREAM_CURSOR_REWIND_SECONDS` | `5` | Seconds subtracted from the cursor on every (re)connect so in-flight events are replayed rather than lost. Negative disables the rewind |
| `JETSTREAM_MAX_CURSOR_AGE_MINUTES` | `360` | Persisted cursors older than this are discarded at startup and the consumer starts from the live tail, avoiding a wire-speed backlog replay. Negative disables the age check |

## Architecture

### Single Binary, Multiple Goroutines

Everything runs inside `cmd/hourstats/main.go` on Fly.io:

| Component | Trigger | What It Does |
|-----------|---------|-------------|
| **Jetstream Consumer** | Always running | WebSocket firehose, filter English posts, write to SQLite |
| **Write Flusher** | 2s ticker / 1500 batch | Batches pending writes to reduce SQLite contention |
| **Analysis Cycle** | Wall-clock ticker (default 30m, configurable; prod runs 60m at :55 via `ANALYSIS_INTERVAL_MINUTES`/`ANALYSIS_OFFSET_MINUTES`) | Hydrate engagement, VADER sentiment, post summary. Runs in its own goroutine so the other tickers keep firing; an overlapping tick is skipped and logged as `cycle_overlap_skipped` |
| **Sparkline** | After analysis | 7-day sentiment chart posted as reply |
| **Trending Topics** | After sparkline | TF-IDF + grouping (Gemini primary → `GROUP_FALLBACK_MODEL` → offline co-occurrence clustering → suppress), reply to sparkline (if enabled) |
| **Daily Cycle** | Midnight UTC | SQLite backup to S3, daily aggregation (now including the day's firehose total), report rollups (`topic_daily` for the last 3 days, `daily_top_post` for every day `runs` still covers, firehose backfill for daily rows that predate the column), top-post quote reply. Runs in its own goroutine, after waiting up to 15m for an in-flight analysis cycle so the aggregate includes the day's last cycle |
| **Weekly Report** | End of the daily cycle (`REPORTS_ENABLED`) | Week-in-review text root (mood, delta vs prior week, happiest/unhappiest day, stickiest topic, posts analysed) plus a reply quoting the post of the week. Covers the last complete Monday–Sunday week; the guard key `weekly_report_last_week` makes it a no-op until a new week exists, so it posts on Monday and catches up if that run was skipped. Skipped with fewer than 5 daily rows |
| **Monthly Report** | After yearly posting (`REPORTS_ENABLED`) | Candlestick chart root plus a volume chart reply (English and, when tracked for every day, the full firehose; when every day also has a language split, the firehose is stacked as English plus the top 5 languages plus "other"). Covers the previous calendar month; guard key `monthly_report_last_month` (posts on the 1st, catches up if skipped). Skipped with fewer than 20 daily rows |
| **Yearly Posting** | 1st of month 01:00 UTC | 365-day sentiment chart, pinned to profile. Same goroutine/guard as the daily cycle, so chart rendering never overlaps a cycle or a daily run; a skipped tick logs `job_overlap_skipped` |
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
```

Before posting the summary, the #1 post is checked for quote controls via an
authenticated `app.bsky.feed.getPosts` (`Viewer.EmbeddingDisabled`; viewer state
is only populated when authenticated). When quoting is disabled the summary is
posted without the record embed — which would otherwise render as "Removed by
author" — and its first line gains `· no embed, post is quote controlled`. The
check fails open: an API error leaves the embed in place.

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
| `internal/config` | Configuration types |

**Legacy packages** (AWS Lambda era, still in repo): `internal/backup`, `internal/awsutil`, `internal/lambda`, `internal/scheduler`

### SQLite Database

Single file on Fly.io persistent volume: `/data/hourstats-{profile}.db` (WAL mode).

Three connection pools: `writeDB` (1 conn, 30s timeout), `readDB` (4 conns, read-only), `maintDB` (1 conn, 1s timeout for WAL checkpoints).

Key tables: `post_buffer` (2h retention), `runs` (48h), `sentiment_history` (nominally 8 days, but the purge has no caller so every hourly cycle since Jan 2026 is retained), `daily_sentiment` (3 years), `topic_tokens` (26h), `topic_snapshots` (48h), `topic_daily` and `daily_top_post` (400 days, rolled up from `topic_snapshots` and `runs` by the daily cycle), `language_counts` (per cycle, 8 days) and `language_daily` (400 days), `key_value` (permanent).

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
- Image upload: blob reference then embed in post record

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
- Cursor is persisted in SQLite `key_value` table for resume-on-restart
- `DRY_RUN=true` prevents all posting but still runs analysis and stores data
- Legacy AWS Lambda code remains in repo but is not used by the Fly.io binary