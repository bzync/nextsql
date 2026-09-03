---
name: national-data-privacy-skill
description: Philippine National Privacy Commission and Data Privacy Act engineering/compliance skill for Bzync systems. Use when software processes Philippine personal data, student/minor data, employee/customer data, profiling, automated decisions, sharing, retention, privacy impact assessments, breach handling, DPO/DPS registration, privacy engineering, or NPC compliance.
---

# Philippine National Data Privacy Skill

Apply Philippine privacy requirements as **versioned governance and engineering constraints**, not as generic legal boilerplate.

## Authority hierarchy

Prefer, in order:

1. Republic Act No. 10173 (Data Privacy Act of 2012);
2. its Implementing Rules and Regulations;
3. current National Privacy Commission circulars, advisories, rules, decisions, and official guidance;
4. sector-specific Philippine law/regulation;
5. contractual obligations that add protection without reducing statutory rights.

For compliance-sensitive implementation, verify the controlling issuance and effective date against an official source. Do not rely on remembered thresholds or old blog posts.

## Core workflow

Before changing a system that processes personal data:

```text
identify processing activity
 -> classify data and data subjects
 -> determine PIC/PIP role per activity
 -> establish legitimate purpose + lawful basis/authority
 -> minimize collection/use/disclosure
 -> map storage, transfers, retention and deletion
 -> perform/refresh PIA where risk warrants
 -> define organizational/physical/technical controls
 -> preserve data-subject rights
 -> define incident + breach response
 -> document evidence and current NPC source
```

Do not classify Bzync globally as PIC or PIP. A company may be a PIC for its own account/billing/support data and a PIP for customer-controlled hosted data. Determine the role per processing purpose and contract.

## School-system rule

School systems commonly process data about minors and information that may be sensitive personal information under Philippine law, including education-related information. Treat student/parent/guardian/employee datasets as privacy-sensitive by default and verify exact legal classification and authority for each purpose.

Read `references/SCHOOL-MINOR-DATA.md` for school-specific engineering rules.

## Privacy engineering requirements

- privacy by design and by default;
- purpose limitation and proportionality/data minimization;
- least-privilege access and strong authentication;
- tenant/workspace isolation where applicable;
- encrypted transport and appropriate protection at rest;
- explicit retention/disposal rules;
- access/audit logs protected from casual alteration;
- non-production data must not expose unnecessary real personal data;
- exports, reports, analytics and logs require the same privacy review as primary tables;
- backups must participate in retention/restore/deletion design rather than becoming an ungoverned copy;
- vendors/subprocessors must have defined data roles, security obligations and exit/deletion handling.

## High-risk triggers

Escalate to a PIA/privacy review when a change involves any of:

- minors or vulnerable data subjects;
- sensitive personal information;
- large-scale or systematic monitoring;
- biometrics, identity proofing or precise location;
- profiling or automated decision-making;
- AI using personal data;
- new data sharing/integration;
- cross-border transfer;
- new tracking/analytics technology;
- materially changed purpose;
- a new high-impact repository or data lake;
- a security architecture change affecting personal data.

## NPC operational controls

Use current NPC rules to determine, rather than guess:

- DPO and Data Processing System registration obligations;
- required records and privacy notices;
- data sharing/outsourcing requirements;
- data-subject rights process;
- breach assessment, documentation and notification;
- compliance-check evidence;
- automated decision-making/profiling notification or registration requirements.

Registration rules changed over time; current implementations must verify NPC Circular No. 2022-04 and subsequent official issuances rather than relying only on older Circular 17-01.

## Breach handling

Security incidents and personal data breaches are related but not identical. Maintain an incident workflow that can determine:

```text
what happened
what data was involved
whose data was involved
confidentiality/integrity/availability impact
likely harm
containment status
evidence preservation
whether notification is legally required
notification deadline/content/channel under current NPC rules
corrective actions
```

Never suppress or delay a legally required notification to avoid reputation impact.

Read `references/BREACH-RESPONSE.md`.

## Output for privacy-sensitive changes

For material changes include:

```text
Processing purpose
Data/data subjects
PIC/PIP roles
Authority / lawful basis
Data flow and recipients
Retention/deletion
Rights handling
Security controls
PIA requirement/result
Breach implications
Official source + effective date checked
Open legal/privacy questions
```

This skill provides engineering/compliance structure and is not a substitute for qualified legal advice for disputed interpretations.

## Resources

- Read `references/PH-DPA-COMPLIANCE.md` for the source/obligation map and `references/PRIVACY-ENGINEERING.md` for lifecycle controls.
- Structure processing inventories with `schemas/processing-activity.schema.json` and `templates/DATA-INVENTORY.md`.
- Use `templates/PIA.md` for privacy impact assessments.
- Use `references/SCHOOL-MINOR-DATA.md` for learner/minor data and `references/BREACH-RESPONSE.md` for incidents.
