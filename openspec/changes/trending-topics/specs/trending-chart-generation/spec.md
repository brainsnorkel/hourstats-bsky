## MODIFIED Requirements

### Requirement: Bump chart showing topic rank trajectories (internal only)
The system SHALL generate a 1200x800 PNG bump chart showing the rank positions (#1 through #5) of trending topics over the past 24 hours. The chart is generated for internal reference and snapshot storage but is **not included** in the posted trending topics content — posts are text-only. The Y-axis SHALL be inverted (rank #1 at top, #5 at bottom). The X-axis SHALL show 24 hours in UTC with 6-hour markers. Each topic SHALL be drawn as a Gaussian-smoothed line in a distinct colour from the Okabe-Ito palette (colour-blind safe). Topic labels SHALL appear at the right end of each line.

#### Scenario: Chart generated with 24h of snapshots
- **WHEN** topic_snapshots contains data spanning at least 6 hours
- **THEN** a 1200x800 PNG bump chart is generated with rank lines for each topic

#### Scenario: Insufficient snapshot data
- **WHEN** topic_snapshots contains fewer than 2 distinct snapshot_time values
- **THEN** chart generation SHALL be skipped and the system logs an info message

### Requirement: Topic entry and exit animations
Topics entering the top 5 SHALL have their line start from below the #5 rank position (bottom edge). Topics leaving the top 5 SHALL have their line end below the #5 rank position. This visually communicates topics rising into and falling out of the rankings.

#### Scenario: New topic enters top 5
- **WHEN** a topic appears in a snapshot but not the previous snapshot
- **THEN** its line starts from below rank #5, rising to its entry rank

#### Scenario: Topic exits top 5
- **WHEN** a topic appears in a snapshot but not the next snapshot
- **THEN** its line descends below rank #5 at the exit point

### Requirement: Chart branding and styling
The chart SHALL include the "@hourstats.bsky.social" branding watermark in the bottom-left corner (matching existing chart style). The title SHALL read "Bluesky Trending Topics (24h)". The background SHALL be light gray (matching existing charts). Gray dashed lines SHALL appear at each rank boundary (#1 through #5).

#### Scenario: Chart includes branding
- **WHEN** a bump chart is generated
- **THEN** the chart includes the branding watermark, title, and rank boundary lines

### Requirement: Colour palette
The system SHALL assign colours to topics in a stable manner — a topic with the same topic_id SHALL keep its colour across chart regenerations within the same 24-hour window. The palette SHALL use the Okabe-Ito colour-blind safe set: Blue (#0072B2), Vermillion (#D55E00), Bluish Green (#009E73), Orange (#E69F00), Reddish Purple (#CC79A7), with overflow colours Sky Blue (#56B4E9) and Yellow (#F0E442).

#### Scenario: Topic retains colour across charts
- **WHEN** a topic appears in consecutive 6-hour charts
- **THEN** it uses the same colour in both charts
