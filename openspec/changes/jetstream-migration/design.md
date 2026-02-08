## Context

HourStats currently collects posts by polling the Bluesky search API (`app.bsky.feed.searchPosts`) every 30 minutes. This is a pull-based architecture where a Lambda function paginates through search results to retroactively collect posts. The search API has a hard pagination ceiling of ~10,000 results and is explicitly documented as not guaranteeing complete result sets.

Bluesky operates Jetstream — a public, JSON-based WebSocket stream of all network activity. It delivers every post in real time with no rate limits or caps. This design replaces the poll-based fetcher with a push-based Jetstream consumer, while keeping the entire downstream pipeline (processor → sparkline → aggregator → yearly) untouched.

## Goals / Non-Goals

**Goals:**
- Capture **100% of Bluesky posts** in each 30-minute window (currently ~10% coverage)
- Remove dependency on the search API's pagination limits and `since`/`until` quirks
- Remove the `lang="en"` filter to analyze all languages (sentiment analysis already handles this)
- Maintain identical output: same Bluesky post format, same DynamoDB schemas, same downstream pipeline
- Keep infrastructure costs comparable (Fargate Spot is ~$3/month for a single-vCPU task)

**Non-Goals:**
- Changing the sentiment analysis algorithm
- Changing the post format or engagement ranking
- Supporting historical backfill (Jetstream is live-only; for historical data, search API remains available as fallback)
- Multi-region deployment of the consumer

## Architecture Overview

```
CURRENT:
  EventBridge (30min) → Fetcher Lambda → [search API pagination loop] → DynamoDB → Processor Lambda → ...

PROPOSED:
  Jetstream WS ──→ Consumer (ECS Fargate) ──→ DynamoDB (rolling window buffer)
  EventBridge (30min) ──→ Window Trigger Lambda ──→ DynamoDB (close window) ──→ Processor Lambda → ...
```

The consumer runs continuously, writing posts into time-bucketed DynamoDB items. Every 30 minutes, the window trigger Lambda creates a run, "closes" the current window by assigning a runId to buffered posts, and dispatches the processor — exactly as the current fetcher does after completing its search pagination.

## Decisions

### Decision 1: ECS Fargate for the consumer, not Lambda

The Jetstream consumer must maintain a persistent WebSocket connection. Lambda has a 15-minute max execution time and is designed for request/response workloads, not long-lived connections. ECS Fargate provides:
- Always-on compute with no timeout
- Automatic restart on failure via ECS service desired count
- Fargate Spot pricing (~$0.01/hour for 0.25 vCPU / 0.5 GB)
- No servers to manage

Alternative considered: Lambda with SQS — rejected because Jetstream is a WebSocket stream, not a queue. Adding an intermediary (e.g., Kinesis Data Streams) would add cost and complexity for no benefit.

### Decision 2: DynamoDB windowed buffer using the existing table

Posts are written to the existing `hourstats-state` DynamoDB table using a new key pattern:

```
PK (runId): "window-buffer"
SK (postId): "<ISO-minute>#<post-uri-hash>"
```

This keeps all posts in a single, queryable partition. When the window trigger fires, it:
1. Creates a new run (same as today: `runId = "run-<timestamp>"`, `postId = "orchestrator"`)
2. Queries the buffer for posts in the last 30 minutes using SK range
3. Re-writes them as batched `PostBatch` items under the new `runId` (same format the processor already reads)
4. Deletes consumed buffer items (or relies on TTL)

This preserves the exact `GetAllPosts()` contract the processor depends on.

Alternative considered: Separate DynamoDB table for the buffer — rejected because it adds Terraform complexity and the existing table's capacity is sufficient. The buffer partition is isolated by its `PK = "window-buffer"` key.

### Decision 3: Write-through with deduplication, not write-behind

The consumer writes each post to DynamoDB as it arrives (write-through) rather than batching in memory and flushing periodically (write-behind). Rationale:
- Durability: posts survive consumer restarts without loss
- Simplicity: no in-memory buffer management, flush timers, or graceful shutdown concerns
- Cost: DynamoDB on-demand pricing handles burst writes; at ~3,000 posts/minute the cost is ~$0.004/minute ($0.24/hour, $5.76/day)
- Deduplication uses the post URI hash in the sort key — DynamoDB PutItem with the same key is idempotent

Alternative considered: SQS buffer with Lambda consumer — rejected as adding latency and cost for posts that don't need queuing.

### Decision 4: Consumer handles reconnection and backfill internally

Jetstream supports a `cursor` parameter that allows resuming from a specific sequence number. The consumer:
1. Persists its last-processed sequence number to DynamoDB every 10 seconds
2. On restart, reads the stored cursor and reconnects with `?cursor=<seq>`
3. Jetstream replays missed events (backfill window is typically several hours)

This ensures no data loss during consumer restarts, deployments, or transient network issues.

### Decision 5: Remove `lang="en"` filter

The current search query filters `lang="en"`. The Jetstream consumer receives all posts regardless of language. The sentiment analyzer (GoVader) already handles non-English text gracefully — it returns neutral scores for text it can't analyze, which is the correct behavior. Removing the filter means:
- More posts in the sample → more statistically significant sentiment
- Non-English high-engagement posts can appear in top-5 (they'll show as neutral sentiment, which is accurate)

### Decision 6: Keep the search API client code for fallback

The `GetTrendingPostsBatch` and `GetTrendingPosts` methods in `internal/client/bluesky.go` are retained but no longer called in the primary pipeline. They serve as:
- Fallback if the consumer is down (window trigger can fall back to search-based fetch)
- Useful for local development and testing without running a consumer
- Historical queries that Jetstream can't serve

## Component Specifications

### Jetstream Consumer (`cmd/jetstream-consumer/`)

**Runtime:** Go binary running as ECS Fargate task (single instance)

**Responsibilities:**
1. Connect to Jetstream WebSocket: `wss://jetstream2.us-east.bsky.network/subscribe?wantedCollections=app.bsky.feed.post`
2. Parse each incoming JSON event
3. Extract post data: URI, CID, text, author (DID + handle resolution), createdAt
4. Write to DynamoDB `hourstats-state` table with windowed key pattern
5. Persist cursor position every 10 seconds
6. Reconnect on WebSocket close/error with exponential backoff (1s, 2s, 4s, 8s, max 30s)
7. Emit CloudWatch metrics: posts/second, connection status, write errors

**Post data from Jetstream:**
```json
{
  "did": "did:plc:abc123",
  "time_us": 1707000000000000,
  "kind": "commit",
  "commit": {
    "rev": "...",
    "operation": "create",
    "collection": "app.bsky.feed.post",
    "rkey": "xyz789",
    "record": {
      "$type": "app.bsky.feed.post",
      "text": "Hello world",
      "createdAt": "2025-02-08T10:00:00.000Z",
      "langs": ["en"]
    },
    "cid": "bafyrei..."
  }
}
```

**Note on engagement metrics:** Jetstream delivers posts at creation time — they have 0 likes, 0 reposts, 0 replies. The window trigger or processor must hydrate engagement metrics via `app.bsky.feed.getPosts` (batch endpoint, up to 25 URIs per call) before ranking. This is a new step not present in the current architecture where the search API returns posts with current engagement counts.

**DynamoDB buffer write format:**
```
PK (runId):  "window-buffer"
SK (postId): "2025-02-08T10:00#bafyrei..."  (minute-bucket # CID for uniqueness)
```

Item attributes:
```json
{
  "runId": "window-buffer",
  "postId": "2025-02-08T10:00#bafyrei...",
  "post": {
    "uri": "at://did:plc:abc123/app.bsky.feed.post/xyz789",
    "cid": "bafyrei...",
    "text": "Hello world",
    "author": "did:plc:abc123",
    "likes": 0,
    "reposts": 0,
    "replies": 0,
    "createdAt": "2025-02-08T10:00:00.000Z",
    "sentiment": "",
    "engagementScore": 0
  },
  "ttl": 1707091200
}
```

**TTL:** 2 hours (7200 seconds from write time). This ensures buffer cleanup even if the window trigger fails.

**Cursor persistence:**
```
PK (runId):  "jetstream-cursor"
SK (postId): "latest"
Attributes:  { "cursor": 1707000000000, "updatedAt": "2025-02-08T10:00:00Z" }
```

### Window Trigger Lambda (`cmd/lambda-fetcher/` — modified)

**Runtime:** Go Lambda, triggered by EventBridge every 30 minutes (same schedule as today)

**Responsibilities:**
1. Create a new run in DynamoDB (same as current `createRun()` logic)
2. Query the buffer partition for posts in the window: `PK = "window-buffer"` AND `SK BETWEEN "<cutoffMinute>#" AND "<nowMinute>#~"`
3. Hydrate engagement metrics for buffered posts via `app.bsky.feed.getPosts` (batch of 25 URIs per call)
4. Re-key posts as `PostBatch` items under the new `runId` (same format processor reads)
5. Update run state with total post count and API stats
6. Dispatch processor Lambda (same as current `dispatchProcessor()`)
7. Optionally: delete consumed buffer items (or let TTL handle it)

**Fallback:** If the buffer query returns fewer than `minPostsRequired` (250), fall back to search-API-based fetching using the existing `GetTrendingPostsBatch` logic. This handles consumer downtime gracefully.

**Engagement hydration detail:**
- `app.bsky.feed.getPosts` accepts up to 25 AT-URIs per call
- For 50,000 posts, this is 2,000 API calls
- At 3,000 requests/5min rate limit, this takes ~3.3 minutes
- Lambda has a 15-minute timeout, which is sufficient
- Batch calls in goroutines (10 concurrent) with rate limiting to stay under API limits
- Posts that fail hydration retain `likes=0, reposts=0, replies=0` (engagement score = 0, won't appear in top-5)

### Handle Resolution

Jetstream provides the author's DID but not their handle. The current system stores `author` as a handle (e.g., `alice.bsky.social`). Two options:

**Option A (Recommended): Resolve at hydration time.**
When calling `app.bsky.feed.getPosts` to get engagement metrics, the response includes the full `PostView` with `author.handle`. This means handle resolution is free — it comes with the engagement data we already need.

**Option B: Resolve in the consumer.**
Call `com.atproto.identity.resolveHandle` for each unique DID. This adds API calls and latency to the hot path. Not recommended.

## Data Flow

```
1. Jetstream → Consumer: ~1,500-3,000 posts/minute arrive via WebSocket
2. Consumer → DynamoDB: Each post written to "window-buffer" partition with minute-bucket SK
3. EventBridge → Window Trigger: Every 30 minutes
4. Window Trigger → DynamoDB: Query buffer for [cutoff, now) range
5. Window Trigger → Bluesky API: Hydrate engagement via getPosts (batches of 25)
6. Window Trigger → DynamoDB: Write PostBatch items under new runId
7. Window Trigger → Processor Lambda: Async invoke with { runId }
8. Processor → (unchanged from here) → Analyze → Post → Sparkline → etc.
```

## Infrastructure Changes

### New Resources (Terraform)
- **ECR Repository:** `hourstats-jetstream-consumer` — container image for the Go binary
- **ECS Cluster:** `hourstats` (or reuse existing if one exists)
- **ECS Task Definition:** 0.25 vCPU, 0.5 GB RAM, Fargate Spot
- **ECS Service:** desired count = 1, deployment minimum healthy = 0, maximum = 1
- **IAM Role (task):** DynamoDB write access to `hourstats-state` table, CloudWatch Logs, CloudWatch Metrics
- **Security Group:** Outbound-only (WebSocket to Jetstream, HTTPS to DynamoDB endpoint)
- **CloudWatch Log Group:** `/ecs/hourstats-jetstream-consumer`
- **CloudWatch Alarms:** Consumer disconnected > 5 minutes, write error rate > 1%

### Modified Resources
- **Lambda `hourstats-fetcher`:** Code changes only (no infra changes). IAM role gains `dynamodb:Query` on the buffer partition and `bsky.feed.getPosts` via the existing Bluesky client.

### Unchanged Resources
- All other Lambda functions, DynamoDB tables, EventBridge rules, S3 bucket, IAM roles

## Risks / Trade-offs

1. **Persistent compute cost.** ECS Fargate runs 24/7 (~$3-7/month with Spot). Current architecture is pure Lambda (pay-per-invocation). Mitigation: Fargate Spot pricing and the consumer is tiny (0.25 vCPU).

2. **Engagement metrics require hydration.** The search API returns posts with current likes/reposts/replies. Jetstream delivers posts at birth with zero engagement. The `getPosts` batch call adds ~3 minutes of processing time per window. Mitigation: This fits within the Lambda timeout (15 min) and the 30-minute cadence.

3. **Posts at window boundary may have low engagement.** A post created 29 minutes ago has had time to accumulate engagement; one created 1 minute ago has not. This is the same as the current behavior (search API returns engagement at query time, not at post creation time). The hydration step in the window trigger queries current engagement, so the effect is identical.

4. **Consumer single point of failure.** If the consumer dies and ECS takes time to restart, posts during the gap are lost. Mitigation: Jetstream cursor replay covers gaps up to several hours; ECS restarts within ~60 seconds; the fallback to search API covers extended outages.

5. **DynamoDB write volume increases.** Currently: ~100 batch writes per 30-minute run. With Jetstream: ~50,000-100,000 individual writes per 30 minutes. Mitigation: DynamoDB on-demand scales automatically; estimated cost ~$5-6/day. If cost is a concern, batch writes in the consumer (groups of 25 via `BatchWriteItem`) reduce this to ~2,000-4,000 batch requests per 30 minutes.

6. **Handle resolution.** Jetstream provides DIDs, not handles. Mitigation: Option A (resolve at hydration time) is free — the `getPosts` response includes handles.

## Migration Plan

### Phase 1: Build consumer (no production impact)
- Implement `cmd/jetstream-consumer/` 
- Add DynamoDB buffer write logic to `internal/state/`
- Deploy to ECS Fargate alongside existing infrastructure
- Validate: consumer writes to buffer, posts appear with correct schema

### Phase 2: Build window trigger (no production impact)
- Modify `cmd/lambda-fetcher/` to read from buffer instead of search API
- Implement engagement hydration via `getPosts`
- Deploy as new Lambda version with alias
- Validate: window trigger produces identical `PostBatch` items as current fetcher

### Phase 3: Cutover
- Point EventBridge rule to new Lambda version
- Monitor first 3 runs (90 minutes) for:
  - Post count >> previous runs (confirming full coverage)
  - Sentiment analysis produces valid results
  - Top-5 posts have correct engagement scores and clickable links
  - Sparkline posts correctly
- If issues: revert EventBridge to previous Lambda version (instant rollback)

### Phase 4: Cleanup
- Remove search-API pagination loop from fetcher (keep client methods for fallback)
- Update openspec specs to reflect new architecture
- Update asyncapi.yaml with consumer event flow
