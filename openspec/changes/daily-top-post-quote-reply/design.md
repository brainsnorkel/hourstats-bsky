## Context

The bot posts a pinned yearly sentiment chart on the 1st of each month. Currently the yearly chart thread has at most one reply (the volume chart). Each 30-minute analysis cycle stores the top 5 posts (with URI, CID, engagement scores) as JSON in the `runs` table, and daily aggregation computes sentiment statistics from yesterday's runs. There is no mechanism to persist the yearly post's URI/CID across daily cycles or to identify a single "post of the day".

The AT Protocol supports quoting a post via `app.bsky.embed.record` containing a `RepoStrongRef` (URI + CID). The codebase already uses this in `createEmbedCard` for the summary post. Combining `Reply` + `Embed.EmbedRecord` on a `FeedPost` creates a reply that also shows a quoted post inline.

The daily cycle runs on a 24-hour backup ticker in `main.go` (line 93-98). Daily aggregation and yearly posting are called from this ticker. The new daily quote-reply step will be added to the same ticker path.

## Goals / Non-Goals

**Goals:**
- Persist yearly chart post URI/CID so daily cycles can reply to it
- Identify the single highest-engagement post from all of yesterday's 30-minute runs
- Post a daily reply to the yearly chart thread quoting that top post
- Keep the thread flat (all replies point to yearly post as both root and parent)

**Non-Goals:**
- Changing the 30-minute posting chain (summary → sparkline → trendline → volume)
- Generating any new charts or images for the daily reply
- Retroactively posting for missed days
- Supporting multiple yearly posts in the same month (only the latest matters)

## Decisions

### 1. Storage: `key_value` table vs dedicated tables

**Decision**: Use a single generic `key_value` table for yearly post state.

The yearly post URI/CID is just two string values that persist across cycles. A dedicated `yearly_post_state` table with year/uri/cid columns adds schema complexity for a simple key-value lookup.

**Alternatives considered**: A `yearly_post_state` table would be more explicit but the generic table is reusable for future persistent config (e.g., last daily quote timestamp). The daily top post doesn't need its own table either — it's identified at posting time from the runs table and not persisted separately.

### 2. Daily top post: query at posting time vs pre-compute during aggregation

**Decision**: Query the `runs` table at posting time, not during aggregation.

The runs table already stores `top_posts` JSON with full engagement scores for each 30-minute run. At daily quote time, query all runs from yesterday, parse their top posts, and find the single highest-engagement post across all runs. This avoids schema changes to the daily_sentiment or runs tables.

**Alternatives considered**: Adding a `daily_top_post` table populated during aggregation would be cleaner for separation of concerns, but adds a migration, a new store method for writes, and coupling between aggregation and the quote feature. The runs data already exists and runs are retained for 7 days (TTL), which is sufficient.

### 3. Thread structure: flat vs linear chain

**Decision**: Flat thread — each daily reply uses the yearly post as both root and parent.

This keeps all daily top-post replies visible as direct children of the yearly chart. A linear chain (each day replies to the previous day) would push older entries deeper into the thread, making them less discoverable. Bluesky's thread view shows direct replies prominently.

### 4. Scheduling: alongside daily aggregation

**Decision**: Call the new daily quote function from the same 24-hour backup ticker, after `runDailyAggregation`, before the yearly posting check.

This ensures daily aggregation has run (so yesterday's data is processed) and avoids adding another ticker. The function is idempotent — it checks if a reply has already been posted today.

### 5. Idempotency: key_value tracking

**Decision**: Store the last successful daily quote date in the `key_value` table. Before posting, check if today's date matches the stored date. If so, skip.

This prevents duplicate posts on container restarts or re-deployments within the same day.

## Risks / Trade-offs

**[Risk] Yearly post deleted or unavailable** → The reply will fail with an AT Protocol error. Mitigation: catch the error, log a warning, and skip gracefully. Next month's yearly post will reset the stored URI/CID.

**[Risk] No runs from yesterday** → If the bot was down for a full day, there are no runs to query. Mitigation: log and skip. Don't post a reply with no content.

**[Risk] Top post is adult content or deleted** → The quote embed would show a content warning or "post not found". Mitigation: posts are already filtered for adult content during ingestion, so this is unlikely. Deleted posts are an accepted edge case.

**[Trade-off] Flat thread may get long** → After 30+ days, the yearly post will have many replies. This is acceptable — Bluesky collapses long threads and the pinned post stays prominent.

**[Trade-off] No image in the daily reply** → Keeping it text + quote-embed is intentional. The quote card provides visual interest from the quoted post's content. Adding a chart would duplicate the sparkline.
