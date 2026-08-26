# Backup, restore, and PITR

A successful write is not a valid backup. `nextsql backup` publishes the destination only after hash checks **and** a restore-test open.

```bash
# physical backup (pages, WAL, UNDO, users, ACL — still ciphertext)
./nextsql backup \
  --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key \
  --out /backups/nextsql-2026-08-18

# re-check later
./nextsql verify --from /backups/nextsql-2026-08-18 --key-file /etc/nextsql/root.key

# restore into an empty directory
./nextsql restore \
  --from /backups/nextsql-2026-08-18 \
  --data-dir /var/lib/nextsql-restored \
  --key-file /etc/nextsql/root.key
```

The backup directory is not a tar of plaintext files. Layout (`NSBK` v1): `header`, `keystore` (wrapped DEKs only), `manifest`, `members/*`, `verified`. Stolen backups are unreadable without the root unlock key. Audit logs are operational and are **not** part of the backup.

`--out` must not already exist. The tool writes a temporary directory, verifies, then publishes atomically.

## Point-in-time recovery

Enable WAL archival on the server:

```bash
./nextsqld \
  --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key \
  --wal-archive /var/lib/nextsql-wal \
  --user app --password-file /tmp/nextsql.pw
```

Recycled (and checkpoint-time current) segments are copied as sealed `NSWA` archives.

```bash
# replay committed records with LSN <= N
./nextsql restore \
  --from /backups/nextsql-2026-08-18 \
  --data-dir /var/lib/nextsql-pitr \
  --key-file /etc/nextsql/root.key \
  --wal-archive /var/lib/nextsql-wal \
  --until-lsn 12000

# stop at the latest backup/archive stamp <= this time
./nextsql restore \
  --from /backups/nextsql-2026-08-18 \
  --data-dir /var/lib/nextsql-pitr \
  --key-file /etc/nextsql/root.key \
  --wal-archive /var/lib/nextsql-wal \
  --until 2026-08-18T15:04:05Z
```

`--until` is **backup / archive time**, not per-commit time. Do not treat it as a commit-accurate clock.

HA is not a substitute for backups. A wiped replica is restored with `backup` / `restore` (same identity and keys), then rejoined.

Engine note: [`docs/backup.md`](https://github.com/bzync/nextsql/blob/main/docs/backup.md).
