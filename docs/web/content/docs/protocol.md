# Wire protocol

Versioned NextSQL framing spoken by `nextsqld` and the official drivers. Local SQL execution is unchanged. This page is the application contract; the on-the-wire layout lives in [`docs/protocol.md`](https://github.com/bzync/nextsql/blob/main/docs/protocol.md).

## Frame layout

Little-endian. Every length is checked against a configured maximum before allocation. A length field larger than `max_packet` is a protocol error.

```wire
0-3   magic        "NSQL"
4-5   version      u16 = 1
6     type         u8
7     flags        u8 (reserved, 0)
8-11  length       u32  payload bytes; reject if > max_packet (default 1 MiB)
12-   payload      byte[length]
```

## Session flow

The connection lifecycle is single-flight and synchronous. Query execution follows strict flow control:

```wire
Client                                         Server
  | ------------ Hello (v1) -------------------> |
  | <----------- HelloOK (v1, cancel secret) --- |
  | ------------ Auth (password / NSSC1) ------> |
  | <----------- AuthOK ------------------------ |
  | ------------ Query (SQL + params) ---------> |
  | <----------- RowDesc (column metadata) ----- |
  | <----------- DataBatch (row data) ---------- |
  | ------------ FlowAck ----------------------> |
  | <----------- Ready ------------------------- |
```

## Message payloads

```wire
# Hello (Type 0x01, C→S)
0-1    version        u16 = 1
2-3    flags          u16 capability flags (0x0002 for ERR_* public codes)
4-11   cancel_secret  u64 client cancellation secret
12-13  db_len         u16 database name length
14..   database       utf8 bytes
..     user_len       u16 username length
..     user           utf8 bytes
..     realm_len      u16 reserved (0x0000)

# Query (Type 0x05, C→S)
0-3    sql_len        u32 SQL statement byte length (max 16 MiB)
4..    sql_text       utf8 query string
..     param_count    u16 parameter count (max 65,535)
..     params         typed parameter stream

# Error (Type 0x12, S→C)
0-1    code_len       u16 internal error code length
2..    code           utf8 internal code ("unauthorized", "conflict", ...)
..     msg_len        u16 error message length
..     message        utf8 sanitized error message
..     public_code    optional utf8 stable ERR_* code (when negotiated)
```

## Messages

| Type | Direction | Payload |
|---|---|---|
| Hello | C→S | version, flags, cancel secret, database, user, realm (optional trailing field; reserved, must be empty) |
| HelloOK | S→C | version, auth method (1 = password), cancel secret, accepted capability flags (optional trailing `u16`) |
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
| Error | S→C | code + message (never a password or key) + stable `ERR_*` public code |
| Ready | S→C | empty; session can take the next command |
| Unlock / UnlockOK | C↔S | client-held root after password auth when the server requires it |

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
    'key' => $clientRoot,
    'tls' => ['cafile' => $caPath, 'servername' => 'db.example.com'],
]);
```

```ruby
conn = NextSQL.connect(NextSQL::Config.new(
  address: "db.example.com:7210",
  database: "production",
  user: "app",
  password: ENV.fetch("NEXTSQL_DATABASE_PASS"),
  key: client_root_32_bytes,
  tls: NextSQL::TLSConfig.new(
    cafile: "/etc/nextsql/ca.pem",
    server_name: "db.example.com",
  ),
))
```

## Session rules

- One statement per request on the wire.
- A connection is single-flight: a second query while rows are open returns `conflict`.
- Default listen: `127.0.0.1:7210`.
- Packet / SQL text cap: 64 MiB / 16 MiB (configurable within those ceilings). Parameters: 65,535. Prepared statements per session: 64 default, 4,096 ceiling. Concurrent sessions: 128. Idle: 60 s.
- Retryable mutations use the additive NSQL v1 `IdempotentQuery` frame: a
  bounded key plus ordinary SQL/typed parameters. Same-key replays return the
  committed result; different-request reuse is `conflict`.
- Follower reads use the additive `SetReadConsistency` frame (mode +
  `MAX STALENESS`) and the `NodeStatus` frame (key-free replica health). Every
  official driver ships a cluster-routing client (Go `OpenCluster`, JS
  `connectCluster`, PHP `NextSQL\Cluster::connect`, Python and Ruby
  `connect_cluster`) that routes eligible reads to a healthy follower; the
  server enforces every barrier regardless. See [HA](/docs/ha).
- Each deployment serves exactly one database. Hello may name that database;
  an empty name accepts the deployment default, while another database or any
  non-empty realm is rejected without disclosing deployment names.

See [Drivers](/docs/drivers) for language-specific examples and [TLS](/docs/tls) for unlock-over-TLS (`TypeUnlock`).
