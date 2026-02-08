# HourStats Jetstream: Cost Analysis

> Analysis date: February 2026. All prices are us-east-1 (AWS) or nearest equivalent. Prices sourced from official pricing pages.

---

## 1. Traffic Assumptions

| Metric | Value | Source |
|---|---|---|
| Bluesky registered users | 41.41M | [2025 Transparency Report](https://bsky.social/about/blog/01-29-2026-transparency-report-2025) |
| Daily active users | ~3.5M | [Backlinko, Nov 2025](https://backlinko.com/bluesky-statistics) |
| Posts in 2025 | 1.41 billion | 2025 Transparency Report |
| Average posts/day (2025) | 3,860,000 | 1.41B ÷ 365 |
| **Estimated posts/day (mid-2026)** | **5,000,000** | Conservative growth projection (60% YoY reported) |
| Posts per 30-min window | 104,167 | 5M ÷ 48 |
| Posts per minute | 3,472 | 5M ÷ 1,440 |
| Posts per second | ~58 | 5M ÷ 86,400 |
| Posts per month | 152,200,000 | 5M × 30.44 |
| YoY growth | ~60% | Transparency report: 25.94M → 41.41M users |

### Engagement Hydration Load

Each 30-minute window requires hydrating ~104,167 posts via `app.bsky.feed.getPosts` (25 URIs per batch):

| Metric | Value |
|---|---|
| API calls per window | 4,167 |
| API calls per day | 200,000 |
| API calls per month | 6,088,000 |
| Time at 10 concurrent req/s | ~7 minutes |
| Bluesky rate limit | 3,000 req / 5 min (600/min) |
| Fits within Lambda 15-min timeout | ✅ Yes |

---

## 2. Option A: AWS Stack

Architecture: ECS Fargate (consumer) + DynamoDB (buffer/state) + Lambda (analysis/aggregation)

### Component Breakdown

#### 2.1 Jetstream Consumer — ECS Fargate (24/7)

| Resource | Config | Unit Price | Monthly Cost |
|---|---|---|---|
| vCPU | 0.25 vCPU | $0.000011244/vCPU-second | $7.39 |
| Memory | 0.5 GB | $0.000001235/GB-second | $1.62 |
| **On-Demand Total** | | | **$9.02** |
| **Fargate Spot (~70% off)** | | | **$2.71** |

Fargate Spot is suitable here — the consumer reconnects automatically on interruption, and Jetstream supports cursor-based replay to recover missed events.

#### 2.2 DynamoDB: Post Buffer — ⚠ DOMINANT COST

This is where the Jetstream architecture creates a fundamentally different cost profile from the search API approach. The consumer writes **every post** to the buffer — 152 million writes per month.

**On-Demand Mode (naive):**

| Operation | Volume | Unit Price | Monthly Cost |
|---|---|---|---|
| Writes (WRU) | 152,200,000 | $1.25/million | $190.25 |
| Reads (RRU, eventually consistent) | 76,100,000 | $0.25/million | $19.02 |
| Storage | ~0.2 GB | $0.25/GB | $0.05 |
| **Total** | | | **$209.32** |

DynamoDB charges per item in `BatchWriteItem`, not per API call. Batching reduces network overhead but not WRU cost.

**Provisioned Mode (optimized):**

The write pattern is steady (~58 writes/sec average, ~116 peak), making provisioned capacity ideal:

| Resource | Provisioned | Unit Price | Monthly Cost |
|---|---|---|---|
| Write capacity | 60 WCU | $0.00065/WCU-hour | $28.49 |
| Read capacity | 10 RCU (burst for window queries) | $0.00013/RCU-hour | $0.95 |
| **Total** | | | **$29.44** |

Savings vs on-demand: **$179.88/month (86%)**

With auto-scaling configured (min 60, max 150 WCU), the buffer table handles peak loads while keeping base costs low.

**Reserved Capacity (1-year commitment):**

~35% additional discount on provisioned: **$19.14/month**

#### 2.3 DynamoDB: State Tables

| Table | Writes/month | Reads/month | Cost |
|---|---|---|---|
| Run state (init/update/complete) | 4,383 | negligible | < $0.01 |
| Post batches (100 posts/batch) | 1,522,000 | 1,522,000 | $1.91 + $0.38 |
| Sentiment history | 1,461 | 490,936 | < $0.01 |
| Daily sentiment | 30 | 365 | < $0.01 |
| **Total** | | | **$2.03** |

#### 2.4 Lambda Functions

All Lambda usage falls within the free tier (400,000 GB-s + 1M requests/month):

| Function | Invocations/mo | Duration | Memory | GB-seconds |
|---|---|---|---|---|
| Window Trigger | 1,461 | 540s (9 min) | 128 MB | 100,993 |
| Processor | 1,461 | 30s | 128 MB | 5,611 |
| Sparkline Poster | 1,461 | 15s | 256 MB | 5,611 |
| Daily Aggregator | 30 | 10s | 256 MB | 78 |
| Yearly Poster | 1 | 60s | 512 MB | 31 |
| **Total** | **4,415** | | | **112,323** |

Free tier: 400,000 GB-s → **$0.00/month**

#### 2.5 Supporting Services

| Service | Usage | Monthly Cost |
|---|---|---|
| EventBridge | ~1,492 rule invocations | < $0.01 |
| CloudWatch Logs | ~0.06 GB ingestion | $0.03 |
| Public IPv4 (if needed) | 1 address, 24/7 | $3.65 |
| SSM Parameter Store | 2 standard parameters | Free |
| **Total** | | **$3.68** |

#### 2.6 AWS Cost Summary

| Configuration | Monthly | Annual |
|---|---|---|
| Naive (On-Demand everything) | **$224.06** | $2,689 |
| Optimized (Fargate Spot + Provisioned DDB) | **$37.86** | $454 |
| Best (Spot + Reserved DDB, 1-year commit) | **$27.56** | $331 |

**Cost driver:** DynamoDB buffer writes = 85% of naive cost. Provisioned capacity is essential.

---

## 3. Option B: Cloudflare Stack

Architecture: Durable Object (consumer) + D1/SQLite (buffer) + Workers/Cron (analysis)

Base plan: **$5.00/month** (Workers Paid)

### Component Breakdown

#### 3.1 Jetstream Consumer — Durable Object

The consumer runs as a Durable Object that acts as a WebSocket **client** to Jetstream.

| Resource | Volume | Included | Overage Rate | Monthly Cost |
|---|---|---|---|---|
| Requests (WS messages) | 152,200,000 | 1,000,000 | $0.15/million | $22.68 |
| Duration (GB-s) | 336,642 | 400,000 | $12.50/million | $0.00 |
| **Total** | | | | **$22.68** |

At 58 messages/second, the Durable Object never goes idle — the Hibernation WebSocket API provides no cost benefit. Duration stays within the included 400K GB-s because the DO uses minimal memory (128 MB).

#### 3.2 Post Buffer — D1 (SQLite at Edge)

| Resource | Volume | Included | Overage Rate | Monthly Cost |
|---|---|---|---|---|
| Rows written | 152,200,000 | 50,000,000 | $1.00/million | $102.20 |
| Rows read | 152,200,000 | 25,000,000,000 | $0.75/million | $0.00 |
| Storage | ~100 MB | 5 GB | $0.75/GB | $0.00 |
| **Total** | | | | **$102.20** |

#### 3.3 Scheduled Workers

All invocations and CPU time fall within included allowances:

| Resource | Volume | Included | Cost |
|---|---|---|---|
| Invocations | 1,492 | 10,000,000 | $0.00 |
| CPU milliseconds | 14,920,000 | 30,000,000 | $0.00 |

#### 3.4 Cloudflare Blockers & Caveats

| Issue | Severity | Workaround |
|---|---|---|
| **Subrequest limit: 1,000/invocation** | 🔴 Blocker | Hydration needs 4,167 subrequests per window. Must chain ~5 Worker invocations via Service Bindings or Queue. |
| **Image generation** | 🟡 Significant | Workers lack native 2D graphics libraries (no `fogleman/gg` equivalent). Must use Canvas API polyfill, `@cf/image`, or external rendering service. |
| **DO as WebSocket client** | 🟡 Unusual | Durable Objects are designed as WS *servers*. Using one as a persistent client to Jetstream is an uncommon pattern with less community support. |
| **Go language support** | 🟡 Limitation | Workers use JavaScript/TypeScript (or WASM). A Go implementation requires compiling to WASM, adding complexity. |

#### 3.5 Cloudflare Cost Summary

| Component | Monthly Cost |
|---|---|
| Workers Paid plan | $5.00 |
| Durable Object requests | $22.68 |
| D1 row writes | $102.20 |
| Workers (included) | $0.00 |
| **Total** | **$129.88** |
| **Annual** | **$1,559** |

---

## 4. Option C: Fly.io Stack

Architecture: Single Go binary on a VM with SQLite on a persistent volume.

Base plan: **$5.00/month** (Hobby, includes 3 free VMs + 3 GB storage + 160 GB bandwidth)

### Component Breakdown

The entire system runs as a single Go process:
- Jetstream consumer (goroutine)
- Cron scheduler (goroutine, triggers analysis every 30 min)
- Processor, sparkline generator, aggregator (triggered internally)
- SQLite database for buffer + state + sentiment history

| Resource | Config | Included | Monthly Cost |
|---|---|---|---|
| Compute | 1× shared-cpu-1x, 256 MB | 3 VMs free | $0.00 |
| Storage | 1 GB persistent volume (SQLite) | 3 GB free | $0.00 |
| Outbound bandwidth | ~3 GB (hydration API calls) | 160 GB free | $0.00 |
| **Total** | | | **$5.00** (plan only) |

### Why SQLite Changes Everything

The dominant cost in both AWS and Cloudflare is **database writes** — 152 million per month. SQLite eliminates this entirely:

| Database | 152M writes/month cost |
|---|---|
| DynamoDB On-Demand | $190.25 |
| Cloudflare D1 | $102.20 |
| DynamoDB Provisioned | $28.49 |
| **SQLite on disk** | **$0.00** |

SQLite on a persistent volume handles the write load trivially. At 58 writes/second with sub-KB items, SQLite operates at <1% of its throughput capacity (benchmarks show 100K+ inserts/sec for small items in WAL mode).

### Fly.io Advantages

1. **Simplest architecture** — single binary, no service mesh, no IAM, no managed DB
2. **Zero database cost** — SQLite eliminates the #1 cost driver
3. **Native Go execution** — `fogleman/gg` for charts, `govader` for sentiment, `indigo` for AT Protocol all work natively
4. **No subrequest limits** — hydration calls are plain HTTP, no platform restrictions
5. **Free tier covers everything** — 256 MB is sufficient for the workload

### Fly.io Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Single point of failure | 🟡 Medium | Jetstream cursor replay recovers missed events. Add a second VM ($3.14/mo) for redundancy if needed. |
| VM restarts | 🟢 Low | SQLite on persistent volume survives restarts. Consumer reconnects with saved cursor. |
| Limited support | 🟡 Medium | Hobby plan has community support only. Upgrade to Launch ($29/mo) for priority support. |
| Fly.io reliability | 🟡 Medium | Fly.io has occasional platform incidents. Acceptable for a bot that can miss a few windows. |

---

## 5. Comparison Matrix

| | AWS Naive | AWS Optimized | Cloudflare | Fly.io |
|---|---|---|---|---|
| **Monthly cost** | $224 | $28–38 | $130 | **$5** |
| **Annual cost** | $2,689 | $331–454 | $1,559 | **$60** |
| **Complexity** | Medium | Medium | High | **Low** |
| **Image generation** | ✅ Native Go | ✅ Native Go | ⚠️ Workaround needed | ✅ Native Go |
| **Language** | Go (Lambda) | Go (Lambda) | JS/TS (or WASM) | Go |
| **Subrequest limits** | None | None | 1,000/invocation | None |
| **Auto-scaling** | ✅ Fargate + Lambda | ✅ Fargate + Lambda | ✅ Workers | ❌ Manual |
| **Operational maturity** | ✅ High | ✅ High | ✅ High | ⚠️ Medium |
| **Observability** | CloudWatch | CloudWatch | Workers Analytics | Basic metrics |
| **Redundancy** | Multi-AZ | Multi-AZ | Edge (global) | Single VM |
| **DB cost at 152M writes/mo** | $190 (OD) / $29 (Prov) | $29 | $102 | **$0** |

---

## 6. Sensitivity Analysis

### What if Bluesky grows faster?

| Posts/day | Posts/month | AWS Optimized | Cloudflare | Fly.io |
|---|---|---|---|---|
| 3.86M (2025 actual) | 117M | $30 | $97 | $5 |
| 5M (mid-2026 est.) | 152M | $38 | $130 | $5 |
| 10M (2x growth) | 304M | $60 | $257 | $5 |
| 20M (4x growth) | 608M | $105 | $510 | $5¹ |

¹ Fly.io stays at $5 until SQLite or CPU becomes the bottleneck. At 20M posts/day (231 writes/sec), SQLite is still comfortable. CPU may need upgrading to shared-cpu-2x ($6.28/mo) for hydration concurrency. Even at $12/mo total, it's an order of magnitude cheaper than alternatives.

### What if we DON'T buffer all posts?

An alternative design: sample instead of ingesting everything. E.g., keep 1-in-10 posts:

| Strategy | Writes/month | AWS OD | AWS Prov | CF D1 | Fly.io |
|---|---|---|---|---|---|
| All posts | 152M | $190 | $29 | $102 | $0 |
| 1-in-10 sample | 15.2M | $19 | $5 | $10 | $0 |
| 1-in-100 sample | 1.5M | $1.90 | $1 | $1 | $0 |

Sampling reduces costs dramatically on managed databases but introduces selection bias. The spec requires "every post" for statistical validity.

---

## 7. Current System Comparison

The existing hourstats-bsky system uses the search API (`app.bsky.feed.searchPosts`) with Lambda:

| Metric | Current (Search API) | Jetstream (AWS Opt.) | Jetstream (Fly.io) |
|---|---|---|---|
| Monthly cost | ~$5–10 | $28–38 | $5 |
| Posts analyzed/window | ~10,000 (capped) | ~104,000 (all) | ~104,000 (all) |
| Coverage | ~10% of posts | 100% | 100% |
| Always-on process | No | Yes (Fargate) | Yes (VM) |
| DynamoDB usage | Low (10K items/run) | High (152M writes/mo) | None (SQLite) |

**Key takeaway:** Moving to Jetstream with Fly.io achieves **10x better coverage** at **the same cost** as the current system. Moving to Jetstream with AWS achieves 10x better coverage at **3–8x the cost**.

---

## 8. Recommendation

### For lowest cost: **Fly.io** ($5/month)

The workload profile — a single persistent WebSocket consumer with periodic batch processing — is a textbook VPS use case. SQLite eliminates the database cost problem that makes managed cloud expensive. The entire system fits in 256 MB of RAM.

### For operational maturity: **AWS with Provisioned DynamoDB** ($28–38/month)

If you need CloudWatch alerting, multi-AZ redundancy, IAM controls, and zero server management, the AWS stack is well-understood. **Critical:** use provisioned capacity on the buffer table — on-demand DynamoDB is 6x more expensive for this write pattern.

### Not recommended: **Cloudflare** ($130/month)

Cloudflare is the worst fit for this workload:
- Most expensive of the three options
- Subrequest limits require architectural workarounds
- No native image generation
- Requires JavaScript/TypeScript (or WASM compilation from Go)
- Durable Object as WebSocket client is an uncommon pattern

---

## Appendix A: Pricing Sources

| Service | Source | Retrieved |
|---|---|---|
| AWS Fargate | [aws.amazon.com/fargate/pricing](https://aws.amazon.com/fargate/pricing/) | Feb 2026 |
| AWS DynamoDB | [aws.amazon.com/dynamodb/pricing](https://aws.amazon.com/dynamodb/pricing/) | Feb 2026 |
| AWS Lambda | [aws.amazon.com/lambda/pricing](https://aws.amazon.com/lambda/pricing/) | Feb 2026 |
| Cloudflare Workers | [developers.cloudflare.com/workers/platform/pricing](https://developers.cloudflare.com/workers/platform/pricing/) | Feb 2026 |
| Cloudflare Durable Objects | [developers.cloudflare.com/durable-objects/platform/pricing](https://developers.cloudflare.com/durable-objects/platform/pricing/) | Feb 2026 |
| Cloudflare D1 | [developers.cloudflare.com/d1/platform/pricing](https://developers.cloudflare.com/d1/platform/pricing/) | Feb 2026 |
| Fly.io | [fly.io/docs/about/pricing](https://fly.io/docs/about/pricing/) | Feb 2026 |
| Bluesky 2025 Report | [bsky.social/about/blog/01-29-2026-transparency-report-2025](https://bsky.social/about/blog/01-29-2026-transparency-report-2025) | Feb 2026 |

## Appendix B: Key Formulas

```
Fargate monthly = (vCPU × $0.000011244/s + GB × $0.000001235/s) × 2,630,016 seconds
DynamoDB On-Demand writes = writes × $1.25 / 1,000,000
DynamoDB Provisioned writes = WCU × $0.00065/hour × 730.56 hours
Lambda GB-seconds = invocations × duration_seconds × memory_GB
D1 writes = (rows - 50M included) × $1.00 / 1,000,000
DO requests = (messages - 1M included) × $0.15 / 1,000,000
```
