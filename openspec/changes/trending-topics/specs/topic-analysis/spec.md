## ADDED Requirements

### Requirement: TF-IDF candidate extraction
The system SHALL compute TF-IDF scores across all topic_tokens from the rolling 24-hour window every 15 minutes. Term frequency is computed per document (post). Inverse document frequency is computed across the entire 24-hour corpus. The system SHALL select the top 30 terms by TF-IDF score. Terms with document frequency below 10 (appearing in fewer than 10 posts) SHALL be excluded.

#### Scenario: Top 30 terms extracted from 24h window
- **WHEN** the 15-minute topic analysis cycle runs and topic_tokens contains posts from the last 24 hours
- **THEN** the system computes TF-IDF and returns the top 30 terms ranked by score, excluding terms with document frequency below 10

#### Scenario: Insufficient data for analysis
- **WHEN** the topic_tokens table contains fewer than 100 posts from the last 24 hours
- **THEN** the system SHALL skip the analysis cycle and log an info message

### Requirement: LLM-powered synonym grouping and labeling
The system SHALL send the top 30 TF-IDF terms to the Google Gemini Flash API in a single request. The prompt SHALL instruct the LLM to: group synonyms and related terms together, label each group with a 2-4 word human-readable name, provide a one-sentence description, and return additional synonym terms not in the top 30 that would also indicate each topic. The response SHALL be parsed as JSON. The system SHALL produce 5-10 groups maximum.

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

### Requirement: Exemplar post hydration
At trending post time (every 6 hours), the system SHALL identify the highest-engagement exemplar post for each top-5 topic. For each topic, the system SHALL: query topic_tokens for post URIs whose token set intersects the topic's keyword+synonym set, sample up to 50 matching URIs, batch-fetch their current engagement metrics via the Bluesky FeedGetPosts API (25 URIs per call), and select the post with the highest total engagement (likes + reposts + replies). The exemplar URI and author handle SHALL be stored in the topic_snapshots row. If FeedGetPosts fails, the exemplar fields SHALL be left empty and a warning logged.

#### Scenario: Exemplar found for topic
- **WHEN** the 6-hour posting cycle runs and topic-matching posts exist
- **THEN** the system fetches current engagement for sampled URIs and stores the highest-engagement post's URI and author handle in the snapshot

#### Scenario: No matching posts for topic
- **WHEN** a topic's keyword set matches zero URIs in topic_tokens
- **THEN** exemplar_uri and exemplar_handle are stored as empty strings

#### Scenario: API failure during exemplar hydration
- **WHEN** FeedGetPosts returns an error
- **THEN** the system logs a warning and stores empty exemplar fields; the trending post proceeds without exemplars for affected topics

### Requirement: Topic snapshot storage
The system SHALL store each top-5 ranking as rows in the `topic_snapshots` table with columns: id (autoincrement), snapshot_time (TEXT), rank (INTEGER 1-5), topic_id (TEXT), label (TEXT), description (TEXT), post_count (INTEGER), keywords (TEXT, JSON array), exemplar_uri (TEXT, default ''), exemplar_handle (TEXT, default ''). Snapshots older than 48 hours SHALL be purged.

#### Scenario: Snapshot stored after ranking
- **WHEN** the top 5 topics are determined
- **THEN** 5 rows are inserted into topic_snapshots with the current UTC timestamp and each topic's rank, label, description, post count, keywords, and exemplar fields (populated at 6-hour posting time, empty at 15-minute analysis time)

#### Scenario: Old snapshots purged
- **WHEN** the topic analysis cycle runs
- **THEN** topic_snapshots rows with snapshot_time older than 48 hours are deleted
