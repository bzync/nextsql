# Logical export and import (Phase 14)

A successful write is not a valid export. NextSQL publishes an export
directory only after integrity checks and an import test succeed.

```text
snapshot scan → encode schema + inlined rows → seal payload → header
    → verify hashes → import-test into a throwaway dest → write verified
    → atomic publish
```

This is a **logical** dump. It is not a page-level backup. Dest pages,
WAL, and UNDO are created under dest keys. Use `nextsql backup` /
`restore` for physical PITR.

## Commands

```text
nextsql export --data-dir DIR --key-file FILE --out DEST
nextsql import --from DEST --data-dir DIR --key-file FILE
```

`--key-file` is the external root unlock key. It is never stored in the
export directory. Keys are never accepted in connection URLs.

## On-disk layout (`NSXP` v1)

An export is a directory, not a SQL text file.

| Name | Role |
|---|---|
| `header` | Plaintext identity, counts, wrapped export DEK (`NSXP`) |
| `keystore` | Copy of `nextsql.db.keys` (wrapped DEKs only; never the root) |
| `payload` | AEAD-sealed record stream (`NSXL`) |
| `verified` | Written last, after hash checks and an import-test apply |

The export DEK is generated per export and wrapped under the database
master (`crypto.DomainBackup` = `'B'`). Stolen export directories are
unreadable without the external root unlock key.

Chunk AEAD binds the payload name plus chunk index in AAD so pieces
cannot be swapped.

## Payload records

Plaintext (then sealed) records:

- table descriptors (`NSCT` without page IDs)
- committed rows (`NSRW`) with `VECTOR` payloads **inlined**, not heap
  references

Indexes are recreated on import (`CREATE INDEX` / `UNIQUE` / `SPATIAL` /
`FULLTEXT` / `VECTOR … USING HNSW`), including JSON path keys.

`CREATE TABLE` on import emits stored `FOREIGN KEY` / `CONSTRAINT` clauses.
Parent tables are created before children. Cyclic FK graphs fail closed.

Uncommitted writes are not exported. The scan uses a snapshot.

## Verify

`Verify` / the export publish gate:

1. Authenticate the header checksum.
2. Unwrap the export DEK (keystore + root, or the live source provider).
3. Decrypt the payload and check SHA-256 and counts.
4. Import into a temporary database and run a probe `SELECT`.

Tamper, truncate, or a wrong key fails closed. An unpublished
`*.partial` directory from a crash is deleted and never treated as an
export.

## Import

`Import` refuses a directory that has no `verified` marker.

Dest is created if `nextsql.db` is missing, with a **new** identity and
envelope under the same root. Existing dest tables with the same name
fail closed (`already_exists`). Schema and rows are applied in one
transaction (`BEGIN` … `CREATE TABLE` … `INSERT` … `CREATE INDEX` …
`COMMIT`).

## Crash during export

Injected crash points: before write, during write, before verify. The
destination is not published. The source database remains openable.

## Related

Upgrade catalogs, admission control, metrics, and official
`nextsql-bench` workloads are in `docs/ops.md`.
