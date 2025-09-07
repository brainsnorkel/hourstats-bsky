# Testing Guide for HourStats Bluesky Bot

This document describes how to test the HourStats Bluesky bot locally and in different environments. The bot uses a multi-Lambda serverless architecture with comprehensive testing at multiple levels.

## Prerequisites

- Go 1.25 or later
- A Bluesky account with app password
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
go test ./cmd/lambda-orchestrator/
```

Run tests with verbose output:
```bash
go test ./internal/analyzer/ -v
go test ./cmd/lambda-orchestrator/ -v
```

### Lambda Function Tests

Test individual Lambda functions:
```bash
make test-lambdas
```

This will test each Lambda function individually:
- `lambda-orchestrator` - Workflow orchestration
- `lambda-fetcher` - Post collection
- `lambda-analyzer` - Sentiment analysis
- `lambda-aggregator` - Data aggregation
- `lambda-poster` - Post publishing

### Integration Tests

Test the complete Step Functions workflow:
```bash
# Test with dry-run mode (recommended)
make test-multi-lambda

# Test with real execution (requires AWS credentials)
make test-workflow
```

## Local Testing

### 1. Set Up Environment Variables

Create a `.env` file or set environment variables:
```bash
export BLUESKY_HANDLE="your-handle.bsky.social"
export BLUESKY_PASSWORD="your-app-password"
```

### 2. Dry Run Mode

Test the application without posting to Bluesky:
```bash
make dry-run
```

This will:
- Authenticate with Bluesky
- Fetch posts from the timeline
- Perform sentiment analysis
- Log the results without posting

### 3. Full Test Run

Run the application with real posting (be careful!):
```bash
make run
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

### Lambda Function Tests

Each Lambda function has specific test scenarios:

#### Orchestrator Lambda
- Event structure validation
- Run ID generation
- Response structure validation
- DynamoDB state management

#### Fetcher Lambda
- Bluesky API authentication
- Post collection and filtering
- Cursor-based pagination
- Adult content filtering
- Time-based filtering

#### Analyzer Lambda
- Sentiment analysis on collected posts
- Engagement score calculation
- Post ranking logic
- Data validation

#### Aggregator Lambda
- Post ranking by engagement
- Community sentiment calculation
- Top posts selection
- Data aggregation

#### Poster Lambda
- Post formatting
- Bluesky API posting
- Rich text facets
- Error handling

### Workflow Integration Tests

- End-to-end Step Functions execution
- DynamoDB state persistence
- Lambda function coordination
- Error handling and recovery
- Dry-run mode validation

## Manual Testing

### 1. Test Authentication

```bash
go run cmd/trendjournal/main.go
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
go run cmd/trendjournal/main.go &
ps aux | grep trendjournal
```

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
        go-version: 1.25
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
- Unit tests for orchestrator Lambda
- Integration tests for Step Functions workflow
- Local testing with dry-run mode
- AWS Lambda function testing
- End-to-end workflow testing

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
- [ ] Add chaos engineering tests for AWS service failures
- [ ] Add monitoring and alerting tests
