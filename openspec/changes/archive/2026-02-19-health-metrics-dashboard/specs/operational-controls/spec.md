## MODIFIED Requirements

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
