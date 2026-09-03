---
name: software-architect
description: "Design and evolve software architecture for production systems: domain boundaries, modularity, service decomposition, APIs/events, data ownership, scalability, reliability, integrations, migrations, ADRs, and technical-debt management. Use for architecture decisions, system decomposition, major features, platform evolution, or modernization."
---

# Software Architect

Architecture is the set of important decisions that are expensive to change. Keep reversible choices lightweight.

## Principles

- Start from domain boundaries and quality attributes, not fashionable topology.
- A modular monolith is valid when it satisfies scale/team needs.
- Split services only when independent ownership/deployment/scaling or isolation justifies distributed complexity.
- Define a single source of truth for important data.
- Prefer explicit contracts between modules/services.
- Design for evolutionary migration rather than big-bang replacement.
- Make failure domains and trust boundaries visible.

## Required architecture questions

For material decisions determine:

- What problem/quality attribute drives this decision?
- What is the current architecture?
- What are the constraints?
- What alternatives are viable?
- What becomes harder with each alternative?
- How will data and clients migrate?
- How will we observe failure?
- How reversible is the choice?

## Boundary design

Avoid splitting by technical layer only. Prefer cohesive domains with clear ownership:

```text
bounded domain
  ├─ application/use cases
  ├─ domain rules
  ├─ persistence adapters
  └─ external contracts
```

Cross-domain access should go through deliberate contracts rather than arbitrary table access.

## Data architecture

Define:

- authoritative owner;
- consistency requirement;
- transaction boundary;
- replication/read-model behavior;
- retention/history;
- migration strategy.

Do not use events merely to avoid defining ownership.

## Architecture records

For high-impact choices create or update an ADR with:

```text
Context
Decision
Alternatives
Consequences
Migration/Rollback
Status
```

See `references/ARCHITECTURE-REVIEW.md`.
