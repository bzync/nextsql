# Backups, restore, and PITR (Phase 14)

A successful write or upload is not a valid backup. NextSQL publishes a
backup directory only after integrity checks and a restore test succeed.

```text
checkpoint → copy ciphertext → seal members → manifest → verify hashes
    → restore-test open → write verified → atomic publish
```

## Commands

```text
nextsql backup  --data-dir DIR --key-file FILE --out DEST
nextsql verify  --from DEST --key-file FILE
nextsql restore --from DEST --data-dir DIR --key-file FILE
                [--wal-archive DIR] [--until-lsn N | --until RFC3339]
```

`nextsqld --wal-archive DIR` installs a WAL archiver so recycled (and
checkpoint-time current) segments are copied for point-in-time recovery.

## On-disk layout (`NSBK` v1)

A backup is a directory, not a tar of plaintext files.

| Name | Role |
|---|---|
| `header` | Plaintext identity, LSNs, time, wrapped backup DEK (`NSBK`) |
| `keystore` | Copy of `nextsql.db.keys` (wrapped DEKs only; never the root) |
| `manifest` | AEAD-sealed member inventory (`NSMF`) |
| `members/*` | AEAD-sealed file chunks (`NSBM`) |
| `verified` | Written last, after hash checks and a restore-test open |

Members include the data file, keystore, WAL control and segments, UNDO,
an optional pending page-reclamation intent, and optional users/ACL files.
Audit logs are operational and are not part of the backup. A restored
reclamation intent is authenticated and replayed by the first normal database
open before sessions are accepted.

Workflow, trigger, schedule, task, and task-index descriptors live inside the
encrypted catalog pages in the data member. Restore and PITR therefore recover
their committed state through the same page-image/WAL path. PITR coverage
includes a scheduled `PENDING` task and its workflow identity; task workers are
started only after normal database recovery and leader initialization.

The backup DEK is generated per backup and wrapped under the database
master (`crypto.DomainBackup` = `'B'`). Pages, WAL, and UNDO inside the
members stay in their original envelopes. Stolen backup directories are
unreadable without the external root unlock key.

Chunk AEAD uses unique generations and binds the member name plus chunk
index in AAD so pieces cannot be swapped.

## Verify

`Verify` / `nextsql verify`:

1. Authenticate the header checksum.
2. Unwrap the backup DEK with the root (via the keystore sidecar).
3. Decrypt the manifest and check every member SHA-256.
4. Restore into a temporary directory and open the engine.

Tamper, truncate, or a wrong key fails closed. An unpublished
`*.partial` directory from a crash is deleted and never treated as a backup.

## Restore and PITR

`Restore` refuses a directory that has no `verified` marker.

Point-in-time recovery is **base backup + archived WAL**:

```text
restore members → overlay archived segments → redo until LSN
```

- `--until-lsn N` replays committed records with `LSN <= N`.
- `--until RFC3339` maps onto the latest backup durable LSN or archive
  entry whose recorded time is `<=` the request.

Timestamp resolution is **backup / archive time**, not per-commit time.
Archive entries are stamped when a segment is copied at checkpoint.
Do not advertise commit-accurate clocks until WAL records carry
timestamps.

WAL archive format (`NSWA` v1): sealed segment copies plus a checksummed
index of id, LSN range, archive time, and SHA-256. `DirArchiver`
implements `wal.Archiver`.

## Crash during backup

Injected crash points: before copy, during copy, before manifest, before
verify. The destination is not published. The source database remains
openable.

Logical `export` / `import` is a separate Phase 14 increment (`docs/export.md`).

## Related

Upgrade catalogs, admission control, metrics, and official
`nextsql-bench` workloads are in `docs/ops.md`.
