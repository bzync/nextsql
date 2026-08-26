# Native wire protocol (Phase 8)

Versioned NextSQL framing spoken by `nextsqld` and the official drivers (`drivers/go`, `drivers/node`, `drivers/bun`, `drivers/deno`, `drivers/php`). Node, Bun, and Deno ship TypeScript types (`drivers/js/types.d.ts`). Local SQL execution is unchanged (`docs/sql.md`). This document is the on-the-wire contract.

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
  password: process.env.NEXTSQL_PASSWORD,
  key: clientRoot, // 32-byte Buffer when the server requires a client key
  tls: { ca, servername: "db.example.com" },
});
```

```php
$conn = NextSQL\Client::connect([
    'address' => 'db.example.com:7210',
    'database' => 'production',
    'user' => 'app',
    'password' => getenv('NEXTSQL_PASSWORD'),
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
  password: process.env.NEXTSQL_PASSWORD,
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
  password: Deno.env.get("NEXTSQL_PASSWORD"),
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
  password: process.env.NEXTSQL_PASSWORD,
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
| Hello | C→S | version, flags, cancel secret, database, user |
| HelloOK | S→C | version, auth method (1 = password), cancel secret |
| Auth | C→S | password (TLS) |
| AuthOK | S→C | empty |
| Query | C→S | SQL (`u32` length) + typed parameters |
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

## Authentication

Passwords are stored as PBKDF2-HMAC-SHA256 (100 000 iterations, 16-byte salt, 32-byte hash) in `nextsql.users` (`NSAU` v1). The file mode is `0600`. Authentication failures use the same error for unknown user and bad password. Nothing in this file is a plaintext password.

The auth file is not the storage DEK. The root stays in `--key-file` or in the
client `KeyProvider`. HelloOK `AuthMethod` `2` means the client must unlock.

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
| Result bytes on the wire | 64 MiB |
| Idle | 60 s |

These sit on top of the Phase 7 per-query worker / memory / disk / I/O / time budget.

## Threat model (honest)

TLS 1.3 protects the wire on remote connections. At-rest pages, WAL, and UNDO stay encrypted with the Phase 13 envelope (`docs/security.md`). A live unlocked `nextsqld` process still has keys, pages, and result rows in RAM; a privileged host attacker can see them. NextSQL does not claim otherwise. RBAC and audit apply after authentication.
