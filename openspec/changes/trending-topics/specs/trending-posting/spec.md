## ADDED Requirements

### Requirement: Post trending topics every 6 hours
The system SHALL post a standalone Bluesky post every 6 hours containing the bump chart image and a text listing of the top 5 trending topics. The post SHALL NOT be threaded to the existing 30-minute sentiment analysis posts.

#### Scenario: Trending post published on schedule
- **WHEN** the 6-hour trending ticker fires and TRENDING_ENABLED is true
- **THEN** the system generates a bump chart, formats the post text, and posts to Bluesky as a standalone post with the chart as an embedded image

#### Scenario: Trending feature disabled
- **WHEN** TRENDING_ENABLED is false or unset
- **THEN** no trending post is published and no topic analysis runs

### Requirement: Post text format with movement arrows
The post text SHALL follow this format: title line, blank line, 5 ranked topic lines with movement indicators, blank line, hashtags. Movement indicators SHALL show: (^N) for topics that rose N positions since last post, (vN) for topics that fell N positions, (->) for unchanged rank, (NEW) for topics not in the previous post's top 5. The ranking comparison is against the most recent previous trending post (6 hours ago), not the most recent 15-minute snapshot.

#### Scenario: Post text with movement arrows
- **WHEN** a trending post is generated and there is a previous trending post
- **THEN** each topic line includes a movement indicator comparing rank to the previous post

#### Scenario: First trending post (no previous)
- **WHEN** a trending post is generated and there is no previous trending post
- **THEN** all topics show (NEW) as their movement indicator

### Requirement: Hashtag facets
The post SHALL include #trending and #hourstatstrend as AT Protocol hashtag facets (RichtextFacet_Tag). Both hashtags SHALL appear on the last line of the post text. This allows users to mute the feature via Bluesky's mute-word functionality.

#### Scenario: Hashtags are proper facets
- **WHEN** a trending post is created
- **THEN** both #trending and #hourstatstrend have RichtextFacet_Tag facets with correct byte offsets

### Requirement: Exemplar post per topic
Each topic line in the trending post SHALL include a link to the highest-engagement exemplar post for that topic. At posting time, the system SHALL query topic_tokens for post URIs matching each topic's keyword+synonym set, sample up to 50 URIs per topic, batch-fetch their current engagement via the Bluesky FeedGetPosts API, and select the post with the highest engagement score (likes + reposts + replies). The exemplar's author handle SHALL appear on the topic line as a clickable mention.

#### Scenario: Exemplar post found for topic
- **WHEN** the trending post is generated and topic-matching posts exist in topic_tokens
- **THEN** each topic line includes the exemplar's author handle as a mention (e.g. "🔥 @handle.bsky.social")

#### Scenario: No matching posts found for topic
- **WHEN** a topic has no matching URIs in topic_tokens (edge case)
- **THEN** the topic line omits the exemplar mention and posts normally

#### Scenario: FeedGetPosts API failure
- **WHEN** the FeedGetPosts call fails for a batch of URIs
- **THEN** the system logs a warning and omits exemplars for affected topics without blocking the trending post

### Requirement: Alt text for chart image
The posted chart image SHALL include alt text describing the top 5 topics and their current ranks for accessibility. The alt text SHALL also include the exemplar author handle for each topic when available.

#### Scenario: Alt text describes rankings
- **WHEN** the bump chart is posted
- **THEN** the alt text includes "Bluesky trending topics" and lists the top 5 topics with their rank positions and exemplar handles
