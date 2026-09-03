# API Security

## Authorization matrix

For each endpoint document:

```text
principal types
required authentication
required role/capability
resource ownership/tenant scope
field-level read/write restrictions
rate/usage limits
side effects
idempotency/replay behavior
```

## Multi-tenancy

Never rely on client-supplied `workspace_id`, `school_id`, `account_id` or similar alone. Resolve authorized scope from the authenticated principal and enforce it in service/data access.

## Sensitive business flows

Protect workflows such as signup, verification, checkout, coupon/referral use, password reset, SMS/email sending, provisioning and exports from automated abuse using appropriate quotas, friction, anomaly detection and business rules.

## Version/inventory

Remove or restrict old/debug endpoints, document deployed versions, and track external/internal APIs. Unknown endpoints are unmanaged attack surface.
