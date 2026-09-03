---
name: software-testing-skill
description: End-to-end software quality and testing skill for Bzync projects. Use to design unit, integration, contract, database/migration, end-to-end, UI, accessibility, performance, resilience, security, regression and release-verification strategies; improve CI quality gates; diagnose flaky tests; and create risk-based test plans using the project's existing stack.
---

# Software Testing Skill

Testing exists to provide **decision confidence**, not maximize test count or coverage percentage.

## Start from risk

For each change ask:

```text
What could break?
Who/what would be harmed?
Which invariant must remain true?
At what layer can we detect the failure fastest and most reliably?
What production signal confirms reality after release?
```

## Test layers

Use the smallest reliable layer that proves the behavior, with additional end-to-end coverage for critical journeys.

- **unit** — pure/domain logic, validation, transformations;
- **component/module** — isolated service/UI component behavior;
- **integration** — DB, queue, filesystem, external adapter boundary;
- **contract** — API/event/provider contract compatibility;
- **migration/data** — forward migration, existing data, constraints, rollback/compatibility;
- **end-to-end** — critical user/business journey through real boundaries;
- **UI/visual** — interaction states and intentional visual regressions;
- **accessibility** — semantics, keyboard/focus, labels, contrast/tool checks + manual checks where needed;
- **performance** — latency, throughput, resource usage, load/soak where justified;
- **resilience** — retries, timeouts, dependency failure, failover/restore;
- **security** — authorization, abuse cases, scanners/manual tests according to risk;
- **acceptance/release** — business readiness and production smoke/canary validation.

Do not force a universal "test pyramid" ratio. Architecture and failure cost determine the balance.

## Database and migration testing

For schema/data migrations test:

- empty/new database where relevant;
- realistic existing data;
- null/legacy/edge values;
- constraints/index creation;
- backward compatibility during rolling deployment;
- performance/lock duration for material tables;
- rerun/idempotency expectations;
- rollback or forward-fix plan;
- old and new application versions when overlap occurs.

## External providers

Separate:

1. deterministic contract tests against your adapter;
2. provider sandbox/integration tests;
3. webhook/retry/idempotency tests;
4. limited production smoke/observability.

Never make the whole CI dependent on an unreliable external provider if a controlled contract boundary can cover most behavior.

## Financial/high-integrity workflows

For billing, accounting, provisioning or records systems test invariants such as:

- idempotent event handling;
- no duplicate charge/posting/provisioning;
- immutable/reversible history;
- correct rounding/currency units;
- reconciliation;
- authorization and tenant scope;
- retry/out-of-order event behavior;
- historical record reproducibility.

## Flaky tests

Flaky tests are defects in the quality system. Do not normalize endless retries. Quarantine only with an owner and deadline, identify root cause (time, race, shared state, network, order, random seed, environment), then fix or delete tests that cannot provide trustworthy signal.

## Coverage

Code coverage can expose untested areas but is not proof of behavior. Prefer critical invariant/decision-path coverage, mutation/property tests where valuable, and meaningful end-to-end journey coverage.

## CI quality gates

A gate should be:

- fast enough for its placement;
- deterministic enough to trust;
- tied to risk;
- actionable on failure;
- bypassable only through an explicit audited exception for high-impact gates.

Read `references/CI-QUALITY-GATES.md`.

## Production verification

Testing does not end at deployment. For risky releases define:

```text
pre-deploy checks
migration verification
smoke tests
health/readiness
key metrics/logs
canary/gradual rollout when available
customer/business invariant check
rollback trigger
post-release observation window
```

## Privacy/security in testing

- use synthetic/minimized test data;
- do not copy production personal data casually;
- protect test credentials/secrets;
- security tests must be authorized and scoped;
- test environments should not become an easier path to production data.

## Output

For a substantial test strategy provide:

```text
Risk/change scope
Critical invariants
Test matrix by layer
Fixtures/data/environment
Automation and CI placement
Security/accessibility/performance needs
Flake/isolation concerns
Release verification
Known gaps/residual risk
```

## Resources

- Read `references/TEST-STRATEGY.md` to build a risk matrix, `references/TEST-TYPES.md` for specialized test selection, `references/FLAKY-AND-DEFECTS.md` for unreliable tests and defect learning, and `references/CI-QUALITY-GATES.md` for CI placement.
- Use `templates/TEST-PLAN.md` for substantial changes and `templates/RELEASE-VERIFICATION.md` for deployment checks.
- Use `schemas/test-case.schema.json` when test cases need a machine-readable interchange format.
