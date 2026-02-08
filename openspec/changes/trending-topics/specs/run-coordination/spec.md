## MODIFIED Requirements

### Requirement: Main loop scheduler manages all tickers
The main loop SHALL manage the following tickers in its select loop: analysis ticker (30 minutes, existing), backup ticker (24 hours, existing), topic analysis ticker (15 minutes, new), and trending post ticker (6 hours, new). The topic analysis and trending post tickers SHALL only be created when TRENDING_ENABLED is true. All tickers SHALL be properly stopped on shutdown via defer.

#### Scenario: Trending tickers active when enabled
- **WHEN** the bot starts with TRENDING_ENABLED=true
- **THEN** the main loop select handles cases for all four tickers

#### Scenario: Trending tickers absent when disabled
- **WHEN** the bot starts with TRENDING_ENABLED=false or unset
- **THEN** only the existing analysis and backup tickers are active; no topic analysis or trending post cycles run

#### Scenario: Graceful shutdown stops all tickers
- **WHEN** the bot receives SIGTERM or SIGINT
- **THEN** all active tickers (including trending tickers if enabled) are stopped before exit
