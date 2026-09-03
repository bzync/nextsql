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

### Field-level client encryption (experimental)

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
The randomized `NSCE1.` AES-256-GCM envelope, SQL/catalog/server path, and Go,
Node.js/TypeScript, Bun, Deno, and PHP driver helpers are implemented. The
server permits opaque storage and bare projection but rejects predicates,
expressions, indexes, search, grouping, and ordering. PITR and replication/
failover are now tested (exact-ciphertext restore-to-target-LSN; no lost
acknowledged ciphertext across leader failover); durable key-rotation/
revocation KMS lifecycle remains open, so the capability is experimental
rather than production-gated. See
[`client-encryption.md`](client-encryption.md) for the format, leakage,
rotation, revocation, backup, and context-migration contract.

## RBAC

Users, roles, grants, revocation. Least privilege: a new principal has no
rights until granted. Scopes:

```text
cluster  database  schema  table  column
function  backup  replication  administration  resourcegroup
```

```sql
CREATE USER app IDENTIFIED BY '...';
CREATE ROLE analyst;
GRANT analyst TO app;
GRANT SELECT ON TABLE products TO analyst;
GRANT CDC ON TABLE orders TO streamer;
GRANT ADMIN ON CLUSTER TO dba;
GRANT USAGE ON RESOURCE GROUP reporting TO analyst;
REVOKE SELECT ON TABLE products FROM analyst;
DROP USER app;
```

`GRANT USAGE ON RESOURCE GROUP name TO grantee` is what lets a session run
`SET RESOURCE GROUP name` (see "RESOURCE GROUP" in `docs/sql.md`); cluster
`ADMIN` bypasses it like every other privilege check.

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
over an mTLS connection. A successfully verified credential whose signing key
id is operator-mapped by `token_identity_source_hint` records `oidc`, or
`mtls+oidc` over mTLS. The exact known values bypass the generic secret-word
redactor; unknown or secret-shaped values remain redacted.

### Versioned hash chain and optional signatures

Every new record has a bounded v1 chain trailer:

```text
chain_version = 1
seq           = monotonically increasing u64
prev_hash     = lowercase hex SHA-256 of the previous chained record
hash          = SHA-256("NSAC\\x01" || prev_hash || seq-u64le || canonical-event-json)
sig/key_id    = optional Ed25519 signature over hash and its NSAK key id
```

The canonical event JSON includes `chain_version` and the redacted event
fields, but clears `seq`, `prev_hash`, `hash`, `sig`, and `key_id`. Caller-set
chain fields are discarded. Lines are capped at 1 MiB. Verification streams
one line at a time; startup verifies the retained chain before append, rejects
an incomplete final line, and refuses a symlink, non-regular file, or a file
readable by group/others. Each successful append is synced before the in-memory
head advances.

Pre-chain JSON lines are accepted only as one contiguous legacy prefix. The
first configured signer appends a signed `audit.signing.enabled` transition;
every chained record from that transition onward must be signed. This explicit
transition prevents removing the first signature to silently move the start of
the signed segment. A server that reopens a signed segment requires
`audit_signing_keyset` and verifies it before adding another record.

`NSAK` v1 is a bounded (64-key) Ed25519 keyset with one current key, rotation
overlap, retirement, verify-only export, atomic mode-`0600` writes, and
last-known-good `SIGHUP` reload. Retiring an old key removes its private seed
but retains the public key needed to verify historical records. Configure the
private signer outside the data volume:

```bash
nextsql audit keygen --keyset /secure/nextsql-audit.nsak
nextsql audit export-public --keyset /secure/nextsql-audit.nsak \
  --out /verify/nextsql-audit-public.nsak

nextsqld --audit-signing-keyset /secure/nextsql-audit.nsak ...

nextsql audit verify --file /var/lib/nextsql/nextsql.audit \
  --pubkey /verify/nextsql-audit-public.nsak
nextsql audit verify --file /var/lib/nextsql/nextsql.audit --json

nextsql audit rotate --keyset /secure/nextsql-audit.nsak
kill -HUP <nextsqld-pid>
# Retire the previous id after every verifier has the overlap keyset.
nextsql audit retire --keyset /secure/nextsql-audit.nsak --key-id 1
```

Keyed verification requires a signed transition and validates every signature
after it. Unkeyed verification checks only internal chain consistency. A
legacy-only file is reported as readable, not as tamper-evident.

Threat boundary:

- an unsigned hash chain detects accidental corruption and edits that were not
  followed by recomputing the chain; a writer who can replace the whole file
  can recompute it;
- signed records cannot be rewritten without an accepted private key, so keep
  the signer separate from verify-only copies and protect the signer host;
- neither a hash chain nor per-record signatures in the same local file can
  prove that an attacker did not delete a valid final suffix. Deployments that
  require suffix-truncation/rollback detection must periodically retain the
  latest `(seq, hash, signature)` in an independent append-only/WORM or remote
  transparency system and compare it during verification;
- a privileged attacker on the live signer host may steal the signing key or
  suppress future writes. This feature does not claim protection from that
  host compromise.

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
exactly one OIDC ID token or enabled JWT access token. ID-token validation covers
`iss` / `aud` / `azp` / `exp` / `iat` / `nbf` /
`nonce` and the signature against the cached JWKS, rejects a replayed token,
maps the verified claims through the `NSIP` identity policy, and mints an
`NSSC1.` credential signed by a private `NSTK` key whose public half goes in
every server's `token_verify_keyset`. The credential's lifetime is
`min(configured TTL, time until the IdP token expires)`, its audience is the
deployment audience, and its roles are the policy-mapped set (intersected with
the principal's live real RBAC membership in embedded mode; the server's
`ACL.AllowedScoped` still enforces no-escalation on every statement regardless).
A brief JWKS outage serves soft-stale keys; past
the hard TTL the exchange fails closed. `SIGHUP` reloads the policy and the
issuing keyset with last known-good rollback. Every exchange emits a structured
audit record (issuer, hashed subject, matched rule, principal, mapped and
effective roles, outcome, minted token id) and never logs the ID token, the
credential, or a client secret. In embedded mode, mapped roles are intersected
with the co-located server's live native user and direct/transitive ACL role
membership before minting; a missing user or empty intersection denies.

The OAuth2 **client-credentials JWT path** is implemented and tested.
`nextsql login --client-credentials` obtains a Bearer access token from the
discovered HTTPS token endpoint using a secret read from a bounded regular
mode-`0600` file. A broker profile enables this path with
`access_token_audience`; the broker requires the resource audience plus exact
`client_id`/`azp` binding, asymmetric signature, issuer, expiry/time checks,
replay rejection, and the same `NSIP`/RBAC/TTL boundaries as an ID token. The
client secret is neither sent to the broker nor copied into the local
credential record. Opaque-token RFC 7662 introspection is not implemented.

The **interactive client flow** is also implemented and tested:
`nextsql login --idp NAME` reads a named client profile, performs exact-issuer
discovery and Authorization Code + PKCE S256 with random state/nonce, accepts
the redirect on a transient bounded loopback listener, redeems the code, and
exchanges the ID token at the broker. `nextsql exec --idp NAME` and server-mode
`nextsql status --idp NAME` load the resulting `NSSC1.` credential; an expired
credential is renewed with the cached IdP refresh token when available.
`nextsql whoami` reports non-secret identity/scope metadata and `nextsql logout`
removes the local secret. The versioned local JSON store is atomically written
mode `0600` in a real mode-`0700` directory, uses collision-resistant IdP+host
names, rejects symlinks/permissive files, and bounds reads at 1 MiB. HTTP
redirects are not followed, preventing 307/308 replay of code/refresh/client
secrets; every response is bounded at 1 MiB. No credential is put in a URL,
shell history, ordinary dotenv, or log. This portable file backend does not
protect secrets from a process already running as the same OS account; an OS
keychain backend remains a follow-on.

The **server audit source** is implemented and tested. An operator may map up to
64 broker signing-key ids from the configured verify keyset with
`token_identity_source_hint=7:oidc,9:oidc`. Only `oidc` is accepted. After an
`NSSC1.` signature verifies, a mapped key selects `identity_source` `oidc` (or
`mtls+oidc`); an unverified credential, unknown key, or malformed/unknown hint
stays `token` / `mtls+token`. No client claim is accepted for this decision, no
credential/token id is logged, and there is no credential-format or NSQL wire
change. Every hinted key must be dedicated to broker issuance. Add a rotated
broker key id to the config and restart/roll servers before the broker starts
issuing with it; `SIGHUP` reloads the keyset/revocations, not this inline map.

The **embedded broker mode** is implemented and tested for single-node/non-HA
deployments. `nextsqld --auth-broker-listen ADDR` starts the same exchange
handler on a separate bounded HTTP(S) listener;
`--auth-broker-config FILE` selects the standalone-format config and defaults
to `DATA-DIR/nextsql-auth-broker.conf`. It requires
`token_verify_keyset`, rejects Raft/HA operation, proves the issuer key is
accepted before binding and before publishing a reload, sequences verifier
reload before issuer reload, and consumes live native-user/ACL membership.
Non-loopback broker listeners require their own TLS certificate/key. The SQL
listener still does no OIDC parsing or outbound HTTP.

**Still not implemented:** optional opaque access-token introspection and
optional just-in-time provisioning. JIT remains off and is not part of the core
gate because pre-created native principals preserve the smaller privilege
surface.

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
input fails closed. The authentication broker consumes this engine.

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
`token_revocations=FILE`, `token_audience=STRING`, and the audit-only
`token_identity_source_hint=KEY_ID:oidc[,KEY_ID:oidc...]`. `nextsql token`
subcommands: `keygen`, `rotate`, `retire`, `list-keys`, `export-public`,
`mint`, `revoke`, `verify`. The official drivers need no change — the credential
is passed wherever the password would be. Convenience helpers in the non-Go
drivers are a documented follow-on.

## Password hashing

`internal/auth` (`NSAU` file) stores native SQL-login passwords, separately
from `ENCRYPTED CLIENT` field keys and from short-lived credentials. Two
algorithms coexist in one versioned (`NSAU` v2) container, distinguished per
record by an explicit algorithm byte:

- **Argon2id** (`golang.org/x/crypto/argon2`) — every new record (`Upsert`)
  and every transparently upgraded legacy record. Parameters: time cost 1,
  memory cost 64 MiB, parallelism 4, 32-byte output — the package
  documentation's recommended values. Verification allocates the full 64 MiB
  working set per attempt by design; see the DoS-capacity benchmark below.
- **PBKDF2-HMAC-SHA256** (RFC 8018, hand-rolled — no third-party PBKDF2
  package) — the original algorithm, retained only for decoding pre-existing
  records. No new PBKDF2 record is ever created.

**Backward compatibility.** `NSAU` v1 files (every record implicitly
PBKDF2, no algorithm/memory/parallelism fields) still decode; `Encode`
always writes v2 (explicit algorithm byte plus Argon2id parameters, zero for
a PBKDF2 record) once anything in the store is next persisted, so a v1 file
upgrades to v2 in place the first time it changes.

**Transparent rehash.** After `Store.Verify` succeeds against a legacy
PBKDF2 record, the store re-hashes the already-confirmed-correct password
with Argon2id and persists the replacement before returning — a failed
verify never rehashes. The upgrade is best-effort: a persist failure does
not fail the login that already succeeded, and a concurrent delete/re-upsert
of the same user is detected and skipped rather than clobbered.
(`TestV1FormatDecodesAndVerifies`, `TestNewRecordsAreArgon2idFromCreation`,
`TestTransparentRehashUpgradesToArgon2id`.)

**Authentication DoS benchmark** (`internal/auth/store_bench_test.go`,
`nextsql-bench` hardware: Ryzen 5 7535HS, `linux/amd64`):

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkVerifyPBKDF2` (legacy, 100k iterations) | 14,263,899 | 880 | 13 |
| `BenchmarkVerifyArgon2id` (64 MiB, time 1, parallelism 4) | 16,265,775 | 67,114,496 | 36 |
| `BenchmarkConcurrentLoginAttempts` (12 procs, 3:1 correct:wrong mix) | 11,128,234 | 67,113,893 | 35 |

Argon2id's ~64 MiB-per-attempt memory cost is the load-bearing number for
capacity planning: an operator sizing `max_inflight_queries`-style
connection/auth concurrency limits (Phase 27) must budget roughly
(peak concurrent login attempts × 64 MiB) of resident memory, not just CPU.

## P25 Security 2.0 audit (2026-09-02)

`DESIGNED` below means the target exists in `PROJECT.md`/`TODO.md`; it is not a
shipped-functionality claim. `TESTED` names a concrete automated boundary.
Every implementable-scope item is now `PRODUCTION-GATED` per the dated review
below ("Security review sign-off (Phase 25)"). A handful of sub-features are
deliberate, explicitly documented non-goals rather than open blockers: OCSP
(certificate revocation uses X.509 CRLs instead), optional OIDC opaque-token
introspection, and JIT principal provisioning — all remain off by default.

| Checklist item | Designed | Implemented | Tested | Production-gated / evidence |
|---|---:|---:|---:|---|
| Actual mTLS server | yes | yes | yes | yes — `ServerMTLS`, required-cert handshake test |
| Client certificate validation | yes | yes | yes | yes — system `x509`, configured CA, client EKU/validity/chain |
| Service identity mapping | yes | yes | yes | yes — exact URI-to-Hello-user binding tests |
| Certificate rotation | yes | yes | yes | yes — atomic `SIGHUP` reload, overlap trust rotation, last-known-good rollback test |
| Certificate revocation handling | yes | yes | yes | yes — fail-closed X.509 CRL coverage/revocation/expiry tests; OCSP not implemented |
| Audit authentication identity source | yes | yes | yes | yes — `identity_source` redaction test |
| Signed short-lived credential format | yes | yes | yes | yes — `NSSC1.` Ed25519 wire form; `internal/auth` decode/verify + `FuzzDecodeTokenClaims` |
| Token expiration | yes | yes | yes | yes — not-before/expires-at with skew; `TestTokenExpiry`, `TestTokenNotYetValid`, `TestTokenMaxLifetime`, `TestShortLivedCredentialExpiryClosesSession` |
| Token audience/database scope | yes | yes | yes | yes — `token_audience` match + served-database match; `TestTokenAudienceMismatch`, `TestShortLivedCredentialAudienceMismatch` |
| Token role scope | yes | yes | yes | yes — `ACL.AllowedScoped`, no-escalation guard; `TestACLAllowedScoped`, `TestShortLivedCredentialRoleScope` |
| Token realm/database scope | yes | yes | yes | yes — surfaced on claims; database enforced server-side, realm carried for hosted routing |
| Token signing-key rotation | yes | yes | yes | yes — `NSTK` keyset, current/retired, overlap; `TestTokenKeyRotationOverlap`, `TestTokenKeysetReloadLastKnownGood` |
| Token revocation | yes | yes | yes | yes — `NSTR` token-id + principal-cutoff, `SIGHUP` reload; `TestRevokeByTokenID`, `TestRevokePrincipalCutoff`, `TestShortLivedCredentialRevoked` |
| Token audit | yes | yes | yes | yes — `identity_source` `token`/`mtls+token`; `token.reload` security setting event |
| OIDC design | yes | n/a | n/a | accepted design `docs/design-oidc-external-idp.md` (brokered token exchange → `NSSC1.`; `NSIP` no-escalation mapping) |
| OIDC implementation | yes | yes | yes | yes — standalone and embedded brokers validate ID tokens and client-credentials JWT access tokens vs a bounded cached JWKS and mint `NSSC1.`; `nextsql login` implements Authorization Code/PKCE or `--client-credentials`; key-derived server audit labels verified broker credentials. Embedded mode adds a separate bounded listener, single-node gate, issuer/verifier startup+reload compatibility, and live ACL feed. Fake-IdP→client/embedded broker→`TokenVerifier`, TLS/listener, functional/race/adversarial/config/audit tests. Optional opaque introspection/JIT remain off |
| IdP-to-NextSQL principal mapping | yes | yes | yes | yes — `NSIP` issuer-scoped subject rules + transforms + login-charset check, consumed by the broker; `internal/auth/identitypolicy_test.go`, `FuzzDecodeIdentityPolicy`, `FuzzMapClaims`, `TestExchangeHappyPathMintsVerifiableCredential` |
| External auth remains behind RBAC | yes | yes | yes | yes — every server enforces `ACL.AllowedScoped`; embedded mode also checks the live native user and direct/transitive ACL membership before minting, with empty intersection denial and immediate revocation behavior (`TestExchangeRBACIntersection`, `TestEmbeddedAuthBrokerUsesLiveNativeMembership`) |
| IdP group/role mapping | yes | yes | yes | yes — `NSIP` literal + RE2 `${n}` group→role mappings, 16-role cap, empty ⇒ deny, consumed by the broker; `TestIdentityPolicyGroupRegexCapture`, `TestIdentityPolicyRoleCapDenies`, `TestExchangeRejections` (unmapped groups/subject ⇒ deny) |
| `ENCRYPTED CLIENT` | yes | yes | yes | yes — every item-level blocker, including durable key rotation/revocation, is closed; see `docs/client-encryption.md` "Production-gating sign-off (Phase 25)" — `NSCT` v10; parser/catalog/binder/executor tests |
| Official-driver field encryption | yes | yes | yes | yes — Go, Node.js/TypeScript, Bun, Deno, and PHP provider/keyring/encrypt/decrypt helpers; Go↔non-Go portability fixtures |
| Server-opaque client fields | yes | yes | yes | yes — server structurally validates/stores `NSCE1.` but has no field key; encrypted restart/plaintext-scan test |
| Searchable-encryption leakage contract | yes | no search mode | yes | yes — randomized envelope; predicates/index/search/order/group/distinct/set operations fail closed; leakage documented |
| Field-key rotation | yes | yes | yes | yes — `FileFieldKeyring` (Go/Node/Bun/Deno/PHP): atomic, versioned, 0600 `NSFK1` file; overlap reads after rotation persist across restart; cross-driver format interop |
| Field-key revocation | yes | yes | yes | yes — revoked material zeroed on disk, revoked ids fail closed and can never be reused, current key cannot be revoked directly |
| Field wrong-key/tamper behavior | yes | yes | yes | yes — GCM/context/type/revocation tests + `FuzzInspect` |
| Field backup/restore/PITR | yes | yes | yes | yes — `TestEncryptedClientPITRRestoresExactCiphertextAtTarget`: base backup + archived WAL restored to a target LSN before a later `UPDATE` retains `TEXT ENCRYPTED CLIENT`, returns the exact pre-target ciphertext, excludes the later write, decrypts only via the client helper |
| Field replication/failover | yes | yes | yes | yes — `TestHAEncryptedClientCiphertextSurvivesLeaderFailover`: three-voter cluster confirms identical ciphertext on every replica, no lost acknowledged ciphertext across a leader kill/failover, and correct post-failover replication + decrypt on the remaining follower |
| Argon2id migration evaluation | yes | yes | yes | yes — `golang.org/x/crypto/argon2`, time 1 / memory 64 MiB / parallelism 4; every new record uses it |
| Per-record password-hash versions | yes | yes | yes | yes — `NSAU` v2 adds a per-record algorithm byte (PBKDF2 or Argon2id); `TestNewRecordsAreArgon2idFromCreation` |
| PBKDF2 backward compatibility | yes | yes | yes | yes — `NSAU` v1 files still decode; `Encode` always writes v2; `TestV1FormatDecodesAndVerifies` |
| Transparent login rehash | yes | yes | yes | yes — a successful verify against a legacy record re-hashes with Argon2id and persists; a failed verify never rehashes; `TestTransparentRehashUpgradesToArgon2id` |
| Authentication DoS benchmark | yes | yes | yes | yes — `BenchmarkVerifyPBKDF2`/`BenchmarkVerifyArgon2id`/`BenchmarkConcurrentLoginAttempts`; see "Password hashing" above for numbers |
| Tamper-evident/signed audit chain | yes | yes | yes | yes — `NSAC` v1 hash chain on every record + optional `NSAK` v1 Ed25519 signatures, fail-closed signed-transition rule, startup chain verification; `TestAuditChainVerifiesCleanLog`, `TestAuditChainDetectsTamperedLine`, `TestAuditSigningRoundTrip`, `TestAuditSigningTransitionCannotLoseSignature`, `FuzzDecodeAuditKeys` |
| Audit verification tooling | yes | yes | yes | yes — `nextsql audit keygen/rotate/retire/list-keys/export-public/verify`; `TestAuditVerifyCLI`, `TestAuditKeygenRotateRetireListExportPublicCLI` |

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

## P25 security review sign-off (2026-09-02)

Explicit production-surface pass for the P25 Security 2.0 exit gate (`TODO.md`
"Phase 25 exit gate"). Scope: everything landed since the P16 review above —
mTLS/service identity, short-lived credentials, the external-IdP broker,
field-level client encryption, password-hash evolution, and audit-chain
hardening. Every row in "P25 Security 2.0 audit" is `yes`/`yes`/`yes` across
designed/implemented/tested; this review is the corresponding
production-gating decision, not a re-derivation of that table. Like the P16
review, this is a dated finding of no known critical unresolved production
vulnerability in the reviewed surface — not a proof of zero defects, and not
an external third-party audit.

| Surface | Result |
|---|---|
| mTLS / service identity | `RequireAndVerifyClientCert` under TLS 1.3; the verified leaf's URI SAN must equal the native login user; native password + RBAC stay mandatory on top — mTLS narrows, never replaces, authorization |
| Certificate rotation / revocation | Atomic `SIGHUP` last-known-good reload; a successful mTLS reload closes every accepted connection (including in-flight handshakes) to force reauthentication; X.509 CRLs are signature/time/full-chain checked and fail closed on a revoked serial. OCSP is a documented non-goal, not a silent gap |
| Short-lived credentials | Ed25519-signed, presented in the existing password slot (no new attack surface on the wire format); bounded lifetime (60 s skew, 24 h default / 30 d ceiling max), audience/database/realm/role scope, `NSTK` rotation overlap, `NSTR` fail-closed revocation by id or principal cutoff |
| External IdP (OIDC) | The SQL auth path never parses OIDC and makes no outbound HTTP — it only ever receives an ordinary `NSSC1.` credential from the broker. The broker verifies signatures against a bounded/rate-limited JWKS cache (alg-allowlist rejects `none` and MAC), rejects replay, and maps through `NSIP`, whose result is *intersected* with the principal's real RBAC membership — a compromised or misconfigured IdP mapping can subtract roles, never grant one the principal doesn't already hold |
| Field-level client encryption | The server never holds a field key: it stores and structurally validates opaque `NSCE1.` ciphertext only. Predicates, joins, expressions, indexes, `SEARCH`, `GROUP BY`/`ORDER BY`/`DISTINCT`, set operations, and any context-changing rename/partition/migration on an encrypted column fail closed rather than silently degrading. No searchable/deterministic mode ships, so there is no query-observable ciphertext-equality leakage to reason about |
| Password hashing | Every new record is Argon2id (64 MiB / time 1 / parallelism 4); a correct legacy PBKDF2 verify transparently upgrades in place; a failed verify never rehashes, so a wrong-password guess cannot force extra server-side work beyond one Argon2id-equivalent hash |
| Audit chain | Hash-chained by default; an operator who additionally configures a signing keyset gets records that cannot be forged without the private key. Documented and not silently overclaimed: neither the unsigned chain nor per-record signatures alone prove a valid final suffix wasn't deleted — that needs an external WORM/transparency system, which this feature does not provide |
| Live host | Unchanged from P16: an unlocked `nextsqld` process, an active signing keyset, and a live OIDC broker all hold key material in RAM for the life of the process. Documented; not claimed otherwise |

Explicit non-goals carried forward from the individual item tables, not
tracked as gaps: OCSP; optional OIDC opaque-token introspection; JIT
principal provisioning; searchable/deterministic client-side encryption;
suffix-truncation detection on a local audit file without an external
append-only system.

No critical production defect is tracked as open after this review. P25's
implementable scope and this sign-off together close the P25 phase-wide exit
gate; see `TODO.md` "Phase 25 exit gate" and `docs/client-encryption.md`
"Production-gating sign-off (Phase 25)" for the `ENCRYPTED CLIENT`-specific
consequence of this gate closing.
