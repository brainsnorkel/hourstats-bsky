# jetstream-consumer Specification

## Purpose
A goroutine within the main binary that ingests all Bluesky posts in real time via the Jetstream WebSocket API and buffers them in SQLite for consumption by the analysis pipeline. Replaces the search-API-based post collection with a push-based, complete data stream.

## Requirements

### Requirement: WebSocket Connection Management
The consumer SHALL maintain a persistent WebSocket connection to a Bluesky Jetstream endpoint, subscribing only to post creation events.

#### Scenario: Initial Connection
- **WHEN** the consumer starts as a goroutine
- **THEN** it MUST connect to `wss://jetstream2.us-east.bsky.network/subscribe?wantedCollections=app.bsky.feed.post`
- **AND** the `ConsumerConfig` MUST use callback functions (`OnPost`, `SaveCursor`, `LoadCursor`)
- **AND** if `LoadCursor` returns a cursor from the SQLite `key_value` table, it MUST include the `cursor` query parameter to resume
- **AND** log the connection establishment and cursor position using structured `slog`.

#### Scenario: Reconnection on Failure
- **WHEN** the WebSocket connection is closed or errors
- **THEN** the consumer MUST reconnect using exponential backoff (1s → 60s)
- **AND** resume from the last persisted cursor position
- **AND** log each reconnection attempt with the backoff delay.

#### Scenario: Cursor Persistence
- **WHEN** the consumer has processed events
- **THEN** it MUST persist the latest Jetstream sequence number to the SQLite `key_value` table (key: `jetstream_cursor`) every 10 seconds.

### Requirement: Post Ingestion and Buffering
The consumer SHALL parse incoming Jetstream events and write post data to the SQLite `post_buffer` table.

#### Scenario: Post Creation Event
- **WHEN** a Jetstream event is received with `kind="commit"` and `commit.operation="create"` and `commit.collection="app.bsky.feed.post"`
- **AND** the post has an English language tag (`lang=en`)
- **THEN** the consumer MUST extract: AT-URI, CID, text, author DID, and createdAt
- **AND** write the post to the SQLite `post_buffer` table
- **AND** ensure buffered posts older than 2 hours are periodically purged.

#### Scenario: Non-English or Non-Post Events
- **WHEN** a Jetstream event is received that is not a post creation or does not have `lang=en`
- **THEN** the consumer MUST discard the event without writing to SQLite.

#### Scenario: Malformed Events
- **WHEN** a Jetstream event cannot be parsed or is missing required fields
- **THEN** the consumer MUST log a warning and skip the event without crashing.

### Requirement: Operational Observability
The consumer SHALL use structured logging to enable monitoring.

#### Scenario: Logging
- **WHEN** the consumer is running
- **THEN** it MUST use `slog` JSON logging for all operational events (ingestion rates, errors, connection state).

#### Scenario: Stall Detection
- **WHEN** the consumer has not received a post for more than 5 minutes
- **THEN** it MUST log a warning indicating possible connection or API issues.

### Requirement: Graceful Shutdown
The consumer SHALL handle termination signals cleanly.

#### Scenario: Termination Signal
- **WHEN** the main binary receives a shutdown signal
- **THEN** the consumer MUST persist the current cursor position to SQLite
- **AND** close the WebSocket connection before exiting.

