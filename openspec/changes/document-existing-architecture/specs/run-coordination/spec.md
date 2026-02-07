## ADDED Requirements

### Requirement: Centralized Run State
The system SHALL maintain a centralized state record for each analysis run to coordinate progress across distributed pipeline stages.

#### Scenario: Orchestrator Initialized Run
- **WHEN** a new analysis run begins
- **THEN** the system MUST create a "RunState" record in the database with a "postId" of "orchestrator" and "step" of "orchestrator".
- **AND** initialize the status to "initializing".
- **AND** set a TTL of 48 hours for automatic cleanup.

### Requirement: Step Tracking and Transitions
The system SHALL track the progression of each run through the pipeline stages by updating the step and status in the central state record.

#### Scenario: Fetcher Step Update
- **WHEN** the fetcher stage begins processing
- **THEN** the system MUST update the run state's "step" to "fetcher" and "status" to "fetching".

#### Scenario: Processing and Analysis Transitions
- **WHEN** data collection is complete
- **THEN** the system MUST update the status to "analyzed" and the "step" to "aggregator".
- **AND** store the final net sentiment percentage and the list of top posts in the run state.

### Requirement: Post Storage Coordination
The system SHALL store retrieved posts in batched containers to optimize database performance and facilitate retrieval by subsequent stages.

#### Scenario: Batched Post Storage
- **WHEN** the fetcher receives a batch of posts
- **THEN** the system MUST group them into containers of up to 100 posts
- **AND** store each container with a unique identifier linked to the current run identity.

### Requirement: Summary Metadata Preservation
The system SHALL store the identity of published platform posts to enable threaded interactions in subsequent stages.

#### Scenario: Summary Post URI Tracking
- **WHEN** the summary post is successfully published to the social platform
- **THEN** the system MUST record the post's URI and CID in the run state record.
- **AND** mark the run status as "completed".
