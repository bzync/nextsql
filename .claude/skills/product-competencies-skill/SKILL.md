---
name: product-competencies-skill
description: Product-development competency framework for Bzync Software Development Services. Use to assess or improve product managers, product teams, founders and cross-functional delivery across strategy, discovery, user research, prioritization, UX, analytics, commercial thinking, technical literacy, security/privacy, delivery, operations, experimentation and leadership.
---

# Product Competencies Skill

Use this skill to improve **product capability**, not to create vanity scorecards.

## Competency domains

1. **Customer and problem understanding** — research, jobs/problems, segmentation, evidence.
2. **Product strategy** — vision, positioning, strategic choices, constraints and sequencing.
3. **Discovery** — hypotheses, prototypes, validation, risk reduction before costly build.
4. **Product design/UX** — flows, usability, accessibility, information architecture and design quality.
5. **Prioritization and decision quality** — value/risk/effort/opportunity cost, explicit tradeoffs.
6. **Delivery** — requirements, cross-functional coordination, scope control, release readiness.
7. **Data and analytics** — event/data quality, funnels, cohorts, causal caution, decision metrics.
8. **Commercial competence** — pricing, packaging, unit economics, sales/GTM and retention.
9. **Technical literacy** — architecture/API/data/infrastructure constraints without micromanaging implementation.
10. **Security/privacy/compliance** — identify product risks early and include them in acceptance criteria.
11. **Operations/reliability** — supportability, observability, incidents, rollout/rollback and lifecycle.
12. **Leadership/communication** — written decisions, stakeholder alignment, ownership and learning culture.

## Maturity levels

Use evidence-based levels:

```text
1 — Aware: knows concepts but needs direction
2 — Practicing: executes common cases with support
3 — Independent: owns normal product work end to end
4 — Leading: handles ambiguity, mentors others, improves system/team
5 — Strategic: shapes company/product capability and repeatable operating model
```

Do not promote a person based on tenure or vocabulary alone. Require observable outcomes and repeatable behavior.

## Product requirement quality

A strong product requirement states:

```text
problem/user
outcome
why now
constraints/non-goals
success/guardrail metrics
critical user flows/states
business rules
security/privacy/compliance needs
analytics/observability
rollout/rollback/migration implications
open risks/assumptions
```

Avoid prescribing internal implementation unless it is itself a product/architectural constraint.

## Product review

For major features use this sequence:

```text
problem evidence
 -> target user + desired outcome
 -> alternatives / why build
 -> UX flow
 -> economics/business rules
 -> technical/security/privacy feasibility
 -> success + guardrail metrics
 -> rollout + support/operations
 -> learning plan
```

## Relationship to Bzync roles

- `business-skill`: economics, pricing and market model;
- `senior-ui-ux-designer`: interaction/design quality;
- `chief-technology-officer`: technology/product strategy and investment;
- `bzync-end-to-end-engineer`: execution;
- privacy/security/testing skills: risk and quality gates.

## Competency assessment output

For each domain record:

```text
current level
evidence
strengths
gaps
next-level behavior
practice/project to demonstrate it
measure/review date
```

Use `references/COMPETENCY-MODEL.md` to select domains, `references/MATURITY-RUBRIC.md` to rate evidence, and `references/PRODUCT-REVIEW.md` for product reviews. Record assessments with `templates/PRODUCT-COMPETENCY-ASSESSMENT.md` or `schemas/competency-assessment.schema.json`.
