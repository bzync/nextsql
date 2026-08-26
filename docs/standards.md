# NextSQL standards baseline

NextSQL uses the following standards as design references:

| Area | Baseline |
|---|---|
| SQL language | ISO/IEC 9075:2023 |
| Application call interface concepts | ISO/IEC 9075-3:2023 SQL/CLI |
| Stored routines and procedural database logic | ISO/IEC 9075-4:2023 SQL/PSM |
| External data integration | ISO/IEC 9075-9:2023 SQL/MED |
| Schema metadata | ISO/IEC 9075-11:2023 SQL/Schemata |
| Multidimensional arrays | ISO/IEC 9075-15:2023 SQL/MDA |
| Property graph queries | ISO/IEC 9075-16:2023 SQL/PGQ |
| Remote database protocol principles | ISO/IEC 9579:2000 RDA |
| Transport | TCP with TLS 1.3 for remote production connections |
| Character encoding | Unicode encoded as UTF-8 |

This baseline does not make NextSQL a compatibility layer and does not, by
itself, constitute a claim of formal conformance. NextSQL keeps its native SQL
dialect, NSQL wire protocol, storage formats, and drivers. A feature is only
supported when `system.capabilities`, `TODO.md`, matching-version documentation,
and tests say that it is supported.

SQL/PSM guides stored-routine concepts, while NextSQL's shipped programmable
surface remains the native bounded `WORKFLOW` / `TRIGGER` / `SCHEDULE` / `TASK`
model documented in `docs/workflows.md`. SQL/CLI and RDA guide interface and
remote-access concepts; the authoritative client contract remains the
versioned NSQL protocol in `docs/protocol.md`.

SQL/MED, SQL/MDA, and SQL/PGQ are baselines for future feature design, not
claims that external data wrappers, multidimensional arrays, or property graph
queries are currently shipped. SQL/Schemata guides the canonical `system`
schema without permitting metadata access to bypass RBAC or tenant isolation.

Before claiming conformance for any feature, record:

- the applicable standard, part, and edition;
- the implemented feature/subfeature mapping;
- intentional NextSQL-native differences;
- parser, binder, planner, executor, NULL, transaction, and error semantics;
- protocol/driver exposure where applicable;
- RBAC and tenant behavior;
- conformance and regression tests.

Where a standard permits implementation-defined behavior, NextSQL documents a
deterministic native choice. Where the standard conflicts with an earlier
correctness, durability, security, or resource-safety requirement, the engine
fails explicitly and documents the limitation rather than silently emulating
another database.
