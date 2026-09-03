# Flaky Tests and Defect Learning

## Flake root causes

Common categories:

- clock/timezone;
- randomness without fixed seed;
- race/concurrency;
- shared mutable DB/files/cache;
- test order;
- external network/provider;
- slow/non-deterministic UI animation;
- eventual consistency without bounded polling;
- environment/resource pressure.

## Rules

- record frequency and affected suites;
- reproduce with captured seed/context;
- remove arbitrary sleeps; use condition-based waits with timeout;
- isolate mutable state;
- quarantine only temporarily with owner/deadline;
- do not hide flakes with unlimited retries.

## Production defects

Every meaningful escaped defect should ask whether the missing protection belongs in code invariant, unit/integration/contract/E2E test, observability, release process, or all of them.
