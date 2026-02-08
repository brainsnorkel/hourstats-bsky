## Why

The current fetcher uses `app.bsky.feed.searchPosts` to retroactively query posts from the last 30 minutes. This approach has three fundamental limitations:

1. **Hard pagination cap of ~10,000 results.** The search API is backed by Elasticsearch with a `max_result_window` limit. The codebase already codes around this (`maxIterations=100`, `cursorNum > 10000` guard). Bluesky sees 50,000–100,000+ posts per 30-minute window — the current approach captures a fraction.

2. **The search API is not designed for exhaustive collection.** The lexicon itself warns: cursor *"may not necessarily allow scrolling through entire result set"* and hitsTotal *"may be rounded/truncated, and may not be possible to paginate through all hits."*

3. **Language filter bias.** The current query uses `lang="en"`, excluding all non-English posts from the sample. While the bot posts English summaries, this biases the sentiment analysis toward English-speaking users only.

Jetstream is Bluesky's official real-time JSON event stream. It delivers **every** post as it's created with no rate limits, no pagination, and no caps. It is the correct tool for "get all posts from the last 30 minutes."

## What Changes

- **Replace** the search-API-based `lambda-fetcher` with a persistent Jetstream consumer service (ECS Fargate) that buffers posts into DynamoDB continuously
- **Simplify** the fetcher Lambda into a lightweight "window closer" that stamps a run and dispatches the processor
- **Preserve** the entire downstream pipeline unchanged: processor, sparkline poster, daily aggregator, yearly poster all continue to work with the same DynamoDB state and event contracts

## Capabilities

### New Capabilities
- `jetstream-consumer`: Persistent WebSocket consumer that ingests all `app.bsky.feed.post` events from Jetstream and writes them to DynamoDB in rolling 30-minute windows
- `window-trigger`: Replaces the search-based fetcher — creates a run, closes the current window, and dispatches the processor

### Modified Capabilities
- `post-fetching`: Replaced entirely by `jetstream-consumer` + `window-trigger`
- `run-coordination`: Minor update — run creation now references a pre-populated window rather than triggering a fetch

### Unchanged Capabilities
- `sentiment-analysis`
- `summary-posting`
- `sparkline-generation`
- `daily-aggregation`
- `yearly-charting`
- `operational-controls`

## Impact

- `cmd/lambda-fetcher/` — gutted and replaced with window-trigger logic
- `cmd/jetstream-consumer/` — new ECS service
- `internal/client/bluesky.go` — `GetTrendingPostsBatch` and `GetTrendingPosts` become unused (retained for fallback/testing)
- `internal/state/state.go` — new `WindowManager` for writing/reading windowed post buffers
- `terraform/` — new ECS Fargate task definition, ECR repository, security groups, CloudWatch log group
- `asyncapi.yaml` — updated event flow diagram
- No changes to: processor, sparkline poster, daily aggregator, yearly poster, formatter, analyzer
