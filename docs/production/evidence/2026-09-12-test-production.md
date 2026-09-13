# make test-production — local durable-filesystem run (logs #271-#280)

Recorded 2026-09-12 from `NEXTSQL_REQUIRE_DURABLE_FS=1 make test-production`,
with every change from logs #271-#280 in the tree. Exit 0, **49 packages**, no
failure, panic, data race or timeout. The scratch line below records
`filesystem: ext2/ext3` — a real block device, not `tmpfs`, so the run is
usable durability evidence rather than fsyncs into RAM.

This is the evidence for the engine work in those logs, and it is the run that
matters most for three of them, because each changed a path the whole engine
sits on:

- **#271** bounds statement nesting in the parser and in the finished tree, so
  every statement in every suite passes through the new check.
- **#276** refuses a write whose target row belongs to a still-running
  transaction, at every isolation level — the core write path for `INSERT`,
  `UPDATE` and `DELETE`, including foreign-key cascades and index maintenance.
- **#278** narrows subquery correlation when the inner relation's columns are
  not visible, which every correlated subquery in the suites exercises.

It also covers the new persistent format: `tests/upgrade` replays the retained
data directories written by the shipped `v0.0.1` binaries, which is what proves
catalog `NSCT` v14 (log #273, `CHECK` constraints) stayed opt-in — a database
that declares no check still writes v13 bytes that release can read.

```text
./scripts/test-production.sh production
test scratch: /home/rayan/bzync-server/nextsql/.test-tmp-production/run-1421105 (filesystem: ext2/ext3)
ok  	github.com/bzync/nextsql/internal/config	0.037s
ok  	github.com/bzync/nextsql/internal/limits	0.005s
ok  	github.com/bzync/nextsql/internal/scheduler	0.868s
ok  	github.com/bzync/nextsql/internal/protocol	0.007s
ok  	github.com/bzync/nextsql/internal/json	(cached)
ok  	github.com/bzync/nextsql/internal/security	1.381s
ok  	github.com/bzync/nextsql/internal/auth	0.950s
ok  	github.com/bzync/nextsql/internal/replication	(cached)
ok  	github.com/bzync/nextsql/internal/executor	98.771s
ok  	github.com/bzync/nextsql/internal/sql/ast	1.131s
ok  	github.com/bzync/nextsql/internal/sql/binder	0.040s
?   	github.com/bzync/nextsql/internal/sql/lexer	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/optimizer	0.247s
ok  	github.com/bzync/nextsql/internal/sql/parser	0.136s
?   	github.com/bzync/nextsql/internal/sql/planner	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/types	(cached)
ok  	github.com/bzync/nextsql/internal/catalog	0.199s
ok  	github.com/bzync/nextsql/internal/crypto	0.013s
ok  	github.com/bzync/nextsql/internal/storage	0.669s
ok  	github.com/bzync/nextsql/internal/storage/allocator	0.105s
ok  	github.com/bzync/nextsql/internal/storage/btree	166.432s
ok  	github.com/bzync/nextsql/internal/storage/buffer	(cached)
ok  	github.com/bzync/nextsql/internal/storage/checksum	(cached)
ok  	github.com/bzync/nextsql/internal/storage/file	0.006s
ok  	github.com/bzync/nextsql/internal/storage/format	(cached)
ok  	github.com/bzync/nextsql/internal/storage/integrity	0.004s
ok  	github.com/bzync/nextsql/internal/storage/io	0.008s
ok  	github.com/bzync/nextsql/internal/storage/page	(cached)
ok  	github.com/bzync/nextsql/internal/storage/row	(cached)
ok  	github.com/bzync/nextsql/internal/wal	0.714s
ok  	github.com/bzync/nextsql/internal/recovery	0.072s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	3.675s
ok  	github.com/bzync/nextsql/tests/fault	79.529s
ok  	github.com/bzync/nextsql/internal/storage/io	0.101s
ok  	github.com/bzync/nextsql/tests/upgrade	102.750s
ok  	github.com/bzync/nextsql/tests/crash	4.963s
ok  	github.com/bzync/nextsql/tests/ha	55.592s
ok  	github.com/bzync/nextsql/tests/integration	16.203s
ok  	github.com/bzync/nextsql/internal/protocol	1.020s
ok  	github.com/bzync/nextsql/internal/security	2.535s
ok  	github.com/bzync/nextsql/internal/executor	134.568s
ok  	github.com/bzync/nextsql/internal/storage	1.731s
ok  	github.com/bzync/nextsql/internal/storage/btree	389.484s
ok  	github.com/bzync/nextsql/internal/wal	1.728s
ok  	github.com/bzync/nextsql/internal/recovery	1.086s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	5.449s
ok  	github.com/bzync/nextsql/tests/fault	75.408s
ok  	github.com/bzync/nextsql/cmd/nextsqld	3.973s
ok  	github.com/bzync/nextsql/cmd/nextsql	44.696s
```
