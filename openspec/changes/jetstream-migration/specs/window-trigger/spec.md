# window-trigger Specification

## Purpose
A Lambda function triggered every 30 minutes that closes the current Jetstream buffer window, hydrates engagement metrics, re-keys posts for the processor, and dispatches the downstream pipeline. Replaces the search-API-based fetcher while preserving the identical run coordination contract.

## Requirements

### Requirement: Run Creation
The window trigger SHALL create a new analysis run with the same schema and semantics as the current fetcher.

#### Scenario: EventBridge Scheduled Trigger
- **WHEN** an EventBridge schedule event is received
- **THEN** the system MUST generate a unique run identity (`run-<timestamp>`)
- **AND** calculate a UTC cutoff time (now minus 30 minutes)
- **AND** persist a RunState record in DynamoDB with `postId="orchestrator"` and `step="orchestrator"`
- **AND** set status to "initializing".

### Requirement: Buffer Window Query
The window trigger SHALL read posts from the Jetstream buffer partition that fall within the analysis time window.

#### Scenario: Query Buffered Posts
- **WHEN** a run has been created with a cutoff time
- **THEN** the system MUST query DynamoDB for items with `PK="window-buffer"` and SK in the range `[<cutoff-minute>, <now-minute>]`
- **AND** handle DynamoDB pagination to retrieve all matching items
- **AND** log the total number of buffered posts found.

#### Scenario: Empty or Insufficient Buffer
- **WHEN** the buffer query returns fewer than 250 posts
- **THEN** the system MUST fall back to the search-API-based fetching logic using `GetTrendingPostsBatch`
- **AND** log that it is using the search API fallback with the reason (consumer may be down).

### Requirement: Engagement Hydration
The window trigger SHALL retrieve current engagement metrics for buffered posts, since Jetstream delivers posts with zero engagement at creation time.

#### Scenario: Batch Engagement Lookup
- **WHEN** buffered posts have been read from DynamoDB
- **THEN** the system MUST call `app.bsky.feed.getPosts` in batches of up to 25 AT-URIs per request
- **AND** update each post's likes, reposts, replies, and author handle from the API response
- **AND** run up to 10 concurrent batch requests with rate limiting (max 3,000 requests per 5 minutes)
- **AND** log progress every 500 posts hydrated.

#### Scenario: Hydration Failure for Individual Posts
- **WHEN** a post cannot be hydrated (deleted, private, or API error)
- **THEN** the system MUST retain the post with `likes=0, reposts=0, replies=0`
- **AND** use the DID as the author value
- **AND** log a warning for the failed URI
- **AND** NOT fail the entire run.

### Requirement: Post Re-keying
The window trigger SHALL write hydrated posts into the run's DynamoDB partition using the same `PostBatch` format the processor already reads.

#### Scenario: Batch Write
- **WHEN** posts have been hydrated with engagement metrics
- **THEN** the system MUST group posts into `PostBatch` items of up to 100 posts each
- **AND** write them to DynamoDB with `PK=<runId>` and `SK="<runId>#batch<N>"`
- **AND** set the `step` to "fetcher" (matching the existing contract)
- **AND** update the RunState with `totalPostsRetrieved` and `status="fetching"`.

### Requirement: Processor Dispatch
The window trigger SHALL dispatch the processor Lambda after completing data preparation, using the same contract as the current fetcher.

#### Scenario: Successful Dispatch
- **WHEN** all posts have been re-keyed under the run identity
- **THEN** the system MUST update the RunState cursor to indicate fetching is complete
- **AND** store API stats (total posts, earliest/latest timestamps)
- **AND** asynchronously invoke `hourstats-processor` with payload `{ "runId": "<runId>" }`
- **AND** log the dispatch.

### Requirement: Adult Content Filtering
The window trigger SHALL filter posts with adult content labels, consistent with the current fetcher behavior.

#### Scenario: Labeled Post
- **WHEN** a hydrated post has moderation labels matching `["porn", "sexual", "nudity", "graphic-media"]`
- **THEN** the system MUST exclude the post from the run's post collection
- **AND** log the filtered URI.

### Requirement: Time Budget
The window trigger SHALL complete within the Lambda timeout while leaving time for the processor.

#### Scenario: Execution Time Limit
- **WHEN** the window trigger has been running for 12 minutes
- **AND** the hydration step is still in progress
- **THEN** the system MUST stop hydrating further posts
- **AND** proceed with the posts hydrated so far
- **AND** log a warning with the count of unhydrated posts.
