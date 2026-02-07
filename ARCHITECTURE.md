# HourStats Architecture

HourStats is a Bluesky sentiment analysis bot that runs on AWS Lambda. It fetches trending posts from Bluesky's public API, analyzes sentiment using VADER, and posts hourly summaries back to Bluesky along with sparkline and yearly trend charts.

## System Overview

```
EventBridge (every 30 min)
        |
        v
  +-----------+     async invoke     +-------------+     async invoke     +-------------------+
  |  Fetcher  | ------------------> |  Processor   | ------------------> | Sparkline Poster  |
  +-----------+                     +-------------+                      +-------------------+
  Creates run state                 Analyzes sentiment                   Generates 7-day chart
  Fetches posts from Bluesky API    Ranks top posts                      Posts as reply to summary
  Stores posts in DynamoDB          Posts summary to Bluesky
                                    Stores sentiment history

EventBridge (daily midnight UTC)         EventBridge (daily 1am UTC)
        |                                        |
        v                                        v
  +-------------------+                  +----------------+
  | Daily Aggregator  |                  | Yearly Poster  |
  +-------------------+                  +----------------+
  Aggregates 24h sentiment               Generates 365-day chart
  into daily summary                     Posts to Bluesky + pins
```

## Lambda Functions

### Hourly Pipeline (async invocation chain)

| Function | Trigger | Timeout | Memory | Purpose |
|----------|---------|---------|--------|---------|
| `hourstats-fetcher` | EventBridge `rate(30 minutes)` | 15 min | 128 MB | Creates run state, fetches posts from Bluesky API, stores in DynamoDB, dispatches processor |
| `hourstats-processor` | Async invoke from fetcher | 5 min | 128 MB | Analyzes sentiment (VADER), ranks posts by engagement, posts summary to Bluesky, stores sentiment history, dispatches sparkline poster |
| `hourstats-sparkline-poster` | Async invoke from processor | 5 min | 256 MB | Generates 7-day sparkline PNG, posts as reply to main summary |

### Scheduled Functions (independent)

| Function | Trigger | Timeout | Memory | Purpose |
|----------|---------|---------|--------|---------|
| `hourstats-daily-aggregator` | EventBridge `cron(0 0 * * ? *)` | 5 min | 256 MB | Aggregates 24h of sentiment history into daily summary |
| `hourstats-yearly-poster` | EventBridge `cron(0 1 * * ? *)` | 10 min | 512 MB | Generates yearly sentiment chart, posts to Bluesky, pins to profile |

## Data Flow

### Hourly Pipeline

1. **EventBridge** fires every 30 minutes
2. **Fetcher** creates a `RunState` in DynamoDB with a unique run ID and cutoff time, then fetches posts from Bluesky's trending/search API using cursor-based pagination. Posts are stored in DynamoDB in batches. When complete, it asynchronously invokes the processor.
3. **Processor** reads all posts for the run from DynamoDB, deduplicates by URI, filters by cutoff time, analyzes sentiment using the VADER algorithm, selects top 5 posts by engagement score, formats and posts a summary to Bluesky, stores a sentiment data point for sparkline generation, and asynchronously invokes the sparkline poster.
4. **Sparkline Poster** reads 7 days of sentiment history, generates a sparkline chart image using the `gg` graphics library, and posts it as a reply to the main summary post.

### Daily/Yearly Pipeline

1. **Daily Aggregator** runs at midnight UTC, reads all sentiment data points from the past 24 hours, calculates min/max/average sentiment, and stores a daily summary.
2. **Yearly Poster** runs at 1am UTC, reads up to 365 days of daily summaries, generates a yearly trend chart, and posts it to Bluesky with Wikipedia event links for sentiment extremes.

## DynamoDB Tables

### `hourstats-state`

Primary state table for run coordination and post storage.

| Key | Type | Description |
|-----|------|-------------|
| `runId` (PK) | String | Unique run identifier (`run-{unixnano}`) |
| `postId` (SK) | String | Either step name (`orchestrator`, `fetcher`, `analyzer`, `aggregator`) for run metadata, or `post-{index}` / `batch-{index}` for post data |

**Global Secondary Indexes:**
- `status-index` (PK: `status`, SK: `createdAt`) — query runs by status
- `posts-index` (PK: `runId`, SK: `postId`) — efficient post retrieval
- `runs-index` (PK: `runId`, SK: `createdAt`) — run listing

**TTL:** 2 days (automatic cleanup)

### `hourstats-sentiment-history`

Stores per-run sentiment data points for sparkline generation.

| Key | Type | Description |
|-----|------|-------------|
| `runId` (PK) | String | Run identifier |
| `timestamp` (SK) | String | ISO 8601 timestamp |

**Key fields:** `netSentimentPercent`, `sentimentCategory`, `totalPosts`, `averageCompoundScore`

**GSI:** `timestamp-index` (PK: `timestamp`, SK: `runId`)

### `hourstats-daily-sentiment`

Stores aggregated daily sentiment summaries.

| Key | Type | Description |
|-----|------|-------------|
| `date` (PK) | String | Date in `YYYY-MM-DD` format |
| `runId` (SK) | String | Daily aggregate identifier |

**Key fields:** `averageSentiment`, `minSentiment`, `maxSentiment`, `totalRuns`, `totalPosts`

**GSI:** `date-index` (PK: `date`, SK: `createdAt`)

**TTL:** 3 years

## AWS Resources

### EventBridge Rules

| Rule | Schedule | Target |
|------|----------|--------|
| `hourstats-schedule` | `rate(30 minutes)` | `hourstats-fetcher` |
| `hourstats-daily-aggregation-schedule` | `cron(0 0 * * ? *)` | `hourstats-daily-aggregator` |
| `hourstats-yearly-posting-schedule` | `cron(0 1 * * ? *)` | `hourstats-yearly-poster` |

### SSM Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `/hourstats/bluesky/handle` | String | Bluesky account handle |
| `/hourstats/bluesky/password` | SecureString | Bluesky app password |
| `/hourstats/settings/analysis_interval_minutes` | String | Analysis window (default: 60) |
| `/hourstats/settings/top_posts_count` | String | Number of top posts (default: 5) |
| `/hourstats/settings/min_engagement_score` | String | Minimum engagement threshold |
| `/hourstats/settings/dry_run` | String | Kill switch — prevents posting to Bluesky |

## Project Structure

```
cmd/
  lambda-fetcher/          # Entry point: EventBridge -> fetch posts -> invoke processor
  lambda-processor/        # Entry point: Analyze, aggregate, post summary
  lambda-sparkline-poster/ # Entry point: Generate + post 7-day sparkline chart
  lambda-daily-aggregator/ # Entry point: Aggregate daily sentiment
  lambda-yearly-poster/    # Entry point: Generate + post yearly chart
  graph-lab/               # Local tool for chart design experimentation
  diagnostics/             # Local diagnostic utilities
  dynamodb-backup/         # DynamoDB backup utility
  dynamodb-restore/        # DynamoDB restore utility

internal/
  analyzer/    # VADER sentiment analysis (govader)
  client/      # Bluesky AT Protocol client (post creation, image upload, facets)
  config/      # Configuration types
  formatter/   # Post content formatting (character counting, Bluesky limits)
  lambda/      # SSM config loader for Lambda environment
  sparkline/   # Chart generation (7-day sparkline + yearly chart via gg library)
  state/       # DynamoDB state management (RunState, Post, SentimentHistory, DailySentiment)

terraform/     # Infrastructure as Code (Terraform)
  main.tf      # Lambda functions, DynamoDB tables, EventBridge rules, IAM
  backend.tf   # S3 remote state with DynamoDB locking
  daily-sentiment.tf   # Daily aggregator + yearly poster infrastructure
  sentiment-history.tf # Sentiment history table
```

## Key Design Decisions

1. **Async Lambda chain vs Step Functions**: The pipeline uses direct async Lambda invocations (`InvocationType: Event`) rather than Step Functions. This is simpler to deploy and debug, with the tradeoff of less built-in retry/error handling.

2. **DynamoDB for state coordination**: Run state is shared between Lambdas via DynamoDB rather than passing large payloads between invocations. This avoids Lambda payload size limits (256 KB) when dealing with thousands of posts.

3. **Separate sparkline poster**: Image generation requires more memory (256 MB vs 128 MB) and the `gg` graphics library. Keeping it separate isolates potential failures from the critical summary-posting path.

4. **VADER for sentiment**: Uses the govader library (Go port of VADER) for lightweight, rule-based sentiment analysis that runs within Lambda's memory constraints without requiring ML model loading.

5. **Embedded images**: Sparkline and yearly charts are uploaded directly to Bluesky's blob service rather than stored in S3, eliminating the need for presigned URLs or public buckets.

## Deployment

- **CI/CD**: GitHub Actions (`.github/workflows/deploy-lambda.yml`)
- **Deploy trigger**: Push to `main` branch (only paths matching `cmd/lambda*/**`, `internal/**`, `terraform/**`)
- **Build**: Each Lambda is cross-compiled for `linux/amd64` and packaged as a zip
- **IaC**: Terraform with S3 remote state and DynamoDB state locking
- **Rollback**: Git revert + push to main triggers redeploy. Tag `pre-lambda-simplification` marks the last known-good state before architecture changes.

## Operational Controls

| Control | Mechanism | Effect |
|---------|-----------|--------|
| **Kill switch** | SSM `/hourstats/settings/dry_run` = `"true"` | Prevents all posting to Bluesky |
| **Minimum posts** | Processor requires 1000+ filtered posts | Skips sentiment analysis if insufficient data |
| **Early stop** | Fetcher stops at 14 min to leave buffer for processor dispatch | Prevents Lambda timeout with partial work |
| **TTL cleanup** | DynamoDB TTL on all tables | Automatic data expiry (2 days for runs, 3 years for daily sentiment) |
