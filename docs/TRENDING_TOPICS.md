# Trending Topics

HourStats extracts what Bluesky is talking about and posts a visual summary of the top 5 trending topics every 6 hours.

## How It Works

The trending topics feature runs as two independent cycles alongside the existing 30-minute sentiment analysis:

```
Jetstream firehose
        |
        v
  [Root post?] --yes--> Tokenize --> topic_tokens (SQLite, 26h retention)
        |
        v
  [post_buffer] (existing sentiment pipeline)


Every 15 minutes:                      Every 6 hours:
  topic_tokens (24h window)              Latest snapshot
        |                                      |
        v                                      v
  TF-IDF (top 30 terms)                Hydrate exemplar posts
        |                                (FeedGetPosts API)
        v                                      |
  Gemini Flash (group + label)                 v
        |                               Generate bump chart
        v                                      |
  Rank by post volume (top 5)                  v
        |                               Format post text
        v                                (movement arrows +
  Match identities (Jaccard)             exemplar mentions +
        |                                hashtag facets)
        v                                      |
  Store snapshot                               v
                                        Post to Bluesky
                                        (standalone, not threaded)
```

## The Pipeline

### 1. Tokenization (on ingest)

Every root post (non-reply) from the Jetstream firehose is tokenized in real time:

- Lowercase the text
- Strip URLs, @mentions, and emoji
- Remove ~300 English stopwords (including social media terms like "lol", "bruh", "literally")
- Filter to tokens of 3+ characters

The cleaned token set is stored as a JSON array in `topic_tokens` alongside the post URI. Replies are excluded because they tend to echo the root post's topic without adding signal. Tokens are retained for 26 hours (24-hour analysis window + 2-hour buffer) and then purged.

### 2. TF-IDF Extraction (every 15 minutes)

TF-IDF (Term Frequency-Inverse Document Frequency) scores every token across the rolling 24-hour corpus:

- **Term Frequency**: How often a term appears in a single post
- **Inverse Document Frequency**: Penalises terms that appear in many posts (common words score lower)
- **Result**: Terms that are frequent but distinctive rise to the top

Terms appearing in fewer than 10 posts are excluded to filter noise. The top 30 terms by TF-IDF score are passed to the next stage.

### 3. LLM-Powered Synonym Grouping (Gemini Flash)

Short social media posts (typically under 300 characters) have weak co-occurrence signals. The word "trump" and "potus" rarely appear in the *same* post, so algorithmic clustering fails to group them. An LLM understands semantic relationships.

A single Google Gemini Flash API call receives the top 30 TF-IDF terms and returns:

- **Grouped clusters**: Related terms grouped together (e.g., ["trump", "potus", "maga"])
- **Labels**: A 2-4 word human-readable name for each group (e.g., "Trump & Politics")
- **Descriptions**: A one-sentence summary
- **Synonym maps**: Additional related terms NOT in the top 30 that would also indicate the topic (e.g., "white house", "oval office")

The synonym maps are the key innovation. They're used to recount post volume in the next step, so topics using varied vocabulary get properly aggregated instead of appearing as multiple weak signals.

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

### 6. Bump Chart Generation

Every 6 hours, a 1200x800 PNG bump chart is generated showing the rank positions (#1 through #5) of trending topics over the past 24 hours:

- Y-axis is inverted: rank #1 at top, #5 at bottom
- X-axis shows 24 hours in UTC with 6-hour markers
- Each topic is drawn as a Gaussian-smoothed line using the Okabe-Ito colour-blind safe palette
- Topics entering the top 5 animate from below rank #5 (rising into view)
- Topics leaving the top 5 descend below rank #5 (falling out)
- Topic labels appear at the right end of each line

Colours are stable per topic identity, so the same topic keeps its colour across consecutive charts.

### 7. Exemplar Posts

At posting time, the system identifies the highest-engagement post for each topic:

1. Query `topic_tokens` for post URIs matching the topic's keyword+synonym set (up to 50 per topic)
2. Batch-fetch their **current** engagement (likes + reposts + replies) via the Bluesky `FeedGetPosts` API
3. Select the post with the highest total engagement as the exemplar

This happens at post time rather than ingest time because Jetstream delivers posts with zero engagement (they're brand new). By the time the 6-hour post runs, a post could have accumulated thousands of interactions.

### 8. Posting

The trending post is published as a **standalone** post (not threaded to the sentiment analysis chain) with:

```
Bluesky Trending Topics

1. (^2) AI & Machine Learning - 2,847 posts
   @researcher.bsky.social
2. (NEW) Super Bowl - 1,923 posts
   @sportsfan.bsky.social
3. (->) Climate Policy - 1,456 posts
   @journalist.bsky.social
4. (v1) Crypto Markets - 1,201 posts
   @trader.bsky.social
5. (v2) Gaming - 987 posts
   @streamer.bsky.social

#trending #hourstatstrend
```

Movement indicators compare against the **previous 6-hour post** (not the 15-minute snapshot):
- `(^N)` = rose N positions
- `(vN)` = fell N positions
- `(->)` = unchanged
- `(NEW)` = not in previous top 5

The `#trending` and `#hourstatstrend` hashtags are proper AT Protocol facets, allowing users to mute the feature via Bluesky's mute-word functionality without affecting their sentiment feed.

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

**Jaccard similarity instead of embeddings**: Topic identity only needs to answer "are these roughly the same keywords?" Jaccard on keyword sets is fast, deterministic, and requires no model inference. Embeddings would add latency, cost, and a model dependency for marginal benefit.

**Exemplar hydration at post time instead of ingest**: Posts arrive via Jetstream with zero engagement. The `post_buffer` purges after 2 hours. By querying the Bluesky API at the 6-hour posting point, we get the true most-engaged post with 20+ hours of accumulated likes/reposts/replies.

**Standalone posts instead of threading**: The trending feature is conceptually separate from sentiment analysis. Threading it to the 30-minute chain would add noise. Standalone posts with hashtag facets let users mute independently.

**Bump chart instead of bar/area chart**: Bump charts directly visualise the question "what's rising and falling?" Lines crossing and swapping positions are intuitive. The Okabe-Ito palette ensures colour-blind accessibility.
