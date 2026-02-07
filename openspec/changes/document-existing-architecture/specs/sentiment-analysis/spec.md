## ADDED Requirements

### Requirement: Sentiment Scoring and Categorization
The system SHALL analyze the sentiment of individual posts using VADER sentiment analysis to produce a compound score and assign a sentiment category.

#### Scenario: VADER Analysis
- **WHEN** a post is analyzed for sentiment
- **THEN** the system MUST calculate a compound sentiment score using the VADER algorithm
- **AND** categorize the sentiment as "positive" if the score is >= 0.3, "negative" if the score is <= -0.3, and "neutral" otherwise.

### Requirement: Keyword Fallback
The system SHALL provide a keyword-based fallback mechanism to improve sentiment detection when the primary automated analysis results in a neutral categorization.

#### Scenario: Keyword Sentiment Fallback
- **WHEN** the primary VADER analysis categorizes a post as "neutral"
- **AND** the post content contains more predefined positive words than negative words
- **THEN** the system MUST categorize the post sentiment as "positive".

### Requirement: Engagement Ranking
The system SHALL calculate an engagement score for each post to facilitate ranking and selection of top content.

#### Scenario: Engagement Score Calculation
- **WHEN** calculating the engagement for a post
- **THEN** the system MUST sum the total number of replies, likes, and reposts
- **AND** assign this sum as the engagement score for the post.

### Requirement: Topic Extraction
The system SHALL attempt to extract relevant topics from post content to provide additional context for analysis.

#### Scenario: Keyword and Hashtag Extraction
- **WHEN** extracting topics from post text
- **THEN** the system MUST identify and collect all hashtags
- **AND** identify predefined topic keywords present in the text
- **AND** return a unique list of identified topics.
