# NextSQL production security (Phase 13 baseline; Phase 25 in progress)

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

### Deployment registry root

Newly initialized deployments also generate a separate external registry root
at `--instance-key-file` (default `--key-file.instance`). It unlocks the
independent `nextsql.instance.keys` envelope and encrypted `nextsql.instance`
registry; it is not a user password or a database DEK. Keep it off the data
volume with the database root. The current M1 foundation registers and verifies
only the default database. Realm-specific authentication and multiple routed
database engines remain unimplemented.

`nextsql hosting adopt --confirm` is offline and fail-closed. It acquires the
same exclusive data-directory lock held for the lifetime of `nextsqld`, opens
the existing database with its current root, verifies storage/keystore
identity, and only then creates or resumes the encrypted registry. It does not
copy the database root into the data directory or auto-adopt sibling files.

Server/bootstrap credentials are namespaced separately from clients:
`NEXTSQL_SERVER_USER` with `NEXTSQL_SERVER_PASSWORD_FILE` (preferred) or
`NEXTSQL_SERVER_PASS`. They never fall back to `NEXTSQL_DATABASE_USER` for client
connections. Inline server passwords are accepted for non-interactive
automation but produce a warning; use a mode-`0600` password file or secret
mount where possible.

Database/client dotenv credentials use `NEXTSQL_DATABASE_USER` with
`NEXTSQL_DATABASE_PASSWORD_FILE` (preferred) or `NEXTSQL_DATABASE_PASS`.
Ambiguous `NEXTSQL_USER` / `NEXTSQL_PASSWORD*` names are not accepted, so a
server bootstrap secret cannot silently become an application login input.

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
and `SELECT` does not imply `CDC`. Continuous subscriptions recheck the grant on every pull and write a
`cdc.subscribe` audit event without row keys or resume tokens. New CDC records
are scoped by database and do not carry a row-tenant filter.

## Hosted isolation and legacy migration

Row-level shared tenancy has been removed. `SET TENANT`, `RESET TENANT`, and
`PARTITION BY TENANT` are syntax errors. NextSQL does not inject an implicit
`tenant_id` predicate, rewrite writes, or allow a client-selected row security
context.

The supported isolation direction is an immutable connection binding to one
hosted realm and database. Provision the realm/database with `nextsql init` (or
explicitly adopt an existing default database with `nextsql hosting adopt`),
and authenticate with a database user whose RBAC grants are scoped to that
database. Realm/database routing is a security boundary only when the registry,
authentication, authorization, and database handle all resolve the same stable
IDs; routing alone never grants access.

The current hosting slice provides the encrypted deployment registry and a
registered default realm/database. The server still opens only that registered
default database, so additional live database routing must not be advertised as
shipped yet.

For safe upgrades, a `UUID`, `STRING`, or `TEXT` column named `tenant_id` is
recognized as a legacy shared-tenant marker:

- non-`ADMIN` reads, writes, exports, joins, subqueries, and subscriptions on
  that table fail closed, even when ordinary table privileges were granted;
- cluster `ADMIN` may read the complete legacy table only to export and migrate
  each former tenant into a separately provisioned hosted database. `nextsql
  hosting migrate-tenant` is the supported offline path: it exclusively locks
  both deployments, copies every legacy-tenant table and its matching rows for
  one tenant into a `PROVISIONING` destination in bounded point-verified
  batches, renames the `tenant_id` column to `legacy_tenant_id`, records a
  durable encrypted `nextsql.tenant-migration` intent bound to the source
  identity and tenant, and only then publishes the destination `ACTIVE`; exact
  reruns resume or re-verify without duplicating rows;
- new sessions cannot bind a former tenant value, and no row is injected or
  filtered;
- versioned catalog, schedule/task, WAL, and CDC decoders retain historical
  tenant fields for recovery compatibility, but new runtime records leave those
  fields empty and never use them for authorization.

After migration, use application-specific names such as `account_id` when a
business identifier is needed inside an isolated database. Do not use physical
partition locality as authorization.

## Audit

`nextsql.audit` is JSON lines, mode `0600`. Recorded actions include auth
success/failure, role and grant changes, user DDL, data DDL object names, key
operations, hosting-registry operations, and session termination.

The writer redacts any field that looks like a password, key, token, or secret.
Do not put those values in `Event` fields.

Authentication records include `identity_source`: `native` for the native
password flow, `mtls` for a certificate-binding failure, `mtls+native` when both
the service certificate and native password authenticated the session, `token`
for a short-lived credential, and `mtls+token` when a credential was presented
over an mTLS connection.

## Transport

Remote production connections require TLS 1.3 (`docs/protocol.md`). Loopback
may run plaintext for development.

Set `--tls-client-ca FILE` (or `tls_client_ca=FILE`) together with the server
certificate and key to require mTLS. The TLS handshake uses Go's `crypto/x509`
verification against that trust bundle and requires a valid client-auth
certificate. The verified leaf must contain exactly one NextSQL service URI:

```text
nextsql://service/<principal>
```

`<principal>` is 1–128 lowercase ASCII letters, digits, `.`, `_`, or `-` after
case normalization. It must match the Hello user. The certificate is an
additional service-identity factor: the existing native password and RBAC
`CONNECT`/`ADMIN` checks still run and fail closed. The CLI supplies a pair with
`--tls-client-cert` and `--tls-client-key` (or
`NEXTSQL_TLS_CLIENT_CERT`/`NEXTSQL_TLS_CLIENT_KEY`). The Go driver can install
the same pair in `Config.TLS`.

The server certificate/key and client trust bundle are a reloadable atomic
snapshot. Replace the configured files, then send `SIGHUP` to `nextsqld`. A
successful reload affects new handshakes without restarting the listener. A
failed reload is audited and leaves the last known-good snapshot active. For a
CA rotation, first publish an overlap bundle containing both old and new roots,
reload, rotate clients, then remove the old root and reload again.

`--tls-client-crl FILE` (or `tls_client_crl=FILE`) enables PEM X.509 CRL
checking. Every CRL must be signed by an authority in `tls_client_ca`, have a
current `thisUpdate`, and have a future `nextUpdate`. When CRL checking is
enabled, every non-root certificate in an accepted client chain must have
current issuer coverage; missing, stale, wrongly signed, or revoked coverage
fails the handshake. A CRL reload that newly revokes a certificate takes effect
for the next handshake. After every successful mTLS reload, `nextsqld`
terminates all accepted connections, including pre-authentication handshakes,
so none can become or remain a session under the previous snapshot. Clients
must reconnect under the exact new trust and revocation state. This is
intentionally availability-affecting and fail-closed.

Without `tls_client_crl`, NextSQL has no CRL revocation check; operators can
still remove a CA from the trust bundle and reload. OCSP is not implemented.

## External identity providers (OIDC)

The accepted design is in `docs/design-oidc-external-idp.md` (2026-08-31): a
standalone or embedded authentication broker runs the OIDC flow, validates the
IdP token against a cached JWKS, and mints an ordinary `NSSC1.` short-lived
credential (above), so `nextsqld`'s authentication path is unchanged and never
calls the IdP.

The **authentication broker** is implemented and tested — `cmd/nextsql-auth-broker`,
built on `internal/oidc` (compact JWS verification for RS/PS/ES 256/384/512;
`none` and every MAC algorithm rejected; JWKS cache with a soft and a hard TTL
and rate-limited refresh) and `internal/authbroker`. `POST /v1/exchange` takes
an OIDC ID token, validates `iss` / `aud` / `azp` / `exp` / `iat` / `nbf` /
`nonce` and the signature against the cached JWKS, rejects a replayed token,
maps the verified claims through the `NSIP` identity policy, and mints an
`NSSC1.` credential signed by a private `NSTK` key whose public half goes in
every server's `token_verify_keyset`. The credential's lifetime is
`min(configured TTL, time until the IdP token expires)`, its audience is the
deployment audience, and its roles are the policy-mapped set (intersected with
the principal's real RBAC membership when a membership feed is wired — a later
increment; the server's `ACL.AllowedScoped` still enforces no-escalation on
every statement regardless). A brief JWKS outage serves soft-stale keys; past
the hard TTL the exchange fails closed. `SIGHUP` reloads the policy and the
issuing keyset with last known-good rollback. Every exchange emits a structured
audit record (issuer, hashed subject, matched rule, principal, mapped and
effective roles, outcome, minted token id) and never logs the ID token, the
credential, or a client secret.

**Still not implemented:** the client `nextsql login` flow, the `oidc` /
`mtls+oidc` `identity_source` in the `nextsqld` audit log, the OAuth2
client-credentials grant, the embedded broker mode
(`nextsqld --auth-broker-listen`), and optional just-in-time provisioning.

The **`NSIP` identity-policy engine** that the broker will consult *is*
implemented and tested (`internal/auth/identitypolicy.go`): a versioned,
corruption-validated, `SIGHUP`-last-known-good policy document (mode `0600`,
atomic rename, like `NSTK`/`NSTR`; at-rest envelope encryption is a follow-on
tracked with the same for `NSTK`/`NSTR`). It maps a verified IdP subject to a
native principal through ordered issuer-scoped rules (claim conditions plus a
small pure transform pipeline; the derived name must be a valid
`[a-z0-9._-]{1,128}` login or the mapping fails closed) and maps IdP groups to
native roles (literal or anchored-RE2 with `${n}` capture templates). The mapped
role set is then intersected with the principal's real RBAC membership
(`IdentityPolicy.Authorize`), so an external identity can only ever *narrow*
what a native grant already allows and can never bypass NextSQL RBAC; an empty
intersection is a denial. Every unmatched, ambiguous, or over-cap (>16 roles)
input fails closed. Nothing consumes the engine yet.

## Short-lived credentials

A short-lived credential is an Ed25519-signed set of claims a client presents
**in place of the password** — same wire path (the `Auth` message's password
field), same native principal, same RBAC. Its wire form is `NSSC1.` followed by
base64url of `claims || signature(64)`; the signature covers exactly the claims.
The server routes any password beginning with `NSSC1.` to credential
verification when `token_verify_keyset` is configured, and to password
verification otherwise.

Claims: signing-key id, a random 16-byte token id (for revocation), issued-at /
not-before / expires-at (second precision), the native `principal`, and
optional `audience`, `database`, `realm`, and role scopes.

Verification (`internal/auth`, `TokenVerifier`) fails closed on: a bad or
retired signing key, an invalid signature, `now` outside
`[not-before, expires-at]` (60 s skew), a lifetime over the verifier maximum
(default 24 h, hard ceiling 30 d), an `audience` that does not match a
configured `token_audience` (and a configured audience rejects an unscoped
credential), a `database` scope that does not match the served database, or a
revoked token id / principal cutoff. On success the protocol server also
requires the claimed principal to equal the Hello user and to be a known native
user, applies the role scope to the session (see RBAC), and closes the session
when the credential expires.

**Role scope.** An empty role list inherits the principal's full RBAC. A
non-empty list narrows the session to privileges reachable through exactly
those roles; the principal must already be a member of every listed role, so a
credential can never escalate. Enforced on every statement via
`ACL.AllowedScoped`.

**Signing-key rotation.** `token_verify_keyset` is a versioned `NSTK` set of
Ed25519 keys, each with an id and `retired`/`current` flags. `nextsql token
rotate` adds a new current key; both keys verify during the overlap; `nextsql
token retire --key-id` then stops the old key (its credentials fail closed).
Servers keep a verify-only copy (`nextsql token export-public`); only the
issuer holds private seeds.

**Revocation.** `token_revocations` is a versioned `NSTR` set of revoked token
ids (each kept only until its own expiry) plus per-principal "issued at or
before" cutoffs for bulk revocation. `nextsql token revoke` edits it.
Verification consults it on every credential.

**Reload.** `SIGHUP` atomically reloads the keyset and revocation list; a
failed reload is audited and retains the last known-good state.

**Audit.** Credential logins record `identity_source` `token` (or `mtls+token`
when a client certificate was also presented). `nextsql token` never writes a
credential or private key to disk in a log.

Config keys: `token_verify_keyset=FILE` (required to enable), optional
`token_revocations=FILE` and `token_audience=STRING`. `nextsql token`
subcommands: `keygen`, `rotate`, `retire`, `list-keys`, `export-public`,
`mint`, `revoke`, `verify`. The official drivers need no change — the credential
is passed wherever the password would be. Convenience helpers in the non-Go
drivers are a documented follow-on.

## P25 Security 2.0 audit (2026-08-31)

`DESIGNED` below means the target exists in `PROJECT.md`/`TODO.md`; it is not a
shipped-functionality claim. `TESTED` names a concrete automated boundary.
Nothing in P25 is yet `PRODUCTION-GATED` because the phase exit gate is open.

| Checklist item | Designed | Implemented | Tested | Production-gated / evidence |
|---|---:|---:|---:|---|
| Actual mTLS server | yes | yes | yes | no — `ServerMTLS`, required-cert handshake test |
| Client certificate validation | yes | yes | yes | no — system `x509`, configured CA, client EKU/validity/chain |
| Service identity mapping | yes | yes | yes | no — exact URI-to-Hello-user binding tests |
| Certificate rotation | yes | yes | yes | no — atomic `SIGHUP` reload, overlap trust rotation, last-known-good rollback test |
| Certificate revocation handling | yes | yes | yes | no — fail-closed X.509 CRL coverage/revocation/expiry tests; OCSP not implemented |
| Audit authentication identity source | yes | yes | yes | no — `identity_source` redaction test |
| Signed short-lived credential format | yes | yes | yes | no — `NSSC1.` Ed25519 wire form; `internal/auth` decode/verify + `FuzzDecodeTokenClaims` |
| Token expiration | yes | yes | yes | no — not-before/expires-at with skew; `TestTokenExpiry`, `TestTokenNotYetValid`, `TestTokenMaxLifetime`, `TestShortLivedCredentialExpiryClosesSession` |
| Token audience/database scope | yes | yes | yes | no — `token_audience` match + served-database match; `TestTokenAudienceMismatch`, `TestShortLivedCredentialAudienceMismatch` |
| Token role scope | yes | yes | yes | no — `ACL.AllowedScoped`, no-escalation guard; `TestACLAllowedScoped`, `TestShortLivedCredentialRoleScope` |
| Token realm/database scope | yes | yes | yes | no — surfaced on claims; database enforced server-side, realm carried for hosted routing |
| Token signing-key rotation | yes | yes | yes | no — `NSTK` keyset, current/retired, overlap; `TestTokenKeyRotationOverlap`, `TestTokenKeysetReloadLastKnownGood` |
| Token revocation | yes | yes | yes | no — `NSTR` token-id + principal-cutoff, `SIGHUP` reload; `TestRevokeByTokenID`, `TestRevokePrincipalCutoff`, `TestShortLivedCredentialRevoked` |
| Token audit | yes | yes | yes | no — `identity_source` `token`/`mtls+token`; `token.reload` security setting event |
| OIDC design | yes | n/a | n/a | accepted design `docs/design-oidc-external-idp.md` (brokered token exchange → `NSSC1.`; `NSIP` no-escalation mapping) |
| OIDC implementation | yes | partial | yes | no — broker (`cmd/nextsql-auth-broker`, `internal/oidc` + `internal/authbroker`) validates an ID token vs a cached JWKS and mints an `NSSC1.` credential; fake-IdP→broker→`TokenVerifier` integration test. No `nextsql login` client flow, no `oidc`/`mtls+oidc` audit source, no client-credentials/embedded/JIT |
| IdP-to-NextSQL principal mapping | yes | yes | yes | no — `NSIP` issuer-scoped subject rules + transforms + login-charset check, consumed by the broker; `internal/auth/identitypolicy_test.go`, `FuzzDecodeIdentityPolicy`, `FuzzMapClaims`, `TestExchangeHappyPathMintsVerifiableCredential` |
| External auth remains behind RBAC | yes | partial | yes | no — broker mints the policy-mapped role set; `IdentityPolicy.Authorize` / `IntersectRoles` narrows it to real membership when a feed is wired (`TestExchangeRBACIntersection`), and the server's `ACL.AllowedScoped` enforces no-escalation on every statement regardless. Automatic `security.ACL` membership feed is a later increment |
| IdP group/role mapping | yes | yes | yes | no — `NSIP` literal + RE2 `${n}` group→role mappings, 16-role cap, empty ⇒ deny, consumed by the broker; `TestIdentityPolicyGroupRegexCapture`, `TestIdentityPolicyRoleCapDenies`, `TestExchangeRejections` (unmapped groups/subject ⇒ deny) |
| `ENCRYPTED CLIENT` | syntax only | no | no | no — parser/runtime do not ship it |
| Official-driver field encryption | yes | no | no | no |
| Server-opaque client fields | yes | no | no | no |
| Searchable-encryption leakage contract | conditional design | no search mode | no | no |
| Field-key rotation | yes | no | no | no |
| Field-key revocation | yes | no | no | no |
| Field wrong-key/tamper behavior | yes | no | no | no |
| Field backup/restore/PITR | yes | no | no | no |
| Field replication/failover | yes | no | no | no |
| Argon2id migration evaluation | yes | no | no | no |
| Per-record password-hash versions | yes | no | no | no — current `NSAU` v1 fixes PBKDF2 |
| PBKDF2 backward compatibility | yes | no migration yet | current PBKDF2 tests only | no |
| Transparent login rehash | yes | no | no | no |
| Authentication DoS benchmark | yes | no | no | no |
| Tamper-evident/signed audit design | target only | no | no | no — current log is append-only JSONL |
| Audit verification tooling | yes | no | no | no |

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
| Realm/database isolation | Current server binds one registered default database; legacy `tenant_id` tables fail closed for non-ADMIN and are migration-only |
| RBAC | Least privilege. `authorize` fails closed on any unlisted statement type |
| Live host | An unlocked `nextsqld` process has keys in RAM. Documented; not claimed otherwise |

Change from this pass: `Session.authorize` default is deny when an ACL is
attached. Every current `ast.Stmt` is listed; a future statement must be
added to the switch or it is forbidden.

No critical production defect is tracked as open after this review.
