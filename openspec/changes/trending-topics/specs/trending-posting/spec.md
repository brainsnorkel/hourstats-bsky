## MODIFIED Requirements

### Requirement: Post trending topics every 6 hours
The system SHALL post a standalone text-only Bluesky post every 6 hours listing the top 5 trending topics. The post SHALL NOT be threaded to the existing 30-minute sentiment analysis posts. The post SHALL NOT include a bump chart image.

#### Scenario: Trending post published on schedule
- **WHEN** the 6-hour trending ticker fires and TRENDING_ENABLED is true
- **THEN** the system formats the post text and posts to Bluesky as a standalone text post

#### Scenario: Trending feature disabled
- **WHEN** TRENDING_ENABLED is false or unset
- **THEN** no trending post is published and no topic analysis runs

### Requirement: Post text format
The post text SHALL follow this format: title line ("Topics"), blank line, 5 ranked topic lines, blank line, hashtag. Each topic line SHALL show: rank number, period, topic label, and exemplar author handle (e.g., "1. Bad Bunny @handle.bsky.social"). No movement indicators are included. If the post exceeds the 300-grapheme limit, exemplar mentions SHALL be dropped from the bottom up (lower-ranked topics lose their exemplar first) until the post fits.

#### Scenario: Post text formatted
- **WHEN** a trending post is generated
- **THEN** each topic line shows rank, label, and exemplar handle without movement indicators

### Requirement: Hashtag facets
The post SHALL include #hstrend as an AT Protocol hashtag facet (RichtextFacet_Tag) on the last line of the post text. This allows users to mute the feature via Bluesky's mute-word functionality.

#### Scenario: Hashtag is proper facet
- **WHEN** a trending post is created
- **THEN** #hstrend has a RichtextFacet_Tag facet with correct byte offsets

### Requirement: Exemplar post per topic
Each topic line in the trending post SHALL include a link to the highest-engagement exemplar post for that topic. The exemplar's author handle SHALL appear on the topic line as a clickable AT Protocol link facet pointing to the exemplar post's URL (not the author's profile). Handle deduplication ensures each topic gets a unique exemplar author.

#### Scenario: Exemplar post found for topic
- **WHEN** the trending post is generated and exemplar candidates exist
- **THEN** each topic line includes the exemplar's author handle as a link facet (e.g. "@handle.bsky.social" linking to the post URL)

#### Scenario: No matching posts found for topic
- **WHEN** a topic has no exemplar candidates
- **THEN** the topic line shows only the rank and label without an exemplar mention

### Requirement: Alt text for accessibility
The system SHALL generate alt text describing the top 5 topics and their current ranks. The alt text SHALL also include the exemplar author handle for each topic when available. Format: "Topics: 1. Label (top post by @handle), 2. Label (top post by @handle), ...".

#### Scenario: Alt text describes rankings
- **WHEN** a trending post is created
- **THEN** the alt text lists the top 5 topics with their rank positions and exemplar handles
