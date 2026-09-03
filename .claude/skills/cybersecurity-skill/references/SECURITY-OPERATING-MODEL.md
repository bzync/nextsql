# Security Operating Model

## Cadences

Suggested risk-proportional cadences:

- continuous: alerts, deployment telemetry, critical secret exposure detection;
- per change: security design/code/test review according to risk;
- weekly/monthly: vulnerabilities, privileged changes, unresolved high findings;
- quarterly: high-privilege access, supplier/risk reviews, restore/security exercises as appropriate;
- annually or major change: risk model, incident plan, policy/BC/DR exercises.

Do not mechanically adopt cadence without considering service criticality and company capacity.

## Metrics

Prefer actionable measures:

- age of critical/high vulnerabilities by exposure;
- MFA/privileged access coverage;
- backup restore success and recovery time;
- alert acknowledgement/containment time;
- security defects escaping to production;
- unresolved risk acceptances past review date;
- secrets/dependency/configuration incidents;
- phishing/security training only as one signal, not the whole program.
