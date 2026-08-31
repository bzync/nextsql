# Wire protocol

Versioned NextSQL framing spoken by `nextsqld` and the official drivers. Local SQL execution is unchanged. This page is the application contract; the on-the-wire layout lives in [`docs/protocol.md`](https://github.com/bzync/nextsql/blob/main/docs/protocol.md).

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

## Session rules

- One statement per request on the wire.
- A connection is single-flight: a second query while rows are open returns `conflict`.
- Default listen: `127.0.0.1:7210`.
- Packet / SQL text cap: 1 MiB. Parameters: 256. Prepared statements per session: 64. Concurrent sessions: 128. Idle: 60 s.
- Retryable mutations use the additive NSQL v1 `IdempotentQuery` frame: a
  bounded key plus ordinary SQL/typed parameters. Same-key replays return the
  committed result; different-request reuse is `conflict`.
- Follower reads use the additive `SetReadConsistency` frame (mode +
  `MAX STALENESS`) and the `NodeStatus` frame (key-free replica health). Every
  official driver ships a cluster-routing client (Go `nextsql.Cluster`, JS
  `connectCluster`, PHP `NextSQL\Cluster`) that routes eligible reads to a
  healthy follower; the server enforces every barrier regardless. See
  [HA](/docs/ha).
- Initialized deployments validate a non-empty Hello database against the
  registered logical default; empty selects that default for v1 compatibility.
  This does not yet route among multiple engines.

See [Drivers](/docs/drivers) for language-specific examples and [TLS](/docs/tls) for unlock-over-TLS (`TypeUnlock`).
