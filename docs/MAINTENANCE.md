# Maintenance guide

What an operator has to look after every now and then to keep [@hourstats.bsky.social](https://bsky.app/profile/hourstats.bsky.social) running: the credentials and external services it depends on, the software it is built from, and the data it accumulates. Architecture is in [docs/architecture/README.md](architecture/README.md); backup and restore procedure is in [BACKUP_RECOVERY.md](../BACKUP_RECOVERY.md).

The bot is one Go binary on one Fly.io machine per app (`hourstats-prod`, `hourstats-staging`), with a persistent volume for SQLite. Everything below is about the things around that binary.

## 1. Credentials and where they live

All secrets are Fly secrets. None are in the repository and none should ever be. `fly secrets list -a hourstats-prod` shows names and digests only; setting a secret restarts the machine.

| Secret | Used for | Where to get or rotate it | Rotate when |
|--------|----------|---------------------------|-------------|
| `BLUESKY_HANDLE`, `BLUESKY_PASSWORD` | `com.atproto.server.createSession` on bsky.social before every posting job. The password must be an **app password**, not the account password. | bsky.app → Settings → Privacy and security → App passwords. Revoke the old one after the new one is deployed. | Yearly, and immediately if it is ever exposed. **A copy was exposed in a committed `terraform/tfplan` from January to September 2026 while the GitHub repo was public; rotate it if that has not been done since.** |
| `GOOGLE_AI_API_KEY` | Gemini `generateContent` for topic grouping, alt text, exemplar validation. Google News RSS needs no key. | Google AI Studio → API keys. The key is tied to a Google Cloud project; billing and quota live there. | Yearly, or on exposure. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | Daily `PutObject` of the SQLite backup to `s3://hourstats-sqlite-backups` (us-west-2). | IAM user with `s3:PutObject` on the bucket. Create the new key, deploy it, then delete the old key. | Yearly, or on exposure. |
| Fly.io account and tokens | `fly deploy`, `fly secrets`, `fly ssh`, `fly proxy`, billing. | Account `chris@flatmapit.com`. `fly auth login` for interactive use; `fly tokens create deploy -a hourstats-prod` for any automation. Platform metrics need a `FlyV1` Authorization header. | Tokens: when a machine or CI runner that held one is retired. |

Rotation procedure for any of the first three:

```sh
fly secrets set BLUESKY_PASSWORD='xxxx-xxxx-xxxx-xxxx' -a hourstats-staging   # staging first
fly logs -a hourstats-staging                                                 # wait for "hourstats starting", then a cycle that authenticates
fly secrets set BLUESKY_PASSWORD='xxxx-xxxx-xxxx-xxxx' -a hourstats-prod
```

Staging is normally stopped; `fly machine start` it for the test, and stop it again afterwards so it does not post. `DRY_RUN=true` in `fly.staging.toml` keeps it from posting while still exercising authentication and the Gemini key.

## 2. External services and what can change under you

| Service | Endpoint(s) | What to watch | Where in code |
|---------|-------------|---------------|---------------|
| Bluesky Jetstream | `wss://jetstream2.us-west.bsky.network/subscribe`, with `jetstream1.us-west`, `jetstream2.us-east`, `jetstream1.us-east` as fallbacks | These are community-run public instances; hosts have changed before. `stall_detected` and `consumer_restart` events climbing in `/stats/events`, or repeated endpoint rotation in logs, mean the list needs updating. | `internal/jetstream/consumer.go` (`AllEndpoints`) |
| Bluesky public AppView | `https://public.api.bsky.app` (`app.bsky.feed.getPosts`) | Unauthenticated, cached, no published rate budget. If a cycle logs `hydration_timeout` or runs are marked `low_confidence`, hydration is being throttled; `HYDRATION_HOST=pds` moves it back to the authenticated PDS at the cost of the posting rate budget. | `internal/hydrator`, `HYDRATION_HOST`, `HYDRATION_MAX_UNHYDRATED_PCT` |
| Bluesky PDS | `https://bsky.social` | Rate limit is per IP (3,000 requests per 5 minutes). Posting is a handful of calls per cycle so this only matters if hydration is moved onto it. Indigo (`github.com/bluesky-social/indigo`) tracks lexicon changes; bump it when Bluesky changes a record schema. | `internal/client` |
| Gemini API | `https://generativelanguage.googleapis.com/v1beta/models/<model>:generateContent` | **Model names expire.** Defaults are `gemini-2.5-pro` and `gemini-2.5-flash`; when Google retires them, set `GEMINI_MODEL` and `GROUP_FALLBACK_MODEL` in `fly.prod.toml`. The bot caps itself at 150 calls per rolling 24 hours (`gemini_budget_exhausted` event when hit) and degrades to offline clustering, so a dead key or model does not stop posting, it just makes the trending topics worse. Check billing monthly. | `internal/topics/grouper.go` |
| Google News RSS | `https://news.google.com/rss?hl=en&gl={US,GB,AU}` | Best effort, 3 s timeout, silently skipped. Nothing to maintain unless the feed format changes and grouping quality drops. | `internal/topics/headlines.go` |
| AWS S3 | `s3://hourstats-sqlite-backups/prod/<timestamp>.db` | **No lifecycle rule is set from code.** Objects accumulate one per day (tens of MB each). Add a lifecycle rule in the bucket, or prune by hand. Confirm a recent object exists after any credential change. | `internal/store/backup.go` |
| Wikipedia | `https://en.wikipedia.org/wiki/Portal:Current_events/YYYY_Month_D` | Link targets only. If the portal's subpage naming changes, the yearly-chart facets break silently (the trending footer no longer links anywhere). | `internal/wikipedia` |

Legacy AWS: the original Lambda/DynamoDB deployment was managed with Terraform whose state lives in `s3://hourstats-terraform-state`. The Terraform sources were removed from this repo in September 2026 (they remain in git history before that commit). If any of those AWS resources still exist, decommission them from the AWS console or by restoring the `terraform/` directory from history and running `terraform destroy`; the bot does not use them.

## 3. Software dependencies

| Dependency | Pinned where | Cadence and how |
|------------|--------------|-----------------|
| Go toolchain | `go.mod` (`go 1.24`), `Dockerfile` (`golang:1.24-alpine`) | Bump both together when a new Go minor is out and the previous one is about to leave support (Go supports two minors). `make test` then `make deploy-staging`. |
| Alpine base image | `Dockerfile` (`alpine:3.21`) | Alpine minors get security support for two years; 3.21 is supported until late 2026. Bump the tag, rebuild, check `ca-certificates`/`tzdata`/`sqlite` still install. |
| Go modules | `go.mod` / `go.sum` | Quarterly: `go get -u ./... && go mod tidy && make test`. Watch these in particular: `github.com/bluesky-social/indigo` (pseudo-versioned, tracks Bluesky lexicons), `modernc.org/sqlite` (pure-Go SQLite, occasionally changes pragmas), `github.com/jonreiter/govader` (the sentiment lexicon; an update changes the numbers the bot posts), `github.com/aws/aws-sdk-go-v2/*` (S3 only). |
| Vulnerability scan | none | Monthly: `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`. As of September 2026 it reports GO-2026-5764 (a panic in the AWS event-stream decoder, reachable only through the S3 client); fixed by `go get github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream@v1.7.8`. |
| Diagram tooling | `docs/architecture/render.sh`, `docs/architecture/excalidraw/build.py` | Only when regenerating diagrams: `npm install -g @mermaid-js/mermaid-cli`; Python 3 with no extra packages. |
| Fly CLI | your machine | `brew upgrade flyctl` before a deploy if it has been a while; `fly.toml` keys occasionally deprecate. |

## 4. Data and storage

| Concern | Detail | What to do |
|---------|--------|------------|
| Volume growth | `runs`, `sentiment_history` and `daily_sentiment` are never purged (their purge functions have no caller). Each hourly cycle adds one row to the first two. `stats_snapshots`/`stats_events` are purged at 90 days; everything else is short-lived. | Quarterly: `fly ssh console -a hourstats-prod -C "df -h /data"` and `GET /stats/health` via `fly proxy 9111:9111 -a hourstats-prod`. Extend the volume with `fly volumes extend` well before it fills; SQLite fails ugly on a full disk. |
| WAL file | Checkpointed every 5 minutes; escalates to TRUNCATE above `WAL_CHECKPOINT_THRESHOLD_MB` (50). | `wal_pressure_checkpoint` events appearing every cycle mean readers are holding the WAL open; usually a long stats query. |
| VACUUM | Sundays in the daily job, only if the freelist exceeds `VACUUM_FREELIST_PCT` (20%). | Nothing, unless the log says it keeps skipping while the file keeps growing. |
| Local backups | `/data/backups/hourstats-prod-<ts>.db`, pruned after `BACKUP_RETAIN_DAYS` (7 in prod). Only the nine "essential" tables are copied; `post_buffer`, `topic_tokens`, `cursor` are regenerable. | Nothing routine. They share the volume, so they count toward the growth above. |
| S3 backups | One object per day, no lifecycle. | Monthly: confirm yesterday's object exists; add or check the lifecycle rule. Twice a year: do the restore drill in `BACKUP_RECOVERY.md` against staging (`make sync-staging` is the fast path for a prod snapshot). |
| Staging data | Staging has its own volume and profile; `make sync-staging` copies prod data into it. | Before testing a schema migration on staging, sync first so the migration runs against real data. |

## 5. Platform

- **Apps**: `hourstats-prod` (running), `hourstats-staging` (kept stopped; start it only for a test, stop it after). Both `shared-cpu-1x`, 1 GB RAM, 512 MB swap, region `sjc`, `--ha=false` so there is exactly one machine and one volume each.
- **Memory** is the resource that has caused outages: `GOMEMLIMIT=800MiB`, `GOGC=75`, and the analysis cycle and the daily/yearly jobs are serialised so two chart renders never overlap. `cycle_memory_peak` events in `/stats/events` record the RSS peak of every cycle; if peaks trend toward the limit, that is the signal to resize before it OOMs.
- **Schedule** is wall-clock aligned in UTC and survives deploys: cycle at :55 (prod) or :25 (staging), daily job at 00:00, yearly chart and monthly report at 01:00. A deploy during a cycle is safe; the shutdown sequence drains buffered posts and persists the cursor inside Fly's 15 s `kill_timeout`.
- **Billing**: Fly invoices monthly; the Gemini project and the AWS account are the other two bills. None should exceed a few dollars a month; a jump usually means a runaway retry loop or a lifecycle rule missing on S3.

## 6. Recurring checklist

**Weekly (five minutes)**
- Look at the account: is there a summary every hour, a sparkline and trending reply under it, the daily quote under the pinned yearly chart?
- `fly status -a hourstats-prod`, then `fly proxy 9111:9111 -a hourstats-prod` and `curl localhost:9111/stats/events?hours=168`. Zero `stall_detected`, `consumer_restart`, `cycle_overlap_skipped`, `hydration_timeout`, `gemini_budget_exhausted` is normal; a handful is fine; a daily pattern is not.

**Monthly**
- Gemini usage and bill; AWS bill and S3 object count; Fly invoice.
- Yesterday's S3 backup object exists.
- `govulncheck ./...`.

**Quarterly**
- Volume free space and `/stats/health` database size.
- `go get -u ./... && go mod tidy && make test`, deploy to staging, watch two cycles, deploy to prod.
- Base image tags in the `Dockerfile`.
- Check Google's model deprecation notices against `GEMINI_MODEL` / `GROUP_FALLBACK_MODEL`.
- Check the Jetstream endpoint list is still current.

**Yearly**
- Rotate the Bluesky app password, the Gemini key, and the AWS access key.
- Go minor and Alpine minor bumps.
- Restore drill from an S3 backup into staging.
- Re-read this file and the architecture diagrams against the code; delete what no longer applies.

## 7. Secrets hygiene

- The repository is public. Treat every file as published, including binary artefacts. A committed Terraform plan (`terraform/tfplan`, a zip containing full state) exposed the production Bluesky app password from January to September 2026; the file has been removed from the tree but is still in git history until history is rewritten.
- `.gitignore` now excludes `.env*`, `config.yaml`, `secrets.yaml`, `*.tfstate*`, tool state (`.omc/`, `.claude/`, `.beads/` runtime files) and MCP audit logs. Do not add negations for JSON without a reason.
- Before pushing, `git diff --cached --stat` and a glance at anything that is not source: zips, plans, database files and logs do not belong in the repo.
- Consider a pre-commit secret scanner (`gitleaks` or `trufflehog`) so the next binary artefact is caught by content, not by filename.
