## Why

The bot currently analyses sentiment but says nothing about *what* Bluesky is talking about. Adding a trending-topics feature that extracts, clusters, and ranks the top 5 discussion topics every 6 hours fills this gap, giving followers a data-driven "what's happening on Bluesky" alongside the existing "how Bluesky feels." The #trending and #hourstatstrend hashtags let users mute the feature independently if they find it spammy.

## What Changes

- Continuously tokenise incoming root posts (strip URLs, mentions, emoji, stopwords) and store cleaned token sets in a new SQLite table with 26-hour retention
- Every 15 minutes, compute TF-IDF scores across the rolling 24-hour token window to extract the top 30 candidate terms
- Send candidates to Google Gemini Flash to group synonyms/related terms into topic clusters and generate short labels
- Rank clusters by post volume, select top 5, and store a timestamped ranking snapshot
- Track topic identity across ranking cycles using Jaccard similarity on keyword sets so the same topic keeps its identity as it rises and falls
- Every 6 hours, generate a bump/rank chart (1200x800 PNG) showing how topics moved through the top 5 over the past 24 hours
- Post the chart as a standalone Bluesky post with text listing the top 5 (with movement arrows) and hashtags #trending #hourstatstrend
- Entire feature gated behind `TRENDING_ENABLED` env var (default: false)

## Capabilities

### New Capabilities
- `topic-tokenization`: Preprocess and tokenise incoming root posts, store cleaned tokens with 26-hour retention, purge expired tokens
- `topic-analysis`: TF-IDF computation over 24h rolling window, Gemini-powered synonym grouping and labeling, volume-based ranking to top 5
- `topic-identity-tracking`: Persistent topic identity matching across ranking cycles using Jaccard similarity, peak rank tracking, 7-day retention
- `trending-chart-generation`: Bump/rank chart showing topic positions (#1-#5) over 24 hours with Okabe-Ito colour palette, entry/exit animations, branding
- `trending-posting`: Standalone Bluesky post every 6 hours with trending chart image, top-5 text with movement arrows, #trending and #hourstatstrend hashtag facets

### Modified Capabilities
- `post-fetching`: The Jetstream OnPost callback must additionally tokenise root posts and store tokens in the new topic_tokens table (alongside existing post_buffer insertion)
- `run-coordination`: The main scheduler loop gains two new tickers (15-min topic analysis, 6-hour trending post) integrated into the existing select loop
- `operational-controls`: New environment variables (GOOGLE_AI_API_KEY, TRENDING_ENABLED, TRENDING_INTERVAL, TRENDING_POST_HOURS) and feature flag behaviour

## Impact

- **Store layer**: 3 new SQLite tables (topic_tokens, topic_snapshots, topic_identity) with migrations, CRUD methods, and purge logic
- **New package**: `internal/topics/` with tokenizer, TF-IDF engine, Gemini grouper, ranker, and identity tracker
- **Chart generation**: New `trending_chart_generator.go` in `internal/sparkline/` (bump chart)
- **Bluesky client**: Minor — reuses existing `PostWithImage` for standalone posts; new hashtag facet creation
- **External dependency**: Google Gemini API (HTTP-only, no new Go modules) — new `GOOGLE_AI_API_KEY` env var
- **Dockerfile**: Pass through `GOOGLE_AI_API_KEY` env var
- **Disk**: ~144MB/day for topic_tokens table (26h of root post tokens at ~720k posts/day)
- **Cost**: ~$0.04/day for Gemini Flash API calls (~96 calls/day)
- **No breaking changes** to existing sentiment analysis pipeline, posting chain, or daily/yearly workflows
