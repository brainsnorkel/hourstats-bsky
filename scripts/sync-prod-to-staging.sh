#!/bin/bash

# Sync production data to staging environment
# Creates a volume snapshot of prod and restores it to staging,
# giving staging a copy of the live production database.
#
# Usage: make sync-staging
#    or: scripts/sync-prod-to-staging.sh

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

PROD_APP="hourstats-prod"
STAGING_APP="hourstats-staging"
REGION="sjc"
VOLUME_NAME="data"
VOLUME_SIZE="10"

info()  { echo -e "${BLUE}▸${NC} $*"; }
ok()    { echo -e "${GREEN}✓${NC} $*"; }
warn()  { echo -e "${YELLOW}⚠${NC} $*"; }
fail()  { echo -e "${RED}✗${NC} $*"; exit 1; }

# ─── Preflight checks ──────────────────────────────────────────────

info "Checking prerequisites..."

command -v fly >/dev/null 2>&1 || fail "fly CLI not found"
fly auth whoami >/dev/null 2>&1 || fail "Not authenticated with Fly.io — run 'fly auth login'"

# Get prod volume ID
PROD_VOL=$(fly volumes list -a "$PROD_APP" --json 2>/dev/null | python3 -c "
import json, sys
vols = json.load(sys.stdin)
for v in vols:
    if v['name'] == 'data' and v['state'] == 'created':
        print(v['id'])
        break
" 2>/dev/null) || true

[ -z "$PROD_VOL" ] && fail "No production volume found on $PROD_APP"
ok "Prod volume: $PROD_VOL"

# Get staging machine ID
STAGING_MACHINE=$(fly machines list -a "$STAGING_APP" --json 2>/dev/null | python3 -c "
import json, sys
machines = json.load(sys.stdin)
for m in machines:
    if m.get('config', {}).get('metadata', {}).get('fly_process_group') == 'app':
        print(m['id'])
        break
" 2>/dev/null) || true

STAGING_STATE=""
if [ -n "$STAGING_MACHINE" ]; then
    STAGING_STATE=$(fly machines list -a "$STAGING_APP" --json 2>/dev/null | python3 -c "
import json, sys
machines = json.load(sys.stdin)
for m in machines:
    if m['id'] == '$STAGING_MACHINE':
        print(m['state'])
        break
" 2>/dev/null) || true
    ok "Staging machine: $STAGING_MACHINE ($STAGING_STATE)"
else
    info "No staging machine found — will be created on deploy"
fi

# Get staging volume ID (if any)
STAGING_VOL=$(fly volumes list -a "$STAGING_APP" --json 2>/dev/null | python3 -c "
import json, sys
vols = json.load(sys.stdin)
for v in vols:
    if v['name'] == 'data':
        print(v['id'])
        break
" 2>/dev/null) || true

if [ -n "$STAGING_VOL" ]; then
    ok "Staging volume: $STAGING_VOL (will be replaced)"
else
    info "No staging volume found — will create new"
fi

# ─── Confirmation ───────────────────────────────────────────────────

echo ""
echo -e "${YELLOW}This will:${NC}"
echo "  1. Snapshot the prod volume ($PROD_VOL)"
echo "  2. Stop the staging machine (if running)"
echo "  3. Destroy the staging volume and create a new one from the snapshot"
echo "  4. Rename hourstats-prod.db → hourstats-staging.db"
echo "  5. Clean prod-specific state (yearly_post_uri, daily_quote_last_date)"
echo "  6. Restart staging"
echo ""
echo -e "${YELLOW}Staging will have a copy of prod data but post to @hourstats-staging.bsky.social${NC}"
echo ""
read -p "Continue? [y/N] " -n 1 -r
echo
[[ $REPLY =~ ^[Yy]$ ]] || { info "Aborted."; exit 0; }

# ─── Step 1: Snapshot prod volume ───────────────────────────────────

echo ""
info "Creating snapshot of prod volume..."
fly volumes snapshots create "$PROD_VOL" -a "$PROD_APP" 2>/dev/null

info "Waiting for snapshot to appear..."
SNAPSHOT_ID=""
for i in $(seq 1 10); do
    sleep 5
    SNAPSHOT_ID=$(fly volumes snapshots list "$PROD_VOL" -a "$PROD_APP" --json 2>/dev/null | python3 -c "
import json, sys
for s in json.load(sys.stdin):
    if s.get('status') == 'running':
        print(s['id'])
        break
" 2>/dev/null) || true
    [ -n "$SNAPSHOT_ID" ] && break
done

if [ -z "$SNAPSHOT_ID" ]; then
    SNAPSHOT_ID=$(fly volumes snapshots list "$PROD_VOL" -a "$PROD_APP" --json 2>/dev/null | python3 -c "
import json, sys
for s in json.load(sys.stdin):
    if s.get('status') == 'created':
        print(s['id'])
        break
" 2>/dev/null) || true
fi

[ -z "$SNAPSHOT_ID" ] && fail "Could not find snapshot after creation"
info "Snapshot: $SNAPSHOT_ID — waiting for completion..."

for i in $(seq 1 40); do
    STATUS=$(fly volumes snapshots list "$PROD_VOL" -a "$PROD_APP" --json 2>/dev/null | python3 -c "
import json, sys
for s in json.load(sys.stdin):
    if s['id'] == '$SNAPSHOT_ID':
        print(s.get('status', ''))
        break
" 2>/dev/null) || true
    [ "$STATUS" = "created" ] && { ok "Snapshot ready: $SNAPSHOT_ID"; break; }
    echo -n "."
    sleep 10
done

[ "$STATUS" != "created" ] && fail "Snapshot did not complete within 400 seconds"

# ─── Step 2: Stop staging machine ──────────────────────────────────

if [ -n "$STAGING_MACHINE" ] && [ "$STAGING_STATE" = "started" ]; then
    echo ""
    info "Stopping staging machine..."
    fly machine stop "$STAGING_MACHINE" -a "$STAGING_APP" 2>/dev/null
    ok "Staging stopped"
fi

# ─── Step 3: Replace staging volume ────────────────────────────────

echo ""
if [ -n "$STAGING_VOL" ]; then
    if [ -n "$STAGING_MACHINE" ]; then
        info "Destroying staging machine (to release volume)..."
        fly machine destroy "$STAGING_MACHINE" -a "$STAGING_APP" --force 2>/dev/null
        ok "Machine destroyed"
    fi

    info "Destroying old staging volume..."
    fly volumes destroy "$STAGING_VOL" -a "$STAGING_APP" -y 2>/dev/null
    ok "Old volume destroyed"
fi

info "Creating staging volume from prod snapshot..."
NEW_VOL=$(fly volumes create "$VOLUME_NAME" \
    --app "$STAGING_APP" \
    --region "$REGION" \
    --size "$VOLUME_SIZE" \
    --snapshot-id "$SNAPSHOT_ID" \
    --json -y 2>/dev/null | python3 -c "
import json, sys
vol = json.load(sys.stdin)
print(vol['id'])
" 2>/dev/null) || true

[ -z "$NEW_VOL" ] && fail "Failed to create volume from snapshot"
ok "New staging volume: $NEW_VOL"

# ─── Step 4: Deploy staging (creates machine + mounts volume) ──────

echo ""
info "Deploying staging..."
fly deploy -c fly.staging.toml --ha=false 2>/dev/null
ok "Staging deployed"

# Get the new machine ID
STAGING_MACHINE=$(fly machines list -a "$STAGING_APP" --json 2>/dev/null | python3 -c "
import json, sys
machines = json.load(sys.stdin)
for m in machines:
    if m.get('config', {}).get('metadata', {}).get('fly_process_group') == 'app':
        print(m['id'])
        break
" 2>/dev/null) || true

[ -z "$STAGING_MACHINE" ] && fail "Could not find staging machine after deploy"
ok "Staging machine: $STAGING_MACHINE"

# ─── Step 5: Rename DB and clean prod state ─────────────────────────

echo ""
info "Waiting for machine to be ready..."
sleep 10

info "Renaming database and cleaning prod-specific state..."
fly ssh console -a "$STAGING_APP" -C "/bin/sh -c '
    # The volume has hourstats-prod.db from the snapshot.
    # The bot auto-created a fresh hourstats-staging.db on startup.
    # Replace the fresh one with the prod data.
    if [ -f /data/hourstats-prod.db ]; then
        rm -f /data/hourstats-staging.db /data/hourstats-staging.db-wal /data/hourstats-staging.db-shm
        mv /data/hourstats-prod.db /data/hourstats-staging.db
        mv /data/hourstats-prod.db-wal /data/hourstats-staging.db-wal 2>/dev/null
        mv /data/hourstats-prod.db-shm /data/hourstats-staging.db-shm 2>/dev/null
        echo \"Renamed hourstats-prod.db -> hourstats-staging.db\"
    else
        echo \"No hourstats-prod.db found — volume may already be staging format\"
    fi

    # Clean prod-specific key-value entries that would confuse the staging bot.
    # yearly_post_uri points to a prod thread — staging cannot reply to it.
    # daily_quote_last_date prevents daily quotes until the date rolls over.
    sqlite3 /data/hourstats-staging.db \"
        DELETE FROM key_value WHERE key IN (
            \\\"yearly_post_uri\\\",
            \\\"daily_quote_last_date\\\",
            \\\"daily_quote_post_uri\\\"
        );
    \" 2>/dev/null && echo \"Cleaned prod-specific KV entries\" || echo \"No KV cleanup needed\"

    # Remove old backups from snapshot
    rm -rf /data/backups /data/seed 2>/dev/null

    echo \"=== Volume contents ===\"
    ls -lh /data/
'" 2>/dev/null

ok "Database renamed and cleaned"

# ─── Step 6: Restart staging to pick up renamed DB ──────────────────

echo ""
info "Restarting staging to load renamed database..."
fly machine restart "$STAGING_MACHINE" -a "$STAGING_APP" 2>/dev/null
ok "Staging restarted"

sleep 5
info "Verifying startup..."
fly logs -a "$STAGING_APP" --no-tail 2>/dev/null | grep -E "hourstats starting|database opened|connected to jetstream" | tail -3

# ─── Done ───────────────────────────────────────────────────────────

echo ""
echo -e "${GREEN}═══════════════════════════════════════════════${NC}"
echo -e "${GREEN} Staging synced with production data${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════${NC}"
echo ""
echo "  App:      $STAGING_APP"
echo "  Machine:  $STAGING_MACHINE"
echo "  Volume:   $NEW_VOL (from snapshot $SNAPSHOT_ID)"
echo "  DB:       /data/hourstats-staging.db (prod data, prod KV cleaned)"
echo "  Posts to: @hourstats-staging.bsky.social"
echo ""
echo -e "  ${YELLOW}Note: Staging is running. Stop with:${NC}"
echo "    fly machine stop $STAGING_MACHINE -a $STAGING_APP"
echo ""
