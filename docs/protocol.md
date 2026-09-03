# Native wire protocol (Phase 8)

`ENCRYPTED CLIENT` adds no NSQL frame or wire type. Clients bind and receive
its `NSCE1.` ciphertext through the existing `STRING` value encoding; logical
type and encryption status are catalog metadata (`NSCT` v11).

Versioned NextSQL framing spoken by `nextsqld` and the official drivers (`drivers/go`, `drivers/node`, `drivers/bun`, `drivers/deno`, `drivers/php`, `drivers/python`, `drivers/ruby`). Node, Bun, and Deno ship TypeScript types (`drivers/js/types.d.ts`). Local SQL execution is unchanged (`docs/sql.md`). This document is the on-the-wire contract.

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

```js
// Deno (drivers/deno)
const conn = await connect({
  address: "db.example.com:7210",
  database: "production",
  user: "app",
  password: Deno.env.get("NEXTSQL_DATABASE_PASS"),
  key: clientRoot, // 32-byte Uint8Array when the server requires a client key
  tls: { ca: pem, servername: "db.example.com" },
});
```

```ts
// TypeScript (Node, Bun, Deno)
import { connect, type Config } from "nextsql"; // Bun/Node
// import { connect, type Config } from "./mod.ts"; // Deno

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
| Hello | C→S | version, flags, cancel secret, database, user, realm (optional trailing field) |
| HelloOK | S→C | version, auth method (1 = password), cancel secret |
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
| Error | S→C | code + message (never a password or key) |
| Ready | S→C | empty; session can take the next command |
| Unlock / UnlockOK | C↔S | client-held root after password auth when the server requires it |

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

Passwords are stored as PBKDF2-HMAC-SHA256 (100 000 iterations, 16-byte salt, 32-byte hash) in `nextsql.users` (`NSAU` v1). The file mode is `0600`. Authentication failures use the same error for unknown user and bad password. Nothing in this file is a plaintext password. In mTLS mode the certificate identity is bound before this password check; successful sessions still pass both password authentication and RBAC.

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

For deployments containing the M1 `nextsql.instance` registry, `nextsqld`
loads the registered default logical database name into the existing Hello
check. A matching non-empty Hello database is accepted, a different non-empty
name is `not_found`, and an empty v1 field selects the default for compatibility.
This is default-database validation, not multi-database routing.

`Hello.Realm` (M2-2) works the same way, one layer up: `nextsqld` also loads
the registered default realm's name and rejects a non-empty, non-matching
`Hello.Realm` with `not_found` ("unknown realm"). It is an **additive
trailing field, not a protocol version bump** — the frame header's `Version`
remains a hard equality gate with no negotiation. A client that never
configures a realm emits nothing past `user`, producing the exact pre-realm
wire shape (`DecodeHello` tail-sniffs one more length-prefixed string only
when bytes remain past `user`, mirroring `NSCT`'s V1 field-versioning
pattern in `internal/catalog`); a client that does select a realm requires a
server new enough to decode the trailing field, and fails closed with a
decode error against an older one rather than silently connecting to
whatever that server has open. This is still flat-string identity
validation against the one realm+database pair a given `nextsqld` process
serves, not selectable multi-database engine routing — see
`docs/design-multidatabase-dbaas.md` §8/§16 for that still-open scope
(M2-3/M2-4).

## Streaming and backpressure

SELECT/EXPLAIN send `RowDesc`, then one `DataBatch` at a time. The server waits for `FlowAck` before encoding the next batch. At most one batch is in flight, so a slow client cannot grow server protocol memory without bound. The query result itself remains under the existing executor memory budget (`docs/execution.md`).

DML sends `CommandComplete` only.

## Cancellation

HelloOK includes a 64-bit cancel secret. The driver opens a second connection, sends Hello with the cancel flag and that secret, and the backend cancels the current query context and unblocks any `FlowAck` wait. In-band `Cancel` on the query connection is also accepted while waiting for flow control.

## Limits (defaults)

| Limit | Default |
|---|---|
| Packet | 1 MiB |
| SQL text | 1 MiB |
| Parameters | 256 |
| Prepared statements / session | 64 |
| Concurrent sessions | 128 |
| Concurrent sessions per user | unlimited |
| Result bytes on the wire | 64 MiB |
| Idle | 60 s |
| Statement (per-query wall clock) | 30 s |
| Transaction (total open lifetime) | unbounded |
| Lock wait (contended, non-deadlocking) | unbounded |
| Idle-in-transaction (traffic gap while open) | no distinct bound (falls back to Idle) |

These sit on top of the Phase 7 per-query worker / memory / disk / I/O / time budget — the statement row above *is* that budget's time bound (`scheduler.Limits.Time`), surfaced here because it is also configurable per node. Concurrent sessions, concurrent sessions per user, idle, statement, transaction, and idle-in-transaction are configurable per node (`max_connections`, `max_connections_per_user`, `idle_timeout_ms`, `statement_timeout_ms`, `transaction_timeout_ms`, `idle_transaction_timeout_ms` in `docs/ops.md` "Connection limits" / "Statement, transaction, lock, and idle-transaction timeouts") and are not synchronized across a cluster. Lock wait (`lock_timeout_ms`) is process-wide rather than per-connection — it bounds the shared engine-wide lock table, not one session's limits. An over-limit connection is rejected with `exhausted` after authentication, before a session is created; an over-budget statement or a timed-out lock wait each fail the statement in progress with `exhausted` instead; an over-timeout transaction force-aborts on the next statement dispatched inside it (`transaction_timeout_ms`) or, if none ever arrives, once the idle-in-transaction bound elapses and the connection's next frame read times out (`idle_transaction_timeout_ms`) — either way, and on any other path that tears down a connection with a transaction still open, the transaction is rolled back rather than left holding locks.

## Threat model (honest)

TLS 1.3 protects the wire on remote connections. At-rest pages, WAL, and UNDO stay encrypted with the Phase 13 envelope (`docs/security.md`). A live unlocked `nextsqld` process still has keys, pages, and result rows in RAM; a privileged host attacker can see them. NextSQL does not claim otherwise. RBAC and audit apply after authentication.
