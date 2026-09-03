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

The deployment registry uses a separate external root
(`--instance-key-file`, default `--key-file.instance`) and independent
`nextsql.instance.keys` envelope. It is not a login password. Keep both roots
off the data volume. This M1 foundation does not yet implement realm-local
authentication or selectable database engines.

Online DEK rotation, key-version revocation (kills sessions), and crypto-shred of the keystore are in the production surface. Field-level `ENCRYPTED CLIENT` columns are **experimental**: the randomized `NSCE1.` server/catalog path and Go, Node.js/TypeScript, Bun, Deno, and PHP helpers ship, while PITR and HA coverage remain open. The server stores only opaque ciphertext and rejects predicates, indexes, and search on these fields.

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
GRANT USAGE ON RESOURCE GROUP reporting TO analyst;
REVOKE SELECT ON TABLE products FROM analyst;
DROP USER reporter;
```

A new principal has no rights until granted. Least privilege is fail-closed.

Privileges include `CONNECT`, `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `CREATE`, `DROP`, `INDEX`, `EXECUTE`, `GRANT`, `BACKUP`, `REPLICATION`, `CDC`, `ADMIN`.

`CDC` is independent of `SELECT`. A subscription rechecks its table-scoped
grant on every pull, so revocation stops an open stream. Streams are scoped to the authenticated connection's selected database.

Scopes: `CLUSTER`, `DATABASE`, `SCHEMA`, `TABLE`, `COLUMN`, `FUNCTION`, `BACKUP`, `REPLICATION`, `ADMINISTRATION`. `GRANT SELECT ON products TO analyst` treats a bare name as a table.

`DROP USER` deletes the password hash and disconnects that user's sessions.

## Short-lived credentials

A signed short-lived credential (`NSSC1.`… , Ed25519) is presented **in place of
the password** — same native principal, same RBAC. Enable verification with
`token_verify_keyset=FILE`; optionally add `token_revocations=FILE` and
`token_audience=STRING`. The server enforces the signature, an explicit expiry
(60 s skew, max lifetime 24 h), the audience, the served-database scope, and
revocation, and closes the session at expiry. An optional role scope narrows
the session to privileges reachable through roles the principal already holds —
it can never escalate. The signing keyset (`NSTK`) rotates with an overlap
window; the revocation set (`NSTR`) revokes a single token id or every
credential for a principal issued before a cutoff; `SIGHUP` reloads both. Manage
everything with `nextsql token` (`keygen`, `export-public`, `mint`, `revoke`,
`rotate`, `retire`, `verify`). Auth audit records `identity_source` `token` or
`mtls+token`. Engine note: [`docs/security.md`](https://github.com/bzync/nextsql/blob/main/docs/security.md).

## External identity (OIDC)

The broker, run as `nextsql-auth-broker` or embedded on a separate single-node
`nextsqld` listener, validates an external OIDC ID token or
explicitly enabled client-credentials JWT access token against a bounded cached
JWKS, maps verified claims through the `NSIP` identity policy,
and mints the same short-lived `NSSC1.` credential. `nextsqld` stays offline and
never parses an OIDC token. `nextsql login --idp NAME` implements browser
Authorization Code + PKCE S256 with state/nonce and a transient loopback
callback; `nextsql exec --idp NAME` and server `status --idp NAME` use the
stored credential, renewing it with an IdP refresh token when available.
`whoami` reports non-secret metadata and `logout` removes the local secret.

To distinguish broker-issued sessions in the server audit, dedicate the
broker's signing key and configure
`token_identity_source_hint=KEY_ID:oidc[,KEY_ID:oidc...]` beside
`token_verify_keyset`. The bounded map is consulted only after the `NSSC1.`
signature verifies, producing `oidc` or `mtls+oidc`. Forged/unverified tokens
remain labeled `token`; no source claim is trusted and no token or credential
is logged.

Credential/refresh-token files are atomically written mode `0600` under a real
mode-`0700` directory. Redirected token POST replay, oversized responses/files,
wrong-state callbacks, symlink paths, and group/other-readable files fail
closed. A same-OS-account compromise can still read this portable file store;
an OS-keychain backend is a follow-on. OAuth2 client credentials are supported
when the IdP issues an asymmetric JWT access token: the profile secret comes
from a mode-`0600` file, and the broker requires its configured resource
audience plus exact client binding before applying the normal NSIP/RBAC gate.
Embedded mode uses `--auth-broker-listen` and the standalone-format
`--auth-broker-config` (default `DATA-DIR/nextsql-auth-broker.conf`). It is
rejected with Raft, requires TLS off loopback, verifies issuer/server keyset
compatibility before startup/reload, and intersects roles with the live native
ACL. Opaque-token introspection and JIT provisioning remain optional and off.

## Hosted isolation

Shared row tenancy is removed. `SET TENANT`, `RESET TENANT`, and
`PARTITION BY TENANT` are rejected. Connections bind to a hosted realm and
database. Non-`ADMIN` access to a legacy table containing a `tenant_id` marker
fails closed; an administrator may access it only to migrate each former tenant
into a separately provisioned database. New CDC/task/schedule records do not
carry row-tenant authorization state.

Session audit is a JSON-lines file (mode `0600`). See [TLS](/docs/tls) for the wire and client-held keys. Engine note: [`docs/security.md`](https://github.com/bzync/nextsql/blob/main/docs/security.md).
