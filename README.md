# Bluesky HourStats

**Live Bot:** [@hourstats.bsky.social](https://bsky.app/profile/hourstats.bsky.social)

> **Note:** This project is an experiment in using [Claude](https://claude.ai) and [Cursor](https://cursor.sh) for AI-assisted software development. The bot analyzes Bluesky posts every 30 minutes and posts sentiment summaries with the top 5 most engaged posts.

A Go-based AT Protocol/Bluesky client that analyzes trending posts and sentiment to provide hourly statistics about the Bluesky community.

## What It Does

Bluesky HourStats is an automated bot that:
- Analyzes posts from the last 30 minutes
- Ranks posts by engagement (replies + likes + reposts)
- Performs sentiment analysis using VADER and keyword matching
- Posts summaries with the top 5 posts and overall community sentiment
- Generates 48-hour sentiment sparklines
- Creates yearly sentiment charts (monthly posts)

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
- **48-hour sparklines**: Visual sentiment trends posted periodically
- **Yearly charts**: Monthly posts showing 365 days of sentiment data

## Architecture

The bot runs on AWS Lambda with the following components:

- **Orchestrator**: Initiates analysis runs every 30 minutes via EventBridge
- **Fetcher**: Collects posts from Bluesky API (stops at 14 minutes if >1000 posts collected)
- **Processor**: Analyzes sentiment and ranks posts
- **Poster**: Publishes summaries to Bluesky
- **Sparkline Poster**: Generates and posts 48-hour sentiment charts
- **Daily Aggregator**: Calculates daily sentiment averages (runs at midnight UTC)
- **Yearly Poster**: Generates yearly charts (posts monthly on 1st at 1:00 AM UTC)

State is managed in DynamoDB, and sparkline images are stored in S3.

## Tech Stack

- **Language**: Go 1.24+
- **AT Protocol**: [Bluesky indigo library](https://github.com/bluesky-social/indigo)
- **Sentiment Analysis**: [GoVader](https://github.com/jonreiter/govader)
- **Image Generation**: Go graphics library (fogleman/gg)
- **Cloud**: AWS (Lambda, DynamoDB, EventBridge, S3)
- **Infrastructure**: Terraform

## Getting Started

### Prerequisites

- Go 1.25 or later
- A Bluesky account
- Bluesky app password (not your regular password)

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

3. Set up configuration:
```bash
make setup
```

4. Edit `config.yaml` with your Bluesky credentials:
```yaml
bluesky:
  handle: "your-handle.bsky.social"
  password: "your-app-password"
```

5. Run locally:
```bash
make run
```

### Alternative: Environment Variables

```bash
export BLUESKY_HANDLE="your-handle.bsky.social"
export BLUESKY_PASSWORD="your-app-password"
make run
```

## How It Works

1. **Post Fetching**: Searches all public Bluesky posts from the last 30 minutes
2. **Time Filtering**: Only analyzes posts within the analysis window
3. **Engagement Ranking**: Ranks posts by total engagement (replies + likes + reposts)
4. **Sentiment Analysis**: Uses VADER sentiment analysis with keyword fallback
5. **Posting**: Publishes top 5 posts with sentiment indicators and mood hashtag
6. **Visualizations**: Generates sparklines and yearly charts from historical data

## Features

- ✅ Public post search (all posts, not just followed accounts)
- ✅ Adult content filtering using Bluesky moderation labels
- ✅ Sentiment analysis with 100-word emotion vocabulary
- ✅ Engagement-based ranking (no sentiment boost)
- ✅ Post deduplication
- ✅ 48-hour sentiment sparklines
- ✅ Yearly sentiment charts with month markers
- ✅ Daily sentiment aggregation
- ✅ AWS serverless deployment
- ✅ CI/CD pipeline via GitHub Actions

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

The bot is deployed to AWS using Terraform and GitHub Actions. See [PRODUCTION_DEPLOYMENT.md](PRODUCTION_DEPLOYMENT.md) for detailed deployment instructions.

### AWS Resources

- Lambda functions (orchestrator, fetcher, processor, poster, sparkline-poster, daily-aggregator, yearly-poster)
- DynamoDB tables (state, sentiment history, daily sentiment)
- EventBridge rules (30-minute, daily, monthly schedules)
- S3 bucket (sparkline images)
- IAM roles and policies

## Project Structure

```
hourstats-bsky/
├── cmd/                      # Lambda functions and entry points
│   ├── lambda-orchestrator/  # Orchestrator Lambda
│   ├── lambda-fetcher/       # Fetcher Lambda
│   ├── lambda-processor/     # Processor Lambda
│   ├── lambda-poster/        # Poster Lambda
│   └── ...
├── internal/                 # Shared packages
│   ├── client/              # Bluesky API client
│   ├── analyzer/            # Sentiment analysis
│   ├── formatter/           # Post formatting
│   ├── sparkline/           # Chart generation
│   └── state/               # DynamoDB state management
├── terraform/               # Infrastructure as Code
└── docs/                    # Documentation
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Submit a pull request

## License

MIT License - Copyright (c) 2025 Chris Gentle FlatMapIT Pty Ltd - @xop.co on Bluesky

See [LICENSE](LICENSE) for details.
