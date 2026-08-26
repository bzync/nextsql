# Contributing to NextSQL

NextSQL is a correctness-first native database engine.

Contributions must preserve its architecture, security model, durability guarantees, and native semantics.

---

## Before You Start

Read:

- `README.md`
- `AGENTS.md`
- `SKILLS.md`
- `TODO.md`
- `ROADMAP.md` (simplified roadmap; `TODO.md` remains authoritative)
- `ARCHITECTURE.md`
- relevant `docs/*`

---

## Contribution Rules

Do not:

- introduce PostgreSQL/MySQL compatibility hacks without explicit project approval;
- bypass the parser/binder/planner architecture;
- weaken WAL/fsync behavior for benchmark gains;
- weaken tenant isolation;
- invent cryptography;
- add unbounded workers/queues;
- add an LLM to deterministic query planning;
- claim unsupported HA/security guarantees.

---

## Development Flow

1. Identify the owning phase.
2. Confirm the task is not blocked by an earlier open gate.
3. Make the smallest coherent change.
4. Add applicable tests.
5. Run race/fuzz/crash tests where required.
6. Update documentation.
7. Update `CHANGELOG.md` for notable shipped behavior.
8. Update `TODO.md` only when evidence supports the status change.

---

## Pull Request Requirements

A PR should explain:

- problem;
- design;
- affected subsystems;
- persistence impact;
- WAL/recovery impact;
- protocol impact;
- security impact;
- tenant/RBAC impact;
- performance impact;
- tests run;
- benchmarks run;
- documentation changed.

---

## Required Checks

At minimum:

```bash
go test ./...
```

Where applicable:

```bash
go test -race ./...
go test ./tests/integration
go test ./tests/crash
go test ./tests/ha
```

Add targeted fuzzing and benchmarks when required.

---

## Persistent Formats

Any persistent format change must include:

- explicit versioning;
- backward/upgrade behavior;
- corruption validation;
- encryption behavior;
- recovery behavior;
- backup/PITR behavior;
- tests.

---

## Protocol Changes

NSQL changes must consider:

- protocol versioning;
- old/new driver behavior;
- capability negotiation;
- malformed packet handling;
- size limits;
- cancellation/backpressure;
- security.

---

## SQL Changes

SQL changes should integrate through all applicable layers:

```text
parser
binder
catalog
logical plan
optimizer
physical plan
executor
WAL/MVCC
recovery
replication
RBAC
protocol
EXPLAIN
tests
docs
```

---

## Performance Changes

Performance work must not sacrifice correctness.

For ANN/vector work, always report:

- recall@K;
- p50/p95/p99;
- QPS;
- memory;
- index size;
- build time where relevant.

---

## Commit Quality

Prefer commits that are:

- scoped;
- testable;
- reversible;
- documented;
- explicit.

Avoid mixing unrelated refactors with correctness-sensitive changes.

---

## Security

Do not submit secrets.

Potential vulnerabilities should follow `SECURITY.md` rather than being publicly disclosed before remediation.

---

## Licensing

By contributing, you must have the right to submit the code under the repository's applicable license terms.
