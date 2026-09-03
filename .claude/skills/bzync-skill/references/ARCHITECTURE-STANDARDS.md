# Software Architecture Standards

## Start from quality attributes

Architecture decisions should name the driver: maintainability, security, latency, availability, independent deployment, data consistency, isolation, compliance, cost or team ownership.

## Boundaries

For important domains define:

- authoritative owner;
- public interface;
- data store/transaction boundary;
- dependencies;
- failure behavior;
- observability.

Avoid direct cross-domain table access when a deliberate contract is required.

## Service decomposition

Do not split into services merely because the project is growing. Distributed systems add networking, consistency, deployment and observability complexity. Split when ownership, scaling, failure isolation or deployment independence justifies it.

## Data

For stateful changes define:

- source of truth;
- consistency expectation;
- history/retention;
- migration and rollback;
- backup/recovery impact.

## High-impact decisions

Create an ADR or equivalent for choices involving new persistent stores, externally visible contracts, major runtime changes, public exposure, irreversible migrations, cross-region behavior or significant vendor lock-in.
