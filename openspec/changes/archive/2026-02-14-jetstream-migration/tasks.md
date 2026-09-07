# Jetstream Migration — Implementation Tasks

## Phase 1: Infrastructure (Fly.io)
- [x] Create `fly.prod.toml` and `fly.staging.toml` with shared-cpu-1x config
- [x] Create multi-stage `Dockerfile` (golang:1.24-alpine builder → alpine:3.21 runtime)
- [x] Create `.dockerignore`
- [x] Create Fly.io apps in sjc region with persistent volumes
- [x] Set secrets via `fly secrets set`

## Phase 2: SQLite Store (`internal/store/`)
- [x] Create `store.go` with connection management, WAL mode, busy timeout, schema migration
- [x] Create `post_buffer.go` with InsertPost, GetPostsSince, PurgeOldPosts
- [x] Create `cursor.go` with SaveCursor, GetCursor for Jetstream position tracking
- [x] Create `runs.go` with SaveRun, GetRun, UpdateRun for analysis cycle state
- [x] Create `sentiment_history.go` with SaveSentimentDataPoint, GetSentimentHistory
- [x] Create `daily_sentiment.go` with SaveDailySentiment, GetDailySentimentRange
- [x] Create `daily_top_post.go` for tracking highest engagement posts per day
- [x] Create `key_value.go` for general key-value storage
- [x] Create `topic_store.go` for trending topics data (tokens, snapshots, identities)
- [x] Create `backup.go` with S3 backup streaming (essential tables only, temp file to avoid OOM)
- [x] Create `store_test.go` and `topic_store_test.go` with comprehensive unit tests

## Phase 3: Jetstream Consumer (`internal/jetstream/`)
- [x] Create `types.go` with Event, Commit, PostRecord structs
- [x] Create `consumer.go` with WebSocket connection to Jetstream, event parsing, cursor management
- [x] Implement ConsumerConfig with OnPost callback, SaveCursor/LoadCursor hooks
- [x] Filter for kind=commit, operation=create, collection=app.bsky.feed.post
- [x] English language filter (require explicit lang=en tag)
- [x] Create `consumer_test.go` with unit tests

## Phase 4: Engagement Hydrator (`internal/hydrator/`)
- [x] Create `hydrator.go` with batch getPosts API calls (25 URIs/batch)
- [x] Implement concurrent hydration with semaphore
- [x] Handle resolution from DID via getPosts response
- [x] Adult content label filtering
- [x] Create `hydrator_test.go` with unit tests

## Phase 5: Main Binary (`cmd/hourstats/`)
- [x] Create `main.go` with single-binary architecture
- [x] Wire Jetstream consumer as auto-restarting goroutine with exponential backoff
- [x] Implement wall-clock aligned scheduler (fires at clean UTC boundaries)
- [x] 30-min analysis cycle: read buffer → hydrate → analyze → post → sparkline → trendline
- [x] Daily cycle: backup + daily aggregation + daily top-post quote reply
- [x] Yearly cycle: yearly chart + profile pin (monthly on 1st at 01:00 UTC)
- [x] Stall detection: warn if no Jetstream posts received for 5+ minutes
- [x] Graceful shutdown on SIGTERM/SIGINT with cursor persistence
- [x] Root vs reply sentiment tracking in analysis cycle

## Phase 6: Charting Extensions
- [x] Sentiment trendline chart (root vs reply) in `internal/sparkline/sentiment_trendline_generator.go`
- [x] Daily volume chart in `internal/sparkline/daily_volume_generator.go`
- [x] Yearly volume chart in `internal/sparkline/yearly_volume_generator.go`
- [x] Trending topics bump chart in `internal/sparkline/trending_chart_generator.go`
- [x] LLM-generated alt text for trending chart via Gemini

## Phase 7: Trending Topics (`internal/topics/`)
- [x] Create `tokenizer.go` — lowercase, strip URLs/@mentions/emoji, stopword removal
- [x] Create `tfidf.go` — TF-IDF scoring across 24h corpus
- [x] Create `grouper.go` — Gemini Flash semantic grouping with synonym maps
- [x] Create `ranker.go` — volume-based ranking (top 5 by post count)
- [x] Create `tracker.go` — Jaccard similarity identity tracking with stable UUIDs
- [x] Create `exemplar.go` — highest-engagement exemplar post selection
- [x] Create `formatter.go` — post text formatting with movement indicators
- [x] Create `analyzer.go` — orchestrates 15-min analysis cycle and 6-hour posting
- [x] Create `types.go` — shared type definitions
- [x] Unit tests for all packages (tokenizer, tfidf, grouper, ranker, tracker, exemplar, formatter, analyzer)

## Phase 8: Data Migration Tools
- [x] Create `cmd/import-dynamodb/main.go` for seeding SQLite from DynamoDB exports
- [x] Create `cmd/force-trending/main.go` for manual trending topic triggers

## Phase 9: Parallel Run (IN PROGRESS)
- [ ] Verify daily aggregation runs at midnight UTC
- [ ] Verify yearly chart posting on staging
- [ ] Verify cursor persistence survives Fly.io restarts
- [ ] Verify consumer auto-reconnect on WebSocket failure
- [ ] Run 1 week stable on staging
- [ ] Compare sentiment distribution vs Lambda production

## Phase 10: Production Cutover (NOT STARTED)
- [ ] Configure prod Fly.io secrets
- [ ] Seed prod SQLite with DynamoDB historical data
- [ ] Deploy to hourstats-prod
- [ ] Disable AWS EventBridge rules
- [ ] Monitor first 6 runs

## Phase 11: AWS Decommission (NOT STARTED)
- [ ] Export final DynamoDB archive
- [ ] terraform destroy Lambda + EventBridge
- [ ] 30-day DynamoDB retention period
- [ ] Final terraform destroy
- [ ] Remove Lambda CI/CD workflow
- [ ] Clean up cmd/lambda-* and internal/state/ code

## Documentation Updates (IN PROGRESS)
- [x] Create docs/TRENDING_TOPICS.md
- [ ] Update ARCHITECTURE.md for Fly.io architecture
- [ ] Update README.md (architecture, tech stack, project structure)
- [ ] Mark AWS docs as legacy
- [ ] Update BACKUP_RECOVERY.md for SQLite/S3

