## Why

The current fetcher uses `app.bsky.feed.searchPosts` to retroactively query posts from the last 30 minutes. This approach has three fundamental limitations:

1. **Hard pagination cap of ~10,000 results.** The search API is backed by Elasticsearch with a `max_result_window` limit. The codebase already codes around this (`maxIterations=100`, `cursorNum > 10000` guard). Bluesky sees 50,000–100,000+ posts per 30-minute window — the current approach captures a fraction.

2. **The search API is not designed for exhaustive collection.** The lexicon itself warns: cursor *"may not necessarily allow scrolling through entire result set"* and hitsTotal *"may be rounded/truncated, and may not be possible to paginate through all hits."*

3. **Language filter bias.** The current query uses `lang="en"`, excluding all non-English posts from the sample. While the bot posts English summaries, this biases the sentiment analysis toward English-speaking users only.

Jetstream is Bluesky's official real-time JSON event stream. It delivers **every** post as it's created with no rate limits, no pagination, and no caps. It is the correct tool for "get all posts from the last 30 minutes."

## What Changes

- **Replace** the search-API-based Lambda pipeline with a single Go binary running on Fly.io
- **Replace** DynamoDB state management with SQLite on a persistent volume
- **Replace** the ECS Fargate Jetstream consumer with a goroutine within the same binary
- **Replace** EventBridge scheduled triggers with wall-clock aligned in-process tickers
- **Add** trending topics feature (TF-IDF + Gemini Flash, bump chart, every 6h)
- **Add** S3 backup for SQLite database
- **Preserve** the entire downstream logic: VADER sentiment, post formatting, chart generation

## Capabilities

### New Capabilities
- `jetstream-consumer`: Goroutine within the main binary that ingests all `app.bsky.feed.post` events from Jetstream and buffers them in SQLite
- `analysis-scheduler`: Wall-clock aligned tickers that trigger analysis cycles
- `sqlite-store`: Local SQLite storage for buffered posts and run state
- `engagement-hydrator`: Hydrates real-time engagement metrics for buffered posts
- `trending-topics`: Extracts and tracks trending topics using TF-IDF and Gemini Flash
- `s3-backup`: Periodic backup of the SQLite database to S3

### Modified Capabilities
- `post-fetching`: Replaced by `jetstream-consumer` + `engagement-hydrator`
- `run-coordination`: Now internal to the single binary

### Unchanged Capabilities
- `sentiment-analysis`
- `summary-posting`
- `sparkline-generation`
- `daily-aggregation`
- `yearly-charting`
- `operational-controls`

## Impact

- `cmd/hourstats/` — new single binary entry point (replaces all `cmd/lambda-*`)
- `internal/store/` — new SQLite storage layer (replaces `internal/state/`)
- `internal/jetstream/` — new Jetstream consumer package
- `internal/hydrator/` — new engagement hydration package
- `internal/topics/` — new trending topics package
- `internal/sparkline/` — extended with trendline, volume, trending chart generators
- `fly.prod.toml`, `fly.staging.toml` — Fly.io deployment configs
- `Dockerfile` — multi-stage build for Fly.io
- No changes to: `internal/analyzer/`, `internal/formatter/`, `internal/client/` (core logic reused)
