---
name: bzync-end-to-end-engineer
description: Bzync Software Development Services end-to-end engineering orchestrator combining senior software engineering, architecture, infrastructure, UI/UX, CTO governance, product competency, software testing, cybersecurity, web/software security, privacy/ISO governance, and business/accounting/bookkeeping review when relevant. Use for major features, greenfield systems, migrations, platform work, production-readiness reviews, or any Bzync project task spanning multiple technical layers.
---

# Bzync End-to-End Engineer

Deliver production software as one coherent system rather than disconnected frontend, backend and infrastructure tasks.

## Start with project evidence

If `bzync-project` exists, read it first. Otherwise use `bzync-skill` to inspect/generate project context before making material architectural assumptions.

## Core engineering lenses

1. **Senior Software Engineer** — correctness, maintainability, APIs, data, tests and compatibility.
2. **Software Architect** — boundaries, ownership, contracts, scale, reliability and migration.
3. **Infrastructure Engineer** — runtime, network, security, deployability, observability and recovery.
4. **Senior UI/UX Designer** — user flow, accessibility, responsive behavior, states and product-quality interface design.
5. **Chief Technology Officer** — value, risk, sequencing, cost, technical leverage and long-term maintainability.
6. **Product Competencies** — problem evidence, product strategy, discovery, prioritization, UX/product quality and measurement.
7. **Software Testing** — risk-based verification, CI gates, E2E/non-functional tests and release confidence.
8. **Software Security** — threat modeling, secure SDLC, supply chain, secrets/crypto and code security.
9. **Web Security** — web/API/browser attack surface and authorization/session/input/outbound controls.
10. **Cybersecurity** — organization/runtime cyber risk, detection, incident response and recovery.
11. **Data Privacy** — applicable Philippine/global privacy roles, purposes, rights, retention, transfers, PIA/DPIA and breach duties.
12. **ISO Governance** — management-system controls/evidence when standards, audits or certification goals apply.

## Business and finance lenses when relevant

For work involving pricing, billing, payments, invoices, subscriptions, refunds, revenue/cost, financial reports, or commercial commitments also use:

13. **Business** — customer value, pricing, unit economics, forecasting, operations and commercial outcomes.
14. **Accounting** — recognition, accrual/deferral, reporting, close and accounting-policy integrity.
15. **Bookkeeping** — source transactions, posting, settlement, reconciliation, period controls and auditability.

## Delivery sequence

```text
repository discovery
 -> outcome + constraints + product evidence
 -> business/financial requirements when relevant
 -> privacy/security/ISO risk and obligations
 -> architecture/data/API + UI/user-flow design
 -> implementation
 -> software testing + security verification
 -> infrastructure/deployment
 -> observability/rollback + post-release checks
 -> documentation/operational handoff
```

## Cross-layer invariants

- server-side authorization is authoritative;
- data/schema changes have migration and rollback consideration;
- APIs are explicit contracts;
- UI exposes real system states rather than hiding failures;
- infrastructure uses the project's established runtime unless intentionally changed;
- secrets never enter source control;
- production-impacting changes have health/telemetry/rollback paths;
- capability is preserved unless removal/replacement is explicitly required;
- monetary workflows remain traceable from customer/business event through ledger/reconciliation when applicable.
- personal-data changes define purpose, role, minimization, retention, rights and incident implications;
- security requirements are testable and tied to threat/risk, not only scanners;
- testing covers critical invariants and migration/release risk;
- ISO/compliance claims are backed by operating evidence and current standard/regulator verification;
- product changes have explicit user/problem evidence and measurable outcomes where material.

## Completion output

For substantial tasks make the result traceable:

```text
Goal
Observed project context
Architecture decisions
Data/API changes
UI/UX changes
Infrastructure/operations changes
Business/accounting/bookkeeping impact (when applicable)
Implementation
Verification performed
Migration/release/rollback
Risks or deferred work
```

Keep the ceremony proportional to the task.

See `references/END-TO-END-CHECKLIST.md`.
