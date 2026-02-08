## Context

The bot runs on Fly.io as a single Go binary consuming the Bluesky Jetstream firehose. English root posts and replies are buffered in SQLite (`post_buffer`) for 2 hours, then analysed every 30 minutes for sentiment. The bot posts summaries, sparklines, trendlines, and volume charts as a reply thread. Daily and monthly cycles handle aggregation and yearly charts.

Currently the bot tells users *how Bluesky feels* (sentiment) but not *what Bluesky is talking about* (topics). There is no topic extraction, no NLP beyond VADER sentiment, and no integration with external LLM APIs. The `post_buffer` purges after 2 hours, far too short for 24-hour topic tracking.

The Jetstream OnPost callback already filters to English posts and distinguishes root posts (rec.Reply == nil) from replies. The chart generation pipeline (`internal/sparkline/`) uses fogleman/gg for 1200x800 PNG images. The Bluesky client supports standalone image posts with hashtag facets.

## Goals / Non-Goals

**Goals:**
- Extract trending topics from root posts using TF-IDF and LLM-assisted synonym grouping
- Track topic rankings over 24 hours with persistent identity across 15-minute update cycles
- Generate a bump chart showing topic position changes over time
- Post a standalone trending-topics update every 6 hours with #trending #hourstatstrend
- Gate the entire feature behind an env var so it can be disabled independently
- Keep costs under $2/month for LLM API usage

**Non-Goals:**
- Real-time topic alerts or notifications
- Topic-based filtering of the existing sentiment analysis pipeline
- Multi-language topic extraction (English only, matching existing firehose filter)
- User-configurable topic watchlists
- Historical topic archives beyond 48 hours of snapshots
- Replacing the existing 30-minute sentiment posting cycle

## Decisions

### 1. Token storage: separate table vs extending post_buffer

**Decision**: New `topic_tokens` table with 26-hour retention, separate from `post_buffer`.

The post buffer purges at 2 hours and stores full post text with engagement data. Topic analysis needs only cleaned tokens retained for 24+ hours. Extending the buffer retention to 24h would balloon disk usage (full text + metadata for all posts) and slow sentiment queries. A dedicated table stores only post URI + JSON token array + timestamp — roughly 200 bytes per post vs ~1KB in the buffer.

**Alternatives considered**: Extending `post_buffer` retention to 24h — rejected due to 5x disk increase and query performance impact on the 30-minute sentiment cycle. Computing tokens on-the-fly from post text — rejected because the buffer purges before the 24h window closes.

### 2. Topic extraction: algorithmic clustering vs LLM-assisted grouping

**Decision**: TF-IDF for candidate extraction + single Google Gemini Flash call for synonym grouping and labeling.

Short social media posts (300 chars) have weak co-occurrence signals. Algorithmic clustering (e.g., co-occurrence matrix) produces noisy, fragmented clusters because semantically related terms ("trump", "potus", "maga") rarely appear in the same short post. An LLM understands semantic relationships and produces clean, human-readable topic labels.

The key innovation is the **synonym map**: the LLM returns not just cluster labels but additional related terms not in the top 30. These synonyms are used to recount post volume — a topic using varied vocabulary ("president", "white house", "oval office") gets properly aggregated instead of appearing as 3 weak signals.

A single API call per 15-minute cycle keeps costs at ~$0.04/day with Gemini Flash.

**Alternatives considered**: Pure co-occurrence clustering — rejected for noise on short posts. Hashtag-only extraction — rejected because many posts don't use hashtags. Keyword frequency without LLM — rejected for lack of semantic grouping. Full LLM analysis of every post — rejected for cost.

### 3. LLM provider: Google Gemini Flash vs OpenAI GPT-4o-mini

**Decision**: Google Gemini 2.0 Flash via REST API.

Pricing is comparable (~$0.04/day), but Gemini has a free tier covering ~50 requests/day (half our usage). The REST API is simple (single HTTP POST, no SDK needed). Fallback to keyword-only labeling if the API is unavailable.

**Alternatives considered**: OpenAI GPT-4o-mini — similar quality for this task but no free tier. Local models — rejected for Fly.io resource constraints (256MB RAM).

### 4. Topic identity: Jaccard similarity on keyword sets

**Decision**: Match topics across ranking cycles using Jaccard similarity (threshold: 0.3) on the union of cluster keywords + LLM synonyms.

When a new ranking is computed, each cluster's keyword set is compared against recent entries in `topic_identity`. If Jaccard similarity exceeds 0.3, the cluster inherits the existing topic_id. Otherwise, a new UUID is assigned. This allows the bump chart to track "the same topic" as its constituent keywords shift over hours.

The threshold of 0.3 is intentionally low because keyword sets expand with synonyms — a topic about "AI" might have keywords ["ai", "chatgpt", "openai", "llm", "machine", "learning"] and a later cycle might produce ["ai", "claude", "anthropic", "llm", "model", "artificial"]. Jaccard of the full sets (including synonyms) will be ~0.3-0.5 for genuine matches.

**Alternatives considered**: Exact keyword match — too brittle, topics evolve. Embedding similarity — overkill and adds model dependency. Topic label string matching — LLM may rephrase labels.

### 5. Chart type: bump/rank chart

**Decision**: Bump chart with Y-axis showing rank #1 (top) to #5 (bottom), X-axis showing 24 hours UTC.

This directly answers "what's rising and falling" — the visual language of lines crossing and swapping positions is intuitive. Each topic gets a persistent colour from the Okabe-Ito palette (colour-blind safe, matching existing charts). Topics entering the top 5 animate from the bottom edge; topics leaving exit to the bottom.

**Alternatives considered**: Stacked area chart — harder to read individual trajectories. Multi-line volume chart — cluttered, doesn't show relative ranking. Heatmap — not enough data dimensions.

### 6. Posting cadence: 6-hour standalone post

**Decision**: Post as standalone (not threaded to sentiment posts) every 6 hours.

The trending topics feature is conceptually separate from sentiment analysis. Threading it to the 30-minute chain would add noise to every cycle. Standalone posts with #trending #hourstatstrend let users mute the feature via Bluesky's mute-word feature without affecting their sentiment feed.

A 6-hour cadence produces 4 posts/day — enough to track topic evolution without spamming.

### 7. Scheduling: dedicated tickers in main loop

**Decision**: Two new `time.Ticker` instances — 15 minutes for topic analysis, 6 hours for posting — added to the existing select loop in `main.go`.

This follows the established pattern (analysisTicker for 30-min sentiment, backupTicker for 24h). Both are gated by `TRENDING_ENABLED` so they're no-ops when the feature is off.

**Alternatives considered**: Piggybacking on the existing 30-min ticker — rejected because 15-min analysis granularity is needed for smooth bump chart lines. Using a goroutine with sleep — rejected for consistency with existing ticker pattern.

### 8. Exemplar posts: hydrate at post time vs store engagement on ingest

**Decision**: At 6-hour posting time, batch-fetch current engagement for topic-matching posts via `FeedGetPosts` API, pick the highest-engagement post per topic as the exemplar.

Jetstream delivers posts with zero engagement (they're brand new). The `post_buffer` hydrates engagement every 30 minutes but purges after 2 hours — far shorter than the 24-hour topic window. Storing engagement alongside tokens at ingest would capture stale near-zero values.

Instead, at posting time we already know each topic's keyword set. We query `topic_tokens` for matching URIs, sample up to 50 URIs per topic, and call `FeedGetPosts` (the same API the hydrator uses) to get their **current** engagement. A post that's 20 hours old has accumulated far more engagement than it had at minute 0, so this produces better exemplars. Cost: ~10 API calls (250 URIs / 25 per batch) per 6-hour post — negligible given the existing 500/min rate limit.

The exemplar URI and author handle are stored in `topic_snapshots` for chart alt text and movement comparison.

**Alternatives considered**: Storing engagement in `topic_tokens` at ingest — rejected because engagement is 0 at firehose time. Periodically re-hydrating topic_tokens — rejected for complexity and unnecessary API load. Using the post_buffer's engagement data — rejected because it purges at 2h, missing 22h of topic data.

## Risks / Trade-offs

**[Risk] TF-IDF produces noise on short posts** — Social media posts are short and may not produce meaningful term distributions. Mitigation: minimum document frequency threshold (term must appear in >= 10 posts); stopword list tuned for social media; LLM acts as quality filter by grouping noise into "miscellaneous" which is excluded from top 5. Manual review on staging before enabling in production.

**[Risk] LLM returns inconsistent groupings** — Different calls may group the same terms differently. Mitigation: the identity tracker uses keyword Jaccard similarity (not label matching), so even if labels vary, the same keyword cluster is recognised. Fallback: if LLM fails entirely, use top TF-IDF keyword as label.

**[Risk] Topic identity flickers** — A topic may appear as "new" one cycle and "existing" the next if keyword composition shifts rapidly. Mitigation: minimum persistence period (a topic must be absent for 2+ cycles before its identity expires); higher Jaccard threshold if flicker is observed (tunable via constant).

**[Risk] Token table grows unexpectedly** — At ~720k root posts/day, the topic_tokens table reaches ~144MB in 24h. If purge fails, it could grow. Mitigation: size monitoring in analysis cycle; force purge to 12h window if table exceeds 200MB; log warning.

**[Risk] Gemini API costs spike** — Unexpected high call volume. Mitigation: hard rate limit of 100 calls/day in the grouper; `TRENDING_ENABLED` kill switch; billing alerts in Google Cloud.

**[Trade-off] 15-minute analysis granularity vs compute cost** — More frequent analysis produces smoother charts but uses more Gemini calls. 15 minutes is a reasonable balance — 96 calls/day at negligible cost.

**[Trade-off] English-only topics** — Matching the existing firehose filter. Non-English trending topics are invisible. Acceptable for v1; multilingual support is a future enhancement.

**[Trade-off] No retroactive backfill** — If the feature is enabled mid-day, the first 24h of charts will be sparse. Acceptable — the chart fills naturally after one full day of token collection.
