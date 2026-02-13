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

### Phase 6: Parallel Run ✅ COMPLETE

Both systems ran simultaneously. The Fly.io staging app was seeded with production DynamoDB data and validated end-to-end.

| System | Account | Status |
|--------|---------|--------|
| AWS Lambda (production) | `hourstats.bsky.social` | Active — will be deactivated at cutover |
| Fly.io (`hourstats-staging`) | `hourstats-staging.bsky.social` | Validated — full pipeline working |

**Validated:**

- [x] Sentiment analysis cycle (30 min wall-clock aligned)
- [x] Sparkline chart posted as reply (7-day history from imported DynamoDB data)
- [x] Trending topics detected and posted (TF-IDF + Gemini grouping)
- [x] S3 backup uploaded on startup
- [x] Jetstream connected and ingesting (~900 posts/min)
- [x] Post buffer growing, hydration working (including transient API 502 recovery)
- [x] DynamoDB import tool seeded 664 sentiment + 152 daily records
- [x] Cursor persistence across restarts

**Deferred to soak period (Phase 7):**

- [ ] Verify daily aggregation at midnight UTC
- [ ] Verify yearly chart posting (1st of month at 01:00 UTC)
- [ ] 48+ hours of uninterrupted operation

### Phase 7: Production Cutover 🔄 IN PROGRESS

The original plan used a separate `hourstats-prod` Fly.io app. That app was destroyed (unused, v1 only). Instead, the existing `hourstats-staging` app is being promoted in-place by swapping Bluesky credentials and adjusting config to production values.

**Why in-place promotion:** The staging app already has the seeded historical data, a 10GB volume with working SQLite database, and proven stability. Creating a new app would require re-seeding and re-validating. The Fly.io app name (`hourstats-staging`) is internal — the Bluesky account it posts to is what makes it "production".

#### Step 1: Update `fly.staging.toml` with production values

```diff
- TRENDING_POST_HOURS = "1"
+ TRENDING_POST_HOURS = "6"
- BACKUP_RETAIN_DAYS = "1"
+ BACKUP_RETAIN_DAYS = "7"
```

#### Step 2: Swap Bluesky credentials

```bash
fly secrets set \
  BLUESKY_HANDLE="hourstats.bsky.social" \
  BLUESKY_PASSWORD="<production-app-password>" \
  -a hourstats-staging
```

This automatically restarts the VM with the new credentials.

#### Step 3: Disable AWS Lambda pipeline (immediately after credential swap)

Disable EventBridge rules to stop the Lambda system from posting. This is instant and reversible.

```bash
aws events disable-rule --name hourstats-schedule
aws events disable-rule --name hourstats-daily-aggregation-schedule
aws events disable-rule --name hourstats-yearly-posting-schedule
```

**Verify disabled:**
```bash
aws events describe-rule --name hourstats-schedule | grep State
# Expected: "State": "DISABLED"
```

#### Step 4: Deploy and monitor

```bash
make deploy-staging
fly logs -a hourstats-staging --no-tail | tail -50
```

Confirm:
- [ ] Posts appearing on `@hourstats.bsky.social` (not staging account)
- [ ] Sparkline reply threaded correctly
- [ ] No duplicate posts from Lambda (EventBridge disabled)
- [ ] Trending topics posting every 6 hours (not 1 hour)
- [ ] S3 backups going to `staging/` prefix (cosmetic — data is the same)

#### Step 5: Monitor first 6 runs (3 hours)

```bash
fly ssh console -a hourstats-staging -C \
  "sh -c \"sqlite3 /data/hourstats-staging.db 'SELECT run_id, status, posts_analyzed, sentiment, net_sentiment_pct FROM runs ORDER BY rowid DESC LIMIT 6;'\""
```

#### Rollback

If the Fly.io app fails after cutover:

```bash
# Re-enable Lambda pipeline (posts resume within 30 minutes)
aws events enable-rule --name hourstats-schedule
aws events enable-rule --name hourstats-daily-aggregation-schedule
aws events enable-rule --name hourstats-yearly-posting-schedule

# Revert Fly.io to staging account (optional, prevents duplicate posts)
fly secrets set \
  BLUESKY_HANDLE="hourstats-staging.bsky.social" \
  BLUESKY_PASSWORD="<staging-app-password>" \
  -a hourstats-staging
```

#### Profile and database naming

The app retains `HOURSTATS_PROFILE=staging`, so the database is `/data/hourstats-staging.db` and S3 backups go to `staging/`. This is cosmetic and has no functional impact. If desired, the profile can be changed to `prod` later by updating `fly.staging.toml` and redeploying — but this will create a new empty database at `/data/hourstats-prod.db` unless the existing DB is renamed first:

```bash
fly machine stop 4d8929e0f59618 -a hourstats-staging
fly ssh console -a hourstats-staging -C \
  "sh -c \"mv /data/hourstats-staging.db /data/hourstats-prod.db && mv /data/hourstats-staging.db-wal /data/hourstats-prod.db-wal 2>/dev/null; mv /data/hourstats-staging.db-shm /data/hourstats-prod.db-shm 2>/dev/null\""
# Then update fly.staging.toml: HOURSTATS_PROFILE = "prod"
# Then: make deploy-staging
```

This is optional and can be done at any time without data loss.

### Phase 8: AWS Decommission ⬜ NOT STARTED

After 1 week of stable Fly.io production posting to `@hourstats.bsky.social`:

#### Step 1: Final DynamoDB archive (safety net)

```bash
# Export sentiment history (already done during Phase 5, but re-export for final state)
go run cmd/dynamodb-backup/main.go
# Files saved to data/seed/
```

#### Step 2: Destroy Lambda functions and EventBridge rules via Terraform

```bash
cd terraform
terraform plan -destroy -target=aws_cloudwatch_event_rule.hourstats_schedule \
  -target=aws_cloudwatch_event_rule.hourstats_daily_aggregation_schedule \
  -target=aws_cloudwatch_event_rule.hourstats_yearly_posting_schedule \
  -target=aws_lambda_function.hourstats_fetcher \
  -target=aws_lambda_function.hourstats_processor \
  -target=aws_lambda_function.hourstats_sparkline_poster \
  -target=aws_lambda_function.hourstats_daily_aggregator \
  -target=aws_lambda_function.hourstats_yearly_poster

# Review the plan, then:
terraform apply -destroy -target=...  # (same targets as above)
```

#### Step 3: Keep DynamoDB tables for 30 days (data retention)

DynamoDB tables are pay-per-request and cost $0 when idle. Keep them as a safety net:
- `hourstats-state` — run coordination data (has TTL, auto-expires)
- `hourstats-sentiment-history` — 664 historical records
- `hourstats-daily-sentiment` — 152 daily aggregates

#### Step 4: Clean up remaining AWS resources (after 30 days)

```bash
cd terraform
terraform destroy
# This removes: DynamoDB tables, IAM roles/policies, S3 backup bucket, Lambda permissions
# Note: hourstats-terraform-state S3 bucket is the Terraform backend — destroy manually if desired
```

#### Step 5: Clean up SSM parameters

```bash
aws ssm delete-parameters --names \
  "/hourstats/bluesky/handle" \
  "/hourstats/bluesky/password" \
  "/hourstats/settings/analysis_interval_minutes" \
  "/hourstats/settings/top_posts_count" \
  "/hourstats/settings/min_engagement_score" \
  "/hourstats/settings/dry_run"
```

#### Step 6: Clean up CloudWatch

```bash
aws logs delete-log-group --log-group-name /aws/lambda/hourstats-fetcher
aws logs delete-log-group --log-group-name /aws/lambda/hourstats-processor
aws logs delete-log-group --log-group-name /aws/lambda/hourstats-sparkline-poster
aws logs delete-log-group --log-group-name /aws/lambda/hourstats-daily-aggregator
aws logs delete-log-group --log-group-name /aws/lambda/hourstats-yearly-poster
```

### Phase 9: Merge to Main ⬜ NOT STARTED

The `migrate-to-jetstream` branch contains all Fly.io work. Merge to `main` after the production soak period confirms stability.

#### Soak criteria (all must pass before merge)

- [ ] 48+ hours posting to `@hourstats.bsky.social` with no missed cycles
- [ ] At least 1 daily aggregation cycle completed (midnight UTC)
- [ ] At least 1 trending topics post at 6-hour interval
- [ ] S3 backup confirmed (check `s3://hourstats-sqlite-backups/staging/`)
- [ ] No OOM kills or unexpected restarts (`fly machine status` shows single start event)
- [ ] Sparkline reply threading correct on every post
- [ ] No ERROR-level log entries (WARN for transient API 502s is acceptable)

#### Pre-merge checklist

```bash
# 1. Ensure all tests pass
make test

# 2. Ensure build is clean
make build

# 3. Check for uncommitted changes
git status

# 4. Verify branch is up to date with remote
git fetch origin
git log origin/migrate-to-jetstream..HEAD  # should be empty
```

#### Merge

```bash
git checkout main
git merge migrate-to-jetstream
git push origin main
```

#### Post-merge cleanup (optional, can be done later)

Remove legacy Lambda code that is no longer deployed:

- `cmd/lambda-fetcher/`
- `cmd/lambda-processor/`
- `cmd/lambda-sparkline-poster/`
- `cmd/lambda-daily-aggregator/`
- `cmd/lambda-yearly-poster/`
- `cmd/dynamodb-backup/`
- `cmd/dynamodb-restore/`
- `cmd/diagnostics/`
- `cmd/local-test/`
- `internal/state/`
- `internal/lambda/`
- `internal/awsutil/`
- `internal/backup/`
- `scripts/deploy-production.sh`
- `Makefile.lambda` (if it exists)
- `terraform/` (after Phase 8 complete)
- `PRODUCTION_DEPLOYMENT.md`

Consider keeping `cmd/import-dynamodb/` until DynamoDB tables are destroyed (Phase 8 Step 4).

#### Recreating a staging/test environment later

If you need a staging environment after merge:

```bash
fly apps create hourstats-test  # or any name
fly volumes create data --size 1 --region sjc -a hourstats-test

# Create a test toml (copy fly.staging.toml, change app name)
fly deploy -c fly.test.toml --ha=false
fly secrets set \
  BLUESKY_HANDLE="hourstats-staging.bsky.social" \
  BLUESKY_PASSWORD="<staging-password>" \
  GOOGLE_AI_API_KEY="<key>" \
  AWS_ACCESS_KEY_ID="<key>" \
  AWS_SECRET_ACCESS_KEY="<secret>" \
  -a hourstats-test
```

---

## Risk Mitigations

| Risk | Mitigation |
|------|------------|
| Fly.io VM restart loses in-flight data | Jetstream cursor persisted to SQLite on volume; consumer replays missed events on restart |
| SQLite corruption | Daily S3 backups with 7-day retention; Fly.io volume snapshots available |
| Fly.io platform outage (during soak) | Re-enable AWS EventBridge rules for instant rollback (keep infra for 1 week post-cutover) |
| Fly.io platform outage (after AWS decommission) | Restore from S3 backup to new Fly.io app; no Lambda fallback |
| Rate limiting on Bluesky API | Rate limiter in hydration code (concurrent batch requests with backoff) |
| Jetstream stall/disconnect | Auto-restart goroutine with exponential backoff; stall detection logs warnings after 5 min silence |
| Accidental double-posting | Disable EventBridge rules before (or immediately after) Fly.io credential swap |

## Timeline

| Phase | Status | Duration |
|-------|--------|----------|
| Phase 0: Infrastructure | ✅ Complete | 1 day |
| Phase 1: Jetstream Consumer | ✅ Complete | 2 days |
| Phase 2: Analysis Pipeline | ✅ Complete | 3 days |
| Phase 3: Charts + Aggregation | ✅ Complete | 3 days |
| Phase 4: Trending Topics | ✅ Complete | 4 days |
| Phase 5: Data Migration + Backups | ✅ Complete | 2 days |
| Phase 6: Parallel Run | ✅ Complete | 7 days |
| Phase 7: Production Cutover | 🔄 In Progress | 1 day |
| Phase 8: AWS Decommission | ⬜ Not Started | 1 day + 30 day wait |
| Phase 9: Merge to Main | ⬜ Not Started | After 48h soak |

## Cost Comparison

| | Current (AWS Lambda) | Target (Fly.io) |
|--|---------------------|-----------------|
| Monthly | ~$5-10 | ~$5 (Hobby plan) |
| Annual | ~$60-120 | ~$60 |
| Post coverage | ~10% | 100% (English posts) |
| Trending topics | N/A | Every 6 hours |
| Backups | DynamoDB PITR + S3 | SQLite → S3 daily |

The Fly.io approach provides 10x better data coverage at the same cost, with the addition of trending topics and improved operational simplicity (single binary, no IAM, no Terraform, no managed DB).
