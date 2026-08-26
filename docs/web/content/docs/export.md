# Logical export and import

Export is a **logical** snapshot (schema + committed rows). It is not a page-level backup and it is not PITR. Vector payloads are inlined; indexes are recreated on import.

```bash
./nextsql export \
  --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key \
  --out /exports/nextsql-2026-08-18

./nextsql import \
  --from /exports/nextsql-2026-08-18 \
  --data-dir /var/lib/nextsql-copy \
  --key-file /etc/nextsql/root.key
```

The destination is created if `nextsql.db` is missing, with a **new** identity under the same root. Existing dest tables with the same name fail closed (`already_exists`). Uncommitted writes are not exported.

An export is not valid until the built-in import-test succeeds (`verified` marker). `--out` must not already exist.

Use physical [backup](/docs/backup) when you need page-level restore or PITR. Use export when you want a portable snapshot of committed rows.

Engine note: [`docs/export.md`](https://github.com/bzync/nextsql/blob/main/docs/export.md).
