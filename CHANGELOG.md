# Changelog

All notable changes to Bluesky HourStats will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **CRITICAL**: Fixed batch overwrite bug in `AddPosts` function where `batchIndex` always started at 0, causing each fetcher iteration to overwrite previous batches. This resulted in only ~97 posts being stored instead of thousands (99% data loss). Fix queries existing batches to find highest index and starts new batches from next available index.
- **CRITICAL**: Fixed missing DynamoDB pagination in `GetAllPosts` causing only first page of results (~100 posts) to be retrieved instead of all posts (800+)
- Fixed missing pagination in `GetSentimentHistory` and `GetSentimentHistoryForRun` functions
- Added proper pagination handling using `LastEvaluatedKey` and `ExclusiveStartKey` for all DynamoDB queries and scans
- Added logging to track pagination progress (page count and items per page)
- Updated sentiment observations to use `TotalPostsRetrieved` instead of filtered post count for accurate reporting

### Technical Details
- DynamoDB Query/Scan operations return up to 1MB of data per request
- When results exceed 1MB, DynamoDB paginates results using `LastEvaluatedKey`
- Previously, only the first page was retrieved, causing ~90% of posts to be missing
- Fix implements proper pagination loop to retrieve all pages until `LastEvaluatedKey` is nil
- Fix affects: `GetAllPosts`, `GetSentimentHistory`, `GetSentimentHistoryForRun`

## [2025-01-05] - Yearly Sentiment Analysis Feature

### Added
- **Daily Sentiment Aggregation**: Automatic daily averaging of 30-minute sentiment data
- **Yearly Sentiment Charts**: Monthly charts showing 365 days of daily sentiment averages
- **Enhanced Sparkline Generator**: Yearly-specific visualization with 25% larger canvas (1500x1000)
- **Month Markers**: Horizontal axis shows month boundaries for yearly charts
- **3-Year Data Retention**: Daily sentiment data preserved for long-term analysis
- **New Lambda Functions**: `hourstats-daily-aggregator` and `hourstats-yearly-poster`
- **New DynamoDB Table**: `hourstats-daily-sentiment` with optimized indexes
- **EventBridge Scheduling**: Automated daily (midnight UTC) and monthly (1st at 1:00 AM UTC) triggers
- **Comprehensive Testing**: Unit tests for daily sentiment and yearly sparkline functionality
- **Design Documentation**: Complete technical specification in `docs/YEARLY_SENTIMENT_DESIGN.md`

### Technical Details
- **Daily Aggregation**: Calculates average, minimum, and maximum sentiment from 24 hours of 30-minute runs
- **Yearly Visualization**: Gaussian trend lines, average sentiment line, and sentiment zone watermarks
- **Data Quality**: Handles missing data gracefully with comprehensive error handling
- **Cost Impact**: ~$4.50-8.50/month additional AWS costs
- **Accessibility**: Comprehensive alt text for yearly charts
- **Fallback Handling**: Graceful degradation when insufficient data is available

## [2025-09-10] - Comprehensive Watermark System for Sparkline

### Added
- **Sentiment Zone Watermarks**: "Positive", "Negative", and "Neutral" watermarks in their respective zones
- **Branding Watermark**: "@hourstats.bsky.social" in bottom left corner for consistent attribution
- **Multiline Extreme Labels**: Latest, lowest, and highest observations with timestamps on new lines
- **Responsive Watermark Sizing**: Font sizes adapt to chart dimensions automatically
- **Smart Watermark Positioning**: Watermarks only appear when zones are large enough

### Changed
- **Enhanced Label Format**: Timestamps now appear on separate lines without "UTC" text
- **Darkened Watermarks**: Increased opacity for positive and negative watermarks for better visibility
- **Improved Visual Hierarchy**: Better organization of chart elements with proper drawing order

### Technical
- **New Watermark Functions**: `drawSentimentWatermarks()`, `drawBrandingWatermark()`, `drawNeutralWatermark()`
- **Conditional Rendering**: Smart logic to show watermarks only when appropriate
- **Color Coordination**: Watermarks match sentiment zone colors for visual consistency

## [2025-09-10] - Enhanced Sparkline Visualization

### Added
- **Day Markers on X-Axis**: Added day names (Mon, Tue, Wed, etc.) for midnight UTC positions
- **Ultra-Light Neutral Zone**: Added subtle background area for -10% to +10% sentiment range
- **Average Line**: Dark grey dotted horizontal line showing calculated average sentiment
- **0% Y-Axis Line**: Added 0% reference line for better sentiment context
- **Comprehensive Labels**: Added labels for latest, lowest, and highest observations
- **Smart Label Positioning**: Labels positioned to avoid overlaps and provide clear context

### Changed
- **Line Width**: Reduced sentiment line width to 50% for cleaner appearance
- **Title**: Updated to "Compound Bluesky Sentiment (UTC)" for consistency
- **Grid Lines**: Removed black borders from neutral zone area for cleaner look

### Technical
- **Enhanced drawLabels()**: Added multiple new label drawing functions
- **Improved Grid Drawing**: Smart filtering to avoid lines in neutral zone
- **Better Visual Hierarchy**: Optimized drawing order for clean appearance

## [2025-09-06] - Cursor Limit Detection & Production Optimization

### Added
- **Cursor Pagination Limit Detection**: Enhanced system to detect and handle Bluesky API pagination limits gracefully
- **Graceful Error Handling**: HTTP 400 InvalidRequest errors at high cursor values (>10,000) now stop gracefully instead of failing
- **Production Schedule Update**: Changed from 60-minute to 15-minute intervals for more frequent updates

### Fixed
- **Cursor Pagination Failures**: System no longer fails when hitting Bluesky API pagination boundaries
- **Error Recovery**: Improved error handling to continue with available data when hitting limits
- **Production Frequency**: More frequent updates every 15 minutes instead of hourly

### Changed
- **Error Detection Logic**: Added intelligent cursor limit detection based on cursor value thresholds
- **Parallel Fetching**: Enhanced parallel API calls to handle cursor limits gracefully
- **Production Schedule**: EventBridge now triggers every 15 minutes with 15-minute analysis intervals
- **Lambda Deployment**: All Lambda functions redeployed with improved error handling

### Technical Details
- Cursor limit detection triggers when cursor values exceed 10,000
- System logs cursor pagination limits and continues with collected data
- Enhanced debugging shows detailed error information for HTTP 400 responses
- Production schedule: `rate(15 minutes)` with `analysisIntervalMinutes: 15`

## [2025-09-06] - Adult Content Filtering & DynamoDB Permissions Fix

### Fixed
- **DynamoDB Permissions**: Fixed missing `dynamodb:BatchWriteItem` permission causing CloudWatch errors
- **Adult Content Filtering**: Added comprehensive adult content filtering based on Bluesky moderation labels
- **Content Safety**: Posts with labels (porn, sexual, nudity, graphic-media) are now properly filtered out

### Changed
- Updated IAM policy to include `dynamodb:BatchWriteItem` permission for Lambda functions
- Enhanced both `GetTrendingPostsBatch` and `GetTrendingPosts` functions with adult content filtering
- Improved content safety by filtering out inappropriate content before analysis

### Technical Details
- Added `hasAdultContentLabel` function to check moderation labels
- Filtering occurs at the API response level before post processing
- Logs filtered posts for monitoring and debugging
- Maintains performance while ensuring content safety

## [2025-09-06] - Embed Cards Implementation

### Added
- **Embed Cards**: Implemented rich embed cards for top posts in Bluesky summaries
- **CID Extraction**: Added Content Identifier (CID) extraction from Bluesky API responses
- **CID Storage**: Added CID field to all Post structs across the application
- **Embed Card Function**: Created `createEmbedCard` function for generating embed cards
- **Rich Post Display**: Posts now display as embedded cards with full content preview

### Changed
- Updated all Post structs in client, state, analyzer, and formatter packages to include CID field
- Modified all conversion functions between different Post types to preserve CID data
- Enabled embed card creation in PostTrendingSummary function
- Updated local-test to properly handle CID data through the entire pipeline

### Technical Details
- CIDs are extracted from `postView.Cid` field in Bluesky API responses
- CID data flows through: API → Client → State → Analyzer → Formatter → Storage → Embed Cards
- Embed cards use `atproto.RepoStrongRef` with URI and CID for proper display
- DynamoDB automatically stores CID data in nested Post objects (no schema change needed)
- Embed cards are created for the first valid post with both URI and CID

## [2025-09-06] - Sentiment Calculation and Link Fixes

### Fixed
- **CRITICAL**: Fixed sentiment calculation to use all posts instead of just top 5 posts
- Fixed clickable links by using web URLs in facets instead of AT Protocol URIs
- Fixed PostTrendingSummary function signature across all components
- Removed unused functions and imports to fix linter warnings

### Changed
- Updated post format to show "Bluesky mood +X%" with net sentiment calculation
- Improved sentiment accuracy by calculating from all analyzed posts
- Enhanced user experience with properly working clickable links

### Technical
- Updated internal/lambda/handler.go to calculate sentiment from all analyzed posts
- Updated internal/scheduler/scheduler.go to calculate sentiment from all analyzed posts
- Updated cmd/lambda-poster/main.go to fetch all posts and calculate sentiment percentages
- Simplified strings.TrimPrefix usage to fix gosimple linter warnings
- Removed unused functions: deduplicatePostsByAuthor, createLinkFacets, createEmbedCard, convertATURItoWebURL

## [2025-01-27] - Critical Bug Fix and Architecture Improvements

### Fixed
- **CRITICAL**: Fixed GitHub Actions deployment failure caused by missing golangci-lint installation
- Fixed undefined `deduplicatePostsByURI` function error in query-runs utility
- Added missing deduplication function to query-runs/main.go for consistency with other components

## [2025-01-27] - Critical Bug Fix and Architecture Improvements

### Fixed
- **CRITICAL**: Fixed fetcher cursor bug where subsequent fetchers would restart from beginning instead of continuing
- Added cursor parameter to FetcherEvent to support proper pagination continuation
- Fetcher now correctly passes cursor to next fetcher in the chain

### Added
- Query utility (`cmd/query-runs/main.go`) for testing and debugging previous runs
- Shared formatter package (`internal/formatter/post_formatter.go`) for consistent post generation
- Sentiment indicators (+/-/x) after each top 5 post in generated content
- `scripts/query-runs.sh` wrapper script for easy utility access
- Run statistics methods in state manager (`ListRuns`, `GetRunStats`)

### Changed
- Unified post formatting across all components (query utility, processor, Bluesky client)
- Removed Step Functions dependency, now uses direct Lambda invocation
- Updated README with new architecture and query utility instructions
- All post content now generated using the same shared formatter code

### Removed
- Step Functions state machine and related IAM roles/policies
- EventBridge Step Functions integration (now directly invokes orchestrator)

## [Previous Releases]

### Added
- **Engagement scores**: Display total engagement (likes + reposts + replies) next to each handle
- **Project rename**: Renamed from TrendJournal to Bluesky HourStats
- New post format: "For {time} Bluesky was {sentiment}" with numbered handle links
- Handle-based post links: "@handle.who .wrote.postX" format with clickable handles
- Initial project setup with Go module
- AT Protocol/Bluesky client integration using indigo library
- Sentiment analysis using GoVader library (now uses actual post content)
- Topic extraction from hashtags and keywords
- Configurable analysis intervals (minutes instead of hours)
- Public post search with pagination (searches all public posts, not just followed accounts)
- Time-filtered analysis (only considers posts from analysis interval)
- Smart posting (skips when no posts found)
- Web-friendly URL conversion for proper link rendering
- Proper Bluesky rich text facets for clickable links (based on official documentation)
- Dry-run mode for safe testing
- Secure configuration management with YAML files
- Comprehensive test suite
- Makefile for build automation
- Documentation and testing guides
- Dynamic time formatting in posts ("X minutes" vs "1 hour")
- Actual post text extraction from AT Protocol records
- Engagement score display (total of replies + likes + reposts)
- **Emotion-based sentiment analysis**: Counts positive/negative/neutral posts to determine overall sentiment
- **Enhanced emotions list**: 10+ emotions per category (positive, negative, neutral) with intensity-based selection
- **Keyword-based sentiment fallback**: Additional sentiment analysis using keyword matching for better accuracy
- **Sentiment distribution logging**: Debug logging shows count and percentage of each sentiment type
- **Adult content filtering**: Uses Bluesky's official moderation labels to filter out inappropriate content
- **Bluesky moderation integration**: Subscribes to official Bluesky moderation labeler for content filtering

### Changed
- Analysis interval changed from hours to minutes for more frequent updates
- Post fetching changed from timeline to public search API with full pagination
- Post ranking now uses total engagement (replies + likes + reposts)
- Configuration system updated to use YAML files instead of environment variables
- Post format updated to remove numbering and include clean URLs with handles
- Post header now dynamically shows time period ("Top five posts in the last X minutes/1 hour")
- Sentiment analysis now uses actual post content instead of placeholder text
- Rich text facets implemented according to Bluesky documentation for proper link rendering
- **Sentiment analysis approach**: Now counts positive/negative/neutral posts instead of averaging scores
- **Emotion selection**: Intensity-based emotion selection based on sentiment dominance percentage
- **Sentiment thresholds**: Adjusted VADER thresholds (0.2/-0.2) for better emotion detection

### Deprecated
- N/A

### Removed
- Post numbering (1., 2., etc.) for cleaner appearance
- Engagement breakdown (now shows total engagement score)
- Fixed timestamp from post header
- Quote posting functionality (PostQuotePost function)
- Quote post integration from scheduler
- Dual posting format (reverted to single summary post only)
- Custom adult content filtering (replaced with Bluesky official labels)

### Fixed
- Fixed AT Protocol URI to web URL conversion for proper link rendering
- Fixed sentiment analysis thresholds for more accurate classification
- Fixed topic extraction to handle punctuation and extract keyword equivalents
- Fixed type conversion issues between client and analyzer packages
- Fixed API search to retrieve all public posts using pagination instead of timeline
- Fixed rich text facets for proper clickable links on Bluesky
- Fixed post text extraction from AT Protocol records
- Fixed datetime parameter issues with search API (removed problematic 'since' parameter)
- Fixed client-side time filtering for accurate post selection
- Fixed quote posting removal syntax errors
- Fixed adult content filtering to use official Bluesky moderation labels
- Fixed duplicate posts appearing in summaries (added deduplication by URI)
- Fixed post truncation to ensure all 5 posts are displayed properly
- Fixed API rate limiting issues with retry logic and reduced post limits
- Fixed time interval display to show correct minutes instead of hours
- **Fixed legacy code issues**: Removed old post format fallback code that was causing "Top five posts" format
- **Fixed sentiment analysis accuracy**: Combined VADER and keyword-based analysis for better emotion detection
- **Fixed emotion selection logic**: Now properly selects emotions based on sentiment dominance rather than simple majority

### Security
- Added secure credential management with git-ignored config files
- Added setup script for safe configuration file creation

## [0.1.0] - 2024-01-04

### Added
- Initial release
- Basic project structure
- Core functionality implementation
- Test suite
- Documentation

### Features
- **Authentication**: Secure login to Bluesky using app passwords
- **Post Fetching**: Retrieve posts from user timeline
- **Sentiment Analysis**: Analyze post sentiment using VADER algorithm
- **Topic Extraction**: Extract topics from hashtags and keywords
- **Engagement Scoring**: Calculate engagement scores based on likes, reposts, replies
- **Scheduling**: Run analysis every hour
- **Logging**: Comprehensive logging for monitoring and debugging

### Technical Details
- **Language**: Go 1.25+
- **Dependencies**: 
  - github.com/bluesky-social/indigo (AT Protocol client)
  - github.com/jonreiter/govader (Sentiment analysis)
- **Architecture**: Modular design with separate packages for client, analyzer, and scheduler
- **Testing**: Unit tests for all major components

### Known Issues
- Post text extraction from AT Protocol records not fully implemented (placeholder text used)
- No actual posting to Bluesky implemented (dry run mode only)
- Basic trending algorithm (no sophisticated trending detection)
- No configuration file support (environment variables only)

### Future Enhancements
- [ ] Implement actual post text extraction
- [ ] Add real posting functionality
- [ ] Implement sophisticated trending algorithms
- [ ] Add configuration file support
- [ ] Add cloud deployment configuration
- [ ] Add more comprehensive error handling
- [ ] Add metrics and monitoring
- [ ] Add support for different feed algorithms
