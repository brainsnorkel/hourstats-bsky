# Changelog

All notable changes to TrendJournal will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
