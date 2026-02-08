## ADDED Requirements

### Requirement: Tokenize root posts on ingest
The system SHALL preprocess and tokenize every root post (non-reply) received from the Jetstream firehose. Tokenization MUST: lowercase the text, strip URLs (http/https patterns), strip @mentions, strip emoji (Unicode emoji ranges), remove English stopwords (~300 common words), and filter tokens to minimum 3 characters. The resulting token set SHALL be stored as a JSON array in the `topic_tokens` SQLite table alongside the post URI and creation timestamp.

#### Scenario: Root post is tokenized and stored
- **WHEN** a root post (rec.Reply == nil) passes the English language filter
- **THEN** the system tokenizes the text and inserts a row into topic_tokens with the post URI, JSON token array, and created_at timestamp

#### Scenario: Reply post is not tokenized
- **WHEN** a reply post (rec.Reply != nil) is received
- **THEN** the system SHALL NOT insert any row into topic_tokens

#### Scenario: Post with only stopwords/URLs produces empty tokens
- **WHEN** a root post's text produces zero tokens after preprocessing
- **THEN** the system SHALL NOT insert a row into topic_tokens

### Requirement: Topic tokens table schema
The system SHALL maintain a `topic_tokens` table with columns: post_uri (TEXT PRIMARY KEY), tokens (TEXT NOT NULL, JSON array), created_at (TEXT NOT NULL). An index SHALL exist on created_at for efficient range queries.

#### Scenario: Table created on first run
- **WHEN** the database is opened and migrations run
- **THEN** the topic_tokens table and index exist

### Requirement: Purge expired topic tokens
The system SHALL purge topic_tokens rows older than 26 hours during each topic analysis cycle. The purge cutoff is 26 hours (24-hour analysis window + 2-hour buffer).

#### Scenario: Tokens older than 26 hours are deleted
- **WHEN** the topic analysis cycle runs
- **THEN** all topic_tokens rows with created_at older than 26 hours ago are deleted

#### Scenario: Recent tokens are retained
- **WHEN** a token row has created_at within the last 26 hours
- **THEN** the row SHALL NOT be deleted by the purge
