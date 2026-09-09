# Node and Bun

The Node and Bun clients have the same shape and share the codec in
[`drivers/js`](https://github.com/bzync/nextsql/tree/master/drivers/js).

The Node client is published to npm as [`@bzync/nextsql`](https://www.npmjs.com/package/@bzync/nextsql)
— MIT, zero runtime dependencies, TypeScript types bundled:

```bash
npm i @bzync/nextsql
```

The Bun client is repository-distributed — import it from `drivers/bun/` in the
tree.

## Node.js / Bun

```js
const { connect } = require("@bzync/nextsql"); // Bun: import from ./drivers/bun/nextsql.js

const conn = await connect({
  address: "127.0.0.1:7210",
  database: "default",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  insecureNoTLS: true,
});

const res = await conn.exec("SELECT name FROM items WHERE price < $1", [
  { kind: "decimal", value: "50.00" },
]);
console.log(res.rows);

const stmt = await conn.prepare("SELECT sku FROM items WHERE sku = $1");
const rows = await stmt.query(["A-1"]);
await stmt.close();
await conn.close();
```

TypeScript: `import { connect, type Config } from "@bzync/nextsql"`.

## Typed parameters

`{ kind: "uuid" | "decimal", value: "…" }`, numbers, strings, booleans, `Date`, `number[]` (vectors), `{ lon, lat }` (points), `{ west, south, east, north }` (boxes), or a plain object (JSON). Explicit wrappers also cover `FLOAT32`/`FLOAT64`, `INTERVAL`, `ENUM`, collections, and `GEOMETRY`/`GEOGRAPHY`.

Follower-read routing uses `connectCluster`. See [High availability](/docs/ha).

Remote TLS:

```js
const conn = await connect({
  address: "db.example.com:7210",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  tls: { ca, servername: "db.example.com" },
});
```

For `--require-client-key`, pass `key` as a 32-byte `Buffer` or `Uint8Array`.

## Client-encrypted fields

Provide `fieldKeys`, then use `encryptField` / `decryptField` for randomized
`NSCE1` columns. Explicit `ENCRYPTED CLIENT DETERMINISTIC` columns use
`encryptFieldDeterministic` / `decryptFieldDeterministic`; bind the produced
`NSCE2` value only to equality/inequality predicates for that exact column.
Deterministic mode leaks equality and frequency. `FileFieldKeyring` provides
versioned durable rotation/revocation; general searchable encryption is not
supported.
