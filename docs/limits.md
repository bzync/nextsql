# Production limits

This document is the authoritative limit catalog for NextSQL. The operational
server limits table is generated from `internal/limits.Catalog()` and checked against it
by `TestDocsLimitsMatchCatalog`.

The distinction the catalog exists to keep is between a *default*, which an
operator moves freely to match their hardware and workload, and a *ceiling*,
which they cannot raise. Without the second, a legitimate tuning knob is also
an unbounded allocation request: `buffer_pages=1000000000` is a plausible
typo that asks for a 16 TiB buffer pool at startup, and overload must mean
controlled rejection, never OOM.

## Operational limits

| Key | Class | Unit | Default | Accepted range | Zero means | Why |
| --- | --- | --- | ---: | --- | --- | --- |
| buffer_pages | memory | 16 KiB pages | 1024 | [1, 4194304] | - | the buffer pool is reserved at startup; the ceiling is 64 GiB of pages, so a mistyped value is rejected instead of becoming an allocation the host cannot satisfy |
| checkpoint_interval_ms | time | ms | 300000 | 0 or [1, 86400000] | no periodic checkpoint; the redo boundary advances only on clean close or backup | bounds the WAL suffix an unclean restart must replay before the node can listen |
| disk_watermark_check_ms | time | ms | 0 | 0 or [1, 86400000] | disk watermarks disabled | a background probe; a longer period than a day is an operator meaning to disable it |
| idle_timeout_ms | time | ms | subsystem default | [1, 86400000] | - | an idle connection holds a session, its buffers and a file descriptor |
| idle_transaction_timeout_ms | time | ms | 0 | 0 or [1, 86400000] | no idle-transaction timeout | an idle open transaction pins a snapshot and its UNDO, holding back vacuum |
| lock_timeout_ms | time | ms | 0 | 0 or [1, 86400000] | wait indefinitely for a lock | bounds how long one statement blocks holding its own locks |
| max_connections | concurrency | count | subsystem default | [1, 1048576] | - | every accepted connection carries a session, buffers and a file descriptor |
| max_connections_per_database | concurrency | count | 0 | 0 or [1, 1048576] | unlimited within max_connections | keeps one database from consuming the whole connection budget |
| max_connections_per_realm | concurrency | count | 0 | 0 or [1, 1048576] | unlimited within max_connections | keeps one tenant realm from consuming the whole connection budget |
| max_connections_per_user | concurrency | count | 0 | 0 or [1, 1048576] | unlimited within max_connections | keeps one principal from consuming the whole connection budget |
| max_frame_bytes | wire | bytes | 67108864 | [64, 67108864] | - | the total decoded bytes of one request, checked before allocation; independent of the statement limit so a statement cannot claim the whole frame by default |
| max_inflight_queries | concurrency | count | 32 | [1, 65536] | - | each admitted query holds a worker, a memory budget and a snapshot; admission control is what turns overload into queueing instead of OOM |
| max_open_databases | memory | count | 8 | 0 or [1, 65536] | leave the default | accepted and ignored: multi-database hosting was removed and a deployment opens exactly one database. Retained so a nextsql.conf written by an earlier release still loads |
| max_parameters | wire | count | 65535 | [1, 65535] | - | the wire parameter count is a uint16, so the ceiling is the representation; decoding grows only with the parameters actually supplied |
| max_prepared_statements | memory | count | 64 | [1, 4096] | - | each prepared statement retains parsed and planned state for the lifetime of a session; the ceiling bounds per-connection resident memory |
| max_query_queue | concurrency | count | 128 | 0 or [1, 1048576] | no queue; reject immediately past max_inflight_queries | a queued request holds its decoded frame while it waits |
| max_result_bytes | result | bytes | 67108864 | [1, 67108864] | - | bounds the bytes one query may produce; a row budget must not silently change SQL semantics, so this fails the query rather than truncating it |
| max_result_rows | result | rows | 1000000 | [1, 2147483648] | - | bounds buffered and streamed rows per query; exceeding it is an explicit exhausted error, never a silent LIMIT |
| max_statement_bytes | wire | bytes | 16777216 | [1, 67108864] | - | bounds SQL text handed to the lexer and parser; must not exceed max_frame_bytes, which already bounds the whole request |
| max_total_buffer_pages | memory | 16 KiB pages | 0 | 0 or [1, 4194304] | unbounded; each database keeps its own buffer_pages | the shared budget across every open database in a multi-database deployment; must be at least buffer_pages |
| prealloc_ahead_pages | storage | 16 KiB pages | 16384 | [1, 1048576] | - | the allocation runway fallocate reserves ahead of the data file, per database; real blocks, so many small databases multiply it |
| query_queue_wait_ms | time | ms | 5000 | [1, 3600000] | - | how long an admitted-but-queued request waits before being rejected; it holds its decoded frame the whole time |
| raft_commit_timeout_ms | time | ms | 0 | 0 or [10, 60000] | the built-in default (50 ms) | how long a leader batches log entries before flushing them to followers; raising it trades commit latency for fewer, larger append rounds |
| raft_election_ms | time | ms | 0 | 0 or [10, 60000] | the built-in default (250 ms) | how long a follower waits without leader contact before campaigning; must be at least raft_heartbeat_ms, or a node campaigns while the leader is still healthy |
| raft_heartbeat_ms | time | ms | 0 | 0 or [10, 60000] | the built-in default (250 ms) | the leader-contact interval, and the unit the follower-read freshness window is built from: the healthy-contact window is five heartbeats, so raising this widens the default MAX STALENESS with it |
| raft_leader_lease_ms | time | ms | 0 | 0 or [10, 60000] | the built-in default (200 ms) | how long a leader may act as leader without contacting a quorum; must not exceed raft_heartbeat_ms, or a leader keeps acting past the point it can still prove it is one |
| replica_lag_check_ms | time | ms | 0 | 0 or [1, 86400000] | replica lag monitoring disabled | a background probe; a longer period than a day is an operator meaning to disable it |
| replica_lag_warn_entries | concurrency | entries | 0 | 0 or [1, 2147483648] | no lag warning threshold | the replicated-entry backlog that raises an operational warning |
| shutdown_drain_ms | time | ms | 30000 | 0 or [1, 3600000] | do not wait for in-flight work on shutdown | how long a shutdown waits for in-flight statements before closing; a drain longer than an hour is an outage, not a drain |
| statement_timeout_ms | time | ms | subsystem default | [1, 86400000] | - | bounds one statement's wall time so a pathological query cannot hold a worker forever |
| task_workers | concurrency | count | 0 | 0 or [1, 4096] | leave the scheduler default | the bounded background pool for scheduled tasks and maintenance |
| transaction_timeout_ms | time | ms | 0 | 0 or [1, 86400000] | no transaction timeout | bounds a whole transaction, which holds locks and pins a snapshot for its lifetime |
| wal_retention_ms | storage | ms | 0 | 0 or [1, 31536000000] | retain WAL history indefinitely | how long checkpointed WAL is kept for PITR and page repair; pruning is a no-op until an archiver is configured |

## Structural and format limits

This catalog does not hold structural or format limits (page size, vector
dimension, geometry vertices, catalog descriptor size). Those are not
operational knobs — changing one is a format or encoding change, and they
live with the code that defines the encoding.

| Limit | Value | Scope / location | Reason and failure mode |
| --- | ---: | --- | --- |
| Logical page | 16 KiB | `internal/storage/format` | Structural page layout; changing it requires a format migration. |
| JSON bytes / depth | 1 MiB / 32 | `internal/json` | Prevents oversized allocation and recursive parser exhaustion. |
| Dense vector dimension | 8192 | `internal/sql/types` | Bounds vector allocation and index work. |
| Sparse vector dimension / NNZ | 2^24 / 65536 | `internal/vector` | Prevents coordinate and posting-list abuse. |
| Fixed geo vertices | 256 | `internal/sql/types` | LINESTRING/POLYGON abuse cap; lifting requires serialized-byte/work budgets. |
| General spatial vertices / depth / parts | 65536 / 8 / 4096 | `internal/sql/types` | GEOMETRY/GEOGRAPHY recursive construction protection. |
| Collection nesting / elements | 8 / 2^20 | `internal/sql/types` | Protects recursive type/decoder and allocation paths. |
| JOIN relations | 8 | `internal/security` | Planner complexity protection; do not raise without independent work budgets. |
| FK cascade depth / rows | 8 / 100,000 | `internal/security` | Bounds recursive mutation, WAL growth and transaction work. |
| Workflow statements / params | 256 / 64 | `internal/catalog` | Bounds stored descriptor and execution work. |
| FTS fuzzy vocabulary | 4096 | `internal/fulltext` | Bounds vocabulary expansion and CPU work. |
