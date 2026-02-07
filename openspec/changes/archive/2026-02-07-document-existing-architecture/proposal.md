## Why

HourStats has no formal specification of its behavior. The architecture is documented in ARCHITECTURE.md but there are no testable specs defining what each component does, its contracts, or its failure modes. Reverse-engineering specs from the existing codebase creates a living specification that can validate future changes and serve as onboarding documentation.

## What Changes

- Create OpenSpec specifications for each major capability of the system
- No code changes — this is documentation of existing behavior
- Specs become the source of truth for "what should this system do?"

## Capabilities

### New Capabilities
- `post-fetching`: Bluesky post collection via AT Protocol search API with cursor-based pagination, time filtering, and early-stop behavior
- `sentiment-analysis`: VADER-based sentiment scoring, engagement ranking, top-N selection, and mood word mapping
- `summary-posting`: Formatting and posting sentiment summaries to Bluesky with facets, character limits, and adult content filtering
- `sparkline-generation`: 7-day sentiment trend chart generation and posting as reply to summary
- `daily-aggregation`: 24-hour sentiment data rollup into daily min/max/average summaries
- `yearly-charting`: 365-day trend chart generation with month markers, Wikipedia event links, and profile pinning
- `run-coordination`: DynamoDB-based state machine for pipeline coordination between Lambda functions
- `operational-controls`: Kill switch (dry run), minimum post threshold, early stop, TTL cleanup

### Modified Capabilities

## Impact

- `openspec/specs/` — new spec files for each capability above
- No changes to application code, infrastructure, or deployment
