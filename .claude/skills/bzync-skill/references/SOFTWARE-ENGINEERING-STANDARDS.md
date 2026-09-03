# Senior Software Engineering Standards

## Before coding

- Read nearby implementation and tests.
- Identify existing public contracts and behavior.
- Find validation, error and transaction conventions.
- Understand backward-compatibility requirements.

## Implementation

Prefer:

- explicit data flow;
- small cohesive functions/modules;
- domain rules outside presentation code;
- server-side authorization;
- deterministic migrations;
- idempotency where retries are possible;
- stable APIs with intentional versioning;
- errors that preserve useful context without leaking secrets.

Avoid:

- broad unrelated refactors;
- duplicate business rules;
- silent behavior changes;
- hidden global state;
- swallowing errors;
- irreversible schema changes without a migration plan.

## Verification

Choose the smallest meaningful set from:

- unit tests;
- integration tests;
- API/contract tests;
- migration tests;
- authorization tests;
- regression tests;
- type-check/lint/build;
- targeted manual flow validation.

Never claim a command/test passed unless it was actually run successfully.
