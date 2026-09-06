# Hosting

One `nextsqld` process can serve more than one database. Isolation is a **hosted realm and database**, not a row-level `tenant_id`. `SET TENANT`, `RESET TENANT`, and `PARTITION BY TENANT` are rejected.

```text
deployment registry  (nextsql.instance, separate root)
  └── realm
        └── database   (its own encrypted file, WAL, UNDO, users, ACL)
```

Connections select a realm and database on Hello. Non-`ADMIN` access to a leftover `tenant_id` column fails closed; migrate each former tenant with `nextsql hosting migrate-tenant`.

This is **not** distributed sharding and not hosted HA. Each managed database is still a single-node or Raft-clustered NextSQL engine. Selectable bounded routing, storage caps, suspend/resume, rename, and offline drop are implemented. Independently addressed backup/PITR/import/export, key rotation/crypto-shred, registry disaster recovery / Raft, and hosted HA remain open. `system.capabilities` row `hosting_isolation` stays **experimental** for that reason.

## Initialize

```bash
nextsql init \
  --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key \
  --realm customer-a --database production \
  --user app --password-file /tmp/nextsql.pw
```

`--realm` and `--database` default to `default`. Keep `--instance-key-file` (default `KEY-FILE.instance`) off the data volume.

Existing pre-registry deployments must not be reinitialized:

```bash
nextsql hosting adopt --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key --confirm
```

## Create realms and databases

```bash
nextsql realm create --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --realm customer-b --database production --database-key-file /etc/nextsql/customer-b.key

nextsql database create --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --realm customer-a --name analytics --database-key-file /etc/nextsql/analytics.key
```

Each database has its own `--database-key-file`. Clients pass `--realm` and `--database` on `nextsql exec` / driver `Config`.

## Suspend, resume, drop

```bash
nextsql database suspend --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --realm customer-a --database analytics --confirm
nextsql database resume  --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --realm customer-a --database analytics --confirm
nextsql database drop    --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --realm customer-a --database analytics --confirm
```

Drop is offline: stop `nextsqld`, then the registry moves the database through `DELETING` to `TOMBSTONED` and reclaims its ID-based directory (`db` plus `.keys` / `.wal` / `.undo` / `.isolated`). It refuses the deployment default and any non-managed database. Idempotent reruns resume.

## Rename

Logical names change; stable IDs and every on-disk path stay put.

```bash
nextsql realm rename --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --realm customer-a --to customer-acme --confirm
nextsql database rename --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --realm customer-acme --database analytics --to warehouse --confirm
```

A no-op rename of the current name is accepted. A colliding realm name, or a colliding database name in the same realm, fails `already exists`. Deleting or tombstoned databases cannot be renamed. Hello `Lookup` follows the new name. Inspect with `system.realms`, `system.databases`, and `system.quotas` (admin-only; empty on a non-hosted deployment).

## Storage caps

```bash
nextsql hosting set-realm-cap --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --realm customer-a --cap-bytes 107374182400 --confirm
nextsql hosting set-database-cap --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --realm customer-a --database production --cap-bytes 21474836480 --confirm
nextsql hosting show --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key
```

A per-database cap may not exceed the realm cap. Growth past the effective cap fails `storage cap exceeded`; deletes and in-place updates still work. Cap edits take the data-dir lock — stop `nextsqld`, change the cap, restart.

`nextsql hosting set-realm-root` delegates per-database cap management for one realm to a secret holder. The holder cannot raise the realm cap or touch another realm.

Open-database count (`max_open_databases`, default 8) and a shared buffer ceiling (`max_total_buffer_pages`) bound process memory. Idle databases evict rather than reject new connections. See [Server configuration](/docs/config).

## Legacy TENANT migration

```bash
nextsql hosting migrate-tenant \
  --source-data-dir /var/lib/legacy --source-key-file /etc/nextsql/legacy.key \
  --tenant '11111111-1111-1111-1111-111111111111' \
  --data-dir /var/lib/isolated --key-file /etc/nextsql/isolated.key \
  --confirm
```

Stop the source first. The destination stays `PROVISIONING` until every row is copied and point-verified, then publishes `ACTIVE`. The legacy column is renamed to `legacy_tenant_id`. Exact reruns resume.

Engine note: [`docs/design-multidatabase-dbaas.md`](https://github.com/bzync/nextsql/blob/main/docs/design-multidatabase-dbaas.md).
