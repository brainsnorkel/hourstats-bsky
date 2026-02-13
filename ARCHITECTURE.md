# HourStats Architecture

HourStats is a Bluesky sentiment analysis bot. It ingests every public English-language Bluesky post in real time via Jetstream, analyzes sentiment using VADER, and posts 30-minute summaries with the top 5 most engaged posts, sparkline charts, and trending topics.

> **Migration status:** The project is migrating from AWS Lambda to Fly.io. Production currently runs on AWS Lambda; the `migrate-to-jetstream` branch contains the complete Fly.io reimplementation. See the [Migration Plan](openspec/changes/jetstream-migration/MIGRATION_PLAN.md) for details.

## System Overview (Fly.io Architecture)

```
Bluesky Network (all posts)
        |
        | Jetstream WebSocket
        v
  +-------------------+
  | Jetstream Consumer |  goroutine — always running
  | (internal/jetstream)|  filters: English, post creates only
  +-------------------+
        |
        | InsertPost()
        v
  +-------------------+
  | SQLite Database   |  /data/hourstats-{profile}.db
  | (internal/store)  |  WAL mode, busy timeout
  +-------------------+
        |
        | Wall-clock aligned tickers
        v
  +---------+  every 30 min   +---------+  every 15 min   +----------+  every 6h
  | Analysis|                 | Topic   |                 | Trending |
  | Cycle   |                 | Analysis|                 | Post     |
  +---------+                 +---------+                 +----------+
  Read posts since cutoff     TF-IDF extraction           Hydrate exemplar posts
  Hydrate engagement          Gemini Flash grouping       Generate bump chart
  VADER sentiment analysis    Volume-based ranking        Format + post to Bluesky
  Post summary to Bluesky     Identity tracking
  Generate sparkline reply    Store snapshot
  Generate trendline reply

  +----------+  daily midnight   +----------+  monthly 1st 01:00 UTC
  | Daily    |                   | Yearly   |
  | Cycle    |                   | Cycle    |
  +----------+                   +----------+
  SQLite backup → S3             Generate yearly chart
  Daily aggregation              Post + pin to profile
  Top-post quote reply
```

## Single Binary Architecture

Everything runs inside a single Go binary (`cmd/hourstats/main.go`) on Fly.io:

| Component | Implementation | Trigger |
|-----------|---------------|---------|
| **Jetstream Consumer** | Goroutine calling `internal/jetstream/consumer.go` | Always running (auto-restart on failure) |
| **Analysis Cycle** | `runAnalysisCycle()` | Wall-clock ticker every 30 min |
| **Sparkline + Trendline** | Called sequentially after analysis | Part of analysis cycle |
| **Topic Analysis** | `topics.Analyzer.RunAnalysisCycle()` | Wall-clock ticker every 15 min |
| **Trending Post** | `topics.Analyzer.RunTrendingPost()` | Wall-clock ticker every 6h |
| **Daily Cycle** | `runBackup()` + `runDailyAggregation()` + `runDailyTopPostQuote()` | Wall-clock ticker daily midnight UTC |
| **Yearly Posting** | `runYearlyPosting()` | Wall-clock ticker daily 01:00 UTC (posts on 1st) |
| **Stall Detection** | Checks `lastPostReceived` atomic | Ticker every 5 min |

### Wall-Clock Aligned Scheduling

Tickers fire at clean UTC clock boundaries (e.g., :00 and :30 for the 30-minute cycle) rather than at fixed intervals from process start. This means deploys and restarts don't shift the schedule.

## Data Flow

### 30-Minute Sentiment Pipeline

1. **Jetstream** → Consumer goroutine receives ~1,500–3,000 posts/min via WebSocket
2. **Consumer** → Filters for English (`lang=en`), post creates only → inserts to SQLite `post_buffer`
3. **Ticker** fires at :00 or :30 UTC → `runAnalysisCycle()`
4. **Read** posts from SQLite since cutoff (30 min ago)
5. **Hydrate** engagement via `app.bsky.feed.getPosts` (25 URIs/batch, concurrent) — resolves handles from DIDs
6. **Analyze** sentiment using VADER, categorize posts (+/-/x), select top 5 by engagement
7. **Post** summary to Bluesky with mood hashtag, clickable handle facets, embed card
8. **Generate** 7-day sparkline chart PNG → post as reply
9. **Generate** sentiment trendline chart (root vs reply) → post as reply
10. **Save** run state and sentiment data point to SQLite

### Trending Topics Pipeline

1. **On ingest**: Root posts (non-replies) are tokenized and stored in `topic_tokens`
2. **Every 15 min**: TF-IDF extracts top 30 terms → Gemini Flash groups into 5 topics → rank by post volume → track identities via Jaccard similarity → store snapshot
3. **Every 6h**: Hydrate exemplar posts → generate bump chart → format post text with movement indicators → post to Bluesky (standalone, not threaded)

### Daily/Yearly Pipeline

1. **Daily** (midnight UTC): Back up SQLite to S3 → aggregate day's sentiment → quote-reply the day's top post
2. **Yearly** (1st of month, 01:00 UTC): Generate 365-day chart → post to Bluesky → pin to profile

## SQLite Database

All state stored in a single SQLite file on Fly.io persistent volume (`/data/hourstats-{profile}.db`). WAL mode enabled, three separate connection pools.

### Connection Pools

| Pool | Max conns | Busy timeout | Purpose |
|------|-----------|--------------|---------|
| `writeDB` | 1 | 30s | All writes (batched post/token inserts, run state, purges) |
| `readDB` | 4 | 30s | All reads (analysis, topics, stats API). `query_only=ON` pragma. |
| `maintDB` | 1 | 1s | WAL checkpoints only. Short timeout so it never blocks writers. |

On startup, `RunStartupMaintenance()` cleans derived tables, purges stale rows, and forces a WAL checkpoint before the firehose connects. During operation, `RunWALCheckpoint()` runs PASSIVE checkpoints every 5 minutes via the maintenance pool. See [docs/WRITE_BOTTLENECK_FIX.md](docs/WRITE_BOTTLENECK_FIX.md) for the full write path design and scaling guidance.

### Tables

| Table | Purpose | Retention |
|-------|---------|-----------|
| `post_buffer` | Buffered posts from Jetstream | 2 hours (purged each cycle) |
| `runs` | Analysis cycle state (run ID, status, post count, sentiment) | 48 hours |
| `sentiment_history` | Per-run sentiment data points (for sparklines) | 8 days |
| `daily_sentiment` | Aggregated daily sentiment (for yearly charts) | 3 years |
| `daily_top_post` | Highest engagement post per day | 3 years |
| `key_value` | Jetstream cursor, general settings | Permanent |
| `topic_tokens` | Tokenized root posts for TF-IDF | 26 hours |
| `token_postings` | Schema preserved but no longer written during ingest | Emptied on startup |
| `topic_snapshots` | 15-min topic analysis results | 48 hours |
| `topic_identity` | Persistent topic UUIDs and colours | 7 days |

## Environment Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `HOURSTATS_PROFILE` | `staging` | Profile name (used in DB filename and logging) |
| `DATA_DIR` | `/data` | Directory for SQLite database and backups |
| `BLUESKY_HANDLE` | (required) | Bluesky account handle |
| `BLUESKY_PASSWORD` | (required) | Bluesky app password |
| `DRY_RUN` | `false` | Prevents all posting to Bluesky |
| `ANALYSIS_INTERVAL_MINUTES` | `30` | Sentiment analysis window |
| `TRENDING_ENABLED` | `false` | Enable trending topics feature |
| `GOOGLE_AI_API_KEY` | (required if trending) | Gemini API key for topic grouping |
| `TRENDING_INTERVAL` | `15` | Topic analysis frequency (minutes) |
| `TRENDING_POST_HOURS` | `6` | Trending post frequency (hours) |
| `S3_BACKUP_BUCKET` | (optional) | S3 bucket for daily SQLite backups |
| `S3_BACKUP_REGION` | `us-west-2` | AWS region for S3 backups |
| `BACKUP_RETAIN_DAYS` | `1` | Local backup retention |
| `AWS_ACCESS_KEY_ID` | (optional) | AWS credentials for S3 backups |
| `AWS_SECRET_ACCESS_KEY` | (optional) | AWS credentials for S3 backups |

## Project Structure

```
hourstats-bsky/
├── cmd/
│   ├── hourstats/             # Main binary — Fly.io entry point (single process)
│   ├── import-dynamodb/       # Tool: seed SQLite from DynamoDB exports
│   ├── force-trending/        # Tool: manually trigger trending topic analysis
│   ├── graph-lab/             # Tool: chart design experimentation
│   ├── lambda-fetcher/        # [Legacy] AWS Lambda fetcher
│   ├── lambda-processor/      # [Legacy] AWS Lambda processor
│   ├── lambda-sparkline-poster/ # [Legacy] AWS Lambda sparkline
│   ├── lambda-daily-aggregator/ # [Legacy] AWS Lambda daily aggregation
│   ├── lambda-yearly-poster/  # [Legacy] AWS Lambda yearly chart (still in use by production)
│   ├── dynamodb-backup/       # [Legacy] DynamoDB backup utility
│   └── dynamodb-restore/      # [Legacy] DynamoDB restore utility
├── internal/
│   ├── store/                 # SQLite storage layer (post buffer, runs, sentiment, topics, backups)
│   ├── jetstream/             # Jetstream WebSocket consumer (event parsing, cursor management)
│   ├── hydrator/              # Engagement hydration (batch getPosts, handle resolution)
│   ├── topics/                # Trending topics (TF-IDF, Gemini grouping, ranking, tracking, charting)
│   ├── analyzer/              # VADER sentiment analysis (govader)
│   ├── client/                # Bluesky AT Protocol client (posting, image upload, facets)
│   ├── formatter/             # Post content formatting (character counting, Bluesky limits)
│   ├── sparkline/             # Chart generation (sparkline, trendline, volume, yearly, trending)
│   ├── state/                 # [Legacy] DynamoDB state management
│   ├── lambda/                # [Legacy] SSM config loader for Lambda
│   ├── awsutil/               # [Legacy] AWS utilities
│   ├── backup/                # [Legacy] DynamoDB backup/restore
│   └── config/                # Configuration types
├── fly.prod.toml              # Fly.io production config (sjc, shared-cpu-1x, 256MB)
├── fly.staging.toml           # Fly.io staging config (sjc, shared-cpu-1x, 512MB)
├── Dockerfile                 # Multi-stage build (golang:1.24-alpine → alpine:3.21)
├── Makefile                   # Build, test, deploy targets
├── terraform/                 # [Legacy] AWS infrastructure as Code
├── openspec/                  # Architecture specifications
│   ├── specs/                 # Main specs (post-fetching, sentiment, charting, etc.)
│   └── changes/               # Change proposals (jetstream-migration, trending-topics, etc.)
└── docs/                      # Feature documentation
```

## Key Design Decisions

1. **Single binary on Fly.io**: All components run in one process as goroutines. No networking between components, no IAM, no managed databases. $5/month for the Hobby plan covers everything.

2. **SQLite over DynamoDB**: At ~25 writes/sec (English posts from Bluesky, batched 500 at a time every 2 seconds), SQLite handles the load in WAL mode with headroom to ~400 writes/sec. DynamoDB would cost $29–190/month for the same write volume; SQLite costs $0.

3. **Jetstream over search API**: The search API captures ~10% of posts with a 10,000-result pagination cap. Jetstream delivers 100% of posts in real time via WebSocket.

4. **VADER for sentiment**: Lightweight, rule-based sentiment analysis (govader library) that works well for short social media text without requiring ML model loading.

5. **Embedded images**: All charts are uploaded directly to Bluesky's blob service as image embeds, eliminating the need for external image hosting.

6. **Wall-clock aligned scheduling**: Tickers fire at UTC clock boundaries (:00, :30) rather than at intervals from process start. This ensures consistent posting times regardless of deploys or restarts.

7. **English-only filter**: The Jetstream consumer requires explicit `lang=en` tags on posts for sentiment analysis parity with the production Lambda system.

## Deployment

- **Build**: `make build-hourstats` (CGO_ENABLED=0 for Alpine)
- **Deploy staging**: `make deploy-staging` (runs `fly deploy -c fly.staging.toml --ha=false`)
- **Deploy production**: `make deploy-prod` (runs `fly deploy -c fly.prod.toml --ha=false`)
- **Container**: Multi-stage Docker build — Go builder then Alpine runtime with ca-certificates, tzdata, sqlite CLI
- **Secrets**: `fly secrets set BLUESKY_HANDLE=... BLUESKY_PASSWORD=... -a hourstats-staging`
- **Logs**: `make fly-logs-prod` or `make fly-logs-staging`
- **Status**: `make fly-status`

## Operational Controls

| Control | Mechanism | Effect |
|---------|-----------|--------|
| **Kill switch** | `DRY_RUN=true` env var | Prevents all posting to Bluesky |
| **Trending toggle** | `TRENDING_ENABLED=false` env var | Disables trending topics feature |
| **Stall detection** | 5-minute silence check on Jetstream | Logs warning if consumer stops receiving posts |
| **Auto-restart** | Consumer goroutine with exponential backoff (1s → 60s) | Recovers from WebSocket disconnections |
| **Cursor persistence** | SQLite key_value table | Jetstream resumes from last position on restart |
| **Data retention** | Per-table TTL enforcement in purge cycles | Automatic cleanup (2h posts, 8d history, 3y daily) |
| **S3 backups** | Daily SQLite → S3 with configurable retention | Disaster recovery for persistent data |

## Legacy Architecture (AWS Lambda)

The production system currently runs on AWS Lambda. See [AWS_SERVERLESS_DESIGN.md](AWS_SERVERLESS_DESIGN.md) for the legacy architecture documentation. The [Migration Plan](openspec/changes/jetstream-migration/MIGRATION_PLAN.md) tracks the transition to Fly.io.
