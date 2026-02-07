## ADDED Requirements

### Requirement: Trigger and Run Creation
The system SHALL support triggering analysis runs via EventBridge schedules or direct invocations, creating a unique run identity and time window for each new analysis cycle.

#### Scenario: EventBridge Scheduled Trigger
- **WHEN** an EventBridge schedule event is received with a set source and no run identity
- **THEN** the system MUST generate a new unique run identity
- **AND** calculate a UTC cutoff time based on the analysis interval (default 30 minutes)
- **AND** persist the new run state in the database.

### Requirement: Search and Pagination
The system SHALL retrieve posts from the Bluesky API within the defined time window using cursor-based pagination to ensure comprehensive data collection.

#### Scenario: Iterative Fetching
- **WHEN** starting a fetch operation
- **THEN** the system MUST iteratively request batches of posts from the Bluesky API using a cursor
- **AND** filter out posts that fall outside the defined time window
- **AND** stop when the oldest post in a batch is older than the cutoff time or no more pages are available.

### Requirement: Sequential Execution and Dispatch
The system SHALL orchestrate the pipeline by asynchronously dispatching the next processing stage only after successfully completing the data collection phase.

#### Scenario: Processor Dispatch
- **WHEN** all posts within the time window have been successfully fetched and stored
- **THEN** the system MUST update the run state to indicate fetching is complete
- **AND** asynchronously invoke the processor stage with the run identity.

### Requirement: Early Termination and Thresholds
The system SHALL implement safety limits to prevent execution timeouts and ensure sufficient data for analysis.

#### Scenario: Time-based Early Stop
- **WHEN** the fetching process exceeds 14 minutes of execution time
- **AND** at least 1000 posts have been collected
- **THEN** the system MUST stop fetching new posts and proceed to the dispatch phase to avoid Lambda timeout.

#### Scenario: Minimum Data Requirement
- **WHEN** fewer than 250 posts have been retrieved
- **THEN** the system SHALL attempt to continue fetching even if the API indicates no more pages, until the threshold is met or no further progress can be made.
