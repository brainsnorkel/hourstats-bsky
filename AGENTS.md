# Agent Instructions

## Project at a Glance

**HourStats** is a Bluesky sentiment analysis bot. Single Go binary on Fly.io. Ingests the full
English-language firehose via Jetstream, runs VADER sentiment every 30 minutes, and posts summaries
with sparklines, trending topics, and yearly charts.

```
Live bot:  @hourstats.bsky.social
Language:  Go 1.24 (CGO_ENABLED=0)
Database:  SQLite (WAL mode, pure Go driver)
Deploy:    Fly.io (Docker, Alpine 3.21)
Backups:   AWS S3 (daily)
```

> See [ARCHITECTURE.md](ARCHITECTURE.md) for full technical details.
> See [CLAUDE.md](CLAUDE.md) for coding conventions and package reference.

---

## System Architecture

```
                    ┌─────────────────────────────────────────────┐
                    │           Single Go Binary (Fly.io)         │
                    │                cmd/hourstats                 │
                    │                                             │
 Bluesky Network    │  ┌──────────────┐    ┌──────────────────┐  │
 (all public posts) │  │  Jetstream   │    │  Wall-Clock      │  │
        │           │  │  Consumer    │    │  Schedulers      │  │
        │ WebSocket │  │  (goroutine) │    │                  │  │
        ▼           │  │              │    │  :00/:30  30-min │  │
   ┌─────────┐      │  │  filter:     │    │  00:00   daily   │  │
   │Jetstream│──────┼─▶│  lang=en     │    │  01:00   yearly  │  │
   │ Server  │      │  │  creates only│    │  :05     stall   │  │
   └─────────┘      │  └──────┬───────┘    │  :05     WAL chk │  │
                    │         │            └────────┬─────────┘  │
                    │         ▼ InsertPost()        │            │
                    │  ┌──────────────┐             │            │
                    │  │   SQLite DB  │◄────────────┘            │
                    │  │  /data/hs-   │  read posts,             │
                    │  │  {profile}.db│  write results            │
                    │  └──────┬───────┘                          │
                    │         │                                  │
                    │         ▼                                  │
                    │  ┌──────────────┐    ┌──────────────────┐  │
                    │  │ Bluesky API  │    │ Gemini Pro API   │  │
                    │  │ (hydration,  │    │ (topic grouping) │  │
                    │  │  posting)    │    │                  │  │
                    │  └──────────────┘    └──────────────────┘  │
                    └─────────────────────────────────────────────┘
                                      │
                                      ▼
                              ┌──────────────┐
                              │   AWS S3     │
                              │ (daily       │
                              │  backups)    │
                              └──────────────┘
```

---

## 30-Minute Analysis Pipeline

Every 30 minutes at `:00` and `:30` UTC, this pipeline runs end-to-end:

```
 ┌─────────┐   ┌───────────┐   ┌─────────┐   ┌──────────┐   ┌──────────────┐
 │  Read   │   │  Hydrate  │   │  VADER  │   │  Select  │   │   Post to    │
 │  posts  │──▶│engagement │──▶│sentiment│──▶│  top 5   │──▶│   Bluesky    │
 │from     │   │(25/batch, │   │analysis │   │by engage-│   │              │
 │SQLite   │   │10 concurr)│   │         │   │ment      │   │  Summary     │
 │(30-min  │   └───────────┘   └─────────┘   └──────────┘   │  + mood tag  │
 │ window) │                                                  └──────┬───────┘
 └─────────┘                                                         │
                          ┌──────────────────────────────────────────┤
                          │                                          │
                          ▼                                          ▼
                 ┌─────────────────┐                      ┌──────────────────┐
                 │  Sparkline      │                      │  Trendline       │
                 │  (7-day chart)  │                      │  (root vs reply) │
                 │  reply to       │                      │  reply to        │
                 │  summary        │                      │  summary         │
                 └────────┬────────┘                      └──────────────────┘
                          │
                          ▼
                 ┌─────────────────┐
                 │ Trending Topics │
                 │ TF-IDF (2h win) │
                 │ → Gemini group  │
                 │ → rank + track  │
                 │ reply to        │
                 │ sparkline       │
                 └─────────────────┘
```

**Bluesky post threading:**

```
  Sentiment Summary (root post)
   └── Sparkline chart (reply)
        ├── Trendline chart (reply)
        └── Trending topics (reply)
```

---

## Daily & Yearly Pipelines

```
 DAILY (midnight UTC)                    YEARLY (1st of month, 01:00 UTC)
 ────────────────────                    ────────────────────────────────
 ┌──────────────┐                        ┌──────────────────┐
 │ SQLite       │                        │ Generate 365-day │
 │ backup → S3  │                        │ sentiment chart   │
 └──────┬───────┘                        └────────┬─────────┘
        │                                         │
        ▼                                         ▼
 ┌──────────────┐                        ┌──────────────────┐
 │ Aggregate    │                        │ Post chart to    │
 │ daily        │                        │ Bluesky          │
 │ sentiment    │                        └────────┬─────────┘
 └──────┬───────┘                                 │
        │                                         ▼
        ▼                                ┌──────────────────┐
 ┌──────────────┐                        │ Pin to profile   │
 │ Top-post     │                        └──────────────────┘
 │ quote reply  │
 │ to yearly    │
 │ thread       │
 └──────────────┘
```

---

## SQLite Database Layout

```
 ┌────────────────────────────────────────────────────────────────┐
 │                    SQLite (WAL mode)                           │
 │              /data/hourstats-{profile}.db                      │
 │                                                                │
 │  Connection Pools:                                             │
 │  ┌─────────────────┬───────────────────┬────────────────────┐  │
 │  │   writeDB (1)   │   readDB (4)      │   maintDB (1)      │  │
 │  │   30s timeout   │   read-only       │   1s timeout       │  │
 │  │   all writes    │   analysis reads  │   WAL checkpoints  │  │
 │  └─────────────────┴───────────────────┴────────────────────┘  │
 │                                                                │
 │  Tables:                                                       │
 │  ┌───────────────────┬──────────────────────┬───────────────┐  │
 │  │ Table             │ Purpose              │ Retention     │  │
 │  ├───────────────────┼──────────────────────┼───────────────┤  │
 │  │ post_buffer       │ Jetstream posts      │ 2 hours       │  │
 │  │ runs              │ Analysis cycle state │ 48 hours      │  │
 │  │ sentiment_history │ Per-run data points  │ 8 days        │  │
 │  │ daily_sentiment   │ Aggregated daily     │ 3 years       │  │
 │  │ daily_top_post    │ Best post per day    │ 3 years       │  │
 │  │ topic_tokens      │ Tokenized root posts │ 26 hours      │  │
 │  │ topic_snapshots   │ Topic analysis runs  │ 48 hours      │  │
 │  │ topic_identity    │ Persistent topic IDs │ 7 days        │  │
 │  │ key_value         │ Cursor, settings     │ permanent     │  │
 │  └───────────────────┴──────────────────────┴───────────────┘  │
 └────────────────────────────────────────────────────────────────┘
```

---

## Project Structure

```
hourstats-bsky/
│
├── cmd/                              # ── Executables ──────────────────
│   ├── hourstats/                    #   Main binary (Fly.io entry point)
│   ├── force-trending/               #   Tool: manual trending trigger
│   ├── graph-lab/                    #   Tool: chart experimentation
│   ├── import-dynamodb/              #   Tool: DynamoDB → SQLite seed
│   ├── hourstats-stats/              #   Tool: stats CLI
│   └── lambda-*/                     #   [Legacy] AWS Lambda handlers
│
├── internal/                         # ── Core Packages ────────────────
│   ├── jetstream/                    #   WebSocket consumer + cursor mgmt
│   ├── store/                        #   SQLite layer (schema, queries, backup)
│   ├── analyzer/                     #   VADER sentiment (govader)
│   ├── hydrator/                     #   Engagement hydration (batch API)
│   ├── topics/                       #   TF-IDF, Gemini grouping, tracking
│   ├── client/                       #   Bluesky API (posting, uploads, facets)
│   ├── formatter/                    #   Post formatting (grapheme limits)
│   ├── sparkline/                    #   Chart generation (all chart types)
│   ├── stats/                        #   Runtime statistics collector
│   ├── statsapi/                     #   HTTP stats API (port 9111)
│   ├── config/                       #   Configuration types
│   ├── state/                        #   Sentiment data point types
│   └── {legacy}/                     #   awsutil, backup, lambda, scheduler
│
├── openspec/                         # ── Architecture Specs ───────────
│   ├── specs/                        #   Main specs (post-fetching, etc.)
│   └── changes/                      #   Change proposals
│
├── terraform/                        #   [Legacy] AWS infrastructure
├── docs/                             #   Feature documentation
├── scripts/                          #   Operational scripts
│
├── fly.prod.toml                     #   Fly.io production config
├── fly.staging.toml                  #   Fly.io staging config
├── Dockerfile                        #   Multi-stage build
├── Makefile                          #   Build, test, deploy targets
├── .beads/                           #   Issue tracking (bd)
└── .golangci.yml                     #   Linter configuration
```

---

## Issue Tracking (Beads)

This project uses **[Beads](https://github.com/steveyegge/beads)** (`bd`) for issue tracking.
Issues live in `.beads/issues.jsonl` and sync via git.

### Issue Lifecycle

```
 ┌──────────┐    bd create     ┌─────────────┐    bd update    ┌──────────────┐
 │          │  "description"   │             │  --status       │              │
 │  (none)  │─────────────────▶│    open     │  in_progress   │ in_progress  │
 │          │                  │             │────────────────▶│              │
 └──────────┘                  └──────┬──────┘                 └──────┬───────┘
                                      │                               │
                                      │ bd close                      │ bd close
                                      ▼                               ▼
                               ┌─────────────┐                ┌──────────────┐
                               │   closed    │                │    closed    │
                               └─────────────┘                └──────────────┘
```

### Quick Reference

```bash
bd ready                              # Find available work
bd show <id>                          # View issue details
bd create "Title"                     # Create new issue
bd update <id> --status in_progress   # Claim work
bd close <id>                         # Complete work
bd list                               # View all issues
bd sync                               # Sync with git
```

---

## Development Workflow

### Build, Test, Deploy

```
 ┌───────────────────────────────────────────────────────────────────────┐
 │                     Development Commands                              │
 ├───────────────────────────────────────────────────────────────────────┤
 │                                                                       │
 │  BUILD            TEST             LINT             DEPLOY            │
 │  ─────            ────             ────             ──────            │
 │  make build-      make test        make fmt         make deploy-prod  │
 │  hourstats        (go test ./...)  make lint        make deploy-      │
 │                                    (golangci-lint)  staging           │
 │                                                                       │
 │  LOCAL RUN                         OPERATIONS                         │
 │  ─────────                         ──────────                         │
 │  go run ./cmd/hourstats            make fly-status                    │
 │  (set env vars first)              make fly-logs-prod                 │
 │                                    make fly-logs-staging              │
 │                                    make sync-staging                  │
 └───────────────────────────────────────────────────────────────────────┘
```

### Required Environment Variables

```bash
# Minimum for local run (with DRY_RUN)
export BLUESKY_HANDLE="your-handle.bsky.social"
export BLUESKY_PASSWORD="your-app-password"
export DATA_DIR="./data"
export DRY_RUN=true

# For trending topics
export TRENDING_ENABLED=true
export GOOGLE_AI_API_KEY="your-key"
```

---

## Coding Conventions

| Convention | Rule |
|------------|------|
| **Logging** | `log/slog` with structured key-value pairs. NO `log.Printf`. |
| **Errors** | Wrap with context: `fmt.Errorf("failed to X: %w", err)` |
| **API calls** | Always use `context.WithTimeout` |
| **Post limits** | 300 **graphemes** (use `[]rune`, not `len(string)`) |
| **Facets** | Byte offsets (not rune) for `ByteStart`/`ByteEnd` |
| **Testing** | stdlib `testing` only. Interfaces for testability. |
| **AT URIs** | `at://did:plc:xxx/app.bsky.feed.post/yyy` |

---

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until
`git push` succeeds.

```
 ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
 │ 1. File      │     │ 2. Quality   │     │ 3. Update    │
 │ issues for   │────▶│ gates        │────▶│ issue status  │
 │ remaining    │     │ (test, lint, │     │ (close done,  │
 │ work         │     │  build)      │     │  update WIP)  │
 └──────────────┘     └──────────────┘     └──────┬───────┘
                                                   │
                                                   ▼
 ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
 │ 6. Verify    │     │ 5. Clean up  │     │ 4. PUSH TO   │
 │ all changes  │◀────│ stashes,     │◀────│ REMOTE       │
 │ committed &  │     │ prune        │     │ (MANDATORY)  │
 │ pushed       │     │ branches     │     │              │
 └──────┬───────┘     └──────────────┘     └──────────────┘
        │                                  git pull --rebase
        ▼                                  bd sync
 ┌──────────────┐                          git push
 │ 7. Hand off  │                          git status ✓
 │ context for  │
 │ next session │
 └──────────────┘
```

**Push commands (copy-paste):**

```bash
git pull --rebase
bd sync
git push
git status  # MUST show "up to date with origin"
```

**Critical rules:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing — that leaves work stranded locally
- NEVER say "ready to push when you are" — YOU must push
- If push fails, resolve and retry until it succeeds

---

## Architecture Diagrams

Use **mermaid-ascii** ([github.com/AlexanderGrooff/mermaid-ascii](https://github.com/AlexanderGrooff/mermaid-ascii))
to render architecture diagrams as ASCII art in design docs, proposals, and code comments.

```bash
pip install mermaid-ascii               # Install
mermaid-ascii < diagram.mmd             # Render from file
echo 'graph LR; A-->B;' | mermaid-ascii # Render inline
```

**When to use:**
- Proposals and design docs (`openspec/`)
- Code comments where a visual helps (data flow, state machines)
- PR descriptions explaining architectural changes

**Convention:** Keep Mermaid source in a fenced `mermaid` block, followed by rendered ASCII in
a fenced `text` block.

---

## Related Documentation

| Document | Purpose |
|----------|---------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Full system architecture, design decisions, environment config |
| [CLAUDE.md](CLAUDE.md) | Coding conventions, package reference, tech stack |
| [README.md](README.md) | Project overview, getting started, features |
| [TESTING.md](TESTING.md) | Test infrastructure and patterns |
| [PRODUCTION_DEPLOYMENT.md](PRODUCTION_DEPLOYMENT.md) | [Legacy] AWS deployment guide |
| [BACKUP_RECOVERY.md](BACKUP_RECOVERY.md) | Backup strategy and disaster recovery |
| [docs/TRENDING_TOPICS.md](docs/TRENDING_TOPICS.md) | Trending topics technical walkthrough |
| [docs/WRITE_BOTTLENECK_FIX.md](docs/WRITE_BOTTLENECK_FIX.md) | Write path design and scaling |
