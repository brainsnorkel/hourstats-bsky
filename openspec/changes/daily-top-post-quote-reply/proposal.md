## Why

The yearly sentiment chart is pinned and gets daily visibility, but it's a static snapshot updated monthly. Replying to it each day with a quote-embed of yesterday's highest-engagement post creates a living thread of "post of the day" entries, giving followers a reason to revisit the thread and providing historical context for what drove engagement on any given day.

## What Changes

- Persist the yearly post URI/CID so daily cycles can reply to it
- During daily aggregation, identify the single highest-engagement post from yesterday's runs
- Post a reply to the yearly chart thread that quote-embeds yesterday's top post
- New Bluesky client method combining reply threading with quote-embed (`app.bsky.embed.record`)
- Chain daily replies as a flat thread under the yearly post (each reply's parent = yearly post, not previous day)

## Capabilities

### New Capabilities
- `daily-top-post-reply`: Daily reply to the yearly chart thread quoting the highest-engagement post from the previous day. Covers: persistent yearly post tracking, daily top post identification, quote-embed reply posting, and thread structure.

### Modified Capabilities
- `yearly-charting`: After posting and pinning the yearly chart, the system must now persist its URI/CID for use by daily reply cycles.
- `daily-aggregation`: Alongside computing sentiment statistics, the aggregation step must now identify and persist the URI/CID of the single highest-engagement post from yesterday's runs.

## Impact

- **Store layer**: New table(s) for yearly post state and daily top post tracking
- **Bluesky client**: New `PostReplyWithQuote` method (reply + embed.record, no image)
- **Scheduler/main**: New daily step after aggregation to post the quote reply
- **Yearly posting flow**: Small addition to persist URI/CID after posting
- **No breaking changes** to existing posting chain or 30-minute cycle
