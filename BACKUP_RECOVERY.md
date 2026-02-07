# DynamoDB Backup & Recovery

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
