# Security, Privacy, Quality and Product Governance

## Default orchestration

For every material production change, decide explicitly whether each lens applies:

- product competency / customer outcome;
- software testing;
- software security;
- web/API security;
- organization/runtime cybersecurity;
- Philippine/global privacy;
- ISO/audit management-system requirements.

"Not applicable" should be a conscious scope decision for high-impact work, not accidental omission.

## Mandatory triggers

### Always test
All code/data/infrastructure changes need proportionate verification. Use `software-testing-skill`.

### Security escalation
Invoke security skills for authentication/authorization, public endpoints, files, webhooks, payments, multi-tenancy, admin/control planes, secrets/cryptography, new dependencies/build systems, sensitive data and internet exposure.

### Privacy escalation
Invoke privacy skills whenever personal data is newly collected, repurposed, shared, exported, retained differently, analyzed/profiled, processed by AI, moved across jurisdictions/vendors, or exposed by an incident.

### ISO escalation
Use `iso-skill` when the task changes an ISMS/PIMS/QMS/BCMS/AI-management scope/control/process, produces audit evidence, or makes a certification/compliance claim.

### Product competency escalation
Use `product-competencies-skill` for product strategy, prioritization, discovery, UX/product acceptance, analytics or team capability reviews.

## Done means evidence

A production change is not "secure", "privacy compliant", "tested" or "ISO compliant" because a document says so. Require implementation and evidence appropriate to the claim.
