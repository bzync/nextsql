# Architecture Review

Evaluate quality attributes that matter to the project:

- correctness and data integrity;
- maintainability and team cognitive load;
- availability and fault isolation;
- latency and throughput;
- security and privacy;
- deployability and rollback;
- observability;
- cost efficiency;
- compliance/data residency;
- vendor portability where strategically relevant.

## Red flags

- Shared database tables across supposedly independent services without ownership rules.
- New distributed system components with no operational reason.
- Synchronous chains across many services for one user action.
- Eventual consistency introduced without user-visible semantics.
- Cache used as source of truth accidentally.
- No migration path from existing data/contracts.
- Architecture diagram contradicts deployable artifacts.
