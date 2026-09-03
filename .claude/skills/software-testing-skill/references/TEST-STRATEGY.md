# Test Strategy

## Risk-based matrix

| Behavior/risk | Unit | Integration | Contract | E2E | Non-functional | Production verification |
|---|---|---|---|---|---|---|

Do not duplicate every assertion at every layer. Put detailed edge cases low in the stack and reserve end-to-end tests for journeys/integration confidence.

## Testability as architecture feedback

If a core business rule can only be tested through a fragile browser test, the architecture may have hidden business logic in the UI. If authorization cannot be tested without a full deployment, security boundaries may be too implicit. Use test difficulty as design feedback.

## Fixtures

- deterministic clocks/IDs/randomness where possible;
- explicit factories/builders;
- minimal fixtures per behavior;
- no hidden dependency on prior tests;
- cleanup/isolation for mutable integration state;
- seeded data versioned with the schema when material.
