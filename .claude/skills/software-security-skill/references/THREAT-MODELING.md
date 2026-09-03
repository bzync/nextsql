# Threat Modeling

## Minimal model

1. What are we building/changing?
2. What valuable assets/data/actions exist?
3. What are the trust boundaries and actors?
4. What can go wrong/what can be abused?
5. What controls prevent/detect/recover?
6. What is still risky and who owns it?

## Abuse-case format

```text
As an attacker/misused principal,
I attempt to <action>
against <asset/flow>
to cause <impact>.
```

Then create a testable security requirement.

## Diagrams

Prefer simple data-flow/trust-boundary diagrams over visually complex diagrams that hide ownership. Mark internet, user browser, API, internal service, database, queue, object storage, admin plane, third-party provider and cross-tenant boundaries where relevant.
