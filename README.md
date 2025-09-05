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

1. @username.bsky.social (15) +
2. @anotheruser.bsky.social (12) -
3. @thirduser.bsky.social (8) x
4. @fourthuser.bsky.social (6) +
5. @fifthuser.bsky.social (4) x
```

**Features:**
- **Time period**: Configurable (minutes or hours)
- **Sentiment**: Emotion-based analysis (passionate, excited, steady, etc.)
- **Top 5 posts**: Ranked by total engagement score
- **Engagement scores**: Total score in parentheses (likes + reposts + replies)
- **Sentiment indicators**: + for positive, - for negative, x for neutral
- **Adult content filtering**: Uses Bluesky's official moderation labels


## Project Status

✅ **Production Ready** - Multi-Lambda serverless architecture deployed and running on AWS

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
- [x] AWS serverless deployment with multi-Lambda architecture
- [x] DynamoDB state management for large-scale data processing
- [x] AWS Step Functions orchestration for workflow management
- [x] EventBridge scheduling for automated execution

## Tech Stack

- **Language**: Go 1.24+
- **AT Protocol**: [Bluesky indigo library](https://github.com/bluesky-social/indigo)
- **Sentiment Analysis**: [GoVader](https://github.com/jonreiter/govader)
- **Cloud Platform**: AWS (Lambda, Step Functions, DynamoDB, EventBridge)
- **State Management**: DynamoDB with TTL and GSI
- **Orchestration**: AWS Step Functions
- **Scheduling**: AWS EventBridge (every 30 minutes)
- **Infrastructure**: Terraform with S3 remote state

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

## Multi-Lambda Architecture

The system uses a sophisticated serverless architecture to handle large-scale data processing without timeout issues. The workflow is orchestrated by AWS Step Functions and uses DynamoDB for state management.

### Architecture Diagram

```mermaid
graph TD
    A[EventBridge<br/>Every 30 minutes] --> B[Step Functions<br/>Workflow]
    B --> C[Orchestrator Lambda<br/>Create Run State]
    C --> D[Parallel Fetcher Lambdas<br/>Collect Posts in Batches]
    D --> E[Wait for Completion]
    E --> F[Check Completion<br/>Orchestrator Lambda]
    F --> G{All Data<br/>Collected?}
    G -->|No| E
    G -->|Yes| H[Analyzer Lambda<br/>Sentiment Analysis]
    H --> I[Aggregator Lambda<br/>Rank & Prepare Results]
    I --> J[Poster Lambda<br/>Publish Summary]
    J --> K[End]
    
    D --> L[Fetcher Failure]
    L --> M[Workflow Failure]
    C --> M
    E --> M
    F --> M
    H --> M
    I --> M
    J --> M
```

### Lambda Functions

#### 1. **Orchestrator Lambda** (`hourstats-orchestrator`)
- **Purpose**: Initiates the workflow and manages run state
- **Duration**: ~1 minute
- **Memory**: 128MB
- **Responsibilities**:
  - Creates unique run ID and initial state in DynamoDB
  - Calculates estimated number of fetch batches needed
  - Handles completion checking logic

#### 2. **Fetcher Lambda** (`hourstats-fetcher`)
- **Purpose**: Collects posts from Bluesky API in batches
- **Duration**: ~5 minutes per batch
- **Memory**: 256MB
- **Responsibilities**:
  - Fetches 100 posts per batch using cursor-based pagination
  - Filters posts by time and adult content
  - Updates DynamoDB with collected posts and cursor state
  - Handles API rate limiting and retries

#### 3. **Analyzer Lambda** (`hourstats-analyzer`)
- **Purpose**: Performs sentiment analysis on collected posts
- **Duration**: ~3 minutes
- **Memory**: 256MB
- **Responsibilities**:
  - Analyzes sentiment of all collected posts
  - Calculates engagement scores
  - Updates post records with analysis results

#### 4. **Aggregator Lambda** (`hourstats-aggregator`)
- **Purpose**: Ranks posts and prepares final results
- **Duration**: ~1 minute
- **Memory**: 128MB
- **Responsibilities**:
  - Ranks posts by engagement score
  - Determines overall community sentiment
  - Prepares top 5 posts for posting

#### 5. **Poster Lambda** (`hourstats-poster`)
- **Purpose**: Publishes the final summary to Bluesky
- **Duration**: ~1 minute
- **Memory**: 128MB
- **Responsibilities**:
  - Formats the summary post
  - Publishes to Bluesky with proper rich text facets
  - Handles posting errors and retries

### DynamoDB State Management

The system uses DynamoDB to store and manage state across Lambda invocations:

#### Table Schema: `hourstats-state`

**Primary Key**:
- `runId` (String): Unique identifier for each analysis run
- `step` (String): Current step in the workflow

**Attributes**:
- `status` (String): Current status (running, completed, failed)
- `createdAt` (String): ISO timestamp of run creation
- `ttl` (Number): TTL for automatic cleanup (7 days)

**Global Secondary Index**: `status-index`
- Hash key: `status`
- Range key: `createdAt`
- Used for querying active runs

#### Data Flow Between Lambdas

**1. Orchestrator → DynamoDB**:
```json
{
  "runId": "run-1757029004123456789",
  "step": "orchestrator",
  "status": "running",
  "createdAt": "2025-01-05T10:30:00Z",
  "ttl": 1757029200,
  "runState": {
    "currentCursor": "",
    "totalPostsRetrieved": 0,
    "hasMorePosts": true,
    "posts": []
  }
}
```

**2. Fetcher → DynamoDB**:
```json
{
  "runId": "run-1757029004123456789",
  "step": "fetcher-batch-1",
  "status": "completed",
  "createdAt": "2025-01-05T10:31:00Z",
  "ttl": 1757029200,
  "runState": {
    "currentCursor": "next_cursor_value",
    "totalPostsRetrieved": 100,
    "hasMorePosts": true,
    "posts": [
      {
        "uri": "at://did:plc:abc123/app.bsky.feed.post/def456",
        "text": "Post content...",
        "author": "user.bsky.social",
        "likes": 15,
        "reposts": 3,
        "replies": 2,
        "createdAt": "2025-01-05T10:25:00Z",
        "sentiment": "",
        "engagementScore": 0
      }
    ]
  }
}
```

**3. Analyzer → DynamoDB**:
```json
{
  "runId": "run-1757029004123456789",
  "step": "analyzer",
  "status": "completed",
  "createdAt": "2025-01-05T10:35:00Z",
  "ttl": 1757029200,
  "runState": {
    "currentCursor": "final_cursor_value",
    "totalPostsRetrieved": 5000,
    "hasMorePosts": false,
    "posts": [
      {
        "uri": "at://did:plc:abc123/app.bsky.feed.post/def456",
        "text": "Post content...",
        "author": "user.bsky.social",
        "likes": 15,
        "reposts": 3,
        "replies": 2,
        "createdAt": "2025-01-05T10:25:00Z",
        "sentiment": "positive",
        "engagementScore": 20
      }
    ]
  }
}
```

### Workflow Execution

1. **EventBridge Trigger**: Every 30 minutes, EventBridge triggers the Step Functions workflow
2. **Orchestrator**: Creates initial run state and estimates fetch batches
3. **Parallel Fetching**: Multiple fetcher Lambdas run concurrently to collect posts
4. **Completion Check**: Orchestrator checks if all data has been collected
5. **Analysis**: Analyzer processes all collected posts for sentiment
6. **Aggregation**: Aggregator ranks posts and determines community sentiment
7. **Posting**: Poster publishes the final summary to Bluesky

### Benefits of This Architecture

- **Scalability**: Can handle unlimited posts without timeout issues
- **Cost Efficiency**: Only pay for actual compute time used
- **Reliability**: Each step can retry independently
- **Monitoring**: CloudWatch logs for each Lambda function
- **State Persistence**: DynamoDB ensures no data loss between steps
- **Parallel Processing**: Multiple fetchers can run simultaneously

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
├── cmd/
│   ├── trendjournal/         # Main application entry point (local testing)
│   ├── lambda-orchestrator/  # Orchestrator Lambda function
│   ├── lambda-fetcher/       # Fetcher Lambda function
│   ├── lambda-analyzer/      # Analyzer Lambda function
│   ├── lambda-aggregator/    # Aggregator Lambda function
│   └── lambda-poster/        # Poster Lambda function
├── internal/
│   ├── client/               # Bluesky API client with adult content filtering
│   ├── analyzer/             # Emotion-based sentiment analysis
│   ├── scheduler/            # Analysis scheduling logic
│   ├── config/               # Configuration management
│   └── state/                # DynamoDB state management
├── terraform/
│   ├── main.tf               # AWS infrastructure definition
│   └── step-functions-definition.json  # Step Functions workflow
├── .github/workflows/
│   └── deploy-lambda.yml     # CI/CD pipeline
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

## Deployment

The system is deployed using GitHub Actions and Terraform:

### AWS Infrastructure

The deployment creates the following AWS resources:

- **5 Lambda Functions**: Individual functions for each step of the workflow
- **DynamoDB Table**: State management with TTL and GSI
- **Step Functions State Machine**: Workflow orchestration
- **EventBridge Rule**: Scheduled execution every 30 minutes
- **IAM Roles & Policies**: Secure access control
- **CloudWatch Log Groups**: Centralized logging

### Deployment Process

1. **Code Push**: Changes pushed to `main` branch trigger deployment
2. **Build**: Go modules are built for each Lambda function
3. **Package**: Lambda functions are packaged as ZIP files
4. **Deploy**: Terraform applies infrastructure changes
5. **Verify**: Deployment status is reported back to GitHub

### Monitoring

- **CloudWatch Logs**: Each Lambda function has dedicated log groups
- **Step Functions**: Visual workflow execution monitoring
- **DynamoDB**: Table metrics and query performance
- **EventBridge**: Rule execution history

### Local Development

For local development and testing:

```bash
# Run locally with dry-run mode
make run

# Test individual Lambda functions
go test ./cmd/lambda-...

# Test complete workflow
make test-workflow

# Query previous runs and test processor output
./scripts/query-runs.sh list 10          # List last 10 runs
./scripts/query-runs.sh analyze <runID>  # Analyze specific run
```

### Query Utility

The system includes a query utility to inspect previous runs and test what would be posted:

```bash
# List recent runs with details
go run cmd/query-runs/main.go -list -limit=10 -details

# Analyze a specific run and see what would be posted
go run cmd/query-runs/main.go -run <runID>
```

The utility shows:
- Run statistics (posts collected, sentiment, etc.)
- Generated post content that would be posted to Bluesky
- Character count and remaining characters before Bluesky's 300-character limit
- Warnings if the post is too long or close to the limit

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Submit a pull request

## License

MIT
# Trigger new workflow
# Trigger deployment
# Trigger deployment
