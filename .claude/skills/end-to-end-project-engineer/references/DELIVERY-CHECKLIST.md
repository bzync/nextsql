# End-to-End Delivery Checklist

Use selectively according to risk.

## Product / CTO
- User or business outcome is explicit.
- Scope and non-goals are clear.
- Build-vs-buy and operational cost are considered for major dependencies.
- Work is sequenced to reduce irreversible decisions.
- Compliance/licensing/data-residency concerns are identified.

## Architecture
- Bounded context/module ownership is clear.
- Source of truth for each important datum is known.
- API/event schemas are versionable.
- Idempotency/concurrency/failure behavior is considered.
- Migration from current state is defined.

## Software
- Inputs are validated at trust boundaries.
- Errors are typed/structured where appropriate.
- Business rules are testable and not duplicated across layers.
- Compatibility and deprecation paths are explicit.
- Tests cover behavior rather than implementation trivia.

## Data
- Constraints and indexes match invariants/access patterns.
- Migrations are deterministic and safe for existing records.
- Audit/history requirements are preserved.
- Retention/deletion semantics are explicit for sensitive data.

## Infrastructure
- Resource sizing assumptions are documented.
- Secrets are not committed or exposed to clients.
- Health/readiness/liveness behavior is correct.
- Backups and restore are tested for stateful workloads.
- Rollback/failover is realistic, not theoretical.

## Security
- Authentication and authorization are server-enforced.
- Least privilege is used for service/runtime credentials.
- Injection, SSRF, XSS, CSRF, path traversal and upload risks are considered where applicable.
- Rate limits/abuse controls exist on exposed sensitive operations.
- Sensitive actions are auditable.

## UI/UX
- Main task path is obvious.
- Loading, empty, success, validation, partial-failure and fatal-error states exist.
- Keyboard/focus/labels/contrast are accessible.
- Destructive actions communicate consequence and recovery.
- Mobile/responsive behavior is intentional.

## Operations
- Logs include context/correlation IDs when useful.
- Metrics measure availability, latency, errors, saturation and key product outcomes.
- Alerts map to operator action.
- Runbooks cover likely failures.
- Post-release validation is defined.
