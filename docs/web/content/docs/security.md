# Users, roles, and tenants

This is the honest contract. NextSQL is not unhackable, not “100% secure,” and does not survive a live unlocked host compromise.

## Threat model

| Attacker | Gets | Does not get |
|---|---|---|
| Stolen disks, snapshots, WAL, backups, vector or full-text trees | Ciphertext, wrapped DEKs, key IDs | Plaintext, unless they also have an authorized root unlock key |
| Network observer on a remote connection | TLS 1.3 records | SQL, passwords, unlock material |
| Privileged attacker on a **live unlocked** `nextsqld` | Keys, pages, and rows in RAM | Nothing the process can hide. It must decrypt to execute SQL |

## Envelope encryption

```text
External root unlock key     (--key-file, never in the data directory)
        → KEK → database master
              → page / WAL / UNDO / backup / vector / full-text / temp / replication DEKs
```

Established crypto only: AES-256-GCM. No custom cipher, hash, MAC, KDF, or AEAD. The sidecar `nextsql.db.keys` holds wrapped keys, versions, flags, and a nonce high-water. It does not hold the raw root. Mode `0600`.

Online DEK rotation, key-version revocation (kills sessions), and crypto-shred of the keystore are in the production surface. Field-level `ENCRYPTED CLIENT` columns are designed but **not implemented**.

## Bootstrap

`nextsql init --user` / `nextsqld --user` creates a user with `ADMIN` on `CLUSTER` and `CONNECT` on the database. Passwords are hashed (PBKDF2-HMAC-SHA256, 100 000 iterations). They are never stored plaintext.

## SQL

```sql
CREATE USER reporter IDENTIFIED BY 's3cret';
CREATE ROLE analyst;
GRANT analyst TO reporter;
GRANT SELECT ON TABLE products TO analyst;
GRANT CDC ON TABLE orders TO streamer;
GRANT ADMIN ON CLUSTER TO dba;
REVOKE SELECT ON TABLE products FROM analyst;
DROP USER reporter;
```

A new principal has no rights until granted. Least privilege is fail-closed.

Privileges include `CONNECT`, `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `CREATE`, `DROP`, `INDEX`, `EXECUTE`, `GRANT`, `BACKUP`, `REPLICATION`, `CDC`, `ADMIN`.

`CDC` is independent of `SELECT`. A subscription rechecks its table-scoped
grant on every pull, so revocation stops an open stream. Tenant scope comes
only from the authenticated session's `SET TENANT` binding.

Scopes: `CLUSTER`, `DATABASE`, `SCHEMA`, `TABLE`, `COLUMN`, `FUNCTION`, `BACKUP`, `REPLICATION`, `ADMINISTRATION`. `GRANT SELECT ON products TO analyst` treats a bare name as a table.

`DROP USER` deletes the password hash and disconnects that user's sessions.

## Tenants

Tables with a `tenant_id` column (`UUID`, `STRING`, or `TEXT`) are tenant-keyed. Cross-tenant leakage tolerance is 0.

```sql
SET TENANT = '11111111-1111-1111-1111-111111111111';
SET TENANT = $1;
RESET TENANT;
```

- `SET TENANT` is session-local. It is not stored in the catalog.
- Bound `SELECT` / `UPDATE` / `DELETE` / `SEARCH` / `NEAREST` / export get an implicit `tenant_id = <bound>` predicate.
- `INSERT` injects the bound value and rejects a mismatched `tenant_id`. `UPDATE` cannot reassign `tenant_id`.
- Production sessions (ACL attached, not cluster `ADMIN`) **must** `SET TENANT` before they touch a tenant-keyed table. Unbound access is `forbidden`.
- Cluster `ADMIN` may omit `SET TENANT` and see every tenant.

This is row isolation by `tenant_id`, not a separate catalog or encryption domain.

Session audit is a JSON-lines file (mode `0600`). See [TLS](/docs/tls) for the wire and client-held keys. Engine note: [`docs/security.md`](https://github.com/bzync/nextsql/blob/main/docs/security.md).
