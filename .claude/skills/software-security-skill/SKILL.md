---
name: software-security-skill
description: Secure software development lifecycle and software supply-chain skill for Bzync. Use for threat modeling, secure design, code security, dependency/SBOM governance, secrets, cryptography, CI/CD integrity, artifact provenance, SAST/SCA/DAST, code review, vulnerability remediation, release security, and NIST SSDF/OWASP SAMM-aligned engineering.
---

# Software Security Skill

Build security into the software lifecycle instead of treating penetration testing as the final gate.

## Framework anchors

Use open/public frameworks as useful references:

- NIST SP 800-218 SSDF Version 1.1 is the current final version at the snapshot; NIST published a draft Version 1.2 / SP 800-218 Rev.1 in December 2025, so do not treat the draft as final until NIST says so;
- OWASP SAMM can structure software-assurance maturity;
- OWASP ASVS/WSTG support application requirements/testing;
- ecosystem supply-chain frameworks/tools may supplement these.

## Secure SDLC

```text
requirements
 -> threat model/security requirements
 -> architecture/design review
 -> implementation + secure code review
 -> dependency/build controls
 -> automated/manual verification
 -> release approval/provenance
 -> deployment hardening
 -> monitoring/vulnerability response
 -> lessons fed back into engineering
```

## Threat modeling triggers

Threat-model or refresh when a change introduces:

- new trust boundary/external integration;
- authentication/authorization model change;
- admin/privileged capability;
- multi-tenancy/isolation changes;
- new sensitive data;
- file uploads or parser/interpreter behavior;
- webhooks/outbound requests;
- cryptographic/key-management changes;
- payment/billing/provisioning flows;
- AI agents/tool execution;
- infrastructure/control-plane access;
- new supply-chain/build path.

Read `references/THREAT-MODELING.md`.

## Dependency and build integrity

- pin/lock dependencies appropriate to the ecosystem;
- review install/build scripts and new transitive trust;
- keep dependency provenance/inventory/SBOM where useful;
- remove unsupported packages;
- scan dependencies but triage findings by actual reachability/exposure;
- protect CI credentials and branch/release controls;
- use reproducible/traceable builds where practical;
- sign/attest artifacts when the threat model warrants it;
- separate build from production runtime privileges.

## Secrets and cryptography

- use approved secret stores/runtime injection;
- never commit secrets/private keys;
- rotate exposed credentials;
- scope tokens narrowly and expire/revoke;
- use maintained standard cryptographic libraries/protocols;
- do not design custom cryptography;
- inventory keys/certificates and ownership/rotation;
- passwords use modern adaptive password hashing via maintained libraries.

## Security verification

Combine:

- compiler/linter/type checks;
- unit/integration tests for security invariants;
- SAST/code review;
- SCA/dependency analysis;
- secret scanning;
- IaC/container/configuration scanning where applicable;
- DAST/web/API security tests;
- targeted fuzz/property tests for parsers/protocol boundaries;
- manual review/penetration testing for high-risk releases.

No single scanner is proof of security.

## Vulnerability remediation

Prioritize using:

```text
severity + exploitability + exposure + privilege required
+ data/business blast radius + known exploitation + compensating controls
```

Document false positives and risk acceptance; do not permanently suppress findings without ownership/review.

## Output

```text
Threat model/change risk
Security requirements
Design/code/build findings
Supply-chain implications
Tests/scans performed
Residual vulnerabilities/accepted risk
Release decision and required follow-up
```

## Resources

- Read `references/SECURE-SDLC.md` for lifecycle gates, `references/THREAT-MODELING.md` for abuse-case analysis, `references/SUPPLY-CHAIN.md` for dependency/build integrity, and `references/SECRETS-CRYPTO.md` for secret or cryptographic changes.
- Use `templates/THREAT-MODEL.md` for threat-model records and `templates/SECURITY-DESIGN-REVIEW.md` for high-risk design reviews.
