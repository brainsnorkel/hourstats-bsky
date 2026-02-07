# yearly-charting Specification

## Purpose
TBD - created by archiving change document-existing-architecture. Update Purpose after archive.
## Requirements
### Requirement: Yearly Data Aggregation
The system SHALL retrieve and process a full year (365 days) of daily sentiment averages to generate long-term mood visualizations.

#### Scenario: 365-Day Data Fetch
- **WHEN** the yearly charting process is triggered
- **THEN** the system MUST retrieve all available daily sentiment records from the last 365 days
- **AND** ensure they are sorted in chronological order for accurate trend plotting.

### Requirement: Large-Format Visualization
The system SHALL generate a high-resolution 1500x1000 pixel image representing the 365-day sentiment trend with monthly markers and extreme value annotations.

#### Scenario: Monthly Marker Grid
- **WHEN** rendering the yearly chart
- **THEN** the system MUST draw vertical grid lines and month labels (e.g., "Jan", "Feb") at each month boundary within the data range.
- **AND** draw bi-weekly (14-day) ticks to provide additional temporal granularity.

#### Scenario: Title with Date Range
- **WHEN** finalizing the chart visualization
- **THEN** the system MUST include a prominent title (32pt font) containing the start and end dates of the charted period in YYYY-MM-DD format.

### Requirement: Colour-Blind Accessible Rendering
The system SHALL use a colour palette and visual encoding that remains distinguishable for readers with the most common forms of colour vision deficiency (protanopia, deuteranopia, tritanopia).

#### Scenario: Colour-Blind Safe Palette
- **WHEN** selecting colours for sentiment line segments, fills, and annotations
- **THEN** the system MUST avoid relying solely on red/green colour differences to convey meaning
- **AND** MUST use a palette that maintains sufficient contrast and distinguishability under simulated protanopia and deuteranopia conditions (e.g. blue/orange, or colours validated against the Okabe-Ito palette).

#### Scenario: Redundant Visual Encoding
- **WHEN** rendering the yearly chart
- **THEN** the system MUST NOT rely on colour alone to distinguish positive, negative, and neutral regions
- **AND** MUST provide at least one additional visual cue such as labelled watermarks, distinct line patterns, or spatial separation to reinforce meaning.

### Requirement: Event Contextualization
The system SHALL provide historical context for sentiment extremes by linking to relevant external event records.

#### Scenario: Wikipedia Event Linking
- **WHEN** identifying the highest and lowest sentiment days of the year
- **THEN** the system MUST generate Wikipedia "Events" links for those specific dates (e.g., "https://en.wikipedia.org/wiki/October_10")
- **AND** include these links as clickable AT Protocol facets in the summary text accompanying the chart.

### Requirement: Profile Presence
The system SHALL ensure the yearly trend chart remains highly visible to profile visitors by pinning it to the top of the account's feed.

#### Scenario: Automatic Profile Pinning
- **WHEN** the yearly sentiment chart is successfully posted to Bluesky
- **THEN** the system MUST immediately send a profile update request to the AT Protocol to pin the new post to the user's profile.

