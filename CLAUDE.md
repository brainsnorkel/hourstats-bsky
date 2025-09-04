# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

TrendJournal is a Go-based bot for Bluesky/AT Protocol that analyzes trending posts, performs sentiment analysis, and posts summaries of the top 5 most popular posts at configurable intervals.

## Key Commands

### Build and Run
```bash
make setup      # First time setup - creates config.yaml
make build      # Build binary to bin/trendjournal
make run        # Run the application
make dry-run    # Test mode without posting to Bluesky
```

### Testing and Development
```bash
make test       # Run test suite
make fmt        # Format code
make lint       # Lint code (requires golangci-lint)
make deps       # Install/update dependencies
```

### Configuration
- Copy `config.example.yaml` to `config.yaml` and add Bluesky credentials
- Or use environment variables: `BLUESKY_HANDLE` and `BLUESKY_PASSWORD`
- Key settings in config.yaml:
  - `analysis_interval_minutes`: How often to analyze (default: 5)
  - `top_posts_count`: Number of top posts (default: 5)
  - `min_engagement_score`: Minimum engagement to consider (default: 10)
  - `dry_run`: Test mode flag (default: true)

## Architecture

### Core Components

**Main Entry Point**
- `cmd/trendjournal/main.go`: Application entry, loads config, starts scheduler

**Internal Packages**
- `internal/client/bluesky.go`: Bluesky API client using indigo library
  - `Authenticate()`: Login to Bluesky
  - `GetTrendingPosts()`: Fetch public posts via search API with pagination
  - `PostTrendingSummary()`: Post formatted summary with sentiment and rich text facets
  
- `internal/scheduler/scheduler.go`: Orchestrates hourly analysis cycle
  - Runs analysis immediately on startup, then hourly (hardcoded 1-hour ticker)
  - Fetches posts, analyzes sentiment, ranks by engagement, posts summary
  - Converts between client and analyzer post types

- `internal/analyzer/sentiment.go`: Sentiment analysis using GoVader
  - Analyzes post sentiment (positive/negative/neutral)
  - Extracts topics from hashtags and keywords
  - Calculates engagement score (replies + likes + reposts with sentiment boost)

- `internal/config/config.go`: Configuration management
  - Loads from config.yaml or environment variables
  - Validates required settings

### Key Implementation Details

**Post Fetching**
- Uses `FeedSearchPosts` API to search ALL public posts (not just followed)
- Implements pagination to retrieve comprehensive results (up to 10,000 posts safety limit)
- Performs client-side time filtering based on `analysis_interval_minutes`
- Extracts actual post text from record structure (`bsky.FeedPost`)
- Continues pagination until posts fall outside time window

**Rich Text Support**
- Creates clickable link facets using Bluesky's rich text format
- Uses regex to find `https://bsky.app/` URLs in text
- Generates `RichtextFacet` with byte positions for each link

**Post Format**
Generated posts follow this structure:
```
Top five posts in the last [1 hour/X minutes]

https://bsky.app/profile/[did]/post/[id]
@author [total_engagement]
[2-5 similar...]
Bluesky is [emotion]
```

**AT URI to Web URL Conversion**
- Converts `at://did:plc:xxx/app.bsky.feed.post/yyy` 
- To `https://bsky.app/profile/did:plc:xxx/post/yyy`

### Data Flow

1. Scheduler triggers analysis every hour (fixed ticker)
2. Client searches public posts via paginated `FeedSearchPosts` API
3. Posts filtered by time (only last `analysis_interval_minutes`)
4. Text extracted from post records
5. Analyzer processes posts for sentiment and engagement
6. Top 5 posts ranked by total engagement (replies + likes + reposts)
7. Overall sentiment determined from top posts majority
8. Summary posted with rich text facets for clickable links

### Sentiment Emotions

- **Positive**: passionate, enthusiastic, optimistic, confident, inspired
- **Negative**: anxious, pessimistic, uncertain, confused, overwhelmed  
- **Neutral**: contemplative, analytical, curious, observant, reflective

## Important Notes

- Scheduler runs on fixed 1-hour intervals regardless of config setting
- Search uses wildcard query `"*"` with language filter `"en"`
- Pagination continues until posts fall outside time window
- Post text properly extracted from record structure
- Rich text facets enable clickable links in posts
- Engagement score = replies + likes + reposts (with 10% boost for positive sentiment)
- Posts truncated to 300 graphemes (Bluesky limit)

## Dependencies

Main Go dependencies:
- `github.com/bluesky-social/indigo`: Official AT Protocol/Bluesky Go library
- `github.com/jonreiter/govader`: Sentiment analysis library
- Go 1.25+ required