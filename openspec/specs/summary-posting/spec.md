# summary-posting Specification

## Purpose
TBD - created by archiving change document-existing-architecture. Update Purpose after archive.
## Requirements
### Requirement: Top Content Selection
The system SHALL select the most influential posts for inclusion in the periodic summary based on calculated engagement metrics.

#### Scenario: Top 5 Post Selection
- **WHEN** multiple analyzed posts are available for a run
- **THEN** the system MUST rank the posts by engagement score in descending order
- **AND** select exactly the top 5 unique posts for the summary.

### Requirement: Summary Content Formatting
The system SHALL generate a concise summary of community mood including sentiment indicators and a descriptive mood hashtag.

#### Scenario: Mood Hashtag Generation
- **WHEN** formatting the summary content
- **THEN** the system MUST calculate the net sentiment percentage (average compound score * 100)
- **AND** select a descriptive word from a 100-word calibrated vocabulary based on defined sentiment tiers
- **AND** prepend the word with a '#' character as a hashtag.

#### Scenario: Post List Formatting
- **WHEN** formatting the list of top posts
- **THEN** the system MUST include the author's handle and a sentiment indicator for each post
- **AND** use '+' for positive, '-' for negative, and 'x' for neutral sentiment.

### Requirement: Character Limit and Facet Creation
The system SHALL ensure summary posts comply with Bluesky platform constraints while maintaining rich interactivity through clickable links.

#### Scenario: Link Facet Creation
- **WHEN** preparing a summary post for publication
- **THEN** the system MUST identify author handles in the text
- **AND** create AT Protocol facets to make those handles clickable links to the respective posts.
- **AND** ensure the total character count does not exceed 300 characters.

### Requirement: Search Latency Reporting
The system SHALL provide transparency regarding data availability issues when the platform's search results are delayed or insufficient.

#### Scenario: Latency Informational Post
- **WHEN** the number of retrieved posts for the analysis window is below the 1000 post threshold
- **THEN** the system MUST post an informational message explaining that there were too few posts to calculate sentiment
- **AND** include details about the total posts retrieved and the timestamp of the most recent post found by the search.

