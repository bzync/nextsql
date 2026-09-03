---
name: web-security-skill
description: Defensive web and API application security skill for Bzync products. Use when designing, reviewing, testing or hardening web apps, APIs, authentication, authorization, sessions, CORS/CSRF/CSP, uploads, redirects, SSRF, injection, browser security, rate limits, webhooks, multi-tenant access, or OWASP Top 10/ASVS/WSTG verification.
---

# Web Security Skill

Secure web applications from **architecture through runtime verification**.

## Current open references

At this package snapshot:

- OWASP Top 10:2025 is the current awareness release;
- OWASP ASVS 5.0.0 is the current stable ASVS release shown by OWASP;
- OWASP API Security Top 10:2023 is the current stable API Top 10 release found in the official project;
- OWASP WSTG stable is the preferred stable testing reference; the project may have a newer development version.

Verify versions before formal security programs.

## Top risks are not a complete standard

Use Top 10 documents for awareness. For requirements and verification, prefer explicit threat models, ASVS-style security requirements, API-specific controls and test cases.

## Web security review sequence

```text
routes/endpoints + trust boundaries
 -> authentication
 -> authorization at object/function/property level
 -> session/token lifecycle
 -> input/output/data binding
 -> browser controls
 -> server-side outbound requests
 -> files/uploads/downloads
 -> integrations/webhooks
 -> resource/abuse controls
 -> errors/logging
 -> deployment/configuration
 -> security tests
```

## Non-negotiable principles

- authorization is enforced server-side on every protected action/resource;
- identifiers are not authorization;
- multi-tenant scope is part of every query/mutation boundary;
- parameterized/query-safe data access;
- output encoded for context;
- untrusted URLs cannot freely reach internal/cloud metadata/network targets;
- uploaded files are treated as hostile;
- secrets/tokens never placed in URLs/logs when avoidable;
- authentication/session state is revocable and expires appropriately;
- sensitive actions resist CSRF/replay/automation as applicable;
- errors do not expose secrets/internal details to clients;
- rate/resource limits target both technical exhaustion and sensitive business-flow abuse;
- production debug/admin surfaces are constrained or disabled.

## Authentication and authorization

Review:

- credential/MFA policy;
- password reset/recovery;
- account enumeration;
- session fixation/rotation/logout/revocation;
- token audience/issuer/expiry/storage;
- privilege elevation;
- object-level authorization;
- field/property-level authorization;
- function/role authorization;
- tenant/workspace ownership;
- admin/support impersonation;
- IDOR/BOLA regression tests.

## Browser security

Apply appropriate:

- secure/HttpOnly/SameSite cookies;
- CSRF protection for cookie-authenticated state changes;
- deliberate CORS allowlists and credentials behavior;
- Content Security Policy suited to the app;
- clickjacking/frame controls;
- safe redirect handling;
- XSS-safe rendering and escaping;
- cache-control for sensitive pages;
- referrer and browser permission policies as appropriate.

## API/webhook security

- strong authentication and scoped authorization;
- schema/size/content validation;
- idempotency for retryable financial/provisioning operations;
- signed/verified webhooks with replay resistance;
- safe outbound callbacks with SSRF controls;
- inventory/version lifecycle;
- quotas/resource limits;
- third-party API responses treated as untrusted input;
- avoid returning fields merely because the ORM model contains them.

## Testing

Combine automated regression tests with security-specific verification. Use `software-testing-skill` plus WSTG/ASVS-derived checks proportionate to risk.

## Output

```text
Attack surface/trust boundaries
Findings ranked by exploitability + impact
Affected route/component
Concrete defensive change
Regression/security test
Configuration/deployment change
Residual risk
OWASP reference when useful
```

Do not use this skill to provide unauthorized exploitation instructions; keep testing within owned/authorized systems and controlled environments.

## Resources

- Read `references/OWASP-2025.md` before citing snapshot versions, `references/API-SECURITY.md` for endpoint and tenant review, and `references/WEB-CONTROLS.md` for browser/input/upload/SSRF controls.
- Use `templates/WEB-SECURITY-REVIEW.md` to record scope, findings, verification, and the release decision.
