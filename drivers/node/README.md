# @bzync/nextsql

Official [NextSQL](https://nextsql.bzync.com) driver for Node.js. Speaks the native
NSQL v1 wire protocol over TLS 1.3. Zero runtime dependencies. Bundled
TypeScript types.

Encryption keys and passwords are **never** accepted in a connection URL.

```bash
npm i @bzync/nextsql
```

```ts
import { connect } from "@bzync/nextsql";

const conn = await connect({
  address: "db.example.com:7210",
  database: "production",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  tls: { ca, servername: "db.example.com" },
});

const res = await conn.exec("SELECT id, name FROM users WHERE id = $1", [1]);
console.log(res.rows);
await conn.close();
```

Plaintext connections are allowed only on loopback (`insecureNoTLS: true`).
For an HA cluster with follower-read routing, use `connectCluster`.

- Full driver docs: <https://nextsql.bzync.com/docs/drivers>
- Wire protocol: <https://github.com/bzync/nextsql/blob/master/docs/protocol.md>
- Client-side field encryption: <https://nextsql.bzync.com/docs/client-encryption>

MIT licensed.
