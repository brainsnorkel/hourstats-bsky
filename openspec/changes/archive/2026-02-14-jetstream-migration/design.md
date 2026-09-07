## Context

HourStats traditionally collected posts by polling the Bluesky search API (`app.bsky.feed.searchPosts`) every 30 minutes. This pull-based architecture suffered from search API limitations, including a hard pagination ceiling of ~10,000 results and inconsistent result sets.

Bluesky operates Jetstream — a public, JSON-based WebSocket stream of all network activity. It delivers every post in real time with no rate limits or caps. This design reflects the actual implementation that migrated HourStats to a push-based Jetstream consumer, simplifying the architecture from a distributed serverless model to a single-process application.

## Goals / Non-Goals

**Goals:**
- Capture **100% of Bluesky posts** in each 30-minute window (previously ~10% coverage).
- Remove dependency on the search API's pagination limits and `since`/`until` quirks.
- Implement **Trending Topics** using TF-IDF and Gemini Flash.
- Enable **S3 backups** for long-term data durability.
- Improved charting capabilities (trendlines, volume charts).
- Maintain identical core output: same Bluesky post format and sentiment indicators.
- Drastically reduce infrastructure costs (from AWS overhead to ~$5/mo on Fly.io).

**Non-Goals:**
- Changing the core sentiment analysis algorithm (GoVader).
- Supporting historical backfill (Jetstream is live-only; for historical data, search API remains available as fallback).
- Multi-region deployment of the consumer.

## Architecture Overview

The implementation moved away from the proposed AWS Fargate/Lambda/DynamoDB stack in favor of a simpler, more cost-effective single-binary architecture.

```
ACTUAL:
  Jetstream WS ──→ cmd/hourstats (Fly.io Single Binary) ──→ SQLite (Persistent Volume)
                                        │
                                        ├── Consumer Goroutine
                                        ├── Scheduler Goroutines (30m, 15m, 6h, Daily)
                                        └── S3 Backup (Daily)
```

The entire system runs as a single Go binary (`cmd/hourstats/main.go`) on Fly.io. It maintains a persistent WebSocket connection to Jetstream while simultaneously running wall-clock aligned tickers for analysis, aggregation, and trending topic tasks. Data is stored in a local SQLite database on a persistent volume, providing high performance with zero network latency or IAM complexity.

## Decisions

### Decision 1: Single binary on Fly.io, not ECS Fargate/Lambda

Instead of a distributed set of Lambda functions and an ECS service, the entire application is bundled into a single binary.
- **Simplest Architecture**: No network calls between components, no IAM roles, no Terraform for individual functions.
- **Cost**: Runs on a single Fly.io instance for ~$5/mo.
- **Reliability**: A single process simplifies monitoring and state management.

### Decision 2: SQLite on persistent volume, not DynamoDB

SQLite was chosen over DynamoDB for cost and performance reasons.
- **Cost**: Jetstream generates ~152M writes/month. On DynamoDB, this would cost between $29–$190/mo; on a Fly.io volume, it costs $0.
- **Performance**: SQLite handles 58+ writes/second trivially in WAL mode with zero network latency.
- **Simplicity**: No complex partition key design or capacity management.

### Decision 3: Write-through to SQLite post buffer

The consumer writes each post to the SQLite `post_buffer` table as it arrives.
- **Durability**: Posts survive process restarts.
- **Decoupling**: The ingestion path is separated from the analysis path by the database.

### Decision 4: Consumer handles reconnection via SQLite-persisted cursor

The consumer maintains its state by storing the Jetstream cursor.
- **Persistence**: The cursor is stored in a `key_value` table in SQLite rather than DynamoDB.
- **Resume**: On restart, the consumer loads the last cursor and resumes the stream, ensuring no data loss during deployments.

### Decision 5: English language filter retained

While the original proposal suggested removing the language filter, the actual implementation retains the requirement for an explicit `lang=en` tag.
- **Rationale**: This maintains sentiment parity with production history and ensures the top-5 posts remain readable for the target audience.

### Decision 6: Search API client code retained for fallback/testing

The legacy search API client code is kept in the codebase.
- **Fallback**: It can be used if Jetstream is unavailable or for local testing without a live stream.
- **Utility**: It remains useful for historical queries that Jetstream cannot provide.

## Component Specifications

### Jetstream Consumer
A dedicated goroutine in `cmd/hourstats/main.go` calling `internal/jetstream/consumer.go`.
- **WebSocket**: Connects to the Jetstream stream.
- **Callbacks**: Implements `OnPost` (inserts to SQLite via `internal/store/`) and `SaveCursor`/`LoadCursor` for state management.
- **Filtering**: Filters for English language posts and valid feed post collections.

### Analysis Cycle
A wall-clock aligned 30-minute ticker triggers `runAnalysisCycle()`.
- **Data Source**: Reads posts from the SQLite `post_buffer` table using `db.GetPostsSince()`.
- **Hydration**: Uses `internal/hydrator/` to batch call `getPosts` (25 URIs/batch) to get real-time engagement (likes, reposts, replies).
- **Processing**: Analyzes sentiment, formats the summary, and posts to Bluesky.

### Engagement Hydrator (`internal/hydrator/hydrator.go`)
- **Batching**: Resolves handle/DID mappings and fetches engagement metrics in batches of 25.
- **Concurrency**: Parallelizes API requests to stay within analysis window constraints.

### Charts
- Reuses `internal/sparkline/` for generating sparklines, sentiment trendlines, and volume charts.

### Daily/Yearly Tasks
- **Daily**: Wall-clock aligned ticker at midnight UTC runs `runDailyAggregation()`, `runBackup()`, and `runDailyTopPostQuote()`.
- **Yearly**: Wall-clock aligned ticker at 01:00 UTC on the 1st of the year runs `runYearlyPosting()`.

### Trending Topics (`internal/topics/`)
- **Tickers**: Separate 15-minute and 6-hour tickers.
- **Analysis**: Uses TF-IDF for term extraction and Google Gemini Flash for semantic grouping and naming.
- **Output**: Generates bump charts showing topic trajectories.

### S3 Backup (`internal/store/backup.go`)
- **Daily Backup**: Streams the SQLite database file to S3 for disaster recovery and long-term storage.

## Data Flow

1. **Jetstream WS → Consumer goroutine**: ~1,500-3,000 posts/min arrive via WebSocket.
2. **Consumer → SQLite**: Each English post is inserted into the `post_buffer` table.
3. **Wall-clock ticker (every 30 min)** → `runAnalysisCycle()` is triggered.
4. **Analysis → SQLite**: Reads posts since the last cutoff.
5. **Analysis → Bluesky API**: Hydrates engagement via `getPosts` (batches of 25).
6. **Analysis → Bluesky**: Posts sentiment summary + sparkline reply + trendline reply.
7. **Daily ticker** → Triggers `runDailyAggregation()`, `runBackup()`, and `runDailyTopPostQuote()`.
8. **Yearly ticker** → Triggers `runYearlyPosting()`.
9. **Topic tickers** → Triggers `RunAnalysisCycle()` (15 min) and `RunTrendingPost()` (6h).

## Infrastructure

The system is deployed on **Fly.io** using a simplified infrastructure model.

- **Fly.io Apps**: Separate `prod` and `staging` apps configured via `fly.toml`.
- **Docker**: Multi-stage build (starting from `golang:1.24-alpine`, resulting in a slim `alpine:3.21` image).
- **Volumes**: Persistent SSD volumes used for the SQLite database.
- **Secrets**: Managed via `fly secrets set` for Bluesky credentials and S3 keys.
- **Deployment**: Managed via `make deploy-prod` and `make deploy-staging`.

## Risks / Trade-offs

1. **Single Instance**: Running as a single process means a crash takes down the entire bot. However, Jetstream cursor persistence ensures it resumes exactly where it left off upon restart.
2. **SQLite Concurrent Writes**: While SQLite handles the load well in WAL mode, the system must carefully manage connections to avoid "database is locked" errors during heavy analysis cycles.
3. **Engagement Hydration**: Because Jetstream doesn't include engagement metrics, the bot must make thousands of API calls to hydrate metrics. This is mitigated by batching and the 30-minute window.
4. **Volume Storage**: Local storage requires backups. This is addressed by the automated daily S3 backup process.
