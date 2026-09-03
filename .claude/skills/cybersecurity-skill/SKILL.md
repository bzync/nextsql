---
name: cybersecurity-skill
description: Organization-wide cybersecurity risk, governance, operations and resilience skill for Bzync Software Development Services. Use for security strategy, NIST CSF 2.0 profiles, asset/risk management, IAM, vulnerability management, detection, incident response, backups/recovery, third-party risk, cloud/infrastructure security, security metrics, and production security reviews.
---

# Cybersecurity Skill

Manage cybersecurity as an **enterprise risk and operating capability**, not only application vulnerability scanning.

## Framework anchor

Use NIST Cybersecurity Framework 2.0 as an open high-level organizing model when useful:

```text
GOVERN -> IDENTIFY -> PROTECT -> DETECT -> RESPOND -> RECOVER
```

CSF outcomes are not a substitute for engineering design. Map them to concrete Bzync systems, owners, controls and evidence.

## Cybersecurity operating model

### Govern
- security ownership and decision rights;
- risk appetite/acceptance;
- policies and exception process;
- legal/contractual obligations;
- supplier/supply-chain governance;
- metrics and management reporting.

### Identify
- assets, services, data and dependencies;
- threat/risk assessment;
- criticality and business impact;
- vulnerability/exposure inventory;
- architecture and data-flow understanding.

### Protect
- identity/access management;
- secure configuration;
- secrets/key management;
- segmentation and network exposure controls;
- secure development;
- data protection;
- backups and resilience;
- security awareness/competence.

### Detect
- logging and monitoring;
- alert rules with owners/runbooks;
- anomaly/abuse detection;
- vulnerability and configuration drift detection;
- audit of privileged actions.

### Respond
- triage/classification;
- containment;
- communications/escalation;
- forensics/evidence;
- privacy/regulatory assessment;
- eradication and coordinated recovery.

### Recover
- tested restoration;
- service recovery priorities;
- integrity validation;
- customer/stakeholder communication;
- post-incident improvement.

## Bzync minimum security posture

For internet-facing production services, expect at least:

- MFA for privileged/operator access;
- least privilege and server-side authorization;
- separate human/service identities;
- secrets outside source control;
- supported/patched dependencies and OS/runtime;
- vulnerability management with severity + exploitability + exposure context;
- encrypted external transport;
- protected backups with restore tests;
- centralized/usable security-relevant logs;
- incident owner/runbook;
- dependency/vendor inventory;
- production change history and rollback;
- recurring access review for high privilege.

Tailor controls to risk rather than pretending every system needs identical tooling.

## Risk handling

For each security risk record:

```text
asset/service
threat scenario
vulnerability/exposure
business impact
likelihood
existing controls
residual risk
owner
treatment: mitigate/avoid/transfer/accept
expiry/review date
verification evidence
```

Risk acceptance must be explicit, time-bounded where appropriate and owned at a level authorized to accept the business impact.

## Security incidents

Use `references/INCIDENT-RESPONSE.md`. If personal data is involved, invoke the applicable privacy skill immediately.

## Boundaries with other skills

- `web-security-skill`: browser/web/API attack surface;
- `software-security-skill`: secure SDLC and supply chain;
- `infrastructure-engineer`: deployment/runtime mechanics;
- `software-testing-skill`: repeatable verification;
- `iso-skill`: management-system/audit evidence.

## Output for security review

```text
Assets/data/criticality
Threats/exposure
Current controls
Findings ranked by risk
Immediate containment if needed
Recommended treatment
Owner + verification
Residual/accepted risk
Monitoring/incident implications
Standards/privacy mappings when relevant
```

## Resources

- Read `references/NIST-CSF-2.md` when building a CSF profile.
- Read `references/SECURITY-OPERATING-MODEL.md` for ownership, cadence, and metrics.
- Read `references/THREAT-RISK-REGISTER.md` when writing risk scenarios; structure machine-readable records with `schemas/security-risk.schema.json` and use `templates/RISK-REGISTER.md` for reviews.
- Use `references/INCIDENT-RESPONSE.md` and `templates/INCIDENT-RUNBOOK.md` when preparing for or handling an incident.
