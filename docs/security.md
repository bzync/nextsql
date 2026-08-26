# NextSQL production security (Phase 13)

This document is the honest contract for encryption, keys, identity, and audit.
It does not claim that NextSQL is unhackable, 100% secure, or immune to a live
host compromise.

## Threat model

| Attacker | What they get | What they do not get |
|---|---|---|
| Stolen files, disks, snapshots, WAL, backups, vector or full-text trees | Ciphertext, wrapped DEKs, key IDs, crypto metadata | Plaintext, unless they also have an authorized root unlock key |
| Network observer on a remote connection | TLS 1.3 record layer | SQL, passwords, or unlock material |
| Privileged attacker on a **live unlocked** `nextsqld` host | Keys, pages, and result rows that are in RAM | Nothing NextSQL can hide. The process must decrypt to execute SQL. |

NextSQL does not claim that total compromise of a live unlocked host can never
expose data.

## Envelope hierarchy

```text
External / client root unlock key     (never stored in the data directory)
        │
        ▼
       KEK                            (wrapped under the root)
        │
        ▼
 Database master                      (wrapped under the KEK)
        │
        ├─ page DEK
        ├─ WAL DEK
        ├─ UNDO DEK
        ├─ backup DEK
        ├─ vector DEK
        ├─ full-text DEK
        ├─ temp DEK
        └─ replication DEK  (Raft command payloads, `docs/ha.md`)
```

There is no single permanent key for every purpose. Domain DEKs cannot be
unwrapped with the wrong domain AAD.

The sidecar `nextsql.db.keys` (`NSKS` v1) holds wrapped keys, versions, flags,
and a nonce high-water. It does not hold the raw root. Mode `0600`.

`--key-file` (`NSKY`) is the external root unlock key. Keep it off the data
volume. Official drivers never put keys in a URL; they use `KeyProvider`.

WAL and UNDO DEKs are still generated per log and wrapped under the **master**
when an `Envelope` is in use, so rotating the page DEK does not brick recovery.

## Online rotation

```text
Generate Vn+1 → new writes use Vn+1 → re-encrypt remaining units → retire Vn
```

- `Envelope.RotateDomain` makes a new current DEK. Old versions stay readable.
- `Engine.Reencrypt` walks allocated pages and seals them with the current DEK.
- `Envelope.Retire` drops a version after no remaining ciphertext uses it.
- `RotateKEK` / `RotateMaster` / `RotateRoot` are wrapped-key rotations: data
  DEKs stay the same; only wraps change. No logical rebuild.

Every encrypted unit already carries a key-version field (page envelope, WAL
record, wrap blob).

## Revocation and shredding

- Credential revocation: `DROP USER` deletes the password hash and disconnects
  that user's sessions.
- Key-version revocation: `Envelope.Revoke` zeros the version and refuses
  decrypt. All live sessions are terminated.
- Crypto-shredding: `Envelope.Shred("NO KEY = NO RECOVERY")` overwrites the
  keystore (`NSSH`) and zeros RAM keys. Ciphertext remains. There is no recovery
  without a backup of the keystore and root. This requires the exact phrase.

## REQUIRE CLIENT KEY

`nextsqld --require-client-key` does not load `--key-file`. After password
auth the client must send `TypeUnlock` with the root (over TLS). The server
unwraps the keystore in process memory and then opens the database.

The root still exists in RAM for the life of the unlocked process. That is
intentional and is not a zero-knowledge property.

## Nonce uniqueness

Page nonces are `generation || key version`. The superblock reserves generation
batches of 4096 **before** use. Opening a file reserves a fresh batch so crash
replay cannot reuse a generation.

The keystore also stores a nonce high-water. On open, NextSQL takes the max of
the superblock and the keystore so a data-file snapshot rollback that is not
paired with the keystore cannot walk the counter backwards.

Restore, replica promotion, and failover must keep the keystore with the data
file, or call `AdvanceNonceTo` / rotate the DEK (new key version makes old
generations unique again). Randomness alone is not treated as sufficient.

## Zero-knowledge mode (design)

Optional `NEXTSQL ZERO-KNOWLEDGE MODE` is a **client-side** contract:

```text
Client owns plaintext and root keys
Server stores ciphertext and cannot run arbitrary SQL on those fields
```

Arbitrary server-side predicates, joins, `SEARCH`, and `NEAREST` over strongly
client-encrypted values are incompatible with that contract. NextSQL will not
pretend otherwise.

### Field-level client encryption (syntax design)

```sql
CREATE TABLE accounts (
    id UUID PRIMARY KEY,
    email STRING,
    ssn STRING ENCRYPTED CLIENT
);
```

`ENCRYPTED CLIENT` columns are opaque ciphertext to the server. Equality on a
deterministic wrap, if ever offered, is searchable encryption and leaks
equality. That leakage will be documented on the statement that introduces it.
This increment does not implement the column modifier.

## RBAC

Users, roles, grants, revocation. Least privilege: a new principal has no
rights until granted. Scopes:

```text
cluster  database  schema  table  column
function  backup  replication  administration
```

```sql
CREATE USER app IDENTIFIED BY '...';
CREATE ROLE analyst;
GRANT analyst TO app;
GRANT SELECT ON TABLE products TO analyst;
GRANT CDC ON TABLE orders TO streamer;
GRANT ADMIN ON CLUSTER TO dba;
REVOKE SELECT ON TABLE products FROM analyst;
DROP USER app;
```

Passwords are PBKDF2-HMAC-SHA256 in `nextsql.users`. They are never stored
plaintext. `DROP USER` terminates that user's sessions.

Bootstrap (`nextsql init --user` / `nextsqld --user`) grants `ADMIN` on
`CLUSTER` and `CONNECT` on the database.

Local embedded sessions with no ACL attached remain unrestricted so existing
engine tests stay hermetic.

`CDC` is independently grantable at table scope. It does not imply `SELECT`,
and `SELECT` does not imply `CDC`. Continuous subscriptions recheck the grant
on every pull, bind tenant scope from the authenticated session, and write a
`cdc.subscribe` audit event without row keys or resume tokens.

## Tenant isolation

Tables with a `tenant_id` column (`UUID`, `STRING`, or `TEXT`) are tenant-keyed.
Cross-tenant leakage tolerance is 0: a session bound to tenant T never returns
or writes a row whose `tenant_id` is not T.

```sql
SET TENANT = '11111111-1111-1111-1111-111111111111';
SET TENANT = $1;
RESET TENANT;
```

- `SET TENANT` is session-local. It is not stored in the catalog.
- Bound `SELECT` / `UPDATE` / `DELETE` / `SEARCH` / `NEAREST` / export get an
  implicit `tenant_id = <bound>` predicate. On `LEFT JOIN`, the FROM / left
  table predicate stays in `WHERE`; the null-extended (right) table predicate
  is ANDed into that join's `ON` so unmatched left rows are not dropped.
  On `RIGHT JOIN` (before the LEFT rewrite) the preserved right side stays in
  `WHERE` and the null-extended left side goes to `ON`. On `FULL OUTER JOIN`,
  tenant predicates are **not** placed in `ON` or post-join `WHERE`; each
  tenant-keyed input `Scan` is wrapped with `Filter(tenant_id = $t)` below
  the join so unmatched other-tenant rows cannot leak.
  `INSERT` injects the bound value and rejects a mismatched `tenant_id`.
  `UPDATE` cannot reassign `tenant_id`.
- Production sessions (ACL attached, not cluster `ADMIN`) must `SET TENANT`
  before they touch a tenant-keyed table. Unbound access is `forbidden`.
- Cluster `ADMIN` may omit `SET TENANT` and see every tenant.
- Embedded sessions with no ACL stay unrestricted when unbound.

This is row isolation by `tenant_id`, not a separate catalog or encryption
domain. Guessing another tenant's UUID and binding to it is changing the
session context, not a leak of a still-bound tenant.

When both tables are tenant-keyed, the foreign key must include `tenant_id`
on both sides at the same position, so “child of a parent” is the same
tenant as a real key. FK existence checks and inbound probes call
`checkTenantRow` / `tenantVisible` on every parent and child row they
inspect. A bound session cannot use another tenant’s parent as a reference
and cannot cascade into another tenant. `RESTRICT` still treats a matching
child in another tenant as present, so a bound session cannot `DELETE` a
global parent that other tenants still reference. A tenant-keyed parent
cannot be referenced by a global child. Global parent ← tenant child is
allowed (lookup tables) and is a documented foot-gun.

## Audit

`nextsql.audit` is JSON lines, mode `0600`. Recorded actions include auth
success/failure, role and grant changes, user DDL, data DDL object names, key
operations, tenant bind, and session termination.

The writer redacts any field that looks like a password, key, token, or secret.
Do not put those values in `Event` fields.

## Transport

Remote production connections require TLS 1.3 (`docs/protocol.md`). Loopback
may run plaintext for development. mTLS, service identities, short-lived
credentials, and external IdPs are follow-ons.

## Query-abuse limits

| Limit | Value |
|---|---|
| Packet | 1 MiB |
| SQL text | 1 MiB |
| JSON depth | 32 |
| JSON size | 1 MiB |
| Vector dimension | 8192, finite elements |
| LINESTRING / POLYGON vertices | 256 |
| JOIN tables | 8 (FROM + up to seven INNER / LEFT / RIGHT / FULL / CROSS JOINs) |
| Foreign keys per table | 16 |
| Columns per foreign key | 8 |
| FK cascade depth | 8 |
| FK cascade touched rows | 100 000 |
| Result bytes on the wire | 64 MiB |
| Per-query memory / time / workers | `docs/execution.md` |

Untrusted lengths are checked before allocation.

## Temp files and spills

Query spills (`NSPL`) use a per-query AES-256-GCM DEK that exists only in RAM
and is deleted with the spill directory. Official production spills stay
encrypted. The envelope temp domain is reserved for durable temp files.

## What is not claimed

- Zero-knowledge while the server executes SQL on decrypted rows
- Safety if an operator restores a snapshot and reuses a live DEK without
  advancing the nonce high-water or rotating
- Immunity to a privileged attacker on an unlocked host

## P16 security review (2026-08-18)

Explicit production-surface pass for the TODO security gate. Scope: keys,
encryption, authn/authz, audit, transport, protocol limits, tenant isolation,
and fail-closed authorization. This is not a guarantee of zero defects; it is
a dated review that found no known critical unresolved production
vulnerability.

| Surface | Result |
|---|---|
| Crypto primitives | AES-256-GCM and PBKDF2-HMAC-SHA256 only. No custom cipher, MAC, or KDF |
| Envelope / DEKs | Separate domains (page, WAL, UNDO, backup, vector, full-text, temp, replication). Keystore holds wraps, not the root |
| Nonce uniqueness | Superblock generation batches + keystore high-water; crash/reopen reserves a fresh batch |
| Keys in URLs | CLI and official drivers reject key material in connection URLs |
| Passwords | Never stored plaintext. Verify is constant-time; missing users still do a dummy hash |
| Audit | JSON lines mode `0600`. `Redact` strips password / secret / token / key-like fields |
| TLS | TLS 1.3 required for non-loopback `nextsqld` listen addresses |
| Wire limits | Packet, SQL, name, session, and result sizes checked before allocate |
| Tenant isolation | Bound sessions get an implicit `tenant_id` predicate. Unbound production (non-ADMIN) access is `forbidden` |
| RBAC | Least privilege. `authorize` fails closed on any unlisted statement type |
| Live host | An unlocked `nextsqld` process has keys in RAM. Documented; not claimed otherwise |

Change from this pass: `Session.authorize` default is deny when an ACL is
attached. Every current `ast.Stmt` is listed; a future statement must be
added to the switch or it is forbidden.

No critical production defect is tracked as open after this review.
