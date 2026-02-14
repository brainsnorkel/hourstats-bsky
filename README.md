# Bluesky HourStats

**Live Bot:** [@hourstats.bsky.social](https://bsky.app/profile/hourstats.bsky.social)

> **Note:** This project is an experiment in using [Claude](https://claude.ai) and [Cursor](https://cursor.sh) for AI-assisted software development. The bot monitors the Bluesky firehose in real time via Jetstream and posts sentiment summaries every 30 minutes.

A Go-based AT Protocol/Bluesky client that analyzes trending posts and sentiment to provide hourly statistics about the Bluesky community.

## What It Does

Bluesky HourStats is an automated bot that:
- Monitors the Bluesky firehose in real time via Jetstream
- Ranks posts by engagement (replies + likes + reposts)
- Hydrates engagement metrics (likes, reposts, replies) via Bluesky API
- Performs sentiment analysis using VADER and keyword matching
- Posts summaries with the top 5 posts and overall community sentiment
- Generates 7-day sentiment sparklines
- Creates yearly sentiment charts (monthly posts)
- Tracks trending topics (TF-IDF + Gemini Pro)
- Posts daily top-post quote replies to the yearly thread

## Post Format

```
Bluesky is #satisfied

1. @username.bsky.social +
2. @anotheruser.bsky.social -
3. @thirduser.bsky.social x
4. @fourthuser.bsky.social +
5. @fifthuser.bsky.social x
```

- **Mood Hashtag**: Descriptive sentiment word from 100-word vocabulary
- **Top 5 posts**: Ranked by engagement with clickable links
- **Sentiment indicators**: + (positive), - (negative), x (neutral)
- **7-day sparklines**: Visual sentiment trends posted with each summary
- **Trending topics**: Top 5 topic list with exemplar post links, posted every 30 minutes as reply to sparkline
- **Sentiment trendlines**: Original posts vs reply sentiment comparison
- **Yearly charts**: Monthly posts showing 365 days of sentiment data

## Architecture

The bot runs as a single Go binary on [Fly.io](https://fly.io) with the following goroutines:

- **Jetstream Consumer**: Connects to the Bluesky Jetstream WebSocket firehose, filters English posts, and writes them to a local SQLite database. Auto-restarts with exponential backoff (1s → 60s) on disconnect.
- **Analysis Cycle**: Every 30 minutes (wall-clock aligned), reads posts from the window, hydrates engagement via the Bluesky API, runs VADER sentiment analysis, and posts a summary with the top 5 most engaged posts.
- **Sparkline Poster**: Generates and posts a 7-day sentiment sparkline chart as a reply to each summary.
- **Sentiment Trendline**: Posts an original-vs-reply sentiment comparison chart.
- **Trending Topics**: Every 30 minutes (after sparkline), identifies top 5 topics using TF-IDF analysis (2-hour window) grouped by Gemini Pro, posts a text summary as a reply to the sparkline.
- **Daily Cycle**: Aggregates daily sentiment averages, creates local + S3 backups, posts a daily top-post quote reply to the yearly thread.
- **Yearly Poster**: On the 1st of each month at 01:00 UTC, generates and posts a yearly sentiment chart.

State is stored in a local SQLite database (WAL mode) on a persistent Fly.io volume. Backups are uploaded daily to S3.

## Tech Stack

- **Language**: Go 1.24+
- **AT Protocol**: [Bluesky indigo library](https://github.com/bluesky-social/indigo)
- **Firehose**: Bluesky Jetstream (WebSocket)
- **Sentiment Analysis**: [GoVader](https://github.com/jonreiter/govader)
- **Topic Grouping**: Google Gemini Pro API (configurable via GEMINI_MODEL)
- **Image Generation**: Go graphics library (fogleman/gg)
- **Database**: SQLite (WAL mode) via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO)
- **Deployment**: [Fly.io](https://fly.io) (single VM + persistent volume)
- **Backups**: AWS S3
- **Legacy**: AWS Lambda, DynamoDB, EventBridge, Terraform (original architecture, still in repo)

## Getting Started

### Prerequisites

- Go 1.24 or later
- A Bluesky account
- Bluesky app password (not your regular password)
- Fly.io account (for deployment)
- Google AI API key (for trending topics, optional)

### Installation

1. Clone the repository:
```bash
git clone https://github.com/brainsnorkel/hourstats-bsky.git
cd hourstats-bsky
```

2. Install dependencies:
```bash
make deps
```

3. Set environment variables:
```bash
export BLUESKY_HANDLE="your-handle.bsky.social"
export BLUESKY_PASSWORD="your-app-password"
export DATA_DIR="./data"
export DRY_RUN=true
```

4. Run locally:
```bash
mkdir -p data
go run ./cmd/hourstats
```

For trending topics, also set:
```bash
export TRENDING_ENABLED=true
export GOOGLE_AI_API_KEY="your-gemini-api-key"
export GEMINI_MODEL="gemini-2.5-pro"  # optional, defaults to gemini-2.5-pro
```

## How It Works

1. **Firehose Ingestion**: Connects to Bluesky Jetstream WebSocket, receives all public posts in real time, filters to English, stores in SQLite
2. **Engagement Hydration**: Fetches likes, reposts, and reply counts for each post via the Bluesky API
3. **Sentiment Analysis**: Uses VADER sentiment analysis on post text
4. **Engagement Ranking**: Ranks posts by total engagement (replies + likes + reposts)
5. **Posting**: Publishes top 5 posts with sentiment indicators and mood hashtag
6. **Visualizations**: Generates sparklines, sentiment trendlines, and yearly charts
7. **Trending Topics**: TF-IDF extraction + Gemini Pro grouping → text reply to sparkline with exemplar links

## Features

- ✅ Real-time Jetstream firehose ingestion (English posts)
- ✅ Adult content filtering using Bluesky moderation labels
- ✅ Sentiment analysis with 100-word emotion vocabulary
- ✅ Engagement-based ranking with API hydration
- ✅ Post deduplication
- ✅ 7-day sentiment sparklines
- ✅ Original vs reply sentiment trendlines
- ✅ Yearly sentiment charts with month markers
- ✅ Daily sentiment aggregation
- ✅ Trending topics with exemplar links (TF-IDF + Gemini Pro, posted every 30 min as reply to sparkline)
- ✅ Daily top-post quote reply to yearly thread
- ✅ SQLite with WAL mode on persistent Fly.io volume
- ✅ Daily SQLite → S3 backups (essential tables only)
- ✅ Wall-clock aligned scheduling (deploys don't shift schedule)
- ✅ Jetstream auto-reconnect with exponential backoff
- ✅ Stall detection (warns if no posts received for 5 minutes)

### Trending Topics

Every 30 minutes, the bot identifies the top 5 trending topics on Bluesky and posts them as a reply to the sparkline chart (threaded under the sentiment summary). Topics are extracted using TF-IDF analysis (2-hour window) of root posts (filtered for spam and adult content), grouped by Google Gemini Pro for semantic understanding, and tracked with persistent identities across ranking cycles.

Each topic includes a link to the highest-engagement exemplar post. Spam is filtered at three layers: adult content labels at ingestion, multi-hashtag posts at ingestion, and zero-engagement posts at TF-IDF query time. Users can mute trending posts via `#hstrend` without affecting the sentiment feed.

See [docs/TRENDING_TOPICS.md](docs/TRENDING_TOPICS.md) for a detailed technical walkthrough.

## Development

### Building

```bash
make build
```

### Testing

```bash
make test
```

### Code Formatting

```bash
make fmt
```

### Linting

```bash
make lint
```

## Deployment

The bot is deployed to [Fly.io](https://fly.io) as a single Docker container with a persistent volume for SQLite storage.

### Deploy

```bash
# Production
make deploy-prod

# Staging
make deploy-staging

# Both
make deploy-all
```

### Fly.io Resources

- **Production**: `hourstats-prod` — shared-cpu-1x, 256MB RAM, persistent volume at `/data` (SJC region)
- **Staging**: `hourstats-staging` — shared-cpu-1x, 512MB RAM, persistent volume at `/data` (SJC region)

### Operational Commands

```bash
make fly-status          # Check both app statuses
make fly-logs-prod       # Tail production logs
make fly-logs-staging    # Tail staging logs
fly ssh console -a hourstats-prod   # SSH into production
fly sftp shell -a hourstats-prod    # Transfer files (e.g. database)
```

> **Legacy**: The original AWS Lambda/DynamoDB deployment is documented in [PRODUCTION_DEPLOYMENT.md](PRODUCTION_DEPLOYMENT.md) (marked as legacy).

## Project Structure

```
hourstats-bsky/
├── cmd/
│   ├── hourstats/                # Main binary (Jetstream + scheduler + all cycles)
│   ├── import-dynamodb/          # DynamoDB → SQLite migration tool
│   ├── force-trending/           # Manual trending topics trigger
│   ├── graph-lab/                # Chart experimentation tool
│   ├── lambda-fetcher/           # [Legacy] AWS Lambda fetcher
│   ├── lambda-processor/         # [Legacy] AWS Lambda processor
│   ├── lambda-sparkline-poster/  # [Legacy] AWS Lambda sparkline
│   ├── lambda-daily-aggregator/  # [Legacy] AWS Lambda daily aggregator
│   ├── lambda-yearly-poster/     # [Legacy] AWS Lambda yearly poster
│   ├── dynamodb-backup/          # [Legacy] DynamoDB backup utility
│   ├── dynamodb-restore/         # [Legacy] DynamoDB restore utility
│   ├── diagnostics/              # [Legacy] Production diagnostics tool
│   └── local-test/               # [Legacy] Local testing harness
├── internal/
│   ├── store/                    # SQLite database layer (schema, queries, backup)
│   ├── jetstream/                # Jetstream WebSocket consumer
│   ├── hydrator/                 # Engagement hydration via Bluesky API
│   ├── topics/                   # Trending topics (TF-IDF, Gemini grouping, bump chart)
│   ├── client/                   # Bluesky API client
│   ├── analyzer/                 # Sentiment analysis (VADER)
│   ├── formatter/                # Post formatting
│   ├── sparkline/                # Chart generation (sparkline, trendline, yearly, bump)
│   ├── state/                    # [Legacy] DynamoDB state management
│   ├── awsutil/                  # [Legacy] AWS utilities
│   └── backup/                   # [Legacy] DynamoDB backup logic
├── openspec/                     # Architecture specifications
├── Dockerfile                    # Multi-stage build (Go 1.24 → Alpine 3.21)
├── fly.prod.toml                 # Fly.io production config
├── fly.staging.toml              # Fly.io staging config
└── Makefile                      # Build, test, deploy commands
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Submit a pull request

## Acknowledgements

This project uses the following open-source libraries:

- **[VADER Sentiment](https://github.com/cjhutto/vaderSentiment)** - The original Python implementation of VADER (Valence Aware Dictionary and sEntiment Reasoner) by C.J. Hutto. VADER is specifically attuned to sentiments expressed in social media.

- **[GoVader](https://github.com/jonreiter/govader)** - Go port of VADER Sentiment by Jon Reiter. This project uses GoVader for sentiment analysis.

- **[Bluesky indigo](https://github.com/bluesky-social/indigo)** - Official Go library for the AT Protocol/Bluesky by the Bluesky team. Used for Bluesky API integration.

- **[gg](https://github.com/fogleman/gg)** - 2D graphics library for Go by Michael Fogleman. Used for generating sentiment sparkline charts.

- **[modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)** - Pure Go SQLite implementation. Used for the local database without requiring CGO.

- **[Google Gemini](https://ai.google.dev/)** - AI model used for semantic topic grouping in trending topics analysis.

## License

MIT License - Copyright (c) 2025 Chris Gentle FlatMapIT Pty Ltd - @xop.co on Bluesky

See [LICENSE](LICENSE) for details.
****