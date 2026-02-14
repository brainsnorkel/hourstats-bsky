# Production Cutover Runbook

Migrate the hourstats bot from `hourstats-staging` (Fly.io) to a properly-named `hourstats-prod` app, then rebuild `hourstats-staging` as an actual staging environment.

## Current State

| Item | Value |
|------|-------|
| Live bot app | `hourstats-staging` (Fly.io) |
| Live bot account | `@hourstats.bsky.social` |
| Machine | `4d8929e0f59618` (SJC) |
| Volume | `vol_45lo5xq3957k0d1r` (10 GB, ~6% used) |
| DB file | `/data/hourstats-staging.db` (~511 MB + WAL) |
| `hourstats-prod` | Destroyed (does not exist) |
| Staging Bluesky account | `@hourstats-staging.bsky.social` (exists, app password ready) |

## Target State

| Item | Value |
|------|-------|
| **Production** app | `hourstats-prod` → `@hourstats.bsky.social` |
| Prod DB file | `/data/hourstats-prod.db` (full history from fork) |
| Prod S3 backups | `s3://hourstats-sqlite-backups/prod/` |
| **Staging** app | `hourstats-staging` → `@hourstats-staging.bsky.social` |
| Staging DB file | `/data/hourstats-staging.db` (fresh, empty) |
| Staging S3 backups | `s3://hourstats-sqlite-backups/staging/` |
| `make deploy-prod` | Deploys to `hourstats-prod` via `fly.prod.toml` |
| `make deploy-staging` | Deploys to `hourstats-staging` via `fly.staging.toml` |

## Prerequisites

- [ ] Fly.io CLI authenticated (`fly auth whoami`)
- [ ] `@hourstats-staging.bsky.social` app password available
- [ ] Current production secrets noted (from `fly secrets list -a hourstats-staging`)
- [ ] No imminent analysis cycle (check `hourstats-stats summary` for next posting time)
- [ ] S3 backup completed recently (safety net)

## Key Technical Details

**DB filename**: The bot names its database `/data/hourstats-{HOURSTATS_PROFILE}.db`. The forked volume will contain `hourstats-staging.db` but the prod toml sets `HOURSTATS_PROFILE=prod`, so the bot will look for `hourstats-prod.db`. We rename the file using a one-off Alpine container before the first deploy.

**S3 backup paths**: Old backups remain at `staging/*` in the S3 bucket. New prod backups go to `prod/*`. Historical data is preserved.

**Duplicate posting prevention**: The staging bot is stopped BEFORE the prod bot is switched from DRY_RUN to live. There is never a window where both bots are posting.

**Volume fork consistency**: Fly.io volume fork is a filesystem-level snapshot. SQLite WAL mode guarantees consistent reads. Safe to fork while the bot is running.

**Staging cleanup**: The old staging volume contains prod data including `yearly_post_uri` and `daily_quote_last_date` key-value entries pointing to `@hourstats.bsky.social` posts. The rebuilt staging bot cannot reply to those threads. Destroying the old volume and creating a fresh one avoids this problem entirely — the bot creates all tables via `migrate()` on startup.

---

## Phase 1: Create hourstats-prod (~5 min)

### 1.1 Create the app

```bash
fly apps create hourstats-prod
```

### 1.2 Fork the production volume

This creates a point-in-time copy of the live database volume into the new app.

```bash
fly volumes fork vol_45lo5xq3957k0d1r \
  --app hourstats-prod \
  --name data \
  --region sjc
```

Note the new volume ID from the output — you'll need it for step 1.3.

### 1.3 Rename the database file

The forked volume contains `hourstats-staging.db` but the prod app expects `hourstats-prod.db`. Use a one-off Alpine container to rename it:

```bash
fly machine run alpine \
  --app hourstats-prod \
  --rm \
  --volume <NEW_VOLUME_ID>:/data \
  --entrypoint "/bin/sh" \
  -- -c "\
    mv /data/hourstats-staging.db /data/hourstats-prod.db && \
    mv /data/hourstats-staging.db-wal /data/hourstats-prod.db-wal 2>/dev/null; \
    mv /data/hourstats-staging.db-shm /data/hourstats-prod.db-shm 2>/dev/null; \
    rm -rf /data/backups; \
    echo '=== Volume contents ===' && ls -lh /data/"
```

**Verify output**: Should show `hourstats-prod.db` with the expected size (~500 MB).

If `fly machine run` does not support `--volume` with this syntax, alternative approach:

```bash
# Deploy a minimal container first, then SSH in to rename
fly deploy -c fly.prod.toml --ha=false  # (with DRY_RUN=true — see 2.1)
fly ssh console -a hourstats-prod -C "mv /data/hourstats-staging.db /data/hourstats-prod.db"
fly machine restart -a hourstats-prod   # pick up renamed file
```

### 1.4 Set secrets

Copy the production Bluesky credentials and API keys to the new app:

```bash
fly secrets set -a hourstats-prod \
  BLUESKY_HANDLE="<prod-handle>" \
  BLUESKY_PASSWORD="<prod-app-password>" \
  GOOGLE_AI_API_KEY="<gemini-key>" \
  AWS_ACCESS_KEY_ID="<aws-key-id>" \
  AWS_SECRET_ACCESS_KEY="<aws-secret>"
```

Use the same values currently on `hourstats-staging` (from `fly secrets list -a hourstats-staging`). The secrets list shows digests only — you'll need the original values.

---

## Phase 2: Deploy prod in DRY_RUN mode (~5 min)

### 2.1 Temporarily enable DRY_RUN

Edit `fly.prod.toml`:

```toml
[env]
  DRY_RUN = "true"   # ← temporary, for verification only
```

### 2.2 Deploy

```bash
fly deploy -c fly.prod.toml --ha=false
```

### 2.3 Verify prod is running correctly

```bash
# Check logs — should see firehose ingestion, NO posting
fly logs -a hourstats-prod

# Expected log lines:
#   "hourstats starting" with profile=prod, dry_run=true
#   "database opened" with path=/data/hourstats-prod.db
#   "jetstream connected"
#   "DRY_RUN: would post summary" (at next analysis cycle)
```

```bash
# Check stats API
fly proxy 9111:9111 -a hourstats-prod &
curl http://localhost:9111/stats/latest
curl http://localhost:9111/stats/health
kill %1  # stop proxy
```

**Checkpoint**: Prod is ingesting the firehose and serving stats but NOT posting to Bluesky. If anything is wrong, destroy `hourstats-prod` and start over — the original staging bot is still running.

---

## Phase 3: Cutover (~2 min downtime)

This is the critical section. The bot will be offline for approximately 30 seconds between stopping staging and prod going live.

### 3.1 Stop the staging bot

```bash
fly machine stop 4d8929e0f59618 -a hourstats-staging
```

Verify it's stopped:

```bash
fly status -a hourstats-staging
# Machine should show "stopped"
```

### 3.2 Switch prod to live posting

Edit `fly.prod.toml`:

```toml
[env]
  DRY_RUN = "false"   # ← live posting enabled
```

Deploy:

```bash
fly deploy -c fly.prod.toml --ha=false
```

### 3.3 Verify prod is posting

```bash
fly logs -a hourstats-prod

# Expected: NO "DRY_RUN:" prefix on posting logs
# Wait for next analysis cycle (up to 30 min) or check existing ingestion
```

```bash
fly proxy 9111:9111 -a hourstats-prod &
go run ./cmd/hourstats-stats summary
kill %1
```

**Checkpoint**: Production is live on `hourstats-prod`. The old staging app is stopped but not destroyed yet (safety net).

---

## Phase 4: Rebuild staging (~5 min)

### 4.1 Destroy old staging volume

The old volume has production data that would confuse the staging bot. Replace it with a fresh volume.

```bash
# Get the volume ID
fly volumes list -a hourstats-staging

# Destroy the old volume (contains prod data)
fly volumes destroy <OLD_VOLUME_ID> -a hourstats-staging

# Create a fresh empty volume
fly volumes create data --app hourstats-staging --region sjc --size 10
```

### 4.2 Set staging Bluesky credentials

```bash
fly secrets set -a hourstats-staging \
  BLUESKY_HANDLE="hourstats-staging.bsky.social" \
  BLUESKY_PASSWORD="<staging-app-password>"
```

The other secrets (GOOGLE_AI_API_KEY, AWS keys) can remain unchanged — staging can share the same Gemini and S3 credentials.

### 4.3 Deploy staging

No toml changes needed — `fly.staging.toml` already has `HOURSTATS_PROFILE = "staging"` and `DRY_RUN = "false"`.

```bash
fly deploy -c fly.staging.toml --ha=false
```

### 4.4 Verify staging

```bash
fly logs -a hourstats-staging

# Expected:
#   profile=staging
#   "database opened" path=/data/hourstats-staging.db  (fresh, empty)
#   Jetstream connected, ingesting posts
#   First analysis cycle after 30 min
```

Staging will start with an empty database. Sparklines require ~1 hour of data (2+ data points). Yearly charts require daily_sentiment accumulation. This is expected.

---

## Phase 5: Cleanup & verification (~5 min)

### 5.1 Confirm both apps are healthy

```bash
fly status -a hourstats-prod
fly status -a hourstats-staging
```

### 5.2 Verify Makefile alignment

```bash
make fly-status
# Should show both hourstats-prod and hourstats-staging
```

```bash
# Test deploy targets point to correct apps (dry run — just check config)
grep '^app' fly.prod.toml fly.staging.toml
# fly.prod.toml:    app = "hourstats-prod"
# fly.staging.toml: app = "hourstats-staging"
```

### 5.3 Verify S3 backups (next day)

After the daily backup cycle runs:

```bash
aws s3 ls s3://hourstats-sqlite-backups/prod/ --region us-west-2
# Should show a new backup file
```

### 5.4 Update beads

```bash
bd close hourstats-bsky-tq0.18   # Production cutover complete
```

### 5.5 Commit fly.prod.toml

The only file that changed is `fly.prod.toml` (DRY_RUN back to "false"). Commit and push.

---

## Rollback

If anything goes wrong after Phase 3:

### Quick rollback (< 5 min)

```bash
# Stop prod
fly machine stop -a hourstats-prod --select

# Restart the old staging bot
fly machine start 4d8929e0f59618 -a hourstats-staging

# Verify staging is posting again
fly logs -a hourstats-staging
```

The old staging machine still has its original volume. As long as it hasn't been destroyed, rollback is instant.

### When to destroy the old staging volume

Only destroy the old staging volume (Phase 4.1) after:
- [ ] `hourstats-prod` has been running for at least 1 hour
- [ ] At least one successful analysis cycle has completed
- [ ] At least one successful Bluesky post has been made
- [ ] Stats API returns valid data on prod

---

## Estimated Timeline

| Phase | Duration | Bot downtime |
|-------|----------|-------------|
| Phase 1: Create prod | ~5 min | None |
| Phase 2: Deploy DRY_RUN | ~5 min | None |
| Phase 3: Cutover | ~2 min | **~30 seconds** |
| Phase 4: Rebuild staging | ~5 min | None |
| Phase 5: Cleanup | ~5 min | None |
| **Total** | **~22 min** | **~30 seconds** |

## Post-Migration Checklist

- [ ] `hourstats-prod` posting to `@hourstats.bsky.social`
- [ ] `hourstats-staging` posting to `@hourstats-staging.bsky.social`
- [ ] `make deploy-prod` deploys to `hourstats-prod`
- [ ] `make deploy-staging` deploys to `hourstats-staging`
- [ ] S3 backups writing to `prod/` prefix
- [ ] Stats API accessible on both apps via `fly proxy`
- [ ] Health dashboard working (`hourstats-stats health`)
- [ ] Old staging volume destroyed (after stability confirmation)
- [ ] `fly.prod.toml` committed with `DRY_RUN = "false"`
- [ ] Bead tq0.18 closed
