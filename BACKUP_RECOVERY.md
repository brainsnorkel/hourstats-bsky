> **⚠️ LEGACY DOCUMENT** — This document describes the original AWS Lambda architecture. The project is migrating to Fly.io with a single Go binary and SQLite. See [ARCHITECTURE.md](/ARCHITECTURE.md) for the current architecture and the [Migration Plan](openspec/changes/jetstream-migration/MIGRATION_PLAN.md) for migration status.

---

# Backup & Recovery

## How Backups Work (Fly.io + SQLite)

The bot runs on Fly.io with a SQLite database stored on a persistent volume at `/data`. Backups run automatically every 24 hours (wall-clock aligned) and on startup.

### What Gets Backed Up

Only **essential tables** containing irreplaceable data are backed up. Transient tables (`post_buffer`, `topic_tokens`, `cursor`) are regenerated from the live Jetstream firehose and skipped to keep backups small and fast.

| Table | Purpose |
|---|---|
| `runs` | Run state and top posts per analysis cycle |
| `sentiment_history` | Per-run sentiment data points |
| `daily_sentiment` | Daily aggregated sentiment averages |
| `topic_snapshots` | Trending topic analysis snapshots |
| `topic_identity` | Persistent topic identity tracking |
| `key_value` | Configuration and state (e.g. yearly post URI) |

### Local Backups

A SQLite backup is created in `/data/backups/` every 24 hours. Old backups are pruned based on `BACKUP_RETAIN_DAYS` (default: 1 day in staging, 7 in production).

```
/data/backups/hourstats-prod-2026-02-09T120000Z.db
```

### S3 Backups

If configured, backups are also uploaded to S3 after each local backup. The S3 key structure is:

```
s3://{S3_BACKUP_BUCKET}/{profile}/{timestamp}.db
```

**Required environment variables:**
- `S3_BACKUP_BUCKET` — S3 bucket name
- `S3_BACKUP_REGION` — AWS region (default: `us-west-2`)
- `AWS_ACCESS_KEY_ID` — AWS access key
- `AWS_SECRET_ACCESS_KEY` — AWS secret key

### Backup Implementation

Backups use `ATTACH DATABASE` to copy essential tables from the live database to a new file, avoiding `VACUUM INTO` so the main database's WAL writer (firehose ingestion) is never blocked. See `internal/store/backup.go`.

## Recovery

### Option 1: Restore from S3 (Recommended)

Download the latest backup from S3 and place it on the Fly.io volume:

```bash
# 1. Download backup from S3
aws s3 cp s3://BUCKET/prod/2026-02-09T120000Z.db ./hourstats-restored.db

# 2. Upload to Fly.io volume
fly sftp shell -a hourstats-prod
> put hourstats-restored.db /data/hourstats-prod.db

# 3. Restart the app to pick up the restored database
fly apps restart hourstats-prod
```

### Option 2: Restore from Local Backup

If the Fly.io volume still has data, use a local backup:

```bash
# SSH into the machine
fly ssh console -a hourstats-prod

# List available backups
ls -la /data/backups/

# Copy backup over the live database (stop the app first)
cp /data/backups/hourstats-prod-2026-02-09T120000Z.db /data/hourstats-prod.db
```

### Option 3: Fresh Start

If no backup is available, the database will be recreated on startup with empty tables. Historical data will be lost, but the bot will begin collecting new data immediately from the Jetstream firehose.

### Data Loss Expectations

| Scenario | Data Lost |
|---|---|
| Volume survives, app crashes | None — SQLite WAL ensures durability |
| Volume lost, S3 backup exists | Up to 24 hours of data |
| Volume lost, no S3 backup | All historical data |
| S3 backup + DynamoDB import | Can seed from legacy DynamoDB data using `import-dynamodb` tool |

---

# DynamoDB Backup & Recovery (Legacy)

## How Backups Work

All three DynamoDB tables have **Point-in-Time Recovery (PITR)** enabled, providing continuous backups with per-second granularity for the last 35 days.

Before every production deploy, the GitHub Actions pipeline exports all tables to S3 using `export-table-to-point-in-time`. This creates a snapshot in `s3://hourstats-dynamodb-backups/pre-deploy/` with zero read capacity cost.

### Tables

| Table | Purpose | Typical Size |
|---|---|---|
| `hourstats-state` | Run state + post data | ~9,600 items, ~115 MB |
| `hourstats-sentiment-history` | Per-run sentiment records | ~660 items |
| `hourstats-daily-sentiment` | Daily aggregated sentiment | ~150 items |

### S3 Bucket

- **Bucket**: `hourstats-dynamodb-backups`
- **Versioning**: Enabled
- **Encryption**: AES256
- **Public access**: Fully blocked
- **Lifecycle**: Objects expire after 90 days, non-current versions after 30 days

## Recovery Options

### Option 1: PITR Restore (Recommended)

Restores a table to any point in the last 35 days. Creates a **new table** — you then swap it for the original.

```bash
# 1. Restore to a new table at a specific point in time
aws dynamodb restore-table-to-point-in-time \
  --source-table-name hourstats-state \
  --target-table-name hourstats-state-restored \
  --restore-date-time "2026-02-07T21:00:00Z"

# 2. Wait for restore to complete (check status)
aws dynamodb describe-table --table-name hourstats-state-restored \
  --query 'Table.TableStatus'

# 3. Once ACTIVE, verify data looks correct
aws dynamodb scan --table-name hourstats-state-restored \
  --select COUNT

# 4. Delete the original and rename (or update Terraform/Lambda env vars)
#    NOTE: DynamoDB does not support table rename. You must either:
#    a) Delete the old table and create a new one with the original name, or
#    b) Update all Lambda environment variables to point to the restored table
```

Repeat for each table that needs restoring.

### Option 2: Restore from S3 Export

Use the pre-deploy exports stored in S3.

```bash
# 1. List available exports
aws dynamodb list-exports --no-cli-pager

# 2. Find the export you want and check its status
aws dynamodb describe-export \
  --export-arn "arn:aws:dynamodb:us-east-1:ACCOUNT_ID:table/TABLE_NAME/export/EXPORT_ID"

# 3. Import the export into a new table
aws dynamodb import-table \
  --s3-bucket-source S3Bucket=hourstats-dynamodb-backups,S3KeyPrefix=pre-deploy/hourstats-state/AWSDynamoDB/EXPORT_ID/ \
  --input-format DYNAMODB_JSON \
  --table-creation-parameters '{"TableName":"hourstats-state-restored","KeySchema":[{"AttributeName":"runId","KeyType":"HASH"},{"AttributeName":"postId","KeyType":"RANGE"}],"AttributeDefinitions":[{"AttributeName":"runId","AttributeType":"S"},{"AttributeName":"postId","AttributeType":"S"}],"BillingMode":"PAY_PER_REQUEST"}'

# 4. After import completes, re-create GSIs and enable TTL/PITR
#    (imports don't preserve GSIs — re-apply via Terraform)
```

### Option 3: Legacy Go Restore Tool

For backups created with the old `cmd/dynamodb-backup` tool (local or S3):

```bash
# From local backup
go run cmd/dynamodb-restore/main.go \
  -input ./backups/backup-2026-02-07T21-20-39Z \
  -tables "hourstats-state" \
  -verbose

# From S3
go run cmd/dynamodb-restore/main.go \
  -s3-bucket "hourstats-dynamodb-backups" \
  -s3-prefix "manual/backup-2026-02-07T21-20-39Z" \
  -tables "hourstats-state" \
  -verbose
```

## Manual Export (Outside CI/CD)

```bash
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
for TABLE in hourstats-state hourstats-sentiment-history hourstats-daily-sentiment; do
  aws dynamodb export-table-to-point-in-time \
    --table-arn "arn:aws:dynamodb:us-east-1:${ACCOUNT_ID}:table/${TABLE}" \
    --s3-bucket "hourstats-dynamodb-backups" \
    --s3-prefix "manual/${TABLE}" \
    --export-format DYNAMODB_JSON \
    --no-cli-pager
done
```

## CI/CD Integration

The `backup` job in `.github/workflows/deploy-lambda.yml` runs before `deploy`. If the export call fails, the deploy is blocked. Exports run server-side asynchronously — the CI step just initiates them.

Pipeline: `test → build → backup → deploy → notify`
