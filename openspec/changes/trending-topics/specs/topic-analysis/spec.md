## ADDED Requirements

### Requirement: TF-IDF candidate extraction
The system SHALL compute TF-IDF scores across topic_tokens from the rolling 24-hour window every 15 minutes, filtered to only include posts with engagement > 0 (JOIN with post_buffer). Term frequency is computed per document (post). Inverse document frequency is computed across the entire filtered corpus. The system SHALL select the top 50 terms by TF-IDF score (MaxTFIDFTerms). Terms with document frequency below 10 (MinDocFrequency, appearing in fewer than 10 posts) SHALL be excluded. The system SHALL sample at most 20,000 rows (maxTFIDFRows) from topic_tokens to bound compute time.

#### Scenario: Top 50 terms extracted from 24h window
- **WHEN** the 15-minute topic analysis cycle runs and topic_tokens contains posts from the last 24 hours
- **THEN** the system JOINs topic_tokens with post_buffer (requiring engagement > 0), computes TF-IDF on the filtered set, and returns the top 50 terms ranked by score, excluding terms with document frequency below 10

#### Scenario: Insufficient data for analysis
- **WHEN** the topic_tokens table contains fewer than 100 posts (MinCorpusSize) from the last 24 hours
- **THEN** the system SHALL skip the analysis cycle and log an info message

### Requirement: LLM-powered synonym grouping and labeling
The system SHALL send the top 50 TF-IDF terms to the Google Gemini Flash API in a single request at temperature 0.2. The prompt SHALL instruct the LLM to: group synonyms and related terms together, label each group with a 1-3 word subject-only name (NO filler words), provide a one-sentence description, and return additional synonym terms not in the top 50 that would also indicate each topic. The prompt SHALL instruct aggressive merging of related terms into the single most well-known event, person, or subject, aiming for 5-7 truly distinct topics. Terms containing underscores SHALL be explained as multi-word phrases. The response SHALL be parsed as JSON. The system SHALL produce a maximum of 10 groups. After parsing, clusters with generic labels (miscellaneous, general, various, other, etc.) SHALL be filtered out by filterGenericClusters().

#### Scenario: Gemini returns valid grouped terms
- **WHEN** the Gemini API returns a valid JSON response with grouped terms
- **THEN** the system uses the groups, labels, descriptions, and synonym maps for ranking

#### Scenario: Gemini API is unavailable
- **WHEN** the Gemini API call fails (timeout, error, invalid response)
- **THEN** the system SHALL fall back to using the top TF-IDF keyword from each of 5 evenly-spaced score buckets as standalone topic labels, log a warning, and continue the analysis cycle

### Requirement: Volume-based ranking
The system SHALL score each topic cluster by counting the number of posts in the 24-hour window that contain ANY of the cluster's keywords or LLM-provided synonyms. The system SHALL select the top 5 topics by post count and store a timestamped ranking snapshot.

#### Scenario: Top 5 topics ranked by volume
- **WHEN** topic clusters have been created (by LLM or fallback)
- **THEN** the system counts matching posts using expanded keyword+synonym sets, ranks by count, and selects the top 5

### Requirement: Exemplar post selection
At trending post time (every 6 hours), the system SHALL identify the highest-engagement exemplar post for each top-5 topic using a database JOIN (no API calls). For each topic, the system SHALL: JOIN post_buffer with topic_tokens on post URI, use json_each(tt.tokens) to match against the topic's keyword+synonym set, exclude posts with more than one hashtag (spam filter), and rank by engagement (likes + reposts + replies) DESC with keyword match count as tiebreaker. The system SHALL limit to the most recent 6 hours of posts and return up to 20 candidates per topic. Handle deduplication SHALL ensure each topic gets a unique exemplar author. The exemplar URI and author handle SHALL be stored in the topic_snapshots row. If no candidates are found, the exemplar fields SHALL be left empty and a warning logged.

#### Scenario: Exemplar found for topic
- **WHEN** the 6-hour posting cycle runs and topic-matching posts exist in post_buffer JOIN topic_tokens
- **THEN** the system selects the highest-engagement post (with unique author handle) and stores its URI and author handle in the snapshot

#### Scenario: No matching posts for topic
- **WHEN** a topic's keyword set matches zero URIs in the post_buffer/topic_tokens JOIN
- **THEN** exemplar_uri and exemplar_handle are stored as empty strings

#### Scenario: All candidates share handles with higher-ranked topics
- **WHEN** all exemplar candidates for a topic have author handles already used by higher-ranked topics
- **THEN** the system logs a warning and stores empty exemplar fields for that topic

### Requirement: Topic snapshot storage
The system SHALL store each top-5 ranking as rows in the `topic_snapshots` table with columns: id (autoincrement), snapshot_time (TEXT), rank (INTEGER 1-5), topic_id (TEXT), label (TEXT), description (TEXT), post_count (INTEGER), keywords (TEXT, JSON array), exemplar_uri (TEXT, default ''), exemplar_handle (TEXT, default ''). Snapshots older than 48 hours SHALL be purged.

#### Scenario: Snapshot stored after ranking
- **WHEN** the top 5 topics are determined
- **THEN** 5 rows are inserted into topic_snapshots with the current UTC timestamp and each topic's rank, label, description, post count, keywords, and exemplar fields (populated at 6-hour posting time, empty at 15-minute analysis time)

#### Scenario: Old snapshots purged
- **WHEN** the topic analysis cycle runs
- **THEN** topic_snapshots rows with snapshot_time older than 48 hours are deleted
