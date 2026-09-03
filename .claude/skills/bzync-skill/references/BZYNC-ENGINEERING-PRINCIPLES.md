# Bzync Engineering Principles

These principles guide Bzync Software Development Services projects without assuming a specific product stack.

## 1. Repository truth before preference

Inspect the real project before prescribing architecture or tooling. Existing interfaces, data contracts and operational paths are constraints unless the task explicitly changes them.

## 2. Production quality is cross-layer

A feature is not production-ready only because the happy path works. Consider data, authorization, validation, errors, tests, UI states, deployment, observability, migration and rollback.

## 3. Preserve capability deliberately

Refactors and migrations should preserve existing behavior unless replacement/removal is part of the requirement. Breaking changes must be explicit, scoped and migrated.

## 4. Prefer explicit boundaries

Make ownership of business rules, data, APIs, infrastructure and UI responsibilities discoverable. Avoid hidden coupling and duplicated sources of truth.

## 5. Security and privacy are architecture concerns

Use least privilege, server-side authorization, secret isolation, safe defaults, auditable changes and appropriate data retention. Do not bolt security on after feature completion.

## 6. Design for operations

Critical systems need health checks, actionable logs, useful metrics, backups where state exists, tested recovery and a rollback path.

## 7. UI should look intentionally designed

Avoid generic AI-generated visual patterns and decoration without product value. Use hierarchy, typography, spacing, interaction states, accessibility and the project's design system to support real workflows.

## 8. Optimize complexity for the present with an upgrade path

Do not introduce distributed systems, HA topology or framework churn without a concrete requirement. Keep future evolution possible through explicit contracts and migrations.

## 9. Decisions should be reversible when possible

Prefer incremental, observable changes. For difficult-to-reverse decisions, document rationale, alternatives, risks, migration and revisit triggers.

## 10. Evidence over confidence

If something has not been inspected, executed or verified, say so. Never turn an assumption into a repository fact.
