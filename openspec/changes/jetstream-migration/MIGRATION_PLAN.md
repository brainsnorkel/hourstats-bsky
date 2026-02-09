# AWS Lambda → Fly.io Migration Plan

## Current State (Production — AWS Lambda)

5 AWS Lambda functions orchestrated by EventBridge, backed by 3 DynamoDB tables and S3:

| Lambda | Trigger | Purpose |
|--------|---------|---------|
| `hourstats-fetcher` | EventBridge (30 min) | Search API pagination, creates run, stores posts |
| `hourstats-processor` | Async invoke from fetcher | Sentiment analysis, posts summary to Bluesky |
| `hourstats-sparkline-poster` | Async invoke from processor | 7-day chart, posts as reply |
| `hourstats-daily-aggregator` | EventBridge (daily midnight) | Daily sentiment rollup |
| `hourstats-yearly-poster` | EventBridge (monthly 1st) | 365-day chart, profile pin |

| DynamoDB Table | Purpose |
|----------------|---------|
| `hourstats-state` | Run coordination + post storage |
| `hourstats-sentiment-history` | Per-run sentiment data points |
| `hourstats-daily-sentiment` | Daily aggregated sentiment |

Credentials stored in SSM Parameter Store. Charts uploaded directly to Bluesky blob service (no S3 dependency for images).

## Target State (Fly.io — Implemented on `migrate-to-jetstream` branch)

A single Go binary (`cmd/hourstats/main.go`) running on Fly.io with SQLite on a persistent volume. Two separate Fly.io apps share the same codebase, differentiated by environment variables.

```
hourstats-prod (Fly.io app)              hourstats-staging (Fly.io app)
├── fly.prod.toml                        ├── fly.staging.toml
├── /data/hourstats-prod.db (SQLite)     ├── /data/hourstats-staging.db (SQLite)
├── BLUESKY_HANDLE (Fly secret)          ├── BLUESKY_HANDLE (Fly secret)
├── Machine sjc, shared-cpu-1x, 256MB    ├── Machine sjc, shared-cpu-1x, 512MB
└── HOURSTATS_PROFILE=prod               ├── DRY_RUN=false
                                         ├── TRENDING_ENABLED=true
                                         └── S3_BACKUP_BUCKET=hourstats-sqlite-backups
```

All 5 Lambda functions collapse into goroutines within a single binary:

| Lambda | Fly.io Equivalent |
|--------|-------------------|
| `hourstats-fetcher` | Jetstream WebSocket consumer goroutine (always-on) + wall-clock aligned 30-min analysis cycle |
| `hourstats-processor` | Called directly within the analysis cycle function |
| `hourstats-sparkline-poster` | Called directly after processor completes |
| `hourstats-daily-aggregator` | Wall-clock aligned daily cycle (fires at midnight UTC) |
| `hourstats-yearly-poster` | Wall-clock aligned daily check at 01:00 UTC (posts on 1st of month) |

### Additional Features Built During Migration

| Feature | Description |
|---------|-------------|
| **Trending Topics** | TF-IDF + Gemini Flash topic extraction every 15 min, bump chart posted every 6h |
| **Daily Top Post** | Quote-reply to the day's highest engagement post in the yearly thread |
| **Sentiment Trendline** | Root vs reply sentiment tracking with trendline chart |
| **Volume Charts** | Daily and yearly post volume visualisations |
| **S3 Backups** | Daily SQLite backup to S3 with configurable retention |
| **Stall Detection** | Auto-restart if Jetstream goes silent for 5+ minutes |

## Migration Strategy: Greenfield Reimplementation with Parallel Run

The migration is NOT a lift-and-shift. The Fly.io version is a **greenfield reimplementation** using Jetstream instead of the search API, with SQLite replacing DynamoDB. The two systems post to different Bluesky accounts during testing.

---

### Phase 0: Infrastructure ✅ COMPLETE

- [x] Create Fly.io apps `hourstats-prod` and `hourstats-staging` in `sjc` region
- [x] Create persistent volumes for each app
- [x] Set secrets per app (`BLUESKY_HANDLE`, `BLUESKY_PASSWORD`, `GOOGLE_AI_API_KEY`, AWS credentials)
- [x] Write `fly.prod.toml`, `fly.staging.toml`, `Dockerfile`, `.dockerignore`
- [x] Deploy placeholder binary to both apps, verify VMs running

### Phase 1: Jetstream Consumer + SQLite Store ✅ COMPLETE

Built `internal/jetstream/` and `internal/store/` packages:

- [x] WebSocket client connecting to `wss://jetstream2.us-east.bsky.network/subscribe?wantedCollections=app.bsky.feed.post`
- [x] Event parser filtering for `kind=commit`, `operation=create`, `collection=app.bsky.feed.post`
- [x] English language filter (`lang=en` tag required) for sentiment analysis parity
- [x] SQLite post buffer with 2-hour TTL (replaces DynamoDB buffer)
- [x] Cursor persistence via SQLite `key_value` table
- [x] Exponential backoff reconnection (1s → 60s max)
- [x] Auto-restart on unexpected consumer exit
- [x] Stall detection (5-minute silence warning)
- [x] Unit tests for consumer and store

### Phase 2: Analysis Pipeline + Engagement Hydration ✅ COMPLETE

Built processing pipeline as goroutines within single binary:

- [x] Wall-clock aligned 30-minute cron scheduler (fires at :00 and :30)
- [x] Buffer reader (query SQLite for posts since cutoff)
- [x] Engagement hydration via `app.bsky.feed.getPosts` (25 URIs/batch, concurrent)
- [x] Handle resolution from DID (free via getPosts response)
- [x] Adult content label filtering
- [x] VADER sentiment analysis (reused existing `internal/analyzer/`)
- [x] Post formatting (reused existing `internal/formatter/`)
- [x] Bluesky posting (reused existing `internal/client/`)
- [x] Root vs reply sentiment tracking
- [x] `internal/hydrator/` package with unit tests

### Phase 3: Charts + Aggregation ✅ COMPLETE

- [x] Sparkline chart generation (7-day sentiment, reused `internal/sparkline/`)
- [x] Sentiment trendline chart (root vs reply tracking)
- [x] Reply threading (sparkline + trendline posted as replies)
- [x] Daily aggregation (midnight UTC, wall-clock aligned)
- [x] Yearly chart generation with monthly posting
- [x] Daily and yearly volume charts
- [x] Daily top-post quote reply in yearly thread
- [x] Profile pinning for yearly chart

### Phase 4: Trending Topics ✅ COMPLETE

- [x] Token extraction from root posts (on ingest)
- [x] TF-IDF analysis every 15 minutes
- [x] Gemini Flash semantic grouping (LLM synonym maps)
- [x] Volume-based ranking (top 5 topics)
- [x] Identity tracking (Jaccard similarity for persistent topic UUIDs)
- [x] Bump chart generation (1200x800 PNG, Okabe-Ito palette)
- [x] Exemplar post hydration at posting time
- [x] Movement indicators (+/- rank changes)
- [x] Standalone posting with mutable hashtag facets
- [x] LLM-generated alt text via Gemini

### Phase 5: Data Migration + Backups ✅ COMPLETE

- [x] `cmd/import-dynamodb/` tool for seeding SQLite from DynamoDB exports
- [x] S3 backup integration (`internal/store/backup.go`)
- [x] Daily backup cycle (wall-clock aligned)
- [x] Configurable retention (`BACKUP_RETAIN_DAYS`)
- [x] Backs up essential tables only (skips transient post/token data)
- [x] Streaming via temp file to avoid OOM on large databases

### Phase 6: Parallel Run 🔄 IN PROGRESS

Running both systems simultaneously:

| System | Account | Status |
|--------|---------|--------|
| AWS Lambda (production) | `hourstats.bsky.social` | Continues posting normally |
| Fly.io (staging) | `hourstats-staging.bsky.social` | Active, posting to staging account |

**Remaining validation:**

- [ ] Verify daily aggregation runs at midnight UTC (bead tq0.9)
- [ ] Verify yearly chart posting on staging (bead tq0.10)
- [ ] Verify cursor persistence survives restarts (bead tq0.11)
- [ ] Verify consumer auto-reconnect on failure (bead tq0.12)
- [ ] Run for at least 1 week of stable staging operation
- [ ] Compare sentiment distribution between Lambda and Fly.io
- [ ] Compare top-5 engagement scores (Fly.io should find equal or higher)

### Phase 7: Production Cutover ⬜ NOT STARTED

Once confidence is established:

1. **Configure `fly.prod.toml`** with production secrets:
   - `BLUESKY_HANDLE=hourstats.bsky.social`
   - `BLUESKY_PASSWORD` (via `fly secrets set`)
   - `GOOGLE_AI_API_KEY` (via `fly secrets set`)
   - `DRY_RUN=false`
   - `TRENDING_ENABLED=true`
2. **Seed production SQLite** with exported DynamoDB historical data:
   ```bash
   # Export from AWS and import to Fly.io
   go run cmd/import-dynamodb/main.go --help
   fly sftp shell -a hourstats-prod
   ```
3. **Deploy to production**: `make deploy-prod`
4. **Disable AWS EventBridge rules** to stop Lambda pipeline
5. **Monitor first 6 runs** (3 hours):
   - Post count, sentiment, top-5, sparkline threading, trending topics
   - No duplicate posts (only one system posting)
6. **Keep AWS infrastructure intact** for 1 week as rollback

Rollback: re-enable EventBridge rules → Lambda pipeline resumes within 30 minutes.

### Phase 8: AWS Decommission ⬜ NOT STARTED

After 1 week of stable Fly.io production:

1. Export final DynamoDB data as archive backup
2. `terraform destroy` for Lambda functions, EventBridge rules
3. Keep DynamoDB tables in a reduced state (on-demand, delete items via TTL) for 30 days
4. Final `terraform destroy` for all remaining resources
5. Cancel/reduce AWS account usage
6. Update GitHub Actions CI/CD to remove Lambda deploy workflow
7. Clean up Lambda-specific code (`cmd/lambda-*`, `internal/state/`, `internal/lambda/`)

---

## Risk Mitigations

| Risk | Mitigation |
|------|------------|
| Fly.io VM restart loses in-flight data | Jetstream cursor persisted to SQLite on volume; consumer replays missed events on restart |
| SQLite corruption | Daily S3 backups with configurable retention; Fly.io volume snapshots available |
| Fly.io platform outage | Re-enable AWS EventBridge rules for instant rollback (keep infra for 1 week post-cutover) |
| Higher memory than expected | Staging runs at 512MB; prod can start at 256MB and upgrade if needed |
| Rate limiting on Bluesky API | Rate limiter in hydration code (concurrent batch requests with backoff) |
| Jetstream stall/disconnect | Auto-restart goroutine with exponential backoff; stall detection logs warnings after 5 min silence |

## Timeline

| Phase | Status | Duration |
|-------|--------|----------|
| Phase 0: Infrastructure | ✅ Complete | 1 day |
| Phase 1: Jetstream Consumer | ✅ Complete | 2 days |
| Phase 2: Analysis Pipeline | ✅ Complete | 3 days |
| Phase 3: Charts + Aggregation | ✅ Complete | 3 days |
| Phase 4: Trending Topics | ✅ Complete | 4 days |
| Phase 5: Data Migration + Backups | ✅ Complete | 2 days |
| Phase 6: Parallel Run | 🔄 In Progress | 7+ days |
| Phase 7: Production Cutover | ⬜ Not Started | 1 day |
| Phase 8: AWS Decommission | ⬜ Not Started | 1 day + 30 day wait |

## Cost Comparison

| | Current (AWS Lambda) | Target (Fly.io) |
|--|---------------------|-----------------|
| Monthly | ~$5-10 | ~$5 (Hobby plan) |
| Annual | ~$60-120 | ~$60 |
| Post coverage | ~10% | 100% (English posts) |
| Trending topics | N/A | Every 6 hours |
| Backups | DynamoDB PITR + S3 | SQLite → S3 daily |

The Fly.io approach provides 10x better data coverage at the same cost, with the addition of trending topics and improved operational simplicity (single binary, no IAM, no Terraform, no managed DB).
