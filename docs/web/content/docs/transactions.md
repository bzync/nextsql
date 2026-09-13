# Transactions

```sql
BEGIN;                    -- default SNAPSHOT
BEGIN READ COMMITTED;
BEGIN SNAPSHOT;
BEGIN SERIALIZABLE;
SAVEPOINT name;
ROLLBACK TO SAVEPOINT name;
RELEASE SAVEPOINT name;
COMMIT;
ROLLBACK;
```

| Level | Snapshot | Locks |
|---|---|---|
| `READ COMMITTED` | Refreshed each statement | Exclusive key locks until end of transaction |
| `SNAPSHOT` | Taken at `BEGIN` | Exclusive key locks; first-committer-wins on write-write |
| `SERIALIZABLE` | Taken at `BEGIN` | Snapshot plus shared key/range locks (strict 2PL) |

`SERIALIZABLE` is lock-based, not SSI. Deadlock aborts the requester (`deadlock`); that transaction must `ROLLBACK`.

Readers do not see uncommitted writes. Commit is acknowledged only after group-commit WAL + `fsync`. New databases log page deltas (only the bytes that changed) instead of a full 16 KiB page image per write; existing databases keep full images unless `wal_page_deltas=on`.

Without `BEGIN`, each statement is its own committed transaction. `nextsql exec` sends one statement per invocation, so multi-statement transactions need a [driver](/docs/drivers).

On a Raft cluster, a write is acknowledged only after the leader flushes its local WAL **and** a quorum commits the sealed replication batch. SQL is not re-executed on followers, so `UUID()` / `NOW()` / `AI()` stay deterministic. See [high availability](/docs/ha).

Engine notes: [`docs/mvcc.md`](https://github.com/bzync/nextsql/blob/master/docs/mvcc.md), [`docs/wal.md`](https://github.com/bzync/nextsql/blob/master/docs/wal.md).
