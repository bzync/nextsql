# Senior Code Review Guide

## Correctness
- Does behavior match the requirement and existing contracts?
- Are boundary/null/empty/error cases correct?
- Are transactions and state transitions safe?
- Could retries duplicate effects?

## Security
- Is untrusted input validated?
- Is authorization enforced at the server/domain boundary?
- Are secrets/sensitive values excluded from logs and responses?
- Are dynamic SQL, shell, file, URL and template operations safe?

## Maintainability
- Is responsibility in the right module?
- Is logic duplicated?
- Are names and types domain-meaningful?
- Is abstraction earning its complexity?

## Performance
- N+1 queries?
- Unbounded lists or memory use?
- Missing indexes?
- Hot-path blocking/network calls?
- Cache invalidation correctness?

## Tests
- Would tests fail for the bug/behavior being changed?
- Are assertions externally meaningful?
- Are critical negative cases covered?
