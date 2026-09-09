# make test-production — first green HOSTED run (GitHub Actions)

Recorded 2026-09-10 from the `Production evidence gate` workflow run
[34407898651](https://github.com/bzync/nextsql/actions/runs/34407898651), on the
`v0.0.1` release tag at commit `102dc5f`. Exit 0, **48 packages**, no failure,
panic, data race or timeout, `filesystem: ext2/ext3`. Wall clock 21:36:58 →
21:47:48 UTC (10m50s).

**This is the hosted artifact `GAPS.md` had been holding two rows open for.**
Every retained run before it — `2026-09-09-test-production.md`,
`2026-09-10-test-production.md`, `2026-09-10-test-production-log258.md` — is a
local run, and each says in its own text that the hosted CI artifact is separate
and still outstanding. It no longer is.

Two things this run establishes that a local one cannot:

- the scratch path resolves to a real block device on the runner
  (`filesystem: ext2/ext3`, not `tmpfs`), so the durability work is fsyncing to
  a disk rather than into RAM — the failure mode recorded in log #233;
- the profile that runs in CI is the post-log-#256 one, and the packages that
  gate was missing are present and green here: `internal/executor` (30.1s, and
  116.7s again under `-race`), `internal/sql/...`, `internal/replication`,
  `tests/crash`, `tests/ha` and `tests/integration`.

The workflow artifact itself (`nextsql-production-evidence-34407898651`) expires
2026-10-09, which is why the log is copied here rather than left as a link.

```text
./scripts/test-production.sh production
test scratch: /home/runner/work/nextsql/nextsql/.test-tmp-production/run-2343 (filesystem: ext2/ext3)
ok  	github.com/bzync/nextsql/internal/config	0.042s
ok  	github.com/bzync/nextsql/internal/limits	0.004s
ok  	github.com/bzync/nextsql/internal/scheduler	0.864s
ok  	github.com/bzync/nextsql/internal/protocol	0.006s
ok  	github.com/bzync/nextsql/internal/json	(cached)
ok  	github.com/bzync/nextsql/internal/security	1.318s
ok  	github.com/bzync/nextsql/internal/auth	0.831s
ok  	github.com/bzync/nextsql/internal/replication	(cached)
ok  	github.com/bzync/nextsql/internal/executor	30.057s
?   	github.com/bzync/nextsql/internal/sql/ast	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/binder	(cached)
?   	github.com/bzync/nextsql/internal/sql/lexer	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/optimizer	(cached)
ok  	github.com/bzync/nextsql/internal/sql/parser	(cached)
?   	github.com/bzync/nextsql/internal/sql/planner	[no test files]
ok  	github.com/bzync/nextsql/internal/sql/types	(cached)
ok  	github.com/bzync/nextsql/internal/catalog	(cached)
ok  	github.com/bzync/nextsql/internal/crypto	0.015s
ok  	github.com/bzync/nextsql/internal/storage	0.254s
ok  	github.com/bzync/nextsql/internal/storage/allocator	0.041s
ok  	github.com/bzync/nextsql/internal/storage/btree	53.639s
ok  	github.com/bzync/nextsql/internal/storage/buffer	(cached)
ok  	github.com/bzync/nextsql/internal/storage/checksum	(cached)
ok  	github.com/bzync/nextsql/internal/storage/file	0.005s
ok  	github.com/bzync/nextsql/internal/storage/format	(cached)
ok  	github.com/bzync/nextsql/internal/storage/integrity	0.003s
ok  	github.com/bzync/nextsql/internal/storage/io	0.004s
ok  	github.com/bzync/nextsql/internal/storage/page	(cached)
ok  	github.com/bzync/nextsql/internal/storage/row	(cached)
ok  	github.com/bzync/nextsql/internal/wal	0.377s
ok  	github.com/bzync/nextsql/internal/recovery	0.034s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	1.815s
ok  	github.com/bzync/nextsql/tests/fault	7.323s
ok  	github.com/bzync/nextsql/internal/storage/io	0.004s
ok  	github.com/bzync/nextsql/tests/upgrade	12.823s
ok  	github.com/bzync/nextsql/tests/crash	2.507s
ok  	github.com/bzync/nextsql/tests/ha	13.607s
ok  	github.com/bzync/nextsql/tests/integration	18.294s
ok  	github.com/bzync/nextsql/internal/protocol	1.019s
ok  	github.com/bzync/nextsql/internal/security	2.502s
ok  	github.com/bzync/nextsql/internal/executor	116.682s
ok  	github.com/bzync/nextsql/internal/storage	1.439s
ok  	github.com/bzync/nextsql/internal/storage/btree	304.999s
ok  	github.com/bzync/nextsql/internal/wal	1.488s
ok  	github.com/bzync/nextsql/internal/recovery	1.042s
ok  	github.com/bzync/nextsql/internal/txn	(cached)
ok  	github.com/bzync/nextsql/internal/backup	3.934s
ok  	github.com/bzync/nextsql/tests/fault	18.761s
ok  	github.com/bzync/nextsql/cmd/nextsqld	0.375s
ok  	github.com/bzync/nextsql/cmd/nextsql	7.213s
```
