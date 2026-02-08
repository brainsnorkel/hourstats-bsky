# AWS Lambda to Fly.io Migration Plan

## Current State

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

## Target State

Two separate Fly.io apps, each running a single Go binary (`shared-cpu-1x`, 256MB, `sjc` region) with its own SQLite on a 1GB persistent volume.

```
hourstats-prod (Fly.io app)              hourstats-staging (Fly.io app)
├── fly.prod.toml                        ├── fly.staging.toml
├── /data/hourstats-prod.db (SQLite)     ├── /data/hourstats-staging.db (SQLite)
├── BLUESKY_HANDLE=hourstats.bsky.social ├── BLUESKY_HANDLE=hourstats-staging.bsky.social
└── Machine sjc, 256MB                   └── Machine sjc, 256MB, DRY_RUN=true
```

All 5 Lambda functions collapse into goroutines within a single binary:

| Lambda | Fly.io Equivalent |
|--------|-------------------|
| `hourstats-fetcher` | Jetstream WebSocket consumer goroutine (always-on) |
| `hourstats-processor` | Cron-triggered function call (every 30 min) |
| `hourstats-sparkline-poster` | Called directly after processor completes |
| `hourstats-daily-aggregator` | Cron-triggered function call (daily midnight UTC) |
| `hourstats-yearly-poster` | Cron-triggered function call (monthly 1st 01:00 UTC) |

## Migration Strategy: Parallel Run then Cutover

The migration is NOT a lift-and-shift. The Fly.io version is a greenfield reimplementation using Jetstream instead of the search API. The two systems can run in parallel safely because they post to different Bluesky accounts during testing.

### Phase 0: Infrastructure (this phase)

- [x] Create Fly.io apps `hourstats-prod` and `hourstats-staging` in `sjc` region
- [x] Create 1GB persistent volumes for each app
- [x] Set secrets per app (BLUESKY_HANDLE, BLUESKY_PASSWORD)
- [x] Write `fly.prod.toml`, `fly.staging.toml`, `Dockerfile`, `.dockerignore`
- [x] Deploy placeholder binary to both apps, verify VMs running
- [ ] Seed staging database with exported DynamoDB data

### Phase 1: Jetstream Consumer (get data flowing)

Build `internal/jetstream/` package:
- WebSocket client connecting to `wss://jetstream2.us-east.bsky.network/subscribe?wantedCollections=app.bsky.feed.post`
- Event parser filtering for `kind=commit`, `operation=create`, `collection=app.bsky.feed.post`
- SQLite buffer writer (replaces DynamoDB buffer)
- Cursor persistence (every 10 seconds, plus on SIGTERM)
- Exponential backoff reconnection

Validation: deploy to Fly.io, let run for 1 hour, verify posts accumulate in SQLite buffer. Expected: ~90K-180K posts per hour.

### Phase 2: Window Trigger + Processor (produce output)

Build processing pipeline as goroutines:
- 30-minute cron scheduler
- Buffer reader (query SQLite for posts in window)
- Engagement hydration via `app.bsky.feed.getPosts` (25 URIs/batch, 10 concurrent)
- VADER sentiment analysis (reuse existing `internal/analyzer/`)
- Post formatting (reuse existing `internal/formatter/`)
- Bluesky posting (reuse existing `internal/client/`)

Test against **staging account** (`hourstats-staging.bsky.social`):
- Verify post format matches production output
- Verify engagement scores are reasonable (should be higher than search API due to larger sample)
- Verify handles resolve correctly from DIDs

### Phase 3: Sparkline + Aggregation (charts and history)

- Sparkline chart generation (reuse `internal/sparkline/`)
- Reply threading
- Daily aggregation
- Yearly chart generation
- Profile pinning

Test: accumulate 24+ hours of staging data, verify sparkline posts correctly as reply.

### Phase 4: DynamoDB Data Migration

Export production DynamoDB tables to seed the Fly.io SQLite databases:

```bash
# Export from AWS
go run cmd/dynamodb-backup/main.go \
  --tables hourstats-sentiment-history,hourstats-daily-sentiment \
  --output ./backups --compress --verbose

# Transfer to Fly.io
fly sftp shell -a hourstats-bsky
> put backups/... /data/staging/seed/

# Import into SQLite (new tool needed: cmd/import-dynamodb/)
fly ssh console -a hourstats-bsky -C "/usr/local/bin/hourstats import --input /data/staging/seed/"
```

This gives the staging instance real historical data for sparkline and yearly chart testing without waiting days for data to accumulate.

### Phase 5: Parallel Run (confidence building)

Run both systems simultaneously for at least 1 week:

| System | Account | Status |
|--------|---------|--------|
| AWS Lambda (production) | `hourstats.bsky.social` | Continues posting normally |
| Fly.io (staging) | `hourstats-staging.bsky.social` | Posts to staging account |

Compare:
- Post count per window (Fly.io should be 5-10x higher due to Jetstream vs search API)
- Sentiment distribution (should be similar despite different sample sizes)
- Top-5 engagement scores (Fly.io should find equal or higher engagement posts)
- Chart rendering (should be visually identical)
- Uptime (Fly.io consumer should stay connected with minimal reconnects)

### Phase 6: Production Cutover

Once confidence is established:

1. **Remove `DRY_RUN=true`** from `fly.prod.toml` env and redeploy prod app
2. **Disable AWS EventBridge rules** to stop Lambda pipeline
3. **Monitor first 6 runs** (3 hours):
   - Post count, sentiment, top-5, sparkline threading
   - No duplicate posts (only one system posting)
4. **Keep AWS infrastructure intact** for 1 week as rollback

Rollback: re-enable EventBridge rules → Lambda pipeline resumes within 30 minutes.

### Phase 7: AWS Decommission

After 1 week of stable Fly.io production:

1. Export final DynamoDB data as archive backup
2. `terraform destroy` for Lambda functions, EventBridge rules
3. Keep DynamoDB tables in a reduced state (on-demand, delete items via TTL) for 30 days
4. Final `terraform destroy` for all remaining resources
5. Cancel/reduce AWS account usage

### Risk Mitigations

| Risk | Mitigation |
|------|------------|
| Fly.io VM restart loses in-flight data | Jetstream cursor persisted to SQLite on volume; consumer replays missed events on restart |
| SQLite corruption | Daily Fly.io volume snapshots (5-day retention); can fork volume to recover |
| Fly.io platform outage | Re-enable AWS EventBridge rules for instant rollback (keep infra for 1 week post-cutover) |
| Higher memory than expected | Monitor via `fly ssh console -C "ps aux"`. Upgrade to 512MB ($3.32/mo) if needed |
| Rate limiting on Bluesky API | Existing rate limiter in hydration code (10 concurrent, 500 req/min cap) |

## Timeline

| Phase | Duration | Dependency |
|-------|----------|------------|
| Phase 0: Infrastructure | 1 day | None |
| Phase 1: Jetstream Consumer | 2-3 days | Phase 0 |
| Phase 2: Window + Processor | 3-4 days | Phase 1 |
| Phase 3: Sparkline + Aggregation | 2-3 days | Phase 2 |
| Phase 4: Data Migration | 1 day | Phase 0 + backup tools |
| Phase 5: Parallel Run | 7 days | Phase 3 + Phase 4 |
| Phase 6: Production Cutover | 1 day | Phase 5 |
| Phase 7: AWS Decommission | 1 day + 30 day wait | Phase 6 |

**Total: ~3-4 weeks** from start to AWS decommission.

## Cost Comparison

| | Current (AWS Lambda) | Target (Fly.io) |
|--|---------------------|-----------------|
| Monthly | ~$5-10 | ~$2.20 |
| Annual | ~$60-120 | ~$26 |
| Post coverage | ~10% | 100% |

The Fly.io approach is both cheaper and provides 10x better data coverage.
