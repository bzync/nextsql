# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

NextSQL is a native, encrypted-by-default multimodel database engine (Go, module
`github.com/bzync/nextsql`, Go 1.22+). Relational SQL, native JSON, vector search,
full-text search, and geospatial types share one storage engine, one WAL, one MVCC
transaction model, one query optimizer, and one native wire protocol (NSQL). It is
**not** a PostgreSQL/MySQL/MongoDB/Elasticsearch/vector-DB compatibility layer —
never add compatibility hacks or silently copy another engine's semantics.

## Documentation hierarchy — read before non-trivial work

This repo is doc-driven; these files are the actual authority, not just background reading:

| File | Role |
|---|---|
| `TODO.md` | **Current implementation status**, phase gates, sequencing, dependencies, measurements. Always check this before assuming a feature is/isn't done — do not trust stale status text elsewhere (including this file). |
| `SKILLS.md` | Engineering rules, safety constraints, architecture discipline, verification requirements — the agent operating contract. Wins for engineering behavior. |
| `PROJECT.md` | Intended finished-product end-state. Wins for intended scope, not current status. |
| `AGENTS.md` | Repository-agent entrypoint; condensed version of the rules below. |
| `ARCHITECTURE.md` | Structural design reference (expanded version of the Architecture section below). |
| `ROADMAP.md` | Simplified, non-authoritative roadmap derived from `TODO.md`. |
| `docs/*.md` | Per-subsystem implementation details (see table near the end) — authoritative for implementation-specific technical facts. |

If these disagree, `TODO.md` wins for status/sequencing, `SKILLS.md` wins for engineering
behavior, `PROJECT.md` wins for intended scope. Do not silently reconcile contradictions —
fix the docs or report the conflict. Do not read this repo's phase/status numbers as fixed;
re-check `TODO.md`'s own log entries (search for the highest-numbered log entry).

## Commands

### Build

```bash
go build ./...
go build -o nextsql ./cmd/nextsql
go build -o nextsqld ./cmd/nextsqld
go build -o nextsql-bench ./cmd/nextsql-bench
go build -o nextsql-manager ./cmd/nextsql-manager
```

No Makefile — plain `go` tooling throughout. `go vet ./...` should stay clean.

### Test

```bash
go test ./...
go test -race ./...                                    # needs a C compiler
go test ./tests/integration ./tests/crash ./tests/ha    # targeted suites
```

Run a single test:

```bash
go test ./internal/executor/... -run TestName
go test ./internal/executor/... -run TestName -race -v
```

Prefer running the specific touched package(s) plus `tests/integration` under `-race`
rather than the whole repo — `go test ./... -race` across the entire tree can take a long
time and is subject to host-level fsync/disk-contention flakiness unrelated to code
correctness (`internal/storage/btree` alone runs several minutes;
`internal/replication`'s partition/leader-isolation tests are more prone to this).
A change may additionally need restart, crash-injection, WAL/recovery, transaction,
concurrency, fuzz, Raft/failover, backup/restore, PITR, RBAC/tenant-isolation, driver, or
resource-limit tests depending on what it touches — see `SKILLS.md` §21 and the workflow
sections below.

Fuzz any new untrusted decoder (SQL parser input, protocol packets, page decoders, WAL
records, persistent metadata):

```bash
go test -fuzz=Fuzz ./path/to/package
```

### Benchmark

```bash
./nextsql-bench --quick
./nextsql-bench --slo    # labeled suite: hardware/filesystem/encryption/durability/cache printed per row
```

Official benchmarks must keep fsync, WAL, encryption, checksums/authentication, MVCC, and
authentication enabled unless explicitly labeled experimental. Never publish vector latency
without recall.

### Run locally

```bash
printf 'secret\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw

./nextsql init --data-dir /tmp/nextsql-data --key-file /tmp/nextsql-root.key \
  --user app --password-file /tmp/nextsql.pw

./nextsqld --data-dir /tmp/nextsql-data --key-file /tmp/nextsql-root.key \
  --listen 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw

./nextsql exec --addr 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw \
  --insecure -c "SELECT 1"
```

`--insecure` is loopback-only; any non-loopback listen address requires TLS 1.3
(`--tls-cert`/`--tls-key` on the server, `--tls-ca` on the client).

## Architecture

```text
Native NSQL protocol → TLS 1.3 → authn → RBAC/tenant
        → lexer/parser → binder/type-check → catalog
        → logical planner → cost-based optimizer → physical planner
        → vectorized/parallel executor
              ├── relational   clustered B+Tree, secondary indexes
              ├── JSON         binary NSJB, path traversal, path indexes
              ├── full-text    inverted index, postings, BM25
              ├── vector       VECTOR<F32,N>, flat + HNSW, cosine/L2/inner-product
              └── geo          POINT/BOX/LINESTRING/POLYGON, WGS84
        → MVCC + row/range locks + UNDO
        → REDO WAL (LSN-based, group commit, fsync before commit ack)
        → buffer manager → 16 KiB pages
        → AES-256-GCM sealed pages
```

Hybrid queries (structured filter + full-text + ANN in one statement) are a single
physical plan under one cost model — not separate subsystems glued together.

**Encryption envelope** (`internal/crypto`): external root unlock key (`--key-file`,
kept off the data volume) → KEK → database master → domain-specific DEKs (pages, WAL,
UNDO, backup, vector, full-text, temp/spill, replication). No custom cryptographic
primitives, ever. Persistent plaintext should be zero by default in production mode.

**WAL/recovery** (`internal/wal`, `internal/recovery`): LSN-based encrypted/authenticated
records, segments, rotation, group commit, checkpoints, REDO, partial-tail handling, PITR
archive hooks. A commit cannot be acknowledged before its durability boundary is met.

**Transactions/MVCC** (`internal/txn`, `internal/undo`): snapshots, version chains, UNDO,
row/key/range locking, deadlock detection; READ COMMITTED / SNAPSHOT / SERIALIZABLE
(lock-based strict 2PL on the snapshot, not SSI). Readers never observe uncommitted writes.

**High availability** (`internal/replication`): optional Raft cluster (hashicorp/raft,
not a custom consensus implementation), minimum 3 voting nodes. A write is acknowledged
only after the leader's local WAL flush *and* a quorum commits the replication batch; no
leader means writes fail closed. SQL is not re-executed on followers (`UUID()`/`NOW()`/
`AI()` stay deterministic — they're captured once and replicated).

**Multi-database hosting** (`internal/hosting`, `internal/dbmanager`): a separate,
in-progress cross-cutting track (`docs/design-multidatabase-dbaas.md`) layering
selectable multi-database/multi-realm routing, shared buffer/task-worker/scheduler
budgets, and per-realm auth on top of the single-database engine above. Track its own
milestone state (M1/M2/M3...) in that design doc and in `TODO.md`, separate from the
P0–P30 phase list.

## Repository layout

```text
cmd/nextsqld              server
cmd/nextsql                CLI (init, exec, migrate, backup, restore, verify, export,
                            import, diagnose, status, cluster, hosting, audit, token, version)
cmd/nextsql-bench          official benchmark tool
cmd/nextsql-auth-broker    OIDC external-IdP broker (P25)
cmd/nextsql-manager        NextSQL Manager: loopback web UI + JSON API, a pure
                            nextsqld protocol client (P28, MVP in progress)
internal/                  engine: storage, wal, recovery, txn, undo, sql (lexer/parser/
                            binder), executor, catalog, crypto, security, auth, protocol,
                            replication, hosting, dbmanager, vector, fulltext, json, cdc,
                            scheduler, cron, backup, xport, migrate, config, metrics,
                            setup (installer lifecycle), manager (Manager backend), ...
drivers/                   official native-protocol drivers: go, node, bun, deno, php,
                            python, ruby, plus shared TS types in drivers/js
tests/                     integration, crash, ha (cross-package suites; unit tests live
                            alongside their package under internal/)
docs/                      per-subsystem implementation notes (see table below)
docs/web/                  product site (`npm run dev` there)
packaging/, scripts/       Linux/Windows installer sources and build scripts
```

Key `docs/*.md`: `sql.md` (dialect/catalog), `optimizer.md`, `execution.md`, `json.md`,
`fulltext.md`, `vector.md`, `geo.md`, `storage-format.md`, `btree.md`, `wal.md`,
`mvcc.md`, `protocol.md`, `security.md`, `backup.md`, `export.md`, `ops.md` (metrics/
admission/SLOs), `ha.md`, `system-catalog.md`, `partitioning.md`, `cdc.md`, `workflows.md`
(WORKFLOW/TRIGGER/SCHEDULE/TASK), `client-encryption.md`, `standards.md`, `install.md`
(P28 installer/automation surface — `nextsql setup`, resource presets).

## Engineering contract (from `AGENTS.md` / `SKILLS.md`)

**Priority order — never trade an earlier property for a later one:**

```text
correctness → durability → security → integrity → availability
  → latency → throughput → efficiency → developer experience → features
```

Concretely: never weaken fsync/WAL durability for benchmarks; never silently reduce ANN
recall for latency; never weaken tenant isolation for convenience; never bypass RBAC
through any surface (Studio, Manager, CLI, drivers, Intelligence); never bypass the
parser/binder/planner to make SQL syntax work quickly.

**Before modifying code**, identify: owning phase (`TODO.md`), persistent-format impact,
wire/protocol impact, catalog impact, transaction/WAL/recovery impact, Raft/failover
impact, RBAC/tenant impact, resource-abuse risk, driver/API impact, doc impact, benchmark
requirements. Implement the smallest coherent increment — don't start with a large
speculative implementation.

**SQL/DDL features** normally flow: grammar → lexer/parser/AST → binder/type-check →
catalog/dependencies → logical plan → optimizer → physical plan → executor →
transactions/WAL → recovery → replication → RBAC/tenant checks → protocol/driver
exposure → EXPLAIN → metrics → tests/fuzz → docs. Don't skip a relevant layer just to
make syntax pass.

**Every new persistent structure** must define: versioned format, corruption validation,
encryption domain/envelope, allocation/reclamation, WAL semantics, crash/restart
semantics, backup/restore behavior, PITR behavior, replication behavior, upgrade/migration
behavior, decoder fuzzing, integrity diagnostics. Never serialize raw Go structs directly
to disk (no raw memory-layout persistence); never introduce an unversioned persistent
format.

**Distributed/cluster-visible features** must define leader authority, deterministic
replicated state, quorum behavior, failure behavior, retry/idempotency, failover behavior,
duplicate prevention, and clock assumptions — and be tested against partitions, leader
kill, follower repair, and rolling maintenance. Never rely on wall-clock timing alone for
correctness.

**Resource safety**: never introduce unbounded goroutines, allocations from
user-controlled sizes, result buffering, task/subscriber queues, or recursion. Use bounded
worker pools, admission control, memory accounting, execution timeouts, streaming,
backpressure, cancellation, spill. Overload should mean controlled queueing/throttling/
rejection/spill, never OOM.

**Security**: production storage encryption is mandatory; established cryptography only
(never invent a cipher/hash/MAC/KDF/AEAD); never put raw keys in URLs; never log
passwords/keys/tokens/secrets; fail closed; never claim "unhackable"/"100% secure"/
guaranteed zero downtime.

**Before claiming a change complete**, verify (not just assert): it builds, unit +
integration tests pass, race tests pass where applicable, restart/crash and WAL/recovery
behavior is verified where applicable, Raft behavior is verified where applicable, fuzz
coverage exists for new untrusted decoders, RBAC/tenant tests pass, resource limits are
tested, driver/protocol behavior is updated where required, docs are updated, and
`TODO.md` status is left truthful (only mark a phase/gate complete when its exit gate is
actually satisfied — don't convert targets into claims without measured evidence).

**Docs move with behavior in the same change**: update the relevant `docs/*.md`; update
`TODO.md` when status/sequencing/dependencies actually change; update `PROJECT.md` only
when intended product scope changes; update `SKILLS.md` only when the engineering/agent
contract itself changes; update `CHANGELOG.md` for shipped notable changes.
