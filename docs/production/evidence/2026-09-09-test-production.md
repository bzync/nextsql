# make test-production — local durable-filesystem run

Recorded 2026-09-09 from `NEXTSQL_REQUIRE_DURABLE_FS=1 make test-production`,
with every change from logs #247-#256 in the tree. Exit 0, **36 packages** (the
profile covered 24 before log #256 added the query engine, crash, HA and
integration suites), no failure, panic, data race or timeout.

This is LOCAL evidence. The hosted CI artifact required by `GAPS.md` is separate
and still outstanding — a local run does not satisfy it.

```text
./scripts/test-production.sh production
test scratch: /home/rayan/bzync-server/nextsql/.test-tmp-production/run-1733205 (filesystem: ext2/ext3)
ok  	github.com/bzync/nextsql/internal/config	0.034s
ok  	github.com/bzync/nextsql/internal/scheduler	0.828s
ok  	github.com/bzync/nextsql/internal/protocol	(cached)
ok  	github.com/bzync/nextsql/internal/json	(cached)
ok  	github.com/bzync/nextsql/internal/security	1.304s
ok  	github.com/bzync/nextsql/internal/auth	0.757s
ok  	github.com/bzync/nextsql/internal/replication	(cached)
ok  	github.com/bzync/nextsql/internal/executor	111.660s
?   	github.com/bzync/nextsql/internal/sql/ast	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/binder	(cached)
?   	github.com/bzync/nextsql/internal/sql/lexer	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/optimizer	(cached)
ok  	github.com/bzync/nextsql/internal/sql/parser	(cached)
?   	github.com/bzync/nextsql/internal/sql/planner	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/types	(cached)
ok  	github.com/bzync/nextsql/internal/catalog	(cached)
ok  	github.com/bzync/nextsql/internal/crypto	0.017s
ok  	github.com/bzync/nextsql/internal/storage	0.624s
ok  	github.com/bzync/nextsql/internal/storage/allocator	0.104s
ok  	github.com/bzync/nextsql/internal/storage/btree	119.966s
ok  	github.com/bzync/nextsql/internal/storage/buffer	(cached)
ok  	github.com/bzync/nextsql/internal/storage/checksum	(cached)
ok  	github.com/bzync/nextsql/internal/storage/file	0.018s
ok  	github.com/bzync/nextsql/internal/storage/format	(cached)
ok  	github.com/bzync/nextsql/internal/storage/integrity	0.004s
ok  	github.com/bzync/nextsql/internal/storage/io	0.006s
ok  	github.com/bzync/nextsql/internal/storage/page	(cached)
ok  	github.com/bzync/nextsql/internal/storage/row	(cached)
ok  	github.com/bzync/nextsql/internal/wal	2.770s
ok  	github.com/bzync/nextsql/internal/recovery	0.458s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	3.445s
ok  	github.com/bzync/nextsql/tests/fault	62.357s
ok  	github.com/bzync/nextsql/internal/storage/io	0.017s
ok  	github.com/bzync/nextsql/tests/upgrade	69.610s
ok  	github.com/bzync/nextsql/tests/crash	4.610s
ok  	github.com/bzync/nextsql/tests/ha	34.229s
ok  	github.com/bzync/nextsql/tests/integration	15.006s
ok  	github.com/bzync/nextsql/internal/protocol	(cached)
ok  	github.com/bzync/nextsql/internal/security	2.380s
ok  	github.com/bzync/nextsql/internal/executor	114.633s
ok  	github.com/bzync/nextsql/internal/storage	1.742s
ok  	github.com/bzync/nextsql/internal/storage/btree	343.697s
ok  	github.com/bzync/nextsql/internal/wal	2.176s
ok  	github.com/bzync/nextsql/internal/recovery	1.537s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	7.053s
ok  	github.com/bzync/nextsql/tests/fault	60.298s
ok  	github.com/bzync/nextsql/cmd/nextsqld	0.626s
ok  	github.com/bzync/nextsql/cmd/nextsql	75.162s
```
