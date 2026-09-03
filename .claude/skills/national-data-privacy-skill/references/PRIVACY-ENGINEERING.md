# Privacy Engineering

## Lifecycle

Apply privacy from discovery through deletion:

```text
idea -> requirements -> architecture -> implementation -> test -> release
 -> operation -> change -> retention expiry -> deletion -> evidence
```

## Architecture controls

- data classification at domain boundaries;
- field-level purpose documentation for high-risk data;
- privacy-aware API contracts that avoid over-fetching;
- scoped service identities and tenant isolation;
- export/download authorization and audit;
- masking/redaction in UI, logs and support tooling;
- pseudonymization where direct identity is not necessary;
- retention jobs with observable success/failure;
- deletion semantics documented for primary DB, replicas, search indexes, object storage, caches and backups;
- test fixtures generated/synthetic by default;
- analytics events reviewed for personal/sensitive data leakage;
- admin/support impersonation or emergency access strongly controlled and audited.

## Change review

A schema field addition can be a privacy change even when no endpoint changes. A new log line can be a new personal-data repository. A new analytics SDK can be a new recipient/transfer. Review all three.

## Evidence

Keep enough evidence to show the controls are real:

- data-flow diagrams;
- processing inventory;
- PIA records;
- access-control matrices;
- retention schedules and job results;
- vendor/subprocessor inventory;
- privacy/security test evidence;
- incident exercises;
- policy/version history.
