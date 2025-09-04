# Changelog

All notable changes to TrendJournal will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial project setup with Go module
- AT Protocol/Bluesky client integration using indigo library
- Sentiment analysis using GoVader library
- Topic extraction from hashtags and keywords
- Hourly scheduling mechanism
- Comprehensive test suite
- Makefile for build automation
- Configuration management
- Documentation and testing guides

### Changed
- N/A

### Deprecated
- N/A

### Removed
- N/A

### Fixed
- N/A

### Security
- N/A

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
