# Bzync End-to-End Checklist

Use only the relevant items.

## Product / CTO
- Clear outcome and non-goals
- Cost/complexity justified
- Security/privacy impact considered
- Build-vs-buy considered when material
- Revisit trigger for major decisions

## Architecture
- Ownership and boundaries clear
- Data source of truth clear
- API/event contracts explicit
- Compatibility and migration defined
- Failure domains understood

## Software
- Validation and authorization correct
- Transactions/idempotency considered
- Errors preserve useful context
- Tests protect important behavior
- No unrelated broad refactor

## UI/UX
- Main task flow is clear
- Loading/empty/error/success states exist
- Accessibility considered
- Responsive behavior intentional
- Existing design system followed

## Infrastructure
- Exposure/network path understood
- Secrets/IAM safe
- Health/readiness appropriate
- Logs/metrics actionable
- Backup/restore impact considered
- Deployment and rollback defined

## Product competency / outcomes
- User/problem and evidence are explicit
- Success and guardrail metrics defined where material
- Alternatives/non-goals/tradeoffs understood
- Full user states and operational lifecycle considered

## Software testing
- Critical invariants identified
- Unit/integration/contract/E2E split appropriate
- Migration/backward-compatibility tests where applicable
- Accessibility/performance/resilience/security tests based on risk
- Flaky tests not hidden by unlimited retry
- Post-deploy smoke/metrics/rollback triggers defined

## Security
- Threat/trust boundaries reviewed
- AuthN/AuthZ and tenant/object scope verified server-side
- Dependencies/build/secrets/config reviewed
- Public web/API/browser controls reviewed where applicable
- Vulnerability findings triaged by exploitability/exposure/impact
- Detection/incident/recovery implications understood

## Privacy
- Personal data/data subjects/purpose identified
- PIC/PIP or controller/processor role resolved per activity
- Minimization, rights, retention/deletion and vendors/transfers handled
- PIA/DPIA considered for high-risk processing
- Breach implications/runbook considered
- Current official sources checked for compliance-sensitive behavior

## ISO / governance (when applicable)
- Standard edition/status verified
- Scope/control/process ownership clear
- Operating evidence exists; no certification claim from documents alone
- Audit/corrective-action/management-review implications considered

## Business / Finance (when applicable)
- Customer promise and pricing behavior explicit
- Unit economics / direct cost impact considered
- Billing/payment/refund lifecycle traceable
- Accounting recognition requirement identified
- Transactions reconcile to processor/bank/subledgers
- Financial history is append-only/reversible rather than silently rewritten
- Current compliance-sensitive rules verified from authoritative sources

## Release
- Migration order safe
- Backward compatibility considered
- Post-release validation defined
- Operational docs updated where needed
