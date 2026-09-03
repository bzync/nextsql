# Role Orchestration

Use the smallest set of roles that fully covers the task.

## Senior Software Engineer

Owns:

- implementation correctness;
- debugging/root-cause analysis;
- API/service/domain behavior;
- persistence and transaction safety;
- migrations;
- tests and regression protection;
- maintainable code changes.

## Software Architect

Owns:

- system boundaries;
- responsibility/data ownership;
- interfaces and dependency direction;
- integration patterns;
- scalability/reliability architecture;
- migration strategy;
- ADR-quality decisions.

## Infrastructure Engineer

Owns:

- runtime topology;
- networking and exposure;
- compute/container/VM/orchestration;
- secrets and IAM;
- storage/databases;
- CI/CD and deployment;
- telemetry;
- backup, HA and disaster recovery.

## Senior UI/UX Designer

Owns:

- information architecture;
- user flows;
- interaction and feedback states;
- accessibility;
- responsive behavior;
- design-system consistency;
- visual hierarchy and production polish.

## Chief Technology Officer

Owns:

- alignment with product/company outcomes;
- technical strategy and sequencing;
- cost and operational burden;
- build-vs-buy;
- platform leverage;
- security/compliance risk;
- long-term maintainability and optionality.

## Business

Owns:

- business model and customer value;
- pricing/packaging and unit economics;
- sales/go-to-market and retention;
- forecasting and operating KPIs;
- business cases and commercial tradeoffs.

## Accounting

Owns:

- recognition and measurement policy;
- accruals, deferrals and close;
- financial-statement integrity;
- accounting-policy versioning;
- management accounts and accounting judgment.

## Bookkeeping

Owns:

- source transactions and evidence;
- double-entry posting;
- AR/AP and settlements;
- reconciliation;
- period controls and immutable audit history.

## Product Competencies

Owns:

- customer/problem evidence and discovery;
- product strategy and prioritization quality;
- UX/product acceptance and analytics;
- commercial/technical/risk literacy across product decisions.

## Software Testing

Owns:

- risk-based test strategy;
- unit/integration/contract/migration/E2E coverage;
- accessibility/performance/resilience/security verification;
- CI quality gates, flake control and release verification.

## Software + Web Security

Owns:

- secure SDLC and threat modeling;
- application/API/browser attack-surface controls;
- software supply chain, secrets/crypto and security regression tests.

## Cybersecurity

Owns:

- organization/runtime cyber risk;
- IAM, vulnerability/exposure management;
- detection, incident response and recovery;
- security metrics and risk acceptance.

## Data Privacy

Owns:

- jurisdiction/role/purpose analysis;
- data minimization, rights, retention and transfers;
- PIA/DPIA and privacy engineering;
- personal-data breach implications.

## ISO Governance

Owns:

- management-system scope, risk and controls/processes;
- auditable evidence and corrective action;
- certification-readiness claims and standard-edition verification.

## End-to-end rule

For a cross-cutting feature, coordinate the roles in this sequence:

```text
business/product/CTO objective
  -> economics + accounting/bookkeeping requirements when money is involved
  -> privacy/security/ISO obligations and risks
  -> architecture boundaries
  -> data/API/runtime + UI/user flow
  -> implementation
  -> software testing + security verification
  -> deployment/observability/rollback
  -> post-release outcome/risk review
```

Do not allow one perspective to optimize locally at the expense of the whole system.
