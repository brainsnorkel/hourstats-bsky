# Testing Guide for HourStats Bluesky Bot

This document describes how to test the HourStats Bluesky bot locally and in different environments. The bot is a single Go binary (`cmd/hourstats`) deployed to Fly.io; every component — the Jetstream consumer, the analysis cycle, the daily and yearly jobs, the stats API — runs as a goroutine inside that one process against a local SQLite database. Tests are plain `go test` packages using the stdlib `testing` package only; there is no external test framework and no cloud dependency.

## Prerequisites

- Go 1.24 or later
- A Bluesky account with app password (only for manual runs against the live API)
- Internet connection for API calls

## Running Tests

### Unit Tests

Run all unit tests:
```bash
make test
```

Run tests for a specific package:
```bash
go test ./internal/analyzer/
go test ./internal/store/
go test ./cmd/hourstats/
```

Run tests with verbose output:
```bash
go test ./internal/analyzer/ -v
go test ./cmd/hourstats/ -v
```

### Packages With Tests

| Package | Covers |
|---|---|
| `cmd/hourstats` | Cycle wiring, post assembly, extremes, alt text |
| `internal/analyzer` | VADER sentiment scoring and the emoji-aware shadow scorer |
| `internal/client` | AT Protocol posting, facets, image upload, quote controls |
| `internal/formatter` | Grapheme counting and Bluesky 300-character limits |
| `internal/hydrator` | Engagement hydration (interface-driven, no live API needed) |
| `internal/jetstream` | Event parsing, language filtering, cursor management |
| `internal/sparkline` | Chart generation (sparkline, yearly, volume, bump) |
| `internal/stats`, `internal/statsapi`, `internal/procmem` | Runtime statistics and the HTTP stats API |
| `internal/store` | Schema, queries, purges, backups, WAL management |
| `internal/topics` | TF-IDF extraction, grouping, identity tracking |
| `internal/wikipedia` | Current-events URL building |

### Chart Rendering

`cmd/graph-lab` renders every chart type from synthetic data with no Bluesky or Gemini credentials, writing PNGs to `test-results/graph-lab/`:
```bash
make graph-lab
```

## Local Testing

### 1. Set Up Environment Variables

Create a `.env` file or set environment variables:
```bash
export BLUESKY_HANDLE="your-handle.bsky.social"
export BLUESKY_PASSWORD="your-app-password"
```

### 2. Dry Run Mode

Run the whole binary without posting to Bluesky:
```bash
mkdir -p data
DATA_DIR=./data DRY_RUN=true go run ./cmd/hourstats
```

This will:
- Authenticate with Bluesky
- Consume the Jetstream firehose into a local SQLite database
- Hydrate engagement and perform sentiment analysis on each cycle
- Log what it would post without posting

### 3. Full Test Run

Run the application with real posting (be careful!):
```bash
DATA_DIR=./data go run ./cmd/hourstats
```

## Test Scenarios

### Sentiment Analysis Tests

The analyzer package includes comprehensive tests for:
- Positive sentiment detection
- Negative sentiment detection  
- Neutral sentiment detection
- Topic extraction from hashtags and keywords
- Engagement score calculation
- 100-word sentiment scale implementation

### Component Tests

Each component has specific test scenarios:

#### Jetstream Consumer (`internal/jetstream`)
- Event parsing and the bytes-level language pre-filter
- Adult content filtering
- Cursor persistence, rewind, and max-age handling
- Reconnect and stall behaviour

#### Store (`internal/store`)
- Schema migration and per-table purges
- Batched writes and read-pool pragmas
- Backup file creation via `ATTACH DATABASE`

#### Hydrator (`internal/hydrator`)
- Batched `app.bsky.feed.getPosts` calls against the `PostFetcher`/`PostUpdater` interfaces
- Unhydrated-share thresholds and low-confidence runs

#### Analyzer (`internal/analyzer`)
- Sentiment scoring on post text
- The emoji-aware shadow scorer

#### Formatter and Client (`internal/formatter`, `internal/client`)
- Post formatting and 300-grapheme limits
- Rich text facets (byte offsets)
- Quote-control and block detection before embedding
- Error handling

### Cycle Integration Tests

`cmd/hourstats` tests exercise the analysis, daily, weekly, monthly and yearly cycles end to end against a temporary SQLite database with fake clients:
- Cycle wiring and overlap skipping
- Sentiment history persistence
- Post text assembly and truncation order
- Dry-run mode validation

## Manual Testing

### 1. Test Authentication

```bash
DATA_DIR=./data DRY_RUN=true go run ./cmd/hourstats
```

Look for: "Successfully authenticated with Bluesky"

### 2. Test Post Fetching

The application will fetch posts from your timeline and log them.

### 3. Test Sentiment Analysis

Check the logs for sentiment analysis results:
- Sentiment categories (positive/negative/neutral)
- Sentiment scores
- Extracted topics
- Engagement scores

### 4. Test Posting (Dry Run)

The application will log what it would post without actually posting.

## Debugging

### Enable Debug Logging

Add debug logging to see detailed information:
```go
log.SetLevel(log.DebugLevel)
```

### Common Issues

1. **Authentication Failed**
   - Check your Bluesky handle and app password
   - Ensure you're using an app password, not your regular password
   - Verify your account is active

2. **No Posts Retrieved**
   - Check your timeline has posts
   - Verify network connectivity
   - Check Bluesky API status

3. **Sentiment Analysis Issues**
   - Check the text content is being extracted properly
   - Verify the GoVader library is working
   - Test with known positive/negative text

## Performance Testing

### Load Testing

Test with different numbers of posts:
```go
// Modify the limit in GetTrendingPosts
timeline, err := bsky.FeedGetTimeline(ctx, c.client, "reverse-chronological", "", 1000)
```

### Memory Usage

Monitor memory usage during long runs:
```bash
DATA_DIR=./data DRY_RUN=true go run ./cmd/hourstats &
ps aux | grep hourstats
```

In production the same figure is reported by `internal/procmem` (via `/proc/self/statm`) and exposed on the stats API and health charts.

## Continuous Integration

### GitHub Actions

Create `.github/workflows/test.yml`:
```yaml
name: Test
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v2
    - uses: actions/setup-go@v2
      with:
        go-version: "1.24"
    - run: go test ./...
```

## Test Data

### Sample Posts for Testing

Create test posts with known sentiment:
- Positive: "I love this new feature! It's amazing!"
- Negative: "This is terrible. I hate it so much."
- Neutral: "The weather is okay today."

### Sample Topics for Testing

Test with various hashtags and keywords:
- #tech, #ai, #crypto
- #music, #art, #science
- Regular keywords: news, politics, health

## Monitoring

### Log Analysis

Monitor logs for:
- Authentication success/failure
- API rate limits
- Sentiment analysis accuracy
- Posting success/failure

### Metrics

Track key metrics:
- Posts processed per hour
- Sentiment distribution
- Top topics identified
- API response times

## Troubleshooting

### Common Error Messages

1. `failed to authenticate: ...`
   - Check credentials
   - Verify account status

2. `failed to get timeline: ...`
   - Check network connectivity
   - Verify API endpoint

3. `analyzePost() error: ...`
   - Check text content
   - Verify sentiment analyzer

### Getting Help

1. Check the logs for detailed error messages
2. Verify your environment setup
3. Test individual components separately
4. Check the Bluesky API documentation

## Current Test Coverage

### ✅ Implemented
- Unit tests for sentiment analysis
- Unit tests for the store, jetstream consumer, hydrator, formatter, client and topics packages
- Cycle-level tests in `cmd/hourstats` against a temporary SQLite database
- Chart rendering coverage via `cmd/graph-lab`
- Local testing with dry-run mode

### 🔄 In Progress
- Performance benchmarks for large datasets
- Edge case testing (empty timelines, malformed data)
- Load testing for parallel fetchers

### 📋 Future Improvements
- [ ] Add more comprehensive integration tests
- [ ] Add performance benchmarks
- [ ] Add end-to-end testing with mock data
- [ ] Add automated testing for different sentiment scenarios
- [ ] Add testing for edge cases (empty timelines, malformed data)
- [ ] Add fault-injection tests for Jetstream disconnects and Bluesky API failures
- [ ] Add monitoring and alerting tests
