# Trending Topics

HourStats extracts what Bluesky is talking about and posts a text summary of the top 5 trending topics every 6 hours.

## How It Works

The trending topics feature runs as two independent cycles alongside the existing 30-minute sentiment analysis:

```
Jetstream firehose
        |
        v
  [Root post?] --no--> skip tokenization
        |
       yes
        |
  [Adult content?] --yes--> skip tokenization
        |
       no
        |
  [>1 hashtag?] --yes--> skip tokenization (spam filter)
        |
       no
        v
  Tokenize (unigrams + bigrams) --> topic_tokens (SQLite, 26h retention)


Every 15 minutes:                      Every 6 hours:
  topic_tokens (24h window)              Latest snapshot
  filtered: engagement > 0                    |
        |                                     v
        v                              Exemplar selection
  TF-IDF (top 50 terms)                (post_buffer JOIN,
        |                               ranked by engagement)
        v                                     |
  Gemini Flash (group + label,                v
    temperature 0.2,                   Format post text
    aggressive merge)                   (numbered topics +
        |                               exemplar mentions +
        v                               #hstrend facet)
  Filter generic clusters                     |
        |                                     v
        v                              Post to Bluesky
  Rank by post volume (top 5)          (standalone, not threaded)
        |
        v
  Match identities (Jaccard)
        |
        v
  Store snapshot
```

## The Pipeline

### 1. Tokenization (on ingest)

Every root post (non-reply) from the Jetstream firehose is tokenized in real time, subject to three ingestion filters:

1. **Adult content filter**: Posts with Bluesky moderation self-labels (e.g., `sexual`, `nudity`, `graphic-media`) are excluded via `HasAdultContent()`
2. **Hashtag spam filter**: Posts with more than one hashtag are excluded. This blocks promotional spam (e.g., counterfeit jersey listings with 5-8 hashtags)
3. **Empty token filter**: Posts that produce zero tokens after preprocessing are excluded

Tokenization steps:
- Lowercase the text
- Strip URLs, @mentions, and emoji
- Remove ~300 English stopwords (including social media terms like "lol", "bruh", "literally")
- Filter to tokens of 3+ characters
- Extract bigrams: adjacent token pairs joined with underscores (e.g., `bad_bunny`, `super_bowl`). This captures multi-word names and phrases that single tokens miss

The cleaned token set (unigrams + bigrams) is stored as a JSON array in `topic_tokens` alongside the post URI. Replies are excluded because they tend to echo the root post's topic without adding signal. Tokens are retained for 26 hours (24-hour analysis window + 2-hour buffer) and then purged.

### 2. TF-IDF Extraction (every 15 minutes)

TF-IDF (Term Frequency-Inverse Document Frequency) scores every token across the rolling 24-hour corpus:

- **Term Frequency**: How often a term appears in a single post
- **Inverse Document Frequency**: Penalises terms that appear in many posts (common words score lower)
- **Result**: Terms that are frequent but distinctive rise to the top

**Engagement filter**: Before TF-IDF computation, the query JOINs `topic_tokens` with `post_buffer` and requires `(likes + reposts + replies) > 0`. This filters out zero-engagement posts (spam, unhydrated posts) so only posts with real engagement contribute to topic signal.

**Limits**:
- Terms appearing in fewer than 10 posts (`MinDocFrequency`) are excluded to filter noise
- The corpus must contain at least 100 posts (`MinCorpusSize`) or the cycle is skipped
- At most 20,000 rows (`maxTFIDFRows`) are sampled from `topic_tokens` to bound compute time
- The top 50 terms (`MaxTFIDFTerms`) by TF-IDF score are passed to the next stage

### 3. LLM-Powered Synonym Grouping (Gemini Flash)

Short social media posts (typically under 300 characters) have weak co-occurrence signals. The word "trump" and "potus" rarely appear in the *same* post, so algorithmic clustering fails to group them. An LLM understands semantic relationships.

A single Google Gemini Flash API call receives the top 50 TF-IDF terms and returns:

- **Grouped clusters**: Related terms grouped together (e.g., ["trump", "potus", "maga"])
- **Labels**: A 1-3 word subject-only name for each group (e.g., "Donald Trump"). Filler words (Posts, Mentions, Discussions, Event, Content, Media, etc.) are banned by the prompt
- **Descriptions**: A one-sentence summary
- **Synonym maps**: Additional related terms NOT in the top 50 that would also indicate the topic (e.g., "white house", "oval office")

The synonym maps are the key innovation. They're used to recount post volume in the next step, so topics using varied vocabulary get properly aggregated instead of appearing as multiple weak signals.

**Prompt hardening**:
- **Temperature 0.2**: Low temperature for more deterministic, consistent grouping
- **Aggressive merge rule**: The prompt instructs Gemini to "AGGRESSIVELY merge related terms into the single most well-known event, person, or subject." If a major event (Super Bowl, Oscars, etc.) is happening, ALL related terms (teams, players, jerseys, halftime, scores) MUST merge into that event
- **Target 5-7 groups**: "Aim for 5-7 truly DISTINCT topics. When in doubt, MERGE into fewer, bigger groups"
- **Underscore handling**: Terms with underscores are explained as multi-word phrases (e.g., `bad_bunny` = "Bad Bunny")
- **Maximum 10 groups**: Hard cap on output groups

**Generic cluster filter** (`filterGenericClusters`): After Gemini returns, any cluster with a label containing generic words is dropped: miscellaneous, general, various, other, everyday, mixed, assorted, unrelated, uncategorized, uncategorised, unclassified, activities, actions, terms, words, posts, mentions, discussions, content, topics, updates, community, online.

If Gemini is unavailable, the system falls back to using the top 5 TF-IDF terms as standalone topic labels.

**Cost**: ~$0.04/day (96 calls at Gemini Flash pricing). Hard-capped at 100 calls/day.

### 4. Volume-Based Ranking

Each topic cluster is scored by counting how many posts in the 24-hour window contain ANY of its keywords or LLM-provided synonyms. The top 5 by post count are selected.

### 5. Identity Tracking (Jaccard Similarity)

Topics evolve over hours. The keyword set for "AI Discussion" might be `["ai", "chatgpt", "openai", "llm"]` at 9am and `["ai", "claude", "anthropic", "model"]` at 3pm. Without identity tracking, these would appear as two different topics on the chart.

The system maintains a `topic_identity` table that maps keyword sets to stable UUIDs. When a new ranking is computed:

1. Each cluster's keyword+synonym set is compared against recent identities using **Jaccard similarity** (intersection / union of keyword sets)
2. If similarity exceeds 0.3, the cluster inherits the existing identity (same UUID, same chart colour)
3. Otherwise, a new UUID is assigned

The threshold of 0.3 is intentionally low because keyword sets expand with synonyms. A genuine topic match typically scores 0.3-0.5 on the full keyword+synonym union.

Identities are retained for 7 days. Peak rank is tracked so the system knows a topic's historical best position.

### 6. Exemplar Posts

At posting time, the system identifies the highest-engagement post for each topic using a database-only approach (no API calls):

1. **JOIN `post_buffer` with `topic_tokens`**: The `GetExemplarCandidates()` query joins these tables on post URI, then uses `json_each(tt.tokens)` to match against the topic's keyword+synonym set
2. **Multi-hashtag filter**: Posts with more than one hashtag are excluded from exemplar candidates (`LENGTH(pb.text) - LENGTH(REPLACE(pb.text, '#', '')) <= 1`) to avoid promotional content
3. **Engagement ranking**: Results are ordered by `(likes + reposts + replies) DESC`, then by keyword match count as tiebreaker
4. **Handle deduplication**: Each topic gets a unique exemplar author — if the top candidate's handle was already used by a higher-ranked topic, the next candidate is selected
5. **6-hour window**: Only posts from the last 6 hours are considered, ensuring fresh exemplars

This approach replaced the original `FeedGetPosts` API-based hydration. Since `post_buffer` already contains engagement data hydrated by the sentiment analysis cycle, we can read it directly — zero API calls, faster execution, and engagement scores that reflect the latest hydration (72-1,017 engagement scores observed vs 0-4 with the old approach).

### 7. Posting

The trending post is published as a **standalone** post (not threaded to the sentiment analysis chain) with:

```
Topics

1. Bad Bunny @davidcorn.bsky.social
2. Donald Trump @ronfilipkowski.bsky.social
3. Jeffrey Epstein @popcrave.com
4. Discord @legendofnerd.bsky.social
5. Super Bowl @mprnews.org

#hstrend
```

Each topic line shows the rank, topic label, and the exemplar post's author as a clickable link. The `@handle` mentions are AT Protocol link facets pointing to the exemplar post's URL (not the author's profile), so clicking the handle takes you directly to the trending post.

If the post exceeds the 300-grapheme limit, exemplar mentions are dropped from the bottom up (lower-ranked topics lose their exemplar first) until it fits.

The `#hstrend` hashtag is a proper AT Protocol facet, allowing users to mute the feature via Bluesky's mute-word functionality without affecting their sentiment feed.

**Bump chart**: A 1200x800 PNG bump chart is still generated and stored in topic snapshots for historical reference, but is **not included** in the posted content. Posts are text-only for a cleaner, more readable format.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TRENDING_ENABLED` | `false` | Master switch for the entire feature |
| `GOOGLE_AI_API_KEY` | (required) | Gemini API key; feature auto-disables if missing |
| `TRENDING_INTERVAL` | `15` | Topic analysis frequency in minutes |
| `TRENDING_POST_HOURS` | `6` | Trending post frequency in hours |
| `DRY_RUN` | `false` | When true, logs trending posts instead of publishing |

## Data Retention

| Table | Retention | Purge Trigger |
|-------|-----------|---------------|
| `topic_tokens` | 26 hours | Each 15-minute analysis cycle |
| `topic_snapshots` | 48 hours | Each 15-minute analysis cycle |
| `topic_identity` | 7 days | Each 15-minute analysis cycle |

## Why These Choices

**TF-IDF + LLM instead of pure NLP**: Short social posts break traditional clustering. TF-IDF cheaply identifies *what words are trending*, then the LLM groups them semantically. This keeps costs at ~$1/month while producing human-quality topic labels.

**Engagement filter at TF-IDF query time**: Zero-engagement posts are overwhelmingly spam or unhydrated noise. Filtering them at query time (rather than ingestion) preserves the raw token data while ensuring only posts with real engagement influence topic rankings. This single JOIN eliminated spam topics like "NFL Jerseys" that were caused by bot accounts posting counterfeit merchandise listings.

**Hashtag spam filter at ingestion**: Posts with more than one hashtag are disproportionately promotional. The jersey spam that polluted early topic results all had 5-8 hashtags per post. Filtering at ingestion (>1 hashtag → skip tokenization) removes the noise before it enters the pipeline.

**Aggressive Gemini merge prompt + low temperature**: Early results showed fragmented topics — "Super Bowl" and "NFL Jerseys" appearing as separate topics when they should be one. The hardened prompt instructs Gemini to merge sub-topics under the most recognizable event/person name, and temperature 0.2 reduces inconsistency between calls.

**Generic cluster filter**: Even with prompt hardening, Gemini occasionally creates catch-all groups ("Miscellaneous", "Online Community"). The `filterGenericClusters()` post-processor drops these automatically.

**Exemplar from post_buffer JOIN instead of API calls**: The original design called `FeedGetPosts` API to hydrate engagement for exemplar candidates. This was replaced with a direct JOIN between `post_buffer` and `topic_tokens`. Benefits: zero additional API calls, engagement data already hydrated by the sentiment pipeline, much higher engagement scores (posts have accumulated interactions over hours vs zero at firehose time), and faster execution.

**Jaccard similarity instead of embeddings**: Topic identity only needs to answer "are these roughly the same keywords?" Jaccard on keyword sets is fast, deterministic, and requires no model inference. Embeddings would add latency, cost, and a model dependency for marginal benefit.

**Standalone posts instead of threading**: The trending feature is conceptually separate from sentiment analysis. Threading it to the 30-minute chain would add noise. Standalone posts with a hashtag facet let users mute independently.

**Text-only posts (no bump chart image)**: The bump chart image was removed from the posted content for a cleaner format. Topic names and exemplar links communicate the essential information more directly than a chart.

**Bigram extraction**: Single-word tokens miss multi-word names ("Bad Bunny" → "bad" + "bunny" individually). Bigram extraction joins adjacent tokens with underscores so "bad_bunny" appears as a single TF-IDF candidate, producing cleaner labels.
