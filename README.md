# Bluesky HourStats

A Go-based AT Protocol/Bluesky client that analyzes trending posts and sentiment to provide hourly statistics about the Bluesky community.

## Overview

Bluesky HourStats is an automated bot that:
- Searches all public posts from Bluesky/AT Protocol (not just followed accounts)
- Filters out adult content using Bluesky's official moderation labels
- Performs emotion-based sentiment analysis on trending content
- Posts hourly summaries with the top 5 most popular posts and overall community sentiment
- Uses engagement scores (likes + reposts + replies) to rank posts
- Can be deployed to cloud services for continuous operation

## Generated Post Format

The bot posts summaries in this format:
```
For 30 minutes Bluesky was passionate

1. @username.bsky.social (15)
2. @anotheruser.bsky.social (12)
3. @thirduser.bsky.social (8)
4. @fourthuser.bsky.social (6)
5. @fifthuser.bsky.social (4)
```

**Features:**
- **Time period**: Configurable (minutes or hours)
- **Sentiment**: Emotion-based analysis (passionate, excited, steady, etc.)
- **Top 5 posts**: Ranked by total engagement score
- **Engagement scores**: Displayed in parentheses (likes + reposts + replies)
- **Adult content filtering**: Uses Bluesky's official moderation labels


## Project Status

✅ **Functional** - Core functionality implemented and tested, ready for deployment

## Features

- [x] AT Protocol/Bluesky API integration using official indigo library
- [x] Public post search (searches all public posts, not just followed accounts)
- [x] Adult content filtering using Bluesky's official moderation labels
- [x] Emotion-based sentiment analysis with 30+ emotions across positive/negative/neutral categories
- [x] Keyword-based sentiment fallback for improved accuracy
- [x] Engagement score calculation (likes + reposts + replies)
- [x] Time-filtered analysis (only considers new posts during the analysis interval)
- [x] Configurable analysis intervals (minutes)
- [x] Smart posting (skips when no posts found)
- [x] Clickable handle links with proper Bluesky rich text facets
- [x] Dry-run mode for safe testing
- [x] Secure configuration management
- [x] Local testing environment
- [x] Comprehensive logging and debugging
- [ ] Cloud deployment configuration

## Tech Stack

- **Language**: Go 1.25+
- **AT Protocol**: [Bluesky indigo library](https://github.com/bluesky-social/indigo)
- **Sentiment Analysis**: [GoVader](https://github.com/jonreiter/govader)
- **Deployment**: AWS/Cloudflare (planned)
- **Scheduling**: Go time.Ticker

## Getting Started

### Prerequisites

- Go 1.25 or later
- A Bluesky account
- Bluesky app password (not your regular password)

### Installation

1. Clone the repository:
```bash
git clone https://github.com/christophergentle/hourstats-bsky.git
cd hourstats-bsky
```

2. Install dependencies:
```bash
make deps
```

3. Set up configuration (first time only):
```bash
make setup
```

4. Edit `config.yaml` with your Bluesky credentials:
```yaml
bluesky:
  handle: "your-handle.bsky.social"
  password: "your-app-password"
```

5. Run the application:
```bash
make run
```

### Alternative: Environment Variables

You can also use environment variables instead of the config file:
```bash
export BLUESKY_HANDLE="your-handle.bsky.social"
export BLUESKY_PASSWORD="your-app-password"
make run
```

### Configuration

The bot uses `config.yaml` for configuration. Run `make setup` to create it from the template.

**Required settings:**
- `bluesky.handle`: Your Bluesky handle (e.g., "yourname.bsky.social")
- `bluesky.password`: Your Bluesky app password (not your regular password)

**Optional settings:**
- `settings.analysis_interval_minutes`: How often to run analysis in minutes (default: 60)
- `settings.top_posts_count`: Number of top posts to include (default: 5)
- `settings.min_engagement_score`: Minimum engagement to consider trending (default: 10)
- `settings.dry_run`: Test mode without posting (default: true)

**Security:** The `config.yaml` file contains your credentials and is git-ignored for safety.

### How It Works

1. **Public Post Search**: The bot searches all public Bluesky posts (not just followed accounts) using the search API
2. **Time Filtering**: Only analyzes posts from the last `analysis_interval_minutes` period
3. **Engagement Ranking**: Ranks posts by total engagement (replies + likes + reposts)
4. **Sentiment Analysis**: Analyzes the sentiment of the top posts using GoVader
5. **Smart Posting**: Only posts summaries when posts are found; skips when no activity
6. **Web URLs**: Converts AT Protocol URIs to web-friendly URLs for proper link rendering

### Testing

Run the test suite:
```bash
make test
```

Run in dry-run mode (won't post to Bluesky):
```bash
make dry-run
```

## Project Structure

```
hourstats-bsky/
├── cmd/trendjournal/          # Main application entry point
├── internal/
│   ├── client/               # Bluesky API client with adult content filtering
│   ├── analyzer/             # Emotion-based sentiment analysis
│   ├── scheduler/            # Analysis scheduling logic
│   └── config/               # Configuration management
├── config.example.yaml       # Configuration template
├── Makefile                  # Build and run commands
├── CHANGELOG.md              # Project changelog
└── README.md
```

## Development

### Building

```bash
make build
```

### Running Tests

```bash
make test
```

### Code Formatting

```bash
make fmt
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Submit a pull request

## License

MIT
