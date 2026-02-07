# sparkline-generation Specification

## Purpose
TBD - created by archiving change document-existing-architecture. Update Purpose after archive.
## Requirements
### Requirement: Historical Data Retrieval
The system SHALL retrieve exactly 7 days of sentiment history from the persistent storage to provide context for the trend visualization.

#### Scenario: 7-Day Window Query
- **WHEN** the sparkline generation process is triggered
- **THEN** the system MUST query the sentiment history table for all data points within a 168-hour window relative to the current time.

### Requirement: Chart Visualization
The system SHALL generate a 1200x800 pixel image representing the 7-day sentiment trend with color-coded segments and smoothed trend lines.

#### Scenario: Color-Coded Segments
- **WHEN** drawing the sentiment line segments
- **THEN** the system MUST use green for segments where sentiment is > 10%
- **AND** red for segments where sentiment is < -10%
- **AND** gray for segments within the neutral (-10% to +10%) range.

#### Scenario: Gaussian Smoothing
- **WHEN** calculating the trend visualization
- **THEN** the system MUST apply a Gaussian smoothing algorithm (sigma=4.0) to the raw sentiment points
- **AND** overlay a blue dashed trend line on the chart.

### Requirement: Image Annotation and Watermarks
The system SHALL include informative labels, watermarks, and statistics on the generated chart to aid interpretation.

#### Scenario: Sentiment Watermarks
- **WHEN** rendering the chart background
- **THEN** the system MUST draw "Positive" and "Negative" watermarks in the respective regions of the chart.
- **AND** draw a "Neutral" watermark within the center gray zone.

#### Scenario: Branding Watermark
- **WHEN** finalizing the chart image
- **THEN** the system MUST draw the handle "@hourstats.bsky.social" in the bottom-left corner.

### Requirement: Thread Integration
The system SHALL post the generated trend chart as a reply to the primary summary post to maintain a threaded context for users.

#### Scenario: Threaded Sparkline Post
- **WHEN** the sparkline image is successfully generated
- **AND** a valid parent post URI and CID are available from the current run state
- **THEN** the system MUST upload the image as a blob to the Bluesky service
- **AND** create a new post containing the image as a direct reply to the summary post.

### Requirement: Automatic Alt-Text Generation
The system SHALL generate comprehensive descriptive text for the chart to ensure accessibility for all users.

#### Scenario: Detailed Alt-Text
- **WHEN** preparing the chart post
- **THEN** the system MUST calculate current, highest, lowest, and average sentiment values for the 7-day period
- **AND** include these specific values and their timestamps in the image's alt-text metadata.

