# make test-production — local durable-filesystem run (log #258)

Recorded 2026-09-10 from `NEXTSQL_REQUIRE_DURABLE_FS=1 make test-production`,
with every change from logs #247-#258 in the tree. Exit 0, **37 packages**, no
failure, panic, data race or timeout, `filesystem: ext2/ext3`.

This run is the evidence for log #258. It is a second run on the same day as
`2026-09-10-test-production.md` (log #257's evidence, kept as recorded): that
one predates the `startRaft` readiness fix, so it is not evidence for it. The
package that matters here is `internal/replication`, which runs first with the
CPU-bound light set for the reason recorded in log #256 — the hosted gate run
that prompted log #258 failed exactly there, on
`TestStrongReadBarrierRejectsIsolatedLeader`.

A green run is weak evidence for a fix to a defect that reproduced about one run
in ten; the load-bearing evidence for log #258 is the direct invariant test and
its negative control (`TestStartRaftLeavesEveryNodeKnowingTheFullVoterSet`
fails repeatedly at `-count=60` against the un-fixed helper), plus 60/60 green
on the previously flaky test. This run only shows the fix broke nothing else.

This is LOCAL evidence. The hosted CI artifact required by `GAPS.md` is separate
and still outstanding — a local run does not satisfy it.

```text
./scripts/test-production.sh production
test scratch: /home/rayan/bzync-server/nextsql/.test-tmp-production/run-1877527 (filesystem: ext2/ext3)
ok  	github.com/bzync/nextsql/internal/config	0.043s
ok  	github.com/bzync/nextsql/internal/limits	(cached)
ok  	github.com/bzync/nextsql/internal/scheduler	0.840s
ok  	github.com/bzync/nextsql/internal/protocol	(cached)
ok  	github.com/bzync/nextsql/internal/json	(cached)
ok  	github.com/bzync/nextsql/internal/security	1.297s
ok  	github.com/bzync/nextsql/internal/auth	0.821s
ok  	github.com/bzync/nextsql/internal/replication	13.477s
ok  	github.com/bzync/nextsql/internal/executor	57.228s
?   	github.com/bzync/nextsql/internal/sql/ast	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/binder	(cached)
?   	github.com/bzync/nextsql/internal/sql/lexer	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/optimizer	(cached)
ok  	github.com/bzync/nextsql/internal/sql/parser	(cached)
?   	github.com/bzync/nextsql/internal/sql/planner	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/types	(cached)
ok  	github.com/bzync/nextsql/internal/catalog	(cached)
ok  	github.com/bzync/nextsql/internal/crypto	0.012s
ok  	github.com/bzync/nextsql/internal/storage	0.627s
ok  	github.com/bzync/nextsql/internal/storage/allocator	0.106s
ok  	github.com/bzync/nextsql/internal/storage/btree	229.279s
ok  	github.com/bzync/nextsql/internal/storage/buffer	(cached)
ok  	github.com/bzync/nextsql/internal/storage/checksum	(cached)
ok  	github.com/bzync/nextsql/internal/storage/file	0.006s
ok  	github.com/bzync/nextsql/internal/storage/format	(cached)
ok  	github.com/bzync/nextsql/internal/storage/integrity	0.003s
ok  	github.com/bzync/nextsql/internal/storage/io	0.005s
ok  	github.com/bzync/nextsql/internal/storage/page	(cached)
ok  	github.com/bzync/nextsql/internal/storage/row	(cached)
ok  	github.com/bzync/nextsql/internal/wal	0.692s
ok  	github.com/bzync/nextsql/internal/recovery	0.070s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	3.400s
ok  	github.com/bzync/nextsql/tests/fault	54.673s
ok  	github.com/bzync/nextsql/internal/storage/io	0.014s
ok  	github.com/bzync/nextsql/tests/upgrade	111.408s
ok  	github.com/bzync/nextsql/tests/crash	4.891s
ok  	github.com/bzync/nextsql/tests/ha	21.296s
ok  	github.com/bzync/nextsql/tests/integration	17.557s
ok  	github.com/bzync/nextsql/internal/protocol	(cached)
ok  	github.com/bzync/nextsql/internal/security	2.389s
ok  	github.com/bzync/nextsql/internal/executor	120.347s
ok  	github.com/bzync/nextsql/internal/storage	1.713s
ok  	github.com/bzync/nextsql/internal/storage/btree	365.273s
ok  	github.com/bzync/nextsql/internal/wal	5.163s
ok  	github.com/bzync/nextsql/internal/recovery	1.380s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	5.067s
ok  	github.com/bzync/nextsql/tests/fault	73.612s
ok  	github.com/bzync/nextsql/cmd/nextsqld	0.586s
ok  	github.com/bzync/nextsql/cmd/nextsql	98.427s

[exited with code 0]
```
