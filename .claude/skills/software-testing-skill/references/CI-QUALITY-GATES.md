# CI Quality Gates

## Suggested stages

### Pull request / fast
- format/lint/type/compile;
- unit/component tests;
- focused integration tests;
- changed-code security/dependency/secret checks as available.

### Merge/main
- broader integration/contract tests;
- migration tests;
- build artifact verification;
- selected E2E.

### Pre-release
- full critical E2E;
- security/accessibility checks appropriate to risk;
- performance/resilience checks for material changes;
- deployment/migration rehearsal where warranted.

### Post-deploy
- smoke + health;
- migration/schema verification;
- business/technical metrics;
- canary/rollback criteria.

Adapt this to repository size and delivery model. Do not make developers wait for expensive irrelevant tests on every small change.
