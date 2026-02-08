## 1. SQLite Schema & Store Layer

- [ ] 1.1 Add `topic_tokens` table migration to `store.go` `migrate()`: `CREATE TABLE IF NOT EXISTS topic_tokens (post_uri TEXT PRIMARY KEY, tokens TEXT NOT NULL, created_at TEXT NOT NULL)` with index on `created_at`
- [ ] 1.2 Add `topic_snapshots` table migration: `CREATE TABLE IF NOT EXISTS topic_snapshots (id INTEGER PRIMARY KEY AUTOINCREMENT, snapshot_time TEXT NOT NULL, rank INTEGER NOT NULL, topic_id TEXT NOT NULL, label TEXT NOT NULL, description TEXT NOT NULL, post_count INTEGER NOT NULL, keywords TEXT NOT NULL, exemplar_uri TEXT NOT NULL DEFAULT '', exemplar_handle TEXT NOT NULL DEFAULT '')` with index on `snapshot_time`
- [ ] 1.3 Add `topic_identity` table migration: `CREATE TABLE IF NOT EXISTS topic_identity (topic_id TEXT PRIMARY KEY, canonical_label TEXT NOT NULL, keywords TEXT NOT NULL, first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, peak_rank INTEGER NOT NULL)`
- [ ] 1.4 Add `InsertTopicTokens(ctx, postURI, tokensJSON, createdAt string) error` method to store
- [ ] 1.5 Add `GetTopicTokensSince(ctx, cutoff string) ([]TopicTokenRow, error)` method — returns all rows with created_at >= cutoff
- [ ] 1.6 Add `PurgeTopicTokens(ctx, cutoff string) (int64, error)` method — deletes rows older than cutoff, returns count
- [ ] 1.7 Add `InsertTopicSnapshot(ctx, snapshotTime string, rank int, topicID, label, description string, postCount int, keywordsJSON, exemplarURI, exemplarHandle string) error` method
- [ ] 1.8 Add `GetTopicSnapshotsSince(ctx, cutoff string) ([]TopicSnapshotRow, error)` method — returns all snapshots since cutoff ordered by snapshot_time, rank
- [ ] 1.9 Add `PurgeTopicSnapshots(ctx, cutoff string) (int64, error)` method
- [ ] 1.10 Add `UpsertTopicIdentity(ctx, topicID, label, keywordsJSON, firstSeen, lastSeen string, peakRank int) error` method — INSERT OR REPLACE
- [ ] 1.11 Add `GetRecentTopicIdentities(ctx, cutoff string) ([]TopicIdentityRow, error)` method — returns identities with last_seen >= cutoff
- [ ] 1.12 Add `PurgeTopicIdentities(ctx, cutoff string) (int64, error)` method — deletes identities with last_seen older than cutoff
- [ ] 1.13 Add `CountTopicTokensSince(ctx, cutoff string) (int64, error)` method — for minimum-100-post check
- [ ] 1.14 Add `GetTopicTokenURIsByKeywords(ctx, keywords []string, cutoff string, limit int) ([]string, error)` method — returns post URIs from topic_tokens where the token set intersects with the provided keywords, limited to `limit` results, ordered by created_at DESC (most recent first)
- [ ] 1.15 Add `UpdateSnapshotExemplar(ctx, snapshotID int64, exemplarURI, exemplarHandle string) error` method — updates exemplar fields on an existing snapshot row
- [ ] 1.16 Unit tests for all new store methods: insert, query, purge, exemplar queries, edge cases (empty table, duplicates)

## 2. Topic Tokenizer

- [ ] 2.1 Create `internal/topics/types.go` with shared types: `TopicToken`, `TopicCluster`, `TopicSnapshot`, `TopicIdentity`, and constants (min doc frequency = 10, min corpus size = 100)
- [ ] 2.2 Create `internal/topics/tokenizer.go` with `Tokenize(text string) []string` — lowercase, strip URLs (http/https regex), strip @mentions, strip emoji (Unicode ranges), remove English stopwords (~300 words), filter tokens to minimum 3 characters
- [ ] 2.3 Create a stopwords list as an embedded string set (common English stopwords + social media terms like "lol", "lmao", "omg")
- [ ] 2.4 Unit tests for tokenizer: plain text, URLs, mentions, emoji, all-stopwords input, empty input, mixed content

## 3. TF-IDF Engine

- [ ] 3.1 Create `internal/topics/tfidf.go` with `ComputeTFIDF(tokens []TopicTokenRow) []TermScore` — computes TF per document, IDF across corpus, returns top 30 terms by TF-IDF score
- [ ] 3.2 Implement minimum document frequency filter: exclude terms appearing in fewer than 10 posts
- [ ] 3.3 Unit tests: known corpus with expected TF-IDF rankings, edge case with all identical tokens, low-frequency exclusion

## 4. Gemini Grouper

- [ ] 4.1 Create `internal/topics/grouper.go` with `type Grouper struct` holding the Gemini API key and HTTP client
- [ ] 4.2 Implement `GroupAndLabel(ctx, terms []TermScore) ([]TopicCluster, error)` — constructs the Gemini Flash REST API request with prompt: "Here are the top 30 terms by TF-IDF score. Group synonyms and related terms together, then label each group. Return JSON."
- [ ] 4.3 Define the expected JSON response schema: array of `{label, description, keywords, synonyms}`
- [ ] 4.4 Parse the Gemini response JSON, cap at 10 groups maximum
- [ ] 4.5 Implement fallback: if API call fails (timeout, error, invalid JSON), return top 5 TF-IDF terms as standalone single-keyword clusters with the keyword as the label
- [ ] 4.6 Add daily rate limit counter (max 100 calls/day) with reset at midnight UTC
- [ ] 4.7 Unit tests: mock HTTP responses (valid JSON, invalid JSON, timeout), fallback path, rate limit

## 5. Topic Ranker

- [ ] 5.1 Create `internal/topics/ranker.go` with `RankTopics(ctx, clusters []TopicCluster, tokens []TopicTokenRow) []RankedTopic` — for each cluster, count posts containing ANY keyword or synonym, rank by count, return top 5
- [ ] 5.2 Implement expanded matching: for each post's token set, check intersection with cluster keyword+synonym set
- [ ] 5.3 Unit tests: clusters with overlapping keywords, volume counting, tie-breaking (alphabetical label)

## 6. Topic Identity Tracker

- [ ] 6.1 Create `internal/topics/tracker.go` with `type Tracker struct` holding the store reference
- [ ] 6.2 Implement `AssignIdentities(ctx, ranked []RankedTopic) ([]IdentifiedTopic, error)` — loads recent identities (48h), computes Jaccard similarity of keyword sets, assigns existing topic_id if similarity > 0.3, creates new UUID otherwise
- [ ] 6.3 Implement Jaccard similarity function: `jaccard(a, b []string) float64` — |intersection| / |union|
- [ ] 6.4 Update matched identity records: canonical_label, keywords, last_seen, peak_rank (if current rank is better)
- [ ] 6.5 Insert new identity records for unmatched topics
- [ ] 6.6 Purge identity records with last_seen older than 7 days
- [ ] 6.7 Unit tests: matching existing topic, creating new topic, peak rank update, Jaccard edge cases (empty sets, identical sets)

## 7. Bump Chart Generator

- [ ] 7.1 Create `internal/sparkline/trending_chart_generator.go` with `GenerateTrendingChart(snapshots []TopicSnapshotRow, identities []TopicIdentityRow) ([]byte, error)`
- [ ] 7.2 Implement 1200x800 canvas with light gray background, title "Bluesky Trending Topics (24h)", branding watermark "@hourstats.bsky.social"
- [ ] 7.3 Draw Y-axis with inverted ranks (#1 top, #5 bottom) and gray dashed rank boundary lines
- [ ] 7.4 Draw X-axis with 24-hour UTC range and 6-hour markers
- [ ] 7.5 Assign stable colours per topic_id using Okabe-Ito palette: Blue (#0072B2), Vermillion (#D55E00), Bluish Green (#009E73), Orange (#E69F00), Reddish Purple (#CC79A7), overflow Sky Blue (#56B4E9) and Yellow (#F0E442)
- [ ] 7.6 Draw Gaussian-smoothed rank lines for each topic, with topic labels at the right end
- [ ] 7.7 Implement entry animation: topics entering top 5 start from below rank #5
- [ ] 7.8 Implement exit animation: topics leaving top 5 descend below rank #5
- [ ] 7.9 Handle insufficient data: skip chart generation if fewer than 2 distinct snapshot times, log info
- [ ] 7.10 Unit tests: generate chart with mock snapshot data, verify PNG output is non-empty, test colour assignment stability

## 8. Exemplar Hydrator

- [ ] 8.1 Create `internal/topics/exemplar.go` with `type ExemplarHydrator struct` holding a `PostFetcher` (same interface as hydrator package) and the store reference
- [ ] 8.2 Implement `HydrateExemplars(ctx, ranked []IdentifiedTopic) ([]IdentifiedTopic, error)` — for each topic: query `GetTopicTokenURIsByKeywords` with up to 50 URIs, batch-fetch via `FeedGetPosts` (25 per call), pick highest engagement, set `ExemplarURI` and `ExemplarHandle` on the topic
- [ ] 8.3 Handle API failures gracefully: log warning, leave exemplar fields empty, continue to next topic
- [ ] 8.4 Handle edge case: no matching URIs for a topic (leave exemplar empty)
- [ ] 8.5 Unit tests: mock PostFetcher returning posts with varying engagement, API failure path, no matching URIs path

## 9. Trending Post Formatter

- [ ] 9.1 Create `internal/topics/formatter.go` with `FormatTrendingPost(ranked []IdentifiedTopic, previous []IdentifiedTopic) (string, []RichtextFacet)` — generates post text with movement arrows, exemplar mentions, and hashtag facets
- [ ] 9.2 Implement movement indicators: compare current rank to previous post's rank — (^N) rose, (vN) fell, (->) unchanged, (NEW) not in previous top 5
- [ ] 9.3 Include exemplar author handle on each topic line as a mention facet (e.g. "🔥 @handle.bsky.social") when available; omit when exemplar is empty
- [ ] 9.4 Implement hashtag facet construction: calculate byte offsets for #trending and #hourstatstrend, create `RichtextFacet_Tag` entries
- [ ] 9.5 Implement mention facet construction: for each exemplar handle, create `RichtextFacet_Mention` with correct byte offsets and DID
- [ ] 9.6 Implement alt text generation: `FormatAltText(ranked []IdentifiedTopic) string` — "Bluesky trending topics: #1 [label] (top post by @handle), #2 [label], ..."
- [ ] 9.7 Unit tests: all movement indicator cases, first post (all NEW), hashtag and mention byte offset correctness, alt text format, topics with and without exemplars

## 10. Topic Analysis Orchestrator

- [ ] 10.1 Create `internal/topics/analyzer.go` with `type Analyzer struct` composing Tokenizer, TFIDFEngine, Grouper, Ranker, Tracker, ExemplarHydrator, and store reference
- [ ] 10.2 Implement `RunAnalysisCycle(ctx) error` — purge expired tokens (26h), check minimum corpus (100 posts), compute TF-IDF, call Gemini grouper, rank topics, assign identities, store snapshot (without exemplars — those are filled at post time)
- [ ] 10.3 Implement `RunTrendingPost(ctx, bskyClient, dryRun) error` — load latest snapshot, hydrate exemplars for top 5 topics, load previous post's snapshot (6h ago), generate chart, format text with facets and exemplar mentions, post to Bluesky via PostWithImage, handle dry-run mode
- [ ] 10.4 Store previous post's ranking (from key_value or snapshot table) for movement comparison
- [ ] 10.5 Log each step with timing information for monitoring

## 11. Jetstream Integration

- [ ] 11.1 In the Jetstream OnPost callback in `main.go`, after post insertion into post_buffer, add conditional tokenization: if `trendingEnabled && rec.Reply == nil`, tokenize the post text and call `db.InsertTopicTokens`
- [ ] 11.2 Log tokenization failures as warnings without blocking post_buffer insertion
- [ ] 11.3 Verify the callback handles the case where tokenizer returns empty tokens (skip insert)

## 12. Main Loop Wiring

- [ ] 12.1 Add environment variable parsing: `GOOGLE_AI_API_KEY`, `TRENDING_ENABLED` (bool, default false), `TRENDING_INTERVAL` (int, default 15), `TRENDING_POST_HOURS` (int, default 6)
- [ ] 12.2 Validate that `GOOGLE_AI_API_KEY` is set when `TRENDING_ENABLED=true`; if missing, log error and disable trending
- [ ] 12.3 Initialize `topics.Analyzer` with Gemini API key, store, and config
- [ ] 12.4 Create topic analysis ticker (`TRENDING_INTERVAL` minutes) — only when trending enabled
- [ ] 12.5 Create trending post ticker (`TRENDING_POST_HOURS` hours) — only when trending enabled
- [ ] 12.6 Add two new cases to the main select loop calling `analyzer.RunAnalysisCycle` and `analyzer.RunTrendingPost`
- [ ] 12.7 Add deferred Stop() for both new tickers on shutdown
- [ ] 12.8 Verify DRY_RUN applies to trending posts (log instead of posting to Bluesky)

## 13. Verification

- [ ] 13.1 `go build ./...` passes with zero errors
- [ ] 13.2 `go test ./...` passes (including all new unit tests)
- [ ] 13.3 `go vet ./...` passes
- [ ] 13.4 LSP diagnostics clean on all changed files
- [ ] 13.5 Deploy to staging with TRENDING_ENABLED=true and DRY_RUN=true, verify analysis cycles run and log output
- [ ] 13.6 Run with DRY_RUN=false on staging for 24 hours, verify: tokens collected, TF-IDF computed, Gemini called successfully, snapshots stored, exemplars hydrated, chart generated, post published with correct format, facets, and exemplar mentions
- [ ] 13.7 Verify trending posts appear as standalone (not threaded) with #trending #hourstatstrend hashtags as proper facets and exemplar mentions linking to real posts
