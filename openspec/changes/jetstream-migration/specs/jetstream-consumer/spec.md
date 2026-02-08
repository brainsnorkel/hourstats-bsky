# jetstream-consumer Specification

## Purpose
A persistent service that ingests all Bluesky posts in real time via the Jetstream WebSocket API and buffers them in DynamoDB for consumption by the analysis pipeline. Replaces the search-API-based post collection with a push-based, complete data stream.

## Requirements

### Requirement: WebSocket Connection Management
The consumer SHALL maintain a persistent WebSocket connection to a Bluesky Jetstream endpoint, subscribing only to post creation events.

#### Scenario: Initial Connection
- **WHEN** the consumer starts
- **THEN** it MUST connect to `wss://jetstream2.us-east.bsky.network/subscribe?wantedCollections=app.bsky.feed.post`
- **AND** if a stored cursor exists in DynamoDB, it MUST include the `cursor` query parameter to resume from the last processed position
- **AND** log the connection establishment and cursor position.

#### Scenario: Reconnection on Failure
- **WHEN** the WebSocket connection is closed or errors
- **THEN** the consumer MUST reconnect using exponential backoff (1s, 2s, 4s, 8s, max 30s)
- **AND** resume from the last persisted cursor position
- **AND** log each reconnection attempt with the backoff delay.

#### Scenario: Cursor Persistence
- **WHEN** the consumer has processed events
- **THEN** it MUST persist the latest Jetstream sequence number to DynamoDB every 10 seconds
- **AND** use the key pattern `PK="jetstream-cursor", SK="latest"`.

### Requirement: Post Ingestion and Buffering
The consumer SHALL parse incoming Jetstream events and write post data to a DynamoDB buffer partition for downstream consumption.

#### Scenario: Post Creation Event
- **WHEN** a Jetstream event is received with `kind="commit"` and `commit.operation="create"` and `commit.collection="app.bsky.feed.post"`
- **THEN** the consumer MUST extract: AT-URI (constructed from DID + rkey), CID, text, author DID, and createdAt
- **AND** write the post to DynamoDB with `PK="window-buffer"` and `SK="<ISO-minute>#<CID>"`
- **AND** set a TTL of 2 hours from write time.

#### Scenario: Non-Post Events
- **WHEN** a Jetstream event is received that is not a post creation (e.g., likes, follows, deletes, updates)
- **THEN** the consumer MUST discard the event without writing to DynamoDB.

#### Scenario: Malformed Events
- **WHEN** a Jetstream event cannot be parsed or is missing required fields
- **THEN** the consumer MUST log a warning and skip the event without crashing.

### Requirement: Operational Observability
The consumer SHALL emit metrics and logs to enable monitoring and alerting.

#### Scenario: Metrics Emission
- **WHEN** the consumer is running
- **THEN** it MUST emit CloudWatch metrics every 60 seconds:
  - `PostsIngested` (count per minute)
  - `ConnectionStatus` (1 = connected, 0 = disconnected)
  - `WriteErrors` (count per minute)
  - `EventsDiscarded` (non-post events per minute)

#### Scenario: Health Signal
- **WHEN** the consumer has not written a post to DynamoDB for more than 2 minutes
- **THEN** it MUST log a warning indicating possible connection or API issues.

### Requirement: Graceful Shutdown
The consumer SHALL handle termination signals cleanly to minimize data loss.

#### Scenario: SIGTERM Received
- **WHEN** the consumer receives a SIGTERM signal (ECS task stop)
- **THEN** it MUST persist the current cursor position to DynamoDB
- **AND** close the WebSocket connection
- **AND** exit within 30 seconds (ECS stop timeout).
