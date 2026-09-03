# End-to-End Delivery Standard

For non-trivial work use this lifecycle proportionally.

## 1. Discover

Confirm current state, desired outcome, users, constraints, affected contracts and repository evidence.

## 2. Design

Define data, domain ownership, API/events, authorization, UI states, runtime needs, telemetry, migration and rollback.

## 3. Implement vertically

Prefer an exercisable slice:

```text
data/schema
 -> domain/service
 -> API/integration
 -> UI/client
 -> tests
 -> telemetry
 -> deployment/config
```

## 4. Verify

Test the important success, failure, authorization and migration paths. Run only commands relevant to the change and report actual outcomes.

## 5. Release

Plan ordering, backwards compatibility, health checks, staged rollout if needed, monitoring and rollback.

## 6. Operate

Ensure maintainers can diagnose failures and recover safely. Update project docs/instructions when the source of truth changes.

## Definition of done

A production change should be able to answer:

- Does the real user flow work?
- Is data correct and migratable?
- Are authorization and validation enforced?
- Are failures understandable/recoverable?
- Are important paths tested?
- Can it be deployed and rolled back?
- Can operators observe it?
- Does the UI follow the project's design system and accessibility expectations?
- Is the architecture still coherent?
