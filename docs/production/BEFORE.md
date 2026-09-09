# Production hardening baseline

**Captured:** 2026-09-08. This is a code-backed baseline, not a release claim.
The worktree contained unrelated uncommitted changes when the audit began; this
document describes the checked-out implementation as inspected, not a pristine
release artifact.

| Area | Existing behavior | Risk | Severity | Evidence |
| --- | --- | --- | --- | --- |
| Storage/page format | 16 KiB logical pages, authenticated encrypted envelopes, versioned page/superblock formats, allocator and isolated corrupt pages | A full release-gate run was not assembled in one command | HIGH | `internal/storage`, `internal/storage/page`, `internal/storage/file` |
| B+Tree | Clustered B+Tree with split/merge/MVCC and a structural `Check()` walk | 100M randomized invariant soak remains a separate deferred measurement | MEDIUM | `internal/storage/btree/check.go`, `scripts/run-btree-soak.sh` |
| WAL/recovery | Encrypted/checksummed, segmented WAL; fsync/held-commit crash semantics; redo and partial-tail handling | Disk-full/EIO fault injection is not a demonstrated release gate | HIGH | `internal/wal`, `internal/recovery`, `internal/storage/btree/mvcc_test.go` |
| Transactions/locking | MVCC snapshots, undo, locks, deadlock handling and named isolation modes | Isolation matrix and adversarial concurrency release evidence are not consolidated | HIGH | `internal/txn`, `internal/undo`, `docs/mvcc.md` |
| SQL/planner/executor | Native lexer/parser/binder/optimizer/executor; scheduler has time, memory, I/O, disk and result budgets | JOIN relation cap is a fixed syntactic limit; planner search/node budgets are not independently exposed | HIGH | `internal/sql`, `internal/scheduler/budget.go`, `internal/security/limits.go` |
| Protocol | Versioned NSQL frames; frame length checked before allocation; query/parameter decode fuzzers | Packet, SQL text and several payload limits remain coupled at 1 MiB | HIGH | `internal/protocol/frame.go`, `internal/protocol/messages.go` |
| Authentication/TLS/RBAC | Argon2id migration, mTLS/TLS, signed credentials, RBAC and audit chain are implemented | Need repeatable full negative-path release execution per deployment | MEDIUM | `internal/auth`, `internal/security`, `docs/security.md` |
| Backup/restore/upgrade | Encrypted backup manifest/sealing, restore verification, PITR tests and format compatibility controls | Previous-release fixture compatibility is not a declared automated release gate | HIGH | `internal/backup`, `internal/compat`, `cmd/nextsql/lifecycle.go` |
| Runtime/container | Production profile preflight, lifecycle controls, Docker entrypoint tests | Windows/macOS installer execution and recovery-key flow remain blocked | HIGH | `internal/config/production.go`, `internal/dockerentry`, `TODO.md` |
| Observability/error model | Typed internal `nerr` codes and operational catalog/metrics exist | Public errors are not yet normalized to the requested stable `ERR_*` vocabulary | MEDIUM | `internal/nerr/nerr.go`, `docs/ops.md` |
| Test/fuzz/soak organization | Package tests, fuzz targets and a B-tree soak script exist | No single practical production profile or separate named PR/nightly/chaos profiles | HIGH | existing `*_test.go`, `scripts/run-btree-soak.sh` |

## Existing limit inventory

This inventory records enforceable source limits, not just web documentation.

| Limit | Current value | Location | Class | Failure / impact | Recommended action |
| --- | ---: | --- | --- | --- | --- |
| Logical page | 16 KiB | `internal/storage/format` | Structural | format incompatibility | retain; version format changes |
| Wire frame | 1 MiB | `internal/protocol/frame.go` | Safety ceiling/default coupled | protocol error before allocation | separate configurable default from ceiling |
| SQL text | 1 MiB | `internal/protocol/frame.go`, `internal/security/limits.go` | Safety/default coupled | protocol error | separate from frame and catalog descriptor size |
| Parameters | 256 | `internal/protocol/frame.go` | Fixed safety/default | protocol error | redesign decoding/allocation before increasing |
| Prepared statements/session | 64 | `internal/protocol/frame.go` | Production default | protocol error | make configurable with memory accounting |
| Result bytes | 64 MiB | protocol/scheduler | Production budget | explicit exhausted/error | preserve streaming semantics; document total vs batch |
| Result rows | 1,000,000 | `internal/scheduler/budget.go` | Production default | explicit budget failure, not SQL truncation | expose operational configuration |
| JSON bytes/depth/string/elements | 1 MiB / 32 / 1 MiB / 2^20 | `internal/json/json.go` | Safety caps | invalid argument | separate defaults from absolute ceilings after parser audit |
| Dense/bit vector dimension | 8192 | `internal/sql/types/types.go`, `internal/vector/encode.go` | Structural encoding/abuse cap | invalid argument | validate representation before change |
| Sparse dimensions/NNZ | 2^24 / 65536 | `internal/vector/sparse.go` | Safety cap | invalid argument | retain bounded decoder semantics |
| Fixed geo vertices | 256 | `internal/sql/types/types.go` | Abuse cap | invalid argument | redesign around byte/work budgets before lift |
| General spatial vertices/depth/parts | 65536 / 8 / 4096 | `internal/sql/types/spatial.go` | Safety caps | invalid argument | document separately from fixed geo |
| Collection nesting/elements | 8 / 2^20 | `internal/sql/types/types.go`, `internal/json` | Structural/safety | invalid argument | preserve recursive-decoder protection |
| JOIN relations/CTEs/recursive steps | 8 / 32 / 100 | `internal/security/limits.go` | Complexity caps | binder error | add planner work budgets before lifting JOIN cap |
| FK columns | 8 | `internal/catalog/catalog.go` | Catalog format/safety | invalid argument | validate descriptor evolution first |
| FK cascade depth/touched rows | 8 / 100,000 | `internal/security/limits.go` | Safety caps | `exhausted`, atomic rollback | make production defaults configurable only with work budgets |
| Workflow statements/params/nesting | 256 / 64 / 8 | `internal/catalog/workflow.go`, executor | Catalog/safety | invalid argument/exhausted | separate descriptor format from runtime budgets |
| FTS fuzzy vocabulary scan | 4096 | `internal/fulltext` | Query work cap | bounded expansion/error | expose only with CPU/memory budget |
| Scheduler memory/disk/I/O/time | 64 MiB / 256 MiB / 1 GiB / 30 s | `internal/scheduler/budget.go` | Production defaults | `exhausted`/timeout | centralize configuration range validation |
| Workers/inflight/queue | 8 / 32 / 128 | scheduler/config | Production defaults/safety | admission rejection/queue timeout | configure alongside host capacity |
| CDC pending txns/changes/bytes | 1024 / 131072 / 16 MiB | `internal/cdc/cdc.go` | Safety caps | bounded decoder failure | document operational tuning |
| Backup chunk/archive entry | 1 MiB / 1 MiB | `internal/backup/format.go` | Format safety cap | corruption/error | retain until format migration |
| OIDC/JWKS/client credential bodies | 1 MiB | `internal/oidc`, `internal/oidcclient` | Safety cap | HTTP/input error | retain bounded reads |

The checked-in web limits page says general geometry has 256 vertices, while
the actual general-geometry implementation allows 65,536; fixed `LINESTRING`
and `POLYGON` remain 256. This is a documentation contradiction, not silently
reconciled here.
