# AGENTS.md — NextSQL Repository Instructions

> Repository-level instructions for AI coding agents working on NextSQL.
>
> This file is intentionally concise. It tells an agent **how to operate in this repository** and which documents are authoritative.

---

# 1. Read These First

Before changing code, read and follow:

```text
PROJECT.md
TODO.md
SKILLS.md
ROADMAP.md
```

Use them as follows:

```text
PROJECT.md
→ Final expected NextSQL product/end-state.

TODO.md
→ Current implementation status, open work, phase gates, measurements, blockers.

SKILLS.md
→ Engineering rules, safety constraints, architecture discipline,
  verification requirements, and agent operating contract.

ROADMAP.md
→ Simplified, human-readable, non-authoritative roadmap derived from TODO.md.
```

If these files disagree:

1. `TODO.md` wins for **current implementation status**.
2. `SKILLS.md` wins for **engineering behavior, safety, architecture discipline, and verification requirements**.
3. `PROJECT.md` wins for **intended final product scope**.
4. `TODO.md` controls **sequencing and dependency order**.
5. `ROADMAP.md` summarizes that sequence but is not a second status database.
6. Detailed `docs/*` and measured tests/benchmarks are authoritative for implementation-specific technical facts.

Do not silently reconcile contradictions. Fix the documentation or report the conflict.

---

# 2. Current Development Priority

At the current baseline:

```text
P0–P15  complete
P16      complete — exit gate green; terminal 100M B+Tree soak deferred as a standalone measurement
P17      complete — ONLINE rebuild proven for non-partitioned B+Tree/UNIQUE/
         JSON-path/spatial indexes; blocking fallback for unsupported families
P18      implementable scope complete
P19      complete
P20      complete
P21      complete
P22      complete
P23      complete — Vector Engine 2.0; production-gating sign-off 2026-08-31
P24      complete — Full-text Search 2.0; exit gate closed 2026-08-31
P25      complete — Security 2.0; exit gate closed 2026-09-02, security review sign-off in docs/security.md
P26      complete — System catalog / introspection 2.0; exit gate closed 2026-09-02
P27      complete — Operational maturity + workload governance; exit gate closed 2026-09-03
P28      in progress — NextSQL Admin: Setup + Operations modes (2026-09-05: Installer +
         Manager + Studio merged into one product, one binary nextsql-admin — see
         docs/design-admin.md). Operations-mode MVP (all nine M1–M9 slices) is COMPLETE
         (docs/design-admin-operations.md). Setup mode's implemented wizard,
         packaging integration, service flow, and accessibility baseline are verified;
         Linux .tar.gz/.run/.deb/.rpm and silent/offline/upgrade/repair paths are
         live-verified. Recovery-key capability and Windows/macOS execution remain
         capability/environment-blocked.
P29      in progress — Studio M1–M3, five native explorers, Users/Roles,
         Transaction/Lock, Audit, bounded per-tab plan comparison plus
         ANALYZE-only profiler, catalog-aware table/column IntelliSense
         (no keyword completion), and deterministic misspelled FROM/JOIN
         table-name suggestions implemented; MVP gate open
```

P28 is the current release gate — specifically the GUI installer, the only
open piece of its exit gate. Prioritize:

```text
1. Audit designed vs implemented vs tested vs production-gated status for
   whatever surface is being extended, the way P25's audit table did.
2. Fix any correctness regression first.
3. Close a phase only when its exit gate is green.
```

P25 Security 2.0 is complete (exit gate closed 2026-09-02): mTLS / service identity / certificate + trust rotation /
X.509 CRL revocation, and signed short-lived credentials (`NSSC1.` Ed25519
credential in place of the password; expiry + audience/database/role
scope; `NSTK` rotatable signing keyset + verify-only server copy; `NSTR`
fail-closed revocation by token id or per-principal cutoff; `SIGHUP` reload;
`nextsql token` CLI; `ACL.AllowedScoped` role narrowing with no escalation;
session closed at credential expiry; `identity_source` `token` / `mtls+token`).
Config: `token_verify_keyset` / `token_revocations` / `token_audience` /
`token_identity_source_hint`.

P25 external IdP: OIDC design is accepted (`docs/design-oidc-external-idp.md`) —
a brokered token exchange that validates an OIDC token against a cached JWKS and
mints an `NSSC1.` credential (SQL auth path unchanged / offline), plus an `NSIP`
identity policy whose group→role mapping is intersected with real RBAC (no
escalation). Implemented and tested: the `NSIP` policy engine (`internal/auth`)
and the authentication **broker** — `internal/oidc` (JWS/JWKS/ID-token
validation), `internal/authbroker`, `cmd/nextsql-auth-broker` (`POST /v1/exchange`
→ verify ID token → map → mint `NSSC1.`), plus `internal/oidcclient` and
`nextsql login` / `logout` / `whoami` (Authorization Code + PKCE, bounded
loopback callback, strict local credential storage, silent refresh, `--idp`
connection plumbing). Key-derived server audit labeling is implemented:
operator-mapped, successfully verified broker key ids produce `oidc` /
`mtls+oidc`; forged/unverified tokens remain generic. JWT client credentials
are implemented with protected secret-file input, explicit broker resource
audience + client binding, and non-interactive renewal. Embedded single-node
mode is implemented on a separate bounded listener with issuer/verifier checks
and a live native-user/ACL membership feed. Optional opaque introspection and
JIT remain off. Field-level client encryption has an experimental
SQL/catalog/server slice, helpers in the four drivers in P25 scope
(Go/Node.js/TypeScript/Bun/PHP), tested PITR + HA/failover, and durable
`FileFieldKeyring` rotation/revocation. The Python and Ruby drivers added later
do not yet expose field-encryption helpers. Password hashing has migrated
to Argon2id (versioned records, PBKDF2 backward compatibility, transparent
login rehash, DoS benchmarks). Audit hardening is implemented: `nextsql.audit`
carries a versioned `NSAC` hash chain with optional `NSAK` Ed25519 signatures
(rotatable keyset, fail-closed signed-transition rule), verified by the
`nextsql audit` CLI. The phase-wide exit gate — a dated security review
sign-off (`docs/security.md` "P25 security review sign-off") — closed
2026-09-02, so every item above is production-gated. `ENCRYPTED CLIENT`
stays labeled `experimental` in `system.capabilities` only because no
searchable/deterministic mode ships (a deliberate scope decision, not an
open blocker). OCSP, optional OIDC opaque-token introspection, and JIT
principal provisioning remain off by design, not as open blockers.

Stemming, stop-word dictionaries, versioned language analyzers, english synonym dictionary v1, prefix search, fuzzy matching, typo tolerance, highlight/snippet generation, multi-field search, field weighting, and faceting landed: `NSCT` v9 analyzer metadata, `WITH (ANALYZER = 'simple' | 'english' | 'french' | 'german' | 'spanish')`, english v3 = Porter2 + stop-word dictionary v1 + query-time synonym dictionary v1, french/german/spanish v1 = Snowball stemmer + stop list, trailing `*` prefix queries and trailing `~` fuzzy queries, automatic typo tolerance on missing unadorned tokens (fail-closed expansion caps), `HIGHLIGHT`/`SNIPPET` on SEARCH SELECT lists (bounded markers/width), `CREATE FULLTEXT INDEX` / `SEARCH` on 1–8 columns (phrases stay per-field), optional `SEARCH col WEIGHT n` (query-time BM25 tf scale, `(0, 64]`, default 1), `SELECT * … SEARCH … FACET col [, col …]` independent histograms over the full match set (per-facet `LIMIT`, 8 columns / 1024 values fail closed), default BM25/phrase behaviour preserved.

Do not let later feature work destabilize an earlier release gate. P26 System
catalog / introspection 2.0 is complete (exit gate closed 2026-09-02): the
virtual `system` schema core (capabilities/tables/columns/indexes/storage/
replication/raft/workflows/tasks/partitions/stats, RBAC-filtered), all 5 live
tables — `system.sessions`/`system.active_queries`/`system.transactions`/
`system.change_streams`/`system.locks` (node-local, in-memory,
RBAC-filtered — see `docs/system-catalog.md`) — and all nine planned `SHOW`
convenience aliases. P27 Operational maturity + workload governance closed
2026-09-03. **P28 NextSQL Admin (Setup + Operations modes) is the current
release gate**: Operations mode's MVP is complete; Setup mode
(`docs/design-admin-setup.md`) has its standalone M1 flow implemented and
targeted-tested. Packaging integration, richer encryption/advanced flows,
accessibility validation, silent-install coverage, and remaining platform
execution tests are open.

Always verify the latest status in `TODO.md` before acting — its log entries
are numbered and dated; trust the highest-numbered one over any status text
in this file, including the summary above.

---

# 3. Core Engineering Priority

Always optimize in this order:

```text
Correctness
→ durability
→ security
→ integrity
→ availability
→ latency
→ throughput
→ efficiency
→ developer experience
→ features
```

Never trade an earlier property for a later one.

Examples:

- Never weaken fsync/WAL durability to improve benchmarks.
- Never silently reduce ANN recall to improve latency.
- Never weaken tenant isolation for convenience.
- Never bypass parser/binder/planner architecture to make SQL syntax work quickly.
- Never bypass RBAC through any NextSQL Admin mode, CLI, or drivers.
- Never invent a custom cryptographic primitive.
- Never introduce unbounded goroutines, allocations, result buffers, task queues, or subscriber queues.

---

# 4. NextSQL Is Native

NextSQL is not a PostgreSQL, MySQL, or MariaDB compatibility project.

Do not:

- add PostgreSQL/MySQL compatibility hacks;
- make another database protocol authoritative;
- silently copy another engine's semantics;
- make Studio behave like a generic SQL client;
- use an external vector database as a hidden dependency;
- make an LLM part of database correctness.

Define and document NextSQL-native behavior.

---

# 5. Before Modifying Code

Before making a change:

- identify the owning phase;
- read the relevant `TODO.md` section;
- inspect the existing implementation;
- identify persistent format impact;
- identify wire/protocol impact;
- identify catalog impact;
- identify transaction/WAL/recovery impact;
- identify Raft/failover impact;
- identify RBAC/tenant impact;
- identify resource-abuse risks;
- identify driver/API impact;
- identify documentation impact;
- identify benchmark requirements.

Do not begin by generating a large speculative implementation.

Implement the smallest coherent increment.

---

# 6. SQL Feature Workflow

For SQL/DDL features, normally work through:

```text
grammar
→ lexer/parser/AST
→ binder/type checking
→ catalog/dependencies
→ logical plan
→ optimizer
→ physical plan
→ executor
→ transactions/WAL
→ recovery
→ replication
→ RBAC/tenant checks
→ protocol/driver exposure
→ EXPLAIN
→ metrics
→ tests/fuzz
→ docs
```

Not every feature touches every layer, but never skip a relevant layer merely to make syntax pass.

---

# 7. Persistent Storage Workflow

For every new persistent structure:

1. Define a versioned format.
2. Define corruption validation.
3. Define encryption domain/envelope.
4. Define allocation/reclamation.
5. Define WAL semantics.
6. Define crash/restart semantics.
7. Define backup/restore behavior.
8. Define PITR behavior where relevant.
9. Define replication behavior.
10. Define upgrade/migration behavior.
11. Add decoder fuzzing.
12. Add integrity diagnostics.
13. Add benchmarks if performance-sensitive.

Never serialize raw Go structs directly to disk.

Never introduce an unversioned persistent format.

---

# 8. Distributed Feature Workflow

For cluster-visible features, define:

- leader authority;
- deterministic replicated state;
- quorum behavior;
- failure behavior;
- retry/idempotency semantics;
- failover behavior;
- duplicate prevention where applicable;
- clock assumptions.

Test:

- partitions;
- leader kill;
- follower repair;
- rolling maintenance.

Do not rely on wall-clock timing alone for correctness.

---

# 9. Security Rules

Production storage encryption is mandatory.

Use established cryptography only.

Never:

- invent a cipher/hash/MAC/KDF/AEAD;
- put raw encryption keys in URLs;
- log passwords, keys, tokens, or secrets;
- bypass authorization;
- weaken tenant isolation;
- claim “unhackable”, “100% secure”, or guaranteed zero downtime.

Security behavior must fail closed.

Persistent plaintext in production mode should remain zero by default.

---

# 10. Resource Safety

Never introduce:

- unbounded goroutines;
- unbounded allocations from user-controlled sizes;
- unbounded result buffering;
- unbounded task queues;
- unbounded recursion;
- unbounded subscriber buffers;
- unbounded maintenance work.

Use:

- bounded scheduler pools;
- admission control;
- memory accounting;
- CPU/worker budgets;
- I/O budgets;
- execution timeouts;
- streaming;
- backpressure;
- cancellation;
- spill;
- bounded queues.

Overload should result in controlled queueing, throttling, rejection, spill, or cancellation—not OOM.

---

# 11. Testing Requirements

For every meaningful change, run all applicable verification layers:

```text
unit
integration
restart
crash injection
WAL/recovery
transaction
concurrency
race detector
fuzz
Raft/failover
backup/restore
PITR
RBAC
tenant isolation
prepared statements
driver/wire protocol
resource-limit tests
benchmark
documentation
```

At minimum for Go changes, run when applicable:

```bash
go test ./...
go test -race ./...
```

Add targeted fuzzing and benchmarks for modified hot paths or untrusted decoders.

Do not claim completion if relevant tests were not run.

---

# 12. Benchmark Discipline

Official benchmarks keep enabled:

```text
fsync
WAL
encryption
checksums/authentication
MVCC
authentication
durability
```

unless explicitly labeled experimental.

Record enough context to reproduce results:

- CPU
- RAM
- storage
- filesystem
- row width
- query
- indexes
- cache state
- encryption mode
- durability mode
- concurrency
- QPS/TPS
- p50/p95/p99/p99.9 where relevant
- memory
- disk/WAL usage
- recall for ANN/vector results

Never publish vector latency without recall.

---

# 13. Documentation Rules

When changing behavior:

- update the relevant `docs/*`;
- update `TODO.md` status only when implementation + tests + docs + exit requirements justify it;
- update `PROJECT.md` only when intended product scope changes;
- update `SKILLS.md` only when the engineering/agent contract intentionally changes;
- update `TODO.md` when sequencing/dependencies change and derive `ROADMAP.md` from it.

Do not mark design hooks as shipped functionality.

Do not convert targets into claims without measured evidence.

---

# 14. Current Product Direction

Remaining phases are:

```text
P28 NextSQL Admin — Setup + Operations modes  — in progress (Setup-mode GUI M1 landed; installer gate open)
P29 NextSQL Admin — Studio mode                — in progress (M1–M3, focused native/developer-operations slices, bounded plan comparison/ANALYZE profiler, catalog-aware IntelliSense, and misspelled table-name suggestions landed; MVP gate open)
```

P0–P27 are complete. P16's terminal 100M B+Tree soak remains a documented
standalone measurement outside the gate. P17 online rebuild is shipped for
the proven non-partitioned B+Tree-family scope; vector, full-text, and
partitioned indexes deliberately retain the blocking fallback.

Preserve these principles:

- WORKFLOW is runnable manually.
- TRIGGER invokes WORKFLOW rather than creating a separate procedure system.
- SCHEDULE invokes WORKFLOW.
- asynchronous/scheduled execution becomes durable TASK state.
- CDC emits committed changes only.
- tenant partitioning does not replace authorization.
- follower reads have explicit consistency semantics.
- ANN performance always reports recall.
- Studio is web-based and uses official/native interfaces.
- NextSQL Intelligence / built-in RAG (former P30) is not in the product. Do not implement it.

---

# 15. Do Not Do These

Do not:

- skip an earlier correctness gate to add later features;
- introduce hidden compatibility layers;
- make an LLM the query optimizer;
- make AI required for database correctness;
- make any NextSQL Admin mode read raw database files as a shortcut;
- create unbounded worker pools;
- make unsupported availability/security claims;
- weaken durability for official performance results;
- silently reinterpret blocking operations as online;
- treat physical tenant locality as authorization;
- expose uncommitted CDC events;
- route strong reads to followers that cannot satisfy the requested consistency;
- lower vector recall silently;
- mark a phase complete without its exit gate.

---

# 16. Completion Checklist

Before claiming a task complete:

- [ ] Code compiles.
- [ ] Unit/integration tests pass.
- [ ] Race tests pass where applicable.
- [ ] Restart/crash tests pass where applicable.
- [ ] WAL/recovery behavior is verified where applicable.
- [ ] Raft behavior is verified where applicable.
- [ ] Fuzz coverage exists for new untrusted parsers/decoders.
- [ ] RBAC/tenant tests pass.
- [ ] Resource limits are tested.
- [ ] Driver/protocol behavior is updated where required.
- [ ] Benchmarks include correctness metrics where relevant.
- [ ] Documentation is updated.
- [ ] `TODO.md` reflects truthful status.
- [ ] The owning exit gate is actually satisfied.

---

# 17. Final Repository Rule

When uncertain, choose the implementation that is:

```text
more correct
more durable
more secure
more observable
more bounded
more testable
more explicit
```

even if it is initially slower or less feature-rich.

**NextSQL should earn performance and features after correctness.**
