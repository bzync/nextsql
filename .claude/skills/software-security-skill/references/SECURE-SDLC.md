# Secure SDLC

## Definition of ready for high-risk work

- trust boundaries/data classified;
- abuse cases identified;
- security/privacy requirements explicit;
- owner and review level assigned.

## Definition of done

- server-side security invariants implemented;
- security regression tests added;
- dependencies/build changes reviewed;
- secrets/config safe;
- security scans/tests appropriate to risk pass or exceptions are documented;
- observability supports detection;
- runbook/rollback exists for material production risk;
- findings and accepted risks have owners/review dates.

## Secure code review prompts

- Can the caller access another tenant/object by changing an ID?
- Are fields mass-assignable that should not be?
- Can untrusted input reach interpreter/query/path/template/URL sinks?
- Are retries/replays safe?
- Can errors leak secrets or internal data?
- Can resource usage be made unbounded?
- Are authorization checks centralized and testable?
- Does concurrency produce privilege/data-integrity races?
