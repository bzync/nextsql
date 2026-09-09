# make test-production — local durable-filesystem run

Recorded 2026-09-10 from `NEXTSQL_REQUIRE_DURABLE_FS=1 make test-production`,
with every change from logs #247-#257 in the tree. Exit 0, **37 packages** (the
profile covered 24 before log #256 added the query engine, crash, HA and
integration suites, and 36 before log #257 added `internal/limits` — the
operational limit catalog every other package validates against, and the source
`docs/limits.md` is generated from, which no profile had ever run).

This run is the evidence for log #257 (configurable Raft timings). It includes
the live election, replication, failover and partition tests that run on
non-default Raft intervals, and `internal/replication` still runs first, with
the CPU-bound set, for the reason recorded in log #256.

This is LOCAL evidence. The hosted CI artifact required by `GAPS.md` is separate
and still outstanding — a local run does not satisfy it.

```text
./scripts/test-production.sh production
test scratch: /home/rayan/bzync-server/nextsql/.test-tmp-production/run-1822759 (filesystem: ext2/ext3)
ok  	github.com/bzync/nextsql/internal/config	0.042s
ok  	github.com/bzync/nextsql/internal/limits	(cached)
ok  	github.com/bzync/nextsql/internal/scheduler	0.828s
ok  	github.com/bzync/nextsql/internal/protocol	(cached)
ok  	github.com/bzync/nextsql/internal/json	(cached)
ok  	github.com/bzync/nextsql/internal/security	1.281s
ok  	github.com/bzync/nextsql/internal/auth	0.902s
ok  	github.com/bzync/nextsql/internal/replication	13.290s
ok  	github.com/bzync/nextsql/internal/executor	72.814s
?   	github.com/bzync/nextsql/internal/sql/ast	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/binder	(cached)
?   	github.com/bzync/nextsql/internal/sql/lexer	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/optimizer	(cached)
ok  	github.com/bzync/nextsql/internal/sql/parser	(cached)
?   	github.com/bzync/nextsql/internal/sql/planner	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/types	(cached)
ok  	github.com/bzync/nextsql/internal/catalog	(cached)
ok  	github.com/bzync/nextsql/internal/crypto	0.021s
ok  	github.com/bzync/nextsql/internal/storage	0.643s
ok  	github.com/bzync/nextsql/internal/storage/allocator	0.114s
ok  	github.com/bzync/nextsql/internal/storage/btree	195.111s
ok  	github.com/bzync/nextsql/internal/storage/buffer	(cached)
ok  	github.com/bzync/nextsql/internal/storage/checksum	(cached)
ok  	github.com/bzync/nextsql/internal/storage/file	0.006s
ok  	github.com/bzync/nextsql/internal/storage/format	(cached)
ok  	github.com/bzync/nextsql/internal/storage/integrity	0.003s
ok  	github.com/bzync/nextsql/internal/storage/io	0.005s
ok  	github.com/bzync/nextsql/internal/storage/page	(cached)
ok  	github.com/bzync/nextsql/internal/storage/row	(cached)
ok  	github.com/bzync/nextsql/internal/wal	0.716s
ok  	github.com/bzync/nextsql/internal/recovery	0.071s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	3.552s
ok  	github.com/bzync/nextsql/tests/fault	96.767s
ok  	github.com/bzync/nextsql/internal/storage/io	0.014s
ok  	github.com/bzync/nextsql/tests/upgrade	141.677s
ok  	github.com/bzync/nextsql/tests/crash	4.954s
ok  	github.com/bzync/nextsql/tests/ha	44.788s
ok  	github.com/bzync/nextsql/tests/integration	17.264s
ok  	github.com/bzync/nextsql/internal/protocol	1.062s
ok  	github.com/bzync/nextsql/internal/security	3.124s
ok  	github.com/bzync/nextsql/internal/executor	128.535s
ok  	github.com/bzync/nextsql/internal/storage	1.705s
ok  	github.com/bzync/nextsql/internal/storage/btree	341.801s
ok  	github.com/bzync/nextsql/internal/wal	1.773s
ok  	github.com/bzync/nextsql/internal/recovery	1.124s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	5.468s
ok  	github.com/bzync/nextsql/tests/fault	94.582s
ok  	github.com/bzync/nextsql/cmd/nextsqld	3.981s
ok  	github.com/bzync/nextsql/cmd/nextsql	97.414s
```

Exit status 0. No `FAIL`, `panic`, `WARNING: DATA RACE` or timeout appears in
the log above.

An earlier attempt at this same run failed with
`internal/replication/timing.go:4:2: could not import fmt` — a stale build
cache, because the tree was edited while `go test` was running, not a defect.
The run recorded here was started after the tree was final and left untouched.
