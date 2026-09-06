# NextSQL — Codex Project Instructions

> Codex agent operating contract for NextSQL. Codex reads `AGENTS.md` at the repo root and `.codex/AGENTS.md` — keep both in sync. `TODO.md` wins for status, `SKILLS.md` for engineering behavior, `PROJECT.md` for product scope.

## 0. Read First

`PROJECT.md`, `TODO.md`, `SKILLS.md`, `ROADMAP.md` — in that order. Detailed `docs/*` and measured tests/benchmarks are authoritative for technical facts. Report contradictions, don't silently reconcile.

## 1. What NextSQL Is

Native OLTP + vector DB in Go. Not PG/MySQL compatible. Own parser/binder/planner/optimizer/executor, MVCC, WAL, Raft, B+Tree, HNSW. Native drivers/protocols; Studio is web-based via official interfaces. Intelligence/RAG is optional and never on correctness path.

## 2. Current Gate

P0–P27 complete. P16's terminal 100M B+Tree soak is a documented standalone measurement outside the gate. P17's `REBUILD INDEX ... ONLINE` ships for non-partitioned BTree/UNIQUE/JSON-path/spatial indexes; vector, full-text, and partitioned indexes retain the blocking fallback. **P28 NextSQL Admin (Setup + Operations modes) is the current release gate** — Operations mode's MVP is complete, and Setup mode's implemented wizard, Linux packaging, silent/offline/upgrade/repair flows, and accessibility baseline are verified; recovery-key capability and Windows/macOS execution remain blocked. **P29 Studio is in progress** — M1–M3, all five native explorers, Users/Roles, read-only Transaction/Lock and Audit explorers, bounded plan comparison/ANALYZE profiler, catalog-aware table/column IntelliSense (no keyword completion), and deterministic misspelled FROM/JOIN table-name suggestions are implemented; its MVP gate remains open. P30 (Intelligence) is planned. Always verify the highest-numbered log entry in `TODO.md` — it wins over this file.

## 3. Priority Order

Correctness → durability → security → integrity → availability → latency → throughput → efficiency → DX → features. Never trade earlier for later.

## 4. Before Changing Code

Identify owning phase, TODO section, existing impl, and impact on: persistent format, wire/protocol, catalog, txn/WAL/recovery, Raft/failover, RBAC/tenant, resource abuse, driver/API, docs, benchmarks. Smallest coherent increment.

## 5–7. Workflows

SQL: grammar → lexer/parser/AST → binder/types → catalog/deps → logical → optimizer → physical → executor → txn/WAL → recovery → replication → RBAC → protocol/driver → EXPLAIN → metrics → tests/fuzz → docs.

Persistent: versioned format, corruption validation, encryption domain, allocation/reclamation, WAL, crash/restart, backup/restore, PITR, replication, upgrade, decoder fuzzing, diagnostics, benchmarks. Never serialize raw Go structs; never unversioned.

Distributed: leader authority, deterministic replicated state, quorum, failure/retry/idempotency, failover, duplicate prevention, clock assumptions. Test partitions, leader kill, follower repair, rolling maintenance.

## 8–9. Security & Resource Safety

Encryption mandatory, established crypto only, fail closed, no keys in URLs, no secret logging, no isolation bypass. No unbounded goroutines/allocations/buffers/queues/recursion/maintenance — use bounded pools, admission control, budgets, timeouts, streaming, backpressure, spill.

## 10–11. Testing & Benchmarks

Run applicable: unit/integration/restart/crash/WAL/txn/concurrency/race/fuzz/Raft/backup/PITR/RBAC/tenant/prepared/driver/resource/benchmark/docs. Min: `go test ./...` and `go test -race ./...`.

Benchmarks keep fsync/WAL/encryption/checksums/MVCC/auth/durability enabled unless experimental. Record CPU/RAM/storage/FS/row width/query/indexes/cache/enc/durability/concurrency/QPS/p50/p95/p99/p99.9/memory/disk/WAL/recall. Never publish vector latency without recall.

## 12–14. Docs, Direction, Don'ts

Update docs/* on behavior change; TODO only when impl+tests+docs+gate justify; ROADMAP derived from TODO. P19 WORKFLOW/TRIGGER/SCHEDULE/TASK, P20 CDC (committed only), P21 partitioning, P22 follower reads (explicit consistency), P23 Vector 2.0, P24 FTS 2.0, P25 Security 2.0, P26 Catalog 2.0, P27 governance, P28 Installer/Manager, P29 Studio, P30 Intelligence. Never skip gates, add hidden compat, make LLM the optimizer, read raw files via Studio, create unbounded pools, weaken durability, reinterpret blocking as online, treat locality as auth, expose uncommitted CDC, route strong reads to followers that can't satisfy, or lower recall silently.

## 15. Codex Rules

Concise, factual, no emojis unless asked. Search/read before edit. Verify with repo tests (not self-label PASS). Leave uncommitted for review. Revert collateral lockfile rewrites. Boundaries: pair start/end conventions and justify.

## 16. Done Checklist

Compiles, tests/race pass, restart/crash/WAL/Raft where applicable, fuzz/RBAC/tenant/resource/driver/benchmarks, docs, TODO truthful, gate actually satisfied. When uncertain: more correct/durable/secure/observable/bounded/testable/explicit.
