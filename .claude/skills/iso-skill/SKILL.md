---
name: iso-skill
description: ISO-aligned management-system and engineering governance skill for Bzync Software Development Services, especially ISO/IEC 27001 information security, ISO/IEC 27701 privacy, ISO 9001 quality, ISO 22301 business continuity, ISO/IEC 42001 AI management, and related standards. Use for gap assessments, policies, evidence, controls, audits, certification readiness, risk registers, management review, and standards mapping without reproducing proprietary ISO text.
---

# ISO Governance Skill

Use ISO standards as **management-system frameworks connected to real engineering evidence**, not as decorative certificates or copied checklists.

## Copyright / licensing boundary

ISO standard text is proprietary. Do not reproduce clauses, control text or paid standard content from memory or unauthorized copies. Work from:

- organization-owned/licensed standard copies when provided;
- official ISO public summaries/metadata;
- organization policies and evidence;
- lawful mappings/guidance.

Paraphrase at a high level and reference standard identifiers. Alignment does not equal certification.

## Core standards for Bzync

Commonly relevant standards include:

- ISO/IEC 27001 — Information Security Management System (ISMS);
- ISO/IEC 27701 — Privacy Information Management System (PIMS);
- ISO 9001 — Quality Management System (QMS);
- ISO 22301 — Business Continuity Management System (BCMS);
- ISO/IEC 42001 — AI Management System when Bzync develops/operates AI-enabled products;
- related ISO/IEC 27000-family guidance where applicable.

Always verify edition/status before a formal assessment. As of the package snapshot, ISO/IEC 27001:2022 and ISO/IEC 27701:2025 are published; ISO 9001's next edition was under publication around September 2026, so do not automatically treat a draft/final draft as the active certification basis.

## Management-system workflow

```text
scope + context
 -> interested parties / obligations
 -> assets/processes/data/services
 -> risks + opportunities
 -> objectives
 -> policies + controls/processes
 -> owners + competence
 -> evidence + metrics
 -> internal audit
 -> corrective action
 -> management review
 -> continual improvement
```

## Evidence over claims

A control is not implemented because a policy says it exists. Require evidence such as:

- access configuration and review records;
- deployment/approval/change history;
- test/security scan results;
- restore exercises;
- incident records and lessons learned;
- risk treatment records;
- vendor reviews;
- training/competence evidence;
- privacy inventories/PIAs;
- quality metrics and corrective actions;
- management-review decisions.

## Integrations with other Bzync skills

- use `cybersecurity-skill` + `software-security-skill` for technical security implementation;
- use `national-data-privacy-skill` / `global-data-privacy-skill` for legal privacy requirements;
- use `software-testing-skill` for verification evidence;
- use `infrastructure-engineer` for resilience, backup, DR and operations;
- use `chief-technology-officer` for scope, investment and management decisions.

## Certification readiness

For certification-oriented work distinguish:

```text
gap identified
policy/process designed
implemented
operating evidence exists
internally audited
corrective actions closed
management reviewed
external certification assessment
```

Never promise certification based solely on generated documents.

## Output for a gap assessment

```text
Standard + edition/status
Scope
Current evidence
Observed conformity/alignment
Gap
Risk/business impact
Required action
Owner
Target date
Evidence expected
Verification method
```

Read `references/AUDIT-EVIDENCE.md` and `references/ISMS-PIMS-QMS-BCMS.md`.

Use `data/standards.json` to identify the snapshot edition/status, then recheck official ISO metadata for formal work. Read `references/ISO-GOVERNANCE.md` when designing an integrated management system. Use `templates/CONTROL-MAPPING.md` for traceable mappings and `templates/MANAGEMENT-REVIEW.md` for review records.
