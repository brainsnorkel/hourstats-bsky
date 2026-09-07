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
