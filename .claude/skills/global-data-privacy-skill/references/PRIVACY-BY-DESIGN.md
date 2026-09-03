# Privacy by Design for Global Products

## Product requirements

A privacy requirement should be testable. Replace "be GDPR compliant" with concrete behavior such as:

- only necessary attributes collected for this flow;
- optional analytics disabled/controlled as required for the jurisdiction/configuration;
- rights request discovers records in named systems;
- retention expiry deletes/restricts named data stores;
- admin access is scoped and audited;
- user-facing notice version and acceptance/preference evidence are retained when needed.

## Data architecture

- maintain data lineage for sensitive/high-risk datasets;
- prevent accidental data replication through logs/events/analytics;
- separate identifiers from analytical datasets where feasible;
- use pseudonymous identifiers where direct identity is not necessary;
- define deletion semantics before choosing downstream tools;
- make region/location an explicit deployment/data-governance attribute;
- keep backups governed and restorable without losing deletion/retention controls.

## UX

Avoid deceptive designs. Privacy choices must be understandable, accessible and not intentionally harder to refuse than accept where applicable. Do not use consent for processing that is not truly optional merely to simplify the implementation.
