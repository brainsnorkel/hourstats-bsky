## ADDED Requirements

### Requirement: Trigger and Time Window
The system SHALL aggregate sentiment data exactly once per day, processing the full 24-hour window of the previous calendar day in UTC.

#### Scenario: Midnight UTC Trigger
- **WHEN** the aggregator is triggered by an EventBridge schedule at midnight UTC
- **THEN** the system MUST calculate the start and end of the previous calendar day (00:00:00 to 23:59:59 UTC)
- **AND** retrieve all hourly sentiment data points that fall within that window.

### Requirement: Statistics Calculation
The system SHALL calculate summary statistics representing the collective mood and activity volume for the aggregated period.

#### Scenario: Aggregation Statistics
- **WHEN** processing the 24-hour sentiment history
- **THEN** the system MUST calculate the arithmetic mean of all net sentiment percentages as the daily average.
- **AND** identify the absolute minimum and maximum sentiment percentages observed during the period.
- **AND** sum the total number of analysis runs and total posts retrieved for the day.

### Requirement: Idempotent Storage
The system SHALL ensure that daily aggregation for a specific date is performed only once to prevent duplicate entries in the historical record.

#### Scenario: Existing Record Check
- **WHEN** starting an aggregation for a target date
- **THEN** the system MUST check if a record already exists for that date in the daily sentiment table.
- **AND** stop processing if a record exists to maintain data integrity.

### Requirement: Long-term Retention
The system SHALL persist daily aggregated data with a retention policy suitable for multi-year trend analysis.

#### Scenario: TTL Configuration
- **WHEN** storing a new daily sentiment record
- **THEN** the system MUST calculate and assign a DynamoDB TTL value exactly 3 years (1095 days) into the future from the record's creation time.
