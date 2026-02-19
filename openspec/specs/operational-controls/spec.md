# operational-controls Specification

## Purpose
TBD - created by archiving change document-existing-architecture. Update Purpose after archive.
## Requirements
### Requirement: Dry Run Capability
The system SHALL support a "dry run" mode to allow testing of the entire pipeline without publishing any content to the live platform.

#### Scenario: Dry Run Post Suppression
- **WHEN** an analysis or posting stage is triggered
- **AND** the global "dry_run" parameter in the configuration store is set to "true"
- **THEN** the system MUST proceed with data collection and analysis
- **AND** skip the final publication of any summary posts or charts to the social platform.

### Requirement: Data Quality Thresholds
The system SHALL implement minimum data thresholds to ensure that published sentiment analysis is statistically relevant.

#### Scenario: Insufficient Data Handling
- **WHEN** the total number of posts retrieved for an analysis window is below 1000
- **THEN** the system SHALL post an informational message explaining the data shortage instead of a sentiment summary.
- **AND** the sparkline generator SHALL post an informational message about building history if fewer than 2 data points are available for the trend chart.

### Requirement: Execution Safety Limits
The system SHALL implement time-based limits to prevent execution timeouts in serverless environments.

#### Scenario: Early Fetch Stop
- **WHEN** the data collection process exceeds 14 minutes of wall-clock time
- **AND** at least 1000 posts have been collected
- **THEN** the system MUST immediately stop fetching new posts and trigger the analysis stage with the data already collected.

### Requirement: Automatic Cleanup and Expiration
The system SHALL implement automatic expiration of transient operational data to manage storage costs and comply with data privacy best practices.

#### Scenario: Run State TTL
- **WHEN** a new run state or post batch record is created
- **THEN** the system MUST assign a TTL value exactly 48 hours in the future
- **AND** enable DynamoDB automatic deletion for those items.

### Requirement: Environment variable configuration
The system SHALL support the following environment variables for configuration. Existing variables (HOURSTATS_PROFILE, DATA_DIR, BLUESKY_HANDLE, BLUESKY_PASSWORD, DRY_RUN, ANALYSIS_INTERVAL_MINUTES, BACKUP_RETAIN_DAYS, GOOGLE_AI_API_KEY, TRENDING_ENABLED, TRENDING_INTERVAL, TRENDING_POST_HOURS) remain unchanged. New variable: HEALTH_CHART_HOURS (integer, default 24, minimum 1, maximum 48) controlling the default time window for health chart generation via the `/stats/health/chart` endpoint. New variable: HEALTH_CHART_MEMORY_LIMIT_MB (integer, default 1024) controlling the VM memory limit reference line drawn on the Memory + GC chart panel. Set to 0 to disable the reference line.

#### Scenario: Default health chart time window
- **WHEN** HEALTH_CHART_HOURS is not set
- **THEN** the `/stats/health/chart` endpoint uses a 24-hour default time window

#### Scenario: Custom health chart time window
- **WHEN** HEALTH_CHART_HOURS=12
- **THEN** the `/stats/health/chart` endpoint uses a 12-hour default time window (overridable by the `hours` query parameter)

#### Scenario: Health chart hours out of range
- **WHEN** HEALTH_CHART_HOURS=100
- **THEN** the system clamps the value to 48 hours and logs a warning at startup

#### Scenario: Invalid health chart hours
- **WHEN** HEALTH_CHART_HOURS is set to a non-integer value
- **THEN** the system logs a warning at startup and uses the default value of 24

#### Scenario: Default memory limit reference line
- **WHEN** HEALTH_CHART_MEMORY_LIMIT_MB is not set
- **THEN** the health chart Memory + GC panel draws a reference line at 1024MB

#### Scenario: Memory limit reference line disabled
- **WHEN** HEALTH_CHART_MEMORY_LIMIT_MB=0
- **THEN** the health chart Memory + GC panel does not draw a memory limit reference line

#### Scenario: Custom memory limit reference line
- **WHEN** HEALTH_CHART_MEMORY_LIMIT_MB=512
- **THEN** the health chart Memory + GC panel draws a reference line at 512MB

