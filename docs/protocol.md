# Native wire protocol (Phase 8)

`ENCRYPTED CLIENT` adds no NSQL frame or wire type. Clients bind and receive
randomized `NSCE1.` or deterministic `NSCE2.` ciphertext through the existing
`STRING` value encoding; logical type and encryption mode are catalog metadata
(`NSCT` v13).

`STRUCT` / `ARRAY` / `MAP` values (`docs/design-collections.md`) travel as a
self-describing recursive type descriptor after the fixed value header
(depth-bounded, re-validated on decode), then a nested payload — no NSQL
version bump (a scalar value's header is byte-identical to before). A
plain-string / native-list / wrapper param for a collection column is
re-coerced server-side against the destination column type.

Versioned NextSQL framing spoken by `nextsqld` and the official drivers (`drivers/go`, `drivers/node`, `drivers/bun`, `drivers/php`, `drivers/python`, `drivers/ruby`). Node and Bun ship TypeScript types (`drivers/js/types.d.ts`). Local SQL execution is unchanged (`docs/sql.md`). This document is the on-the-wire contract.

ISO/IEC 9075-3:2023 SQL/CLI and ISO/IEC 9579:2000 RDA are conceptual design
references. NextSQL does not expose those interfaces directly: NSQL remains the
authoritative native protocol over TCP, remote production connections require
TLS 1.3, and textual protocol content is Unicode/UTF-8. See
`docs/standards.md`.

## Driver contract

Official drivers never accept encryption keys in a URL.

```go
conn, err := nextsql.Open(nextsql.Config{
    Address:     "db.example.com:7210",
    Database:    "production",
    User:        "app",
    KeyProvider: provider, // reserved; never a URL query parameter
    TLS:         tlsConfig, // TLS 1.3
})
```

```js
const conn = await connect({
  address: "db.example.com:7210",
  database: "production",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  key: clientRoot, // 32-byte Buffer when the server requires a client key
  tls: { ca, servername: "db.example.com" },
});
```

```php
$conn = NextSQL\Client::connect([
    'address' => 'db.example.com:7210',
    'database' => 'production',
    'user' => 'app',
    'password' => getenv('NEXTSQL_DATABASE_PASS'),
    'key' => $clientRoot, // 32-byte string when the server requires a client key
    'tls' => ['cafile' => $caPath, 'servername' => 'db.example.com'],
]);
```

```js
// Bun (drivers/bun)
const conn = await connect({
  address: "db.example.com:7210",
  database: "production",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  key: clientRoot, // 32-byte Uint8Array when the server requires a client key
  tls: { ca: pem, servername: "db.example.com" },
});
```

```ts
// TypeScript (Node, Bun)
import { connect, type Config } from "@bzync/nextsql"; // Node (npm)
// import { connect, type Config } from "./nextsql.js"; // Bun (repo-distributed)

const cfg: Config = {
  address: "db.example.com:7210",
  database: "production",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  key: clientRoot,
  tls: { ca: pem, servername: "db.example.com" },
};
const conn = await connect(cfg);
```

`KeyProvider` supplies the root unlock key for `REQUIRE CLIENT KEY`. The
default server still unlocks from `--key-file` (kept off the data volume).
`--require-client-key` leaves the root off the host: after password auth the
driver sends `TypeUnlock` (32-byte AES key + version) over TLS. Passwords
travel in `Config.Password` (or `--password-file`), never in the address.

Plaintext is allowed only on loopback. Non-loopback listeners and clients require TLS 1.3.

When `nextsqld --tls-client-ca FILE` is configured, TLS additionally requires a
client certificate chaining to that CA. The verified leaf must carry exactly
one `nextsql://service/<principal>` URI SAN matching the Hello user. This does
not replace native password authentication or RBAC. It adds no NSQL frame or
version change. CLI clients load the pair with `--tls-client-cert` and
`--tls-client-key`; Go callers set a client certificate on `Config.TLS`.
`nextsqld` reloads its server key pair, client trust bundle, and optional
`--tls-client-crl` PEM bundle on `SIGHUP`. Publication is atomic and a failed
reload retains the last known-good snapshot. Configured CRLs are signature- and
time-validated and require complete non-root chain coverage; revoked or
uncovered clients fail the handshake. Successful mTLS reload terminates every
accepted connection, including pre-authentication handshakes, so clients
reauthenticate under the new snapshot. OCSP is not implemented. This adds no
NSQL frame or wire-version change.

## Frame

Little-endian. Every length is checked against a configured maximum before allocation.

```text
0-3   magic        "NSQL"
4-5   version      u16 = 1
6     type         u8
7     flags        u8 (reserved, 0)
8-11  length       u32  payload bytes; reject if > max_packet (default 1 MiB)
12-   payload
```

A length field larger than `max_packet` is a protocol error. The implementation does not allocate that many bytes.

## Messages

| Type | Direction | Payload |
|---|---|---|
| Hello | C→S | version, flags, cancel secret, database, user, realm (optional trailing field; reserved, must be empty) |
| HelloOK | S→C | version, auth method (1 = password), cancel secret, accepted capability flags (optional trailing `u16`; see Capability negotiation) |
| Auth | C→S | password (TLS) |
| AuthOK | S→C | empty |
| Query | C→S | SQL (`u32` length) + typed parameters |
| IdempotentQuery | C→S | idempotency key (`u16`) + mutation SQL + typed parameters |
| SetReadConsistency | C→S | mode byte (0 strong, 1 bounded, 2 stale) + `MAX STALENESS` ms (`u64`) → Ready |
| NodeStatus / NodeStatusResp | C↔S | empty → role, `has_leader`, `healthy`, applied LSN, last-contact ms, apply backlog |
| Prepare / PrepareOK | C↔S | SQL → statement id |
| Execute | C→S | statement id + typed parameters |
| CloseStmt / CloseOK | C↔S | statement id |
| FlowAck | C→S | empty; required after each DataBatch |
| Cancel | C→S | in-band, or Hello with cancel flag + secret on a new connection |
| Terminate | C→S | empty |
| RowDesc | S→C | column names and types |
| DataBatch | S→C | row count + self-describing values |
| CommandComplete | S→C | affected row count |
| Error | S→C | code + message (never a password or key) + stable `ERR_*` public code (optional trailing field; see Capability negotiation) |
| Ready | S→C | empty; session can take the next command |
| Unlock / UnlockOK | C↔S | client-held root after password auth when the server requires it |

### Capability negotiation

`Hello.flags` bit 1 (`0x0002`, public error codes) asks the server to include the
stable `ERR_*` public error name (`docs/error-codes.md`) on error frames. The
frame version is *not* bumped for this: a frame whose version is not exactly `1`
is rejected outright, so raising it would disconnect every existing client
rather than upgrade it. Capabilities are negotiated instead.

The server masks a client's requested bits down to those it implements and
echoes the result in `HelloOK`, so an echoed bit is a guarantee, never a repeat
of the request. Both the accepted-flags field and the error frame's public code
are *optional trailing fields*: they are written only when non-empty, so every
frame sent to a client that requested nothing is byte-identical to the
pre-capability NSQL v1 shape. A client that requests the capability must still
accept the two-field error frame, because an older server ignores the bit.

Optional trailing fields are canonical — an empty one is rejected rather than
treated as absent, so one message value has exactly one encoding.

An error frame's legacy lowercase `code` is never replaced, in either
direction. Existing clients branch their retry logic on it.

Strings used for names are `u16` length + bytes. SQL is `u32` length + bytes. Both reject a declared length above the matching limit or past the end of the payload.

`IdempotentQuery` is an additive NSQL v1 frame; existing `Query` and `Execute`
encodings are unchanged. The key is capped at 256 bytes. The server scopes and
hashes it by authenticated user and tenant, then atomically commits the
mutation and bounded replay result. Same-key/different-request reuse returns
`conflict`. The current prepared-statement `Execute` frame has no idempotency
field; clients use `IdempotentQuery` for retryable mutations.

`SetReadConsistency` and `NodeStatus` are additive NSQL v1 frames for
follower-read routing (`docs/ha.md`). `SetReadConsistency` sets the session's
read-consistency mode; `STRONG` (default) is served on the leader behind a
Raft read barrier, `BOUNDED` on any member within `MAX STALENESS` of the
leader, `STALE` on any member with no freshness bound. `NodeStatus` returns the
key-free replica-health snapshot — the same data as `system.replica_health` —
so a client can route without a `STALE` SQL round trip. The server enforces
every barrier regardless of how the client routed. Every official driver
exposes both frames and a cluster-routing client (Go `nextsql.Cluster`,
JS `connectCluster`, PHP `NextSQL\Cluster`).

## Authentication

New passwords are stored as versioned Argon2id records in `nextsql.users`
(`NSAU` v2; 64 MiB, time 1, parallelism 4). Legacy PBKDF2-HMAC-SHA256 records
remain readable and are transparently rehashed after a successful login. The
file mode is `0600`; unknown users and bad passwords return the same error and
perform equivalent bounded hash work. Nothing in this file is a plaintext
password. In mTLS mode the certificate identity is bound before this password
check; successful sessions still pass authentication and RBAC.

The auth file is not the storage DEK. The root stays in `--key-file` or in the
client `KeyProvider`. HelloOK `AuthMethod` `2` means the client must unlock.

**Short-lived credentials.** When `token_verify_keyset` is configured, a client
may send a signed short-lived credential (`NSSC1.`…) in the `Auth` password
field instead of a password. There is no new frame or auth method: the server
recognizes the `NSSC1.` prefix, verifies the Ed25519 signature, validity
window, audience, database scope, and revocation state, requires the claimed
principal to match the Hello user and be a known native user, applies any role
scope, and closes the session at the credential's expiry. See
`docs/security.md`. Drivers pass it wherever `Config.Password` would go.
An optional bounded `token_identity_source_hint=KEY_ID:oidc,...` changes only
the server audit label to `oidc` / `mtls+oidc` after signature verification; it
adds no claim and does not change NSQL or the `NSSC1.` format.

A deployment serves exactly one database, so Hello's `database` and `realm`
fields select nothing: they are identity checks. An empty field means "whatever
this deployment serves"; a field naming anything else is rejected. Unknown
database/realm/user combinations are carried through the password verification
step and collapse to the same generic `unauthorized` result, preventing
pre-authentication existence enumeration.

`Hello.Realm` remains an **additive trailing field, not a protocol version
bump** — the frame header's `Version` remains a hard equality gate with no
negotiation. It is now reserved: multi-realm hosting was removed, every
official driver leaves it empty, and a client that emits nothing past `user`
produces the same wire shape it always did (`DecodeHello` tail-sniffs one more
length-prefixed string only when bytes remain past `user`, mirroring `NSCT`'s
V1 field-versioning pattern in `internal/catalog`).

## Streaming and backpressure

SELECT/EXPLAIN send `RowDesc`, then one `DataBatch` at a time. The server waits for `FlowAck` before encoding the next batch. At most one batch is in flight, so a slow client cannot grow server protocol memory without bound. The query result itself remains under the existing executor memory budget (`docs/execution.md`).

DML sends `CommandComplete` only.

## Cancellation

HelloOK includes a 64-bit cancel secret. The driver opens a second connection, sends Hello with the cancel flag and that secret, and the backend cancels the current query context and unblocks any `FlowAck` wait. In-band `Cancel` on the query connection is also accepted while waiting for flow control.

## Limits (defaults)

| Limit | Default |
|---|---|
| Packet | 64 MiB configurable; 64 MiB ceiling |
| SQL text | 16 MiB configurable; 64 MiB ceiling |
| Parameters | 65,535 configurable; 65,535 ceiling |
| Prepared statements / session | 64 configurable; 4,096 ceiling |
| Concurrent sessions | 128 |
| Concurrent sessions per user | unlimited |
| Result bytes on the wire | 64 MiB configurable; 64 MiB ceiling |
| Idle | 60 s |
| Statement (per-query wall clock) | 30 s |
| Transaction (total open lifetime) | unbounded |
| Lock wait (contended, non-deadlocking) | unbounded |
| Idle-in-transaction (traffic gap while open) | no distinct bound (falls back to Idle) |

These sit on top of the Phase 7 per-query worker / memory / disk / I/O / time budget — the statement row above *is* that budget's time bound (`scheduler.Limits.Time`), surfaced here because it is also configurable per node. Frame, SQL, parameter, prepared-statement, and result-byte limits are independently configurable (`max_frame_bytes`, `max_statement_bytes`, `max_parameters`, `max_prepared_statements`, `max_result_bytes`); SQL text may not exceed its enclosing frame. Concurrent sessions, concurrent sessions per user, idle, statement, transaction, and idle-in-transaction are configurable per node (`max_connections`, `max_connections_per_user`, `idle_timeout_ms`, `statement_timeout_ms`, `transaction_timeout_ms`, `idle_transaction_timeout_ms` in `docs/ops.md` "Connection limits" / "Statement, transaction, lock, and idle-transaction timeouts") and are not synchronized across a cluster. Lock wait (`lock_timeout_ms`) is process-wide rather than per-connection — it bounds the shared engine-wide lock table, not one session's limits. An over-limit connection is rejected with `exhausted` after authentication, before a session is created; an over-budget statement or a timed-out lock wait each fail the statement in progress with `exhausted` instead; an over-timeout transaction force-aborts on the next statement dispatched inside it (`transaction_timeout_ms`) or, if none ever arrives, once the idle-in-transaction bound elapses and the connection's next frame read times out (`idle_transaction_timeout_ms`) — either way, and on any other path that tears down a connection with a transaction still open, the transaction is rolled back rather than left holding locks.

## Threat model (honest)

TLS 1.3 protects the wire on remote connections. At-rest pages, WAL, and UNDO stay encrypted with the Phase 13 envelope (`docs/security.md`). A live unlocked `nextsqld` process still has keys, pages, and result rows in RAM; a privileged host attacker can see them. NextSQL does not claim otherwise. RBAC and audit apply after authentication.
