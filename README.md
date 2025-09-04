# TrendJournal

A Go-based AT Protocol/Bluesky client that analyzes trending topics using top posts and sentiment analysis.

## Overview

TrendJournal is an automated bot that:
- Fetches top posts from Bluesky/AT Protocol
- Performs sentiment analysis on trending content
- Posts the top 5 most popular posts with their associated topics and sentiment every hour
- Can be deployed to cloud services for continuous operation


## Generated post

The generated post shall contain:
 * "Top five this hour {local date and time}" 
   * Links to the five top posts (ranked by likes + reskeets), in the last hour
 * From the sentiment expressed in the five top posts generate text about the sentiment:
   * "Bluesky is {emotion}" - where emotion is something like happy, sad, angry, playful.


## Project Status

🚧 **In Development** - Core functionality implemented, ready for testing

## Features

- [x] AT Protocol/Bluesky API integration using official indigo library
- [x] Post fetching from timeline
- [x] Sentiment analysis using GoVader
- [x] Topic extraction and categorization
- [x] Automated posting every hour
- [x] Local testing environment
- [ ] Cloud deployment configuration
- [ ] Enhanced trending algorithm
- [ ] Configuration management

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
git clone https://github.com/christophergentle/trendjournal.git
cd trendjournal
```

2. Install dependencies:
```bash
make deps
```

3. Set up environment variables:
```bash
export BLUESKY_HANDLE="your-handle.bsky.social"
export BLUESKY_PASSWORD="your-app-password"
```

4. Run the application:
```bash
make run
```

### Configuration

Copy `config.example.yaml` to `config.yaml` and customize the settings:

```yaml
bluesky:
  handle: "your-handle.bsky.social"
  password: "your-app-password"
  
settings:
  analysis_interval_hours: 1
  top_posts_count: 5
  min_engagement_score: 10
  dry_run: true
```

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
trendjournal/
├── cmd/trendjournal/          # Main application entry point
├── internal/
│   ├── client/               # Bluesky API client
│   ├── analyzer/             # Sentiment analysis and topic extraction
│   └── scheduler/            # Hourly scheduling logic
├── pkg/                      # Shared packages
├── config.example.yaml       # Configuration template
├── Makefile                  # Build and run commands
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
