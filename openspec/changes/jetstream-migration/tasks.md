# Jetstream Migration — Implementation Tasks

## Phase 1: Jetstream Consumer Service

### Task 1.1: Jetstream Client Library
- [ ] Create `internal/jetstream/client.go` with WebSocket connection management
- [ ] Implement `Connect(ctx, endpoint, cursor)` that opens WebSocket and returns an event channel
- [ ] Implement exponential backoff reconnection (1s, 2s, 4s, 8s, max 30s)
- [ ] Implement cursor query parameter for resuming from last position
- [ ] Parse Jetstream JSON events into a `JetstreamEvent` struct
- [ ] Filter for `kind="commit"`, `operation="create"`, `collection="app.bsky.feed.post"`
- [ ] Extract: DID, rkey, CID, text, createdAt, langs from `commit.record`
- [ ] Construct AT-URI: `at://<did>/app.bsky.feed.post/<rkey>`
- [ ] **Test:** Unit test event parsing with sample Jetstream JSON payloads
- [ ] **Test:** Unit test AT-URI construction
- [ ] **Reference:** Jetstream event format at https://github.com/bluesky-social/jetstream

### Task 1.2: DynamoDB Buffer Writer
- [ ] Add `WindowBufferManager` to `internal/state/` (new file `window_buffer.go`)
- [ ] Implement `WritePost(ctx, post)` that writes to `PK="window-buffer"`, `SK="<ISO-minute>#<CID>"`
- [ ] Implement `WriteCursor(ctx, cursor int64)` that writes to `PK="jetstream-cursor"`, `SK="latest"`
- [ ] Implement `ReadCursor(ctx) (int64, error)` to read stored cursor on startup
- [ ] Set TTL = current time + 7200 seconds (2 hours) on buffer items
- [ ] Set TTL = current time + 86400 seconds (24 hours) on cursor item
- [ ] Use the existing `hourstats-state` DynamoDB table — no new tables required
- [ ] **Test:** Integration test with DynamoDB Local or mocked client
- [ ] **Constraint:** Post items must use the existing `state.Post` struct for compatibility

### Task 1.3: Consumer Binary
- [ ] Create `cmd/jetstream-consumer/main.go`
- [ ] Wire up: Jetstream client → event channel → buffer writer
- [ ] Persist cursor every 10 seconds in a background goroutine
- [ ] Handle SIGTERM: flush cursor, close WebSocket, exit within 30s
- [ ] Add CloudWatch metrics via embedded metric format (EMF) logs:
  - `PostsIngested`, `ConnectionStatus`, `WriteErrors`, `EventsDiscarded`
- [ ] Add structured logging with `log/slog`
- [ ] **Test:** Manual test against live Jetstream endpoint
- [ ] **Build:** Add to Makefile and Dockerfile (multi-stage Go build)

### Task 1.4: ECS Fargate Infrastructure
- [ ] Create ECR repository: `hourstats-jetstream-consumer`
- [ ] Create ECS cluster (or reference existing): `hourstats`
- [ ] Create ECS task definition: 0.25 vCPU, 0.5 GB RAM, Fargate Spot
- [ ] Create ECS service: desired count = 1, min healthy = 0, max = 1
- [ ] IAM task role: `dynamodb:PutItem`, `dynamodb:GetItem` on `hourstats-state`
- [ ] IAM task execution role: ECR pull, CloudWatch Logs
- [ ] Security group: egress-only (443 for WebSocket + DynamoDB endpoint)
- [ ] CloudWatch log group: `/ecs/hourstats-jetstream-consumer`, 7-day retention
- [ ] CloudWatch alarm: `ConnectionStatus` metric < 1 for 5 minutes → SNS notification
- [ ] All Terraform in `terraform/` directory, following existing conventions
- [ ] **Validate:** `terraform plan` shows only additive changes

## Phase 2: Window Trigger Lambda

### Task 2.1: Buffer Query Logic
- [ ] Add `QueryWindowBuffer(ctx, from, to time.Time) ([]Post, error)` to `WindowBufferManager`
- [ ] Query: `PK="window-buffer"` AND `SK BETWEEN "<from-minute>#" AND "<to-minute>#~"`
- [ ] Handle DynamoDB pagination (LastEvaluatedKey loop)
- [ ] Return posts sorted by createdAt
- [ ] **Test:** Unit test with mocked DynamoDB responses

### Task 2.2: Engagement Hydration
- [ ] Add `HydrateEngagement(ctx, posts []Post) ([]Post, error)` to `internal/client/bluesky.go`
- [ ] Call `app.bsky.feed.getPosts` in batches of 25 URIs
- [ ] Run 10 concurrent goroutines with a semaphore
- [ ] Rate limit: max 500 requests per minute (well under 3,000/5min limit)
- [ ] For each response, update: likes, reposts, replies, author handle
- [ ] For failed/missing posts: retain with zero engagement, log warning
- [ ] Apply adult content label filtering (reuse existing `hasAdultContentLabel`)
- [ ] **Test:** Unit test with mocked API responses
- [ ] **Test:** Verify rate limiting doesn't exceed API limits

### Task 2.3: Modify Fetcher Lambda
- [ ] Refactor `cmd/lambda-fetcher/main.go` Handle method:
  1. Create run (existing `createRun` — unchanged)
  2. Query buffer via `WindowBufferManager.QueryWindowBuffer`
  3. If buffer has >= 250 posts: hydrate engagement, re-key as PostBatch, dispatch processor
  4. If buffer has < 250 posts: fall back to existing `fetchAllPostsInParallel` (search API)
  5. Dispatch processor (existing `dispatchProcessor` — unchanged)
- [ ] Add 12-minute time budget for hydration step (leave 3 min for dispatch + overhead)
- [ ] Log which path was taken (buffer vs fallback) and post counts
- [ ] **Test:** Unit test both paths (buffer path and fallback path)
- [ ] **Test:** Integration test: run consumer for 35 min, trigger window trigger, verify processor receives data

### Task 2.4: PostBatch Re-keying
- [ ] After hydration, write posts using existing `StateManager.AddPosts(ctx, runId, posts)`
- [ ] This already handles batching into groups of 100 and correct DynamoDB key format
- [ ] Update RunState with `totalPostsRetrieved`, `totalAPIPostsReturned`, timestamps
- [ ] **Verify:** Processor's `GetAllPosts(ctx, runId)` returns the re-keyed posts correctly

## Phase 3: Cutover

### Task 3.1: Deploy Consumer
- [ ] Build and push Docker image to ECR
- [ ] `terraform apply` to create ECS resources
- [ ] Verify consumer connects and writes to buffer (check CloudWatch logs + DynamoDB scan)
- [ ] Let run for 1 hour to accumulate data

### Task 3.2: Deploy Modified Fetcher
- [ ] Deploy new Lambda code (same function name, new code)
- [ ] First run: verify it reads from buffer, hydrates engagement, dispatches processor
- [ ] Monitor CloudWatch logs for the first 3 runs (90 minutes):
  - Post count should be 5-10x higher than previous runs
  - Sentiment analysis should produce valid results
  - Top-5 posts should have non-zero engagement and clickable handles
  - Sparkline should post correctly as reply

### Task 3.3: Validate
- [ ] Compare 3 consecutive runs against recent historical runs:
  - Total posts retrieved (expect 30,000–100,000 vs previous 2,000–10,000)
  - Sentiment distribution (should be similar — more data, but similar ratio)
  - Top-5 engagement scores (should be equal or higher — larger pool)
  - Bluesky post format unchanged (mood hashtag, 5 authors with links, sentiment symbols)
- [ ] Verify DynamoDB costs are within acceptable range (check Billing console)
- [ ] Verify ECS Fargate costs (check Cost Explorer)

## Phase 4: Cleanup

### Task 4.1: Remove Dead Code
- [ ] Remove `fetchAllPostsInParallel` from `cmd/lambda-fetcher/main.go` (keep as fallback if desired)
- [ ] Remove timeout-skipping cursor heuristics (no longer needed)
- [ ] Remove `maxIterations` and `earlyStopTime` constants (no longer applicable)
- [ ] Keep `GetTrendingPostsBatch` and `GetTrendingPosts` in `internal/client/bluesky.go` (useful for testing)

### Task 4.2: Update Specifications
- [ ] Update `openspec/specs/post-fetching/spec.md` to reference Jetstream consumer + window trigger
- [ ] Move current post-fetching spec to an archive section
- [ ] Update `asyncapi.yaml` with new event flow (Jetstream → Consumer → DynamoDB → Window Trigger → Processor)
- [ ] Add `openspec/specs/jetstream-consumer/spec.md` (from this change)
- [ ] Add `openspec/specs/window-trigger/spec.md` (from this change)

### Task 4.3: Documentation
- [ ] Update `README.md` Architecture section to describe Jetstream-based pipeline
- [ ] Update project structure diagram to include `cmd/jetstream-consumer/`
- [ ] Add operational runbook: how to restart consumer, check cursor, manually trigger window
