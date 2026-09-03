---
name: global-data-privacy-skill
description: Global privacy engineering and jurisdiction-resolution skill for Bzync products. Use for GDPR, CCPA/CPRA, Singapore PDPA, Brazil LGPD, India DPDP and other privacy regimes, cross-border processing, controller/processor roles, privacy notices, rights requests, DPIAs, vendors/subprocessors, tracking, children, retention, and international product launches.
---

# Global Data Privacy Skill

Design privacy as a **jurisdiction-aware product and data architecture capability**.

## Never use one global rule blindly

Privacy obligations vary by:

```text
data subject location/residency
organization establishment
product market/targeting
processing location
sector
age/child status
data category
controller/processor role
purpose
scale/risk
contractual transfer chain
law effective date
```

Resolve applicable jurisdictions before giving a definitive compliance answer.

## Baseline engineering principles

Regardless of jurisdiction, prefer:

- explicit purposes;
- data minimization;
- transparent notices;
- least privilege;
- retention limits;
- privacy/security by design and default;
- data subject/consumer rights workflows;
- vendor/subprocessor governance;
- incident/breach readiness;
- auditable decisions;
- current-source verification.

These are engineering baselines, not a claim that laws are identical.

## Jurisdiction workflow

```text
business/product scope
 -> where company/entities operate
 -> where users/data subjects are
 -> what data + purpose
 -> applicable sector/child rules
 -> controller/processor/business/service-provider roles
 -> lawful basis/permission model
 -> notice + rights
 -> DPIA/risk assessment
 -> cross-border/vendor mechanism
 -> retention/deletion
 -> security + breach duties
 -> evidence + official source/effective date
```

Read `references/JURISDICTION-RESOLUTION.md`.

## Common regimes to recognize

The seed source map includes, but is not limited to:

- EU/EEA GDPR + EDPB guidance;
- California CCPA as amended by CPRA + CPPA/Attorney General sources;
- Singapore PDPA + PDPC guidance;
- Brazil LGPD + ANPD regulation/guidance;
- India Digital Personal Data Protection Act 2023 + DPDP Rules 2025;
- Philippine DPA via `national-data-privacy-skill`.

Do not assume this list is exhaustive or that a named law is applicable merely because a user can access the service from that location.

## Architecture capabilities for multi-jurisdiction products

Prefer first-class support for:

- region/jurisdiction-aware privacy configuration;
- versioned privacy notices and consent/preferences where applicable;
- processing/activity inventory;
- configurable retention policies;
- export/access/correction/deletion/objection request workflows;
- legal holds separated from ordinary retention;
- subprocessor/vendor inventory and data locations;
- transfer mechanism metadata;
- privacy-safe telemetry;
- deletion propagation to downstream systems;
- immutable/auditable request decision history;
- tenant/customer configuration where the customer is the controller.

## Rights requests

Do not implement a single `DELETE user` endpoint and call it privacy compliance. Rights can differ by regime and may have exceptions, verification requirements, response periods and scope. Build an orchestrated request workflow with identity verification, discovery across systems, decision/exception recording, execution, evidence and communication.

## Children and sensitive data

Apply stricter design review when children, biometrics, health, education, financial, precise-location or other sensitive/high-risk data is involved. Age thresholds and parental-consent rules are jurisdiction-specific; verify current official law.

## Cross-border processing

For each transfer record:

```text
exporter role/jurisdiction
importer role/jurisdiction
data categories/data subjects
purpose
hosting/support locations
subprocessors
transfer mechanism or authority
security measures
retention/deletion
review date
```

Never assume a cloud provider's region setting alone solves legal transfer obligations.

## Output

For privacy-sensitive product work provide:

```text
Applicable jurisdictions (and why)
Roles per processing activity
Data/purpose
Legal permission/basis model
Notices/preferences
Rights handling
DPIA/risk assessment
Transfers/vendors
Retention/deletion
Security/breach obligations
Official sources + effective dates
Unresolved legal questions
```

This skill is for engineering and governance; obtain qualified legal review for material jurisdictional interpretations.

## Resources

- Start with the maintained source map in `data/jurisdictions.json`, then verify the applicable official sources.
- Read `references/PRIVACY-BY-DESIGN.md` for product/data design, `references/CHILDREN-AND-SENSITIVE-DATA.md` for high-risk processing, and `references/CROSS-BORDER-AND-VENDORS.md` for transfers and subprocessors.
- Use `templates/DPIA.md` for high-risk assessments and `templates/DSAR-RUNBOOK.md` for rights-request operations.
