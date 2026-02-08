# HourStats: Bluesky Sentiment Analysis Bot — Greenfield Specification

> This document is a complete, self-contained specification for building a Bluesky sentiment analysis bot from scratch. It assumes no existing codebase. An implementer should be able to build the entire system from this document alone.

---

## 1. Product Description

HourStats is an automated Bluesky bot that:

1. Ingests **every** public Bluesky post in real time via the Jetstream WebSocket API
2. Every 30 minutes, analyzes the sentiment of all posts from the preceding window
3. Posts a summary to Bluesky: an overall mood hashtag, a sentiment percentage, and the 5 highest-engagement posts with sentiment indicators
4. Generates a 7-day sentiment sparkline chart posted as a threaded reply
5. Aggregates daily sentiment averages for long-term storage
6. Generates a yearly (365-day) sentiment chart posted monthly

**Live reference:** [@hourstats.bsky.social](https://bsky.app/profile/hourstats.bsky.social)

### Post Format

```
Bluesky is #satisfied
+11.2% sentiment

1. @alice.bsky.social +
2. @bob.bsky.social -
3. @carol.bsky.social x
4. @dave.bsky.social +
5. @eve.bsky.social x
```

- **Mood hashtag:** One of 100 calibrated words (see §6.3)
- **Sentiment percentage:** Average VADER compound score × 100, with sign
- **Top 5 posts:** Ranked by engagement (likes + reposts + replies). Each @handle is a clickable AT Protocol facet linking to the actual post
- **Sentiment symbols:** `+` positive, `-` negative, `x` neutral
- **Maximum 300 characters** (Bluesky limit)

---

## 2. Architecture Overview

```
                                     ┌─────────────────┐
Bluesky Network                      │  Jetstream WS   │
(all posts)  ──────────────────────► │  Consumer        │ ──► Post Buffer (KV Store)
                                     │  (persistent)    │
                                     └─────────────────┘

                                     ┌─────────────────┐
Cron (every 30 min) ───────────────► │  Window Trigger  │
                                     │                  │──► reads buffer
                                     │                  │──► hydrates engagement
                                     │                  │──► dispatches processor
                                     └─────────────────┘
                                              │
                                              ▼
                                     ┌─────────────────┐
                                     │  Processor       │
                                     │                  │──► sentiment analysis
                                     │                  │──► top-5 selection
                                     │                  │──► posts to Bluesky
                                     │                  │──► stores sentiment history
                                     │                  │──► triggers sparkline
                                     └─────────────────┘
                                              │
                                              ▼
                                     ┌─────────────────┐
                                     │  Sparkline       │
                                     │  Poster          │──► generates 7-day chart
                                     │                  │──► posts as reply
                                     └─────────────────┘

Cron (daily, midnight UTC) ────────► Daily Aggregator ──► calculates daily averages
Cron (monthly, 1st at 01:00 UTC) ──► Yearly Poster ────► generates 365-day chart, pins to profile
```

### Component Summary

| Component | Type | Trigger | Purpose |
|---|---|---|---|
| **Jetstream Consumer** | Persistent process | Always running | Ingests all posts into rolling buffer |
| **Window Trigger** | Scheduled function | Every 30 minutes | Closes window, hydrates engagement, dispatches processor |
| **Processor** | On-demand function | Invoked by Window Trigger | Sentiment analysis, post formatting, Bluesky posting |
| **Sparkline Poster** | On-demand function | Invoked by Processor | 7-day trend chart generation and posting |
| **Daily Aggregator** | Scheduled function | Daily at midnight UTC | Calculates daily sentiment averages |
| **Yearly Poster** | Scheduled function | Monthly on 1st at 01:00 UTC | 365-day chart generation and profile pinning |

---

## 3. Technology Decisions

### 3.1 Infrastructure — Decide Before Starting

This spec is **infrastructure-agnostic** by design. The implementer should choose one of these stacks based on cost and familiarity:

#### Option A: Cloudflare Stack (Cheapest)

| Component | Service | Estimated Cost |
|---|---|---|
| Jetstream Consumer | Cloudflare Worker (WebSocket) or Durable Object | Free tier covers ~100K requests/day; Durable Objects $0.15/million requests |
| Post Buffer | Cloudflare KV or D1 (SQLite) | KV: first 100K reads/day free, $0.50/million after. D1: 5M rows read free/day |
| Scheduled functions | Cloudflare Workers (Cron Triggers) | Free tier: 100K requests/day |
| Sentiment history | D1 (SQLite at edge) | Free tier: 5GB storage |
| Image generation | Worker or external service | Chart generation may need a headless solution — evaluate `@cf/image` or pre-render |
| Secrets | Cloudflare Secrets | Free |

**Cloudflare caveats:**
- Workers have a 30-second CPU time limit (128MB Workers Unbound). Sentiment analysis of 50K+ posts may need batching or a queue.
- Durable Objects can maintain WebSocket state but have cold-start implications.
- Image generation (1200×800 PNG for sparklines) is non-trivial in Workers. Consider generating charts via a lightweight service or Canvas API alternative.
- **Estimated total: $0–5/month** for typical usage.

#### Option B: AWS Serverless Stack

| Component | Service | Estimated Cost |
|---|---|---|
| Jetstream Consumer | ECS Fargate Spot (0.25 vCPU, 0.5GB) | ~$3–7/month |
| Post Buffer | DynamoDB on-demand | ~$5–6/day for 50K+ writes per 30min window |
| Scheduled functions | Lambda + EventBridge | Pennies/month |
| Sentiment history | DynamoDB | ~$1/month |
| Image storage | S3 | ~$0.01/month |
| Secrets | SSM Parameter Store | Free |

**AWS caveats:**
- DynamoDB write volume for the buffer is the main cost driver (~$150–180/month at full volume). Mitigate by using `BatchWriteItem` (25 items/call) in the consumer.
- Lambda 15-minute timeout is sufficient for engagement hydration.
- **Estimated total: $20–50/month** depending on write optimization.

#### Option C: Fly.io / Railway / VPS

| Component | Service | Estimated Cost |
|---|---|---|
| Jetstream Consumer | Long-running process on Fly.io Machine | $1.94/month (shared-cpu-1x, 256MB) |
| Post Buffer | SQLite on volume, or Redis | $0 (bundled) or $0–3/month |
| Scheduled functions | Cron in same process or separate machine | $0–2/month |
| Image generation | Native in process (Go/Node image library) | $0 |
| Secrets | Fly Secrets / Railway env vars | Free |

**VPS/PaaS caveats:**
- Simplest architecture: potentially a single process doing everything.
- No managed auto-scaling — but the workload is steady, not spiky.
- **Estimated total: $2–7/month.**

### 3.2 Language

Go is recommended (the Bluesky ecosystem has strong Go library support via [bluesky-social/indigo](https://github.com/bluesky-social/indigo)), but any language with:
- WebSocket client support
- VADER sentiment analysis library (Go: [govader](https://github.com/jonreiter/govader), Python: [vaderSentiment](https://github.com/cjhutto/vaderSentiment), JS: [vader-sentiment](https://www.npmjs.com/package/vader-sentiment))
- 2D image rendering (Go: [fogleman/gg](https://github.com/fogleman/gg), Python: matplotlib/Pillow, JS: canvas)
- AT Protocol client for posting (Go: indigo, Python: [atproto](https://pypi.org/project/atproto/), JS: [@atproto/api](https://www.npmjs.com/package/@atproto/api))

### 3.3 Authentication

The bot needs a Bluesky account with an App Password for posting summaries and charts. Store the handle and app password as secrets in your chosen platform. Authentication uses `com.atproto.server.createSession`.

---

## 4. Data Models

### 4.1 Post (buffer and run storage)

```
Post {
  uri:              string    // AT Protocol URI: at://did:plc:xxx/app.bsky.feed.post/yyy
  cid:              string    // Content identifier
  text:             string    // Post text content
  author:           string    // Handle (e.g., alice.bsky.social) — resolved at hydration time
  authorDid:        string    // DID (e.g., did:plc:xxx) — from Jetstream
  likes:            int       // Hydrated from getPosts API
  reposts:          int       // Hydrated from getPosts API
  replies:          int       // Hydrated from getPosts API
  sentiment:        string    // "positive" | "negative" | "neutral" — set by analyzer
  engagementScore:  float64   // likes + reposts + replies
  createdAt:        datetime  // Post creation timestamp (from Jetstream record)
}
```

### 4.2 Run State (per analysis cycle)

```
RunState {
  runId:                    string    // Unique: "run-<unix-nano>"
  status:                   string    // "initializing" | "fetching" | "analyzed" | "completed"
  analysisIntervalMinutes:  int       // 30 (default)
  cutoffTime:               datetime  // now - 30 minutes (UTC)
  totalPostsRetrieved:      int
  overallSentiment:         string    // "positive" | "negative" | "neutral"
  netSentimentPercentage:   float64   // Average compound score × 100
  topPosts:                 []Post    // The 5 highest-engagement posts
  topPostURI:               string    // AT-URI of the posted summary (for sparkline reply threading)
  topPostCID:               string    // CID of the posted summary
  createdAt:                datetime
  updatedAt:                datetime
  ttl:                      int64     // Auto-expire after 48 hours
}
```

### 4.3 Sentiment Data Point (per run, for sparklines)

```
SentimentDataPoint {
  runId:                  string
  timestamp:              datetime
  averageCompoundScore:   float64   // VADER compound: -1.0 to +1.0
  netSentimentPercent:    float64   // Compound × 100: -100 to +100
  sentimentCategory:      string    // "positive" | "negative" | "neutral"
  totalPosts:             int
}
```

### 4.4 Daily Sentiment (aggregated, for yearly charts)

```
DailySentiment {
  date:               string    // "YYYY-MM-DD"
  averageSentiment:   float64   // Mean of all runs' netSentimentPercent for the day
  minSentiment:       float64
  maxSentiment:       float64
  totalRuns:          int       // Number of 30-min runs in the day (up to 48)
  totalPosts:         int       // Sum of all posts across runs
  ttl:                int64     // Auto-expire after 3 years
}
```

### 4.5 Jetstream Cursor

```
JetstreamCursor {
  cursor:     int64     // Jetstream sequence number
  updatedAt:  datetime  // Last persisted time
}
```

---

## 5. Component Specifications

### 5.1 Jetstream Consumer

**Type:** Persistent, always-running process.

#### 5.1.1 Connection
- Connect to `wss://jetstream2.us-east.bsky.network/subscribe?wantedCollections=app.bsky.feed.post`
- On startup, read stored cursor from datastore. If present, append `&cursor=<value>` to the URL to resume from last position.
- Jetstream replays missed events (backfill window is typically several hours), so brief downtime does not cause data loss.

#### 5.1.2 Event Processing
- Accept only events where: `kind == "commit"` AND `commit.operation == "create"` AND `commit.collection == "app.bsky.feed.post"`
- Discard all other events (likes, follows, deletes, updates, etc.)
- Extract from each event:
  - `uri`: Construct as `at://<did>/app.bsky.feed.post/<commit.rkey>`
  - `cid`: `commit.cid`
  - `text`: `commit.record.text`
  - `authorDid`: `did` (top-level field)
  - `createdAt`: `commit.record.createdAt`
- Write to post buffer with a key that encodes the minute bucket (for efficient range queries by the window trigger)
- Set buffer item TTL = 2 hours

#### 5.1.3 Cursor Persistence
- Persist the latest Jetstream sequence number every 10 seconds
- On shutdown (SIGTERM), persist immediately before exiting

#### 5.1.4 Reconnection
- On WebSocket close/error: reconnect with exponential backoff (1s, 2s, 4s, 8s, max 30s)
- Always resume from last persisted cursor

#### 5.1.5 Observability
- Log: connection events, reconnections, posts/second, errors
- Metrics (if available on platform): `PostsIngested/min`, `ConnectionStatus`, `WriteErrors/min`

---

### 5.2 Window Trigger

**Type:** Scheduled function, runs every 30 minutes.

#### 5.2.1 Run Creation
1. Generate run ID: `run-<unix-nano-timestamp>`
2. Calculate cutoff time: `now() - 30 minutes` (UTC)
3. Store RunState with status `"initializing"`

#### 5.2.2 Buffer Read
1. Query post buffer for all items with `createdAt` in range `[cutoffTime, now)`
2. Handle pagination (if datastore requires it)
3. If fewer than 250 posts found: **log a warning** and post an informational message to Bluesky about data availability, then exit. (The consumer may be down.)

#### 5.2.3 Engagement Hydration
Jetstream delivers posts at creation time with 0 likes, 0 reposts, 0 replies. You must hydrate engagement before ranking.

1. Call `app.bsky.feed.getPosts` in batches of up to 25 AT-URIs per request
2. The response includes full `PostView` objects with:
   - Current `likeCount`, `repostCount`, `replyCount`
   - `author.handle` (resolves DID → handle for free)
   - Moderation `labels` (for adult content filtering)
3. Update each post's `likes`, `reposts`, `replies`, and `author` from the response
4. Filter out posts with adult content labels: `["porn", "sexual", "nudity", "graphic-media"]`
5. Posts that fail hydration (deleted, private, API error) retain zero engagement and DID as author — they won't appear in top-5

**Rate limiting:** The Bluesky API allows 3,000 requests per 5 minutes. For 50,000 posts at 25/batch = 2,000 requests. Run up to 10 concurrent requests with a rate limiter. This completes in ~2 minutes.

**Time budget:** If hydration is still running at 12 minutes, stop and proceed with what's been hydrated so far.

#### 5.2.4 Dispatch
1. Store all hydrated posts under the run ID in batches (up to 100 posts per batch item)
2. Update RunState: `totalPostsRetrieved`, `status = "fetching"`, timestamps
3. Invoke the Processor with `{ runId: "<run-id>" }`

---

### 5.3 Processor

**Type:** On-demand function, invoked by Window Trigger.

#### 5.3.1 Post Retrieval
1. Read RunState to get run metadata
2. Read all post batches for the run ID
3. Deduplicate by URI (keep higher engagement version)
4. Filter by cutoff time (discard posts outside window)
5. **Minimum threshold:** If fewer than 1,000 posts after filtering, post an informational "search latency" message instead of a sentiment summary, then exit

#### 5.3.2 Sentiment Analysis
For each post:
1. Calculate VADER compound score on the post text
2. Categorize: `>= 0.3` → positive, `<= -0.3` → negative, else neutral
3. If VADER returns neutral, apply keyword fallback: count predefined positive vs negative words. If one side has more, reclassify accordingly.
4. Calculate engagement score: `likes + reposts + replies`

Overall sentiment:
1. Average all compound scores across all posts
2. Clamp each to `[-1.0, +1.0]` before averaging
3. Categorize the average: `>= 0.3` → positive, `<= -0.3` → negative, else neutral
4. Net sentiment percentage = average compound × 100

#### 5.3.3 Top-5 Selection
1. Sort all posts by engagement score descending
2. Select the top 5

#### 5.3.4 Summary Posting
1. Select mood word from 100-word vocabulary based on net sentiment percentage (see §6.3)
2. Format post:
   ```
   Bluesky is #<mood-word>
   <+/->XX.X% sentiment

   1. @<author1> <symbol>
   2. @<author2> <symbol>
   3. @<author3> <symbol>
   4. @<author4> <symbol>
   5. @<author5> <symbol>
   ```
3. Create AT Protocol facets:
   - Hashtag facet for the mood word
   - Link facets for each @handle → web URL of their post (`https://bsky.app/profile/<did>/post/<rkey>`)
4. Create an embed card (quote-post) referencing the #1 post's URI and CID
5. Ensure total character count ≤ 300
6. Post via `com.atproto.repo.createRecord` with collection `app.bsky.feed.post`
7. Store the posted URI and CID in RunState (needed for sparkline reply threading)

#### 5.3.5 Sentiment History Storage
1. Store a `SentimentDataPoint` record for this run
2. This feeds the sparkline generator and daily aggregator

#### 5.3.6 Sparkline Trigger
1. Invoke the Sparkline Poster with `{ runId: "<run-id>" }`

#### 5.3.7 Dry Run Mode
If the `dry_run` configuration flag is `true`, skip steps 5.3.4 through 5.3.6 (no posting, no sparkline). Still perform analysis and log results.

---

### 5.4 Sparkline Poster

**Type:** On-demand function, invoked by Processor.

#### 5.4.1 Data Retrieval
1. Query sentiment history for the last 7 days (168 hours)
2. If fewer than 2 data points: post an informational message ("building history") instead of a chart

#### 5.4.2 Chart Generation
Generate a **1200×800 pixel PNG** with:
- **Line chart** of net sentiment percentage over 7 days
- **Color-coded segments:** Positive (> +10%) in one color, negative (< -10%) in another, neutral in a third
- **Colour-blind accessible palette:** Do NOT rely solely on red/green. Use blue/orange or Okabe-Ito validated colors. Provide redundant visual cues (watermark labels, distinct patterns)
- **Gaussian smoothed trend line** (sigma = 4.0), overlaid as a dashed line
- **Watermarks:** "Positive", "Neutral", "Negative" in their respective zones
- **Branding:** Bot handle in bottom-left corner
- **Y-axis:** Auto-scaled to data range with 10% padding
- **X-axis:** Time labels for each day boundary

#### 5.4.3 Alt-Text Generation
Calculate and include in alt-text metadata:
- Current sentiment value
- 7-day high, low, and average
- Timestamps of high and low points

#### 5.4.4 Posting as Reply
1. Read the parent post's URI and CID from the RunState
2. Upload chart PNG as blob via `com.atproto.repo.uploadBlob`
3. Post with image embed as a reply to the summary post (set `reply.root` and `reply.parent`)

---

### 5.5 Daily Aggregator

**Type:** Scheduled function, runs daily at midnight UTC.

#### 5.5.1 Aggregation
1. Target date = previous calendar day
2. Check if a daily record already exists for this date — if so, skip (idempotent)
3. Query all `SentimentDataPoint` records for the target date
4. Calculate: average sentiment %, min, max, total runs, total posts
5. Store as `DailySentiment` record with TTL = 3 years

---

### 5.6 Yearly Poster

**Type:** Scheduled function, runs monthly on the 1st at 01:00 UTC.

#### 5.6.1 Chart Generation
1. Query all `DailySentiment` records for the last 365 days
2. Generate a **1500×1000 pixel PNG** with:
   - Line chart of daily average sentiment
   - Monthly boundary markers with labels ("Jan", "Feb", etc.)
   - Bi-weekly (14-day) tick marks
   - Colour-blind accessible palette (same requirements as sparkline)
   - Title with date range in YYYY-MM-DD format (32pt font)
   - High and low day annotations

#### 5.6.2 Event Contextualization
1. Identify the highest and lowest sentiment days
2. Generate Wikipedia "Events" links for those dates: `https://en.wikipedia.org/wiki/<Month>_<Day>`
3. Include as clickable AT Protocol facets in the post text

#### 5.6.3 Posting and Pinning
1. Post the chart with descriptive text and Wikipedia link facets
2. Pin the post to the bot's profile by updating `app.bsky.actor.profile` with a `pinnedPost` reference

---

## 6. Reference Data

### 6.1 Bluesky API Endpoints Used

| Endpoint | Purpose | Auth Required |
|---|---|---|
| `app.bsky.feed.getPosts` | Hydrate engagement metrics (batch, up to 25 URIs) | Yes |
| `com.atproto.repo.createRecord` | Post summaries, sparklines, yearly charts | Yes |
| `com.atproto.repo.uploadBlob` | Upload chart images | Yes |
| `com.atproto.repo.putRecord` | Update profile (pin post) | Yes |
| `com.atproto.server.createSession` | Authenticate | N/A |
| `com.atproto.identity.resolveHandle` | Resolve handle → DID (fallback only; getPosts returns handles) | No |

### 6.2 Jetstream

- **Public endpoints (no auth):**
  - `wss://jetstream1.us-east.bsky.network/subscribe`
  - `wss://jetstream2.us-east.bsky.network/subscribe`
  - `wss://jetstream1.us-west.bsky.network/subscribe`
  - `wss://jetstream2.us-west.bsky.network/subscribe`
- **Filter parameter:** `?wantedCollections=app.bsky.feed.post`
- **Resume parameter:** `?cursor=<sequence-number>`
- **Event format:** JSON (not CBOR — Jetstream decodes the firehose for you)
- **Volume:** ~1,500–3,000 events/minute for posts only (as of early 2026)
- **Documentation:** https://docs.bsky.app/blog/jetstream and https://github.com/bluesky-social/jetstream

### 6.3 100-Word Mood Vocabulary

The mood word is selected from a 100-word vocabulary organized into 7 tiers calibrated to observed Bluesky sentiment distribution:

| Tier | Sentiment Range | Words | Vibe |
|---|---|---|---|
| 1 — Extreme Negative | < 0% | 5 words | angry, hostile, grim, miserable, dreadful |
| 2 — Unusually Low | 0% to < 9.5% | 15 words | anxious → despondent |
| 3 — Below Average | 9.5% to < 10.5% | 15 words | flat → solemn |
| 4 — Typical | 10.5% to < 12.5% | 30 words | calm → settled (sub-groups: calm/centered, curious/thoughtful, expressive/social, engaged/balanced) |
| 5 — Above Average | 12.5% to < 14% | 15 words | happy → bright |
| 6 — Unusually High | 14% to < 18% | 15 words | excited → buzzing |
| 7 — Extreme Positive | ≥ 18% | 5 words | euphoric, ecstatic, elated, jubilant, celebratory |

**Selection algorithm:** Determine tier from net sentiment percentage → linearly interpolate within the tier's word range to select the specific word.

### 6.4 VADER Sentiment Thresholds

| Compound Score | Category |
|---|---|
| ≥ 0.3 | positive |
| ≤ -0.3 | negative |
| Between | neutral |

The keyword fallback activates only when VADER returns neutral: compare counts of predefined positive vs negative keywords in the text.

### 6.5 Adult Content Labels (filter out)

`["porn", "sexual", "nudity", "graphic-media"]`

---

## 7. Operational Controls

### 7.1 Dry Run
A configuration flag (`dry_run: true/false`) that, when true, runs the full pipeline but skips all Bluesky posting (summaries, sparklines, yearly charts). Useful for testing.

### 7.2 Minimum Post Thresholds
- **250 posts** in the buffer: minimum to proceed with a run (below this, assume consumer is down)
- **1,000 posts** after deduplication/filtering: minimum to post a sentiment summary (below this, post an informational "insufficient data" message instead)

### 7.3 Data Retention
| Data | TTL |
|---|---|
| Post buffer items | 2 hours |
| Run state + post batches | 48 hours |
| Jetstream cursor | 24 hours |
| Sentiment data points (per-run) | 8 days (covers 7-day sparkline + buffer) |
| Daily sentiment | 3 years |

### 7.4 Error Handling
- Consumer reconnects automatically; no human intervention needed for transient failures
- Window trigger logs and exits on unrecoverable errors; next scheduled run retries
- If the bot's Bluesky authentication fails, all posting functions skip and log the error
- Never crash the consumer process on a single malformed event — log and skip

---

## 8. Implementation Phases

### Phase 1: Consumer + Buffer (get data flowing)
1. Implement Jetstream WebSocket client with reconnection
2. Implement buffer writes to your chosen datastore
3. Implement cursor persistence
4. Deploy and verify: posts appear in buffer, cursor advances

### Phase 2: Window Trigger + Processor (produce output)
1. Implement scheduled function that reads buffer and creates runs
2. Implement engagement hydration via `getPosts`
3. Implement VADER sentiment analysis
4. Implement post formatting with mood word selection and facet creation
5. Implement Bluesky posting
6. Deploy and verify: bot posts sentiment summaries every 30 minutes

### Phase 3: Sparkline Charts (visual trends)
1. Implement sentiment history storage
2. Implement 7-day chart generation (1200×800 PNG)
3. Implement reply threading (sparkline posts as reply to summary)
4. Deploy and verify: sparkline appears as reply

### Phase 4: Daily + Yearly (long-term trends)
1. Implement daily aggregation function
2. Implement yearly chart generation (1500×1000 PNG)
3. Implement profile pinning
4. Deploy and verify: daily records accumulate, yearly chart posts monthly

### Phase 5: Polish
1. Add dry-run mode
2. Add informational messages for insufficient data
3. Add alt-text to all chart images
4. Verify colour-blind accessibility
5. Add monitoring/alerting for consumer health

---

## 9. Testing Strategy

| Level | What to Test |
|---|---|
| **Unit** | Sentiment scoring (VADER + keyword fallback), mood word selection (all 7 tiers), post formatting (character count, facet positions), engagement score calculation |
| **Integration** | Consumer → buffer write → buffer read round-trip. Window trigger → processor invocation chain. Bluesky API authentication and posting (use a test account). |
| **End-to-end** | Run consumer for 35 minutes → trigger window → verify Bluesky post appears with correct format. Verify sparkline reply is threaded correctly. |
| **Accessibility** | Simulate protanopia/deuteranopia on chart images. Verify alt-text contains all required values. |

---

## 10. Appendix: AT Protocol URI Formats

```
AT Protocol URI:  at://did:plc:abc123/app.bsky.feed.post/xyz789
Web URL:          https://bsky.app/profile/did:plc:abc123/post/xyz789
```

To convert AT-URI to web URL: strip `at://`, replace `/app.bsky.feed.post/` with `/post/`, prepend `https://bsky.app/profile/`.
