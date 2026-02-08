## MODIFIED Requirements

### Requirement: Trigger and Time Window
The system SHALL aggregate sentiment data exactly once per day, processing the full 24-hour window of the previous calendar day in UTC, and trigger the daily top-post quote-reply step after aggregation completes.

#### Scenario: Midnight UTC Trigger
- **WHEN** the aggregator is triggered by the 24-hour backup ticker
- **THEN** the system MUST calculate the start and end of the previous calendar day (00:00:00 to 23:59:59 UTC)
- **AND** retrieve all hourly sentiment data points that fall within that window.

#### Scenario: Daily Quote-Reply Trigger
- **WHEN** daily aggregation completes successfully
- **THEN** the system MUST invoke the daily top-post quote-reply step
- **AND** pass the Bluesky client credentials required for posting
