## MODIFIED Requirements

### Requirement: Environment variable configuration
The system SHALL support the following environment variables for configuration. Existing variables (HOURSTATS_PROFILE, DATA_DIR, BLUESKY_HANDLE, BLUESKY_PASSWORD, DRY_RUN, ANALYSIS_INTERVAL_MINUTES, BACKUP_RETAIN_DAYS) remain unchanged. New variables: GOOGLE_AI_API_KEY (required when TRENDING_ENABLED is true, Gemini API key), TRENDING_ENABLED (boolean, default false, gates all trending functionality), TRENDING_INTERVAL (integer, default 15, topic analysis interval in minutes), TRENDING_POST_HOURS (integer, default 6, trending post frequency in hours).

#### Scenario: Trending feature requires API key
- **WHEN** TRENDING_ENABLED is true and GOOGLE_AI_API_KEY is empty
- **THEN** the system SHALL log an error and disable the trending feature (as if TRENDING_ENABLED=false), without affecting the rest of the bot

#### Scenario: Trending feature disabled by default
- **WHEN** TRENDING_ENABLED is not set
- **THEN** the trending feature is disabled; no topic analysis or trending posts occur

#### Scenario: Custom intervals respected
- **WHEN** TRENDING_INTERVAL=10 and TRENDING_POST_HOURS=4
- **THEN** topic analysis runs every 10 minutes and trending posts publish every 4 hours

#### Scenario: DRY_RUN applies to trending posts
- **WHEN** DRY_RUN is true and TRENDING_ENABLED is true
- **THEN** topic analysis runs normally but trending posts are logged instead of published to Bluesky
