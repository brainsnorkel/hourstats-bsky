## Context

HourStats is a production Bluesky sentiment analysis bot running as 5 AWS Lambda functions orchestrated by EventBridge and DynamoDB state. The system has been operating for months with no formal specification. This change reverse-engineers specs from the existing codebase to establish a living specification baseline.

The architecture was recently simplified from 10 Lambda functions to 5 by removing dead code (Step Functions leftovers) and merging the orchestrator into the fetcher.

## Goals / Non-Goals

**Goals:**
- Document all existing behavior as testable WHEN/THEN specifications
- Cover the 8 capabilities identified in the proposal
- Create specs that can validate future changes against expected behavior
- Serve as onboarding documentation for new contributors

**Non-Goals:**
- Proposing architectural changes (this documents what IS, not what SHOULD BE)
- Writing integration tests (specs inform tests but don't replace them)
- Documenting internal implementation details (specs cover external behavior and contracts)

## Decisions

### Decision 1: One spec file per capability

Each of the 8 capabilities gets its own `specs/<name>/spec.md`. This maps cleanly to the Lambda function boundaries and keeps specs focused. Alternative considered: one monolithic spec — rejected because it would be unwieldy and hard to reference.

### Decision 2: Specs describe observable behavior, not implementation

Specs use WHEN/THEN format describing inputs and outputs, not internal algorithms. For example, "WHEN fetcher receives EventBridge trigger THEN it creates a run in DynamoDB" rather than "fetcher calls state.CreateRun with PostID='orchestrator'". Implementation details belong in ARCHITECTURE.md.

### Decision 3: DynamoDB key format documented as spec

The `PostID: "orchestrator"` and `Step: "orchestrator"` values in DynamoDB are load-bearing contracts between Lambda functions. These are documented in the `run-coordination` spec because changing them would break the pipeline.

## Risks / Trade-offs

- **Specs may drift from code**: Specs are only accurate at time of writing. Mitigation: use `/opsx:verify` periodically and update specs when code changes.
- **Over-specification**: Too much detail makes specs brittle. Mitigation: focus on contracts between components, not internal logic.
- **Reverse-engineering may miss edge cases**: Code inspection doesn't capture all runtime behavior. Mitigation: supplement with CloudWatch log analysis and manual testing over time.
