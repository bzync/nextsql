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
    'key' => $clientRoot,
    'tls' => ['cafile' => $caPath, 'servername' => 'db.example.com'],
]);
```

## Session rules

- One statement per request on the wire.
- A connection is single-flight: a second query while rows are open returns `conflict`.
- Default listen: `127.0.0.1:7210`.
- Packet / SQL text cap: 1 MiB. Parameters: 256. Prepared statements per session: 64. Concurrent sessions: 128. Idle: 60 s.

See [Drivers](/docs/drivers) for language-specific examples and [TLS](/docs/tls) for unlock-over-TLS (`TypeUnlock`).
