# analysis-cycle Specification

## Purpose
A function triggered every 30 minutes by a wall-clock aligned ticker within the main binary that reads buffered posts from SQLite, hydrates engagement metrics, analyzes sentiment, posts a summary to Bluesky, and generates chart replies. Replaces the Lambda-based window trigger and processor dispatch.

## Requirements

### Requirement: Cycle Triggering
The analysis cycle SHALL be triggered every 30 minutes (aligned to :00 and :30) by an in-process ticker.

#### Scenario: Wall-Clock Aligned Trigger
- **WHEN** the wall clock reaches a 30-minute boundary
- **THEN** the system MUST initiate the analysis cycle
- **AND** generate a unique run identity (`run-<timestamp>`)
- **AND** calculate a UTC cutoff time (now minus 30 minutes)
- **AND** create a record in the SQLite `runs` table with status "initializing".

### Requirement: Buffer Retrieval
The analysis cycle SHALL read posts from the SQLite `post_buffer` that fall within the analysis time window.

#### Scenario: Fetch Buffered Posts
- **WHEN** a cycle starts
- **THEN** the system MUST call `db.GetPostsSince(ctx, cutoff)` to retrieve all posts from SQLite buffered since the last cycle
- **AND** log the total number of buffered posts retrieved.

### Requirement: Engagement Hydration
The analysis cycle SHALL retrieve current engagement metrics for buffered posts via the `internal/hydrator` package.

#### Scenario: Batch Engagement Lookup
- **WHEN** buffered posts have been retrieved
- **THEN** the system MUST call `app.bsky.feed.getPosts` in batches of up to 25 AT-URIs per request
- **AND** update each post's likes, reposts, and replies from the API response
- **AND** log progress every 500 posts hydrated.

#### Scenario: Hydration Failure for Individual Posts
- **WHEN** a post cannot be hydrated (deleted, private, or API error)
- **THEN** the system MUST retain the post with existing data or zeroed metrics
- **AND** NOT fail the entire analysis cycle.

### Requirement: Analysis and Posting
The analysis cycle SHALL perform sentiment analysis and post results to Bluesky in a single sequential flow.

#### Scenario: Sequential Execution
- **WHEN** hydration is complete
- **THEN** the system MUST perform sentiment analysis (VADER) on all hydrated posts
- **AND** rank the top 5 posts by engagement
- **AND** post the summary to Bluesky
- **AND** generate and post the sentiment sparkline chart as a reply.

### Requirement: Adult Content Filtering
The analysis cycle SHALL filter posts with adult content labels.

#### Scenario: Labeled Post
- **WHEN** a hydrated post has moderation labels matching `["porn", "sexual", "nudity", "graphic-media"]`
- **THEN** the system MUST exclude the post from the analysis
- **AND** log the filtered URI.

### Requirement: Time Management
The analysis cycle SHALL complete within a reasonable time to ensure it does not overlap with the next cycle.

#### Scenario: Execution Timeout
- **WHEN** the cycle has been running for more than 15 minutes
- **THEN** it MUST log a critical warning
- **AND** ensure all resources are released.

