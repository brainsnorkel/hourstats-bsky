## ADDED Requirements

### Requirement: Persistent Key-Value Storage
The system SHALL provide a generic key-value store for persisting operational state across process restarts and daily cycles.

#### Scenario: Store and retrieve a key-value pair
- **WHEN** a component stores a value under a string key
- **THEN** the system MUST persist the key and value in the `key_value` table
- **AND** a subsequent retrieval by the same key MUST return the stored value

#### Scenario: Upsert semantics
- **WHEN** a value is stored under a key that already exists
- **THEN** the system MUST replace the existing value with the new value
- **AND** update the `updated_at` timestamp

### Requirement: Daily Top Post Identification
The system SHALL identify the single highest-engagement post from all analysis runs of the previous calendar day (UTC).

#### Scenario: Find top post from yesterday's runs
- **WHEN** the daily quote-reply step executes
- **THEN** the system MUST query all runs with `created_at` within yesterday's UTC date boundaries
- **AND** parse the `top_posts` JSON from each run
- **AND** select the single post with the highest engagement score across all runs
- **AND** return that post's URI, CID, author handle, and engagement score

#### Scenario: No runs from yesterday
- **WHEN** no runs exist for yesterday's date
- **THEN** the system MUST skip the daily quote-reply step
- **AND** log a warning indicating no runs were found

#### Scenario: Top post has empty URI or CID
- **WHEN** the highest-engagement post has an empty URI or CID
- **THEN** the system MUST skip the daily quote-reply step
- **AND** log a warning indicating the top post lacks required identifiers

### Requirement: Quote-Embed Reply Posting
The system SHALL post a reply to the yearly chart thread that quotes yesterday's top post using the AT Protocol `app.bsky.embed.record` mechanism.

#### Scenario: Successful quote-reply post
- **WHEN** the yearly post URI/CID is available and a valid top post is identified
- **THEN** the system MUST create a post with both a `Reply` reference (root and parent pointing to the yearly post) and an `Embed.EmbedRecord` referencing the top post
- **AND** the reply text MUST include the date and the author handle of the quoted post

#### Scenario: Yearly post URI not available
- **WHEN** no yearly post URI/CID is stored in the key-value table
- **THEN** the system MUST skip the daily quote-reply step
- **AND** log an informational message that no yearly post is available to reply to

#### Scenario: AT Protocol error during posting
- **WHEN** the reply post fails due to an AT Protocol error (e.g., deleted yearly post, network failure)
- **THEN** the system MUST log the error as a warning
- **AND** continue normal operation without retrying

### Requirement: Daily Idempotency
The system SHALL ensure the daily quote-reply is posted at most once per calendar day.

#### Scenario: First execution of the day
- **WHEN** the daily quote-reply step runs and the stored `daily_quote_last_date` key does not match today's UTC date
- **THEN** the system MUST proceed with posting the quote-reply
- **AND** update `daily_quote_last_date` to today's UTC date after successful posting

#### Scenario: Repeated execution on same day
- **WHEN** the daily quote-reply step runs and the stored `daily_quote_last_date` key matches today's UTC date
- **THEN** the system MUST skip posting
- **AND** log that the daily quote has already been posted

### Requirement: Flat Thread Structure
The system SHALL maintain a flat thread under the yearly chart by setting both root and parent references to the yearly post for every daily reply.

#### Scenario: Reply references point to yearly post
- **WHEN** creating a daily quote-reply post
- **THEN** the `Reply.Root` URI/CID MUST be the yearly chart post's URI/CID
- **AND** the `Reply.Parent` URI/CID MUST also be the yearly chart post's URI/CID
