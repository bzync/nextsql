# Node and Bun

The Node.js and Bun clients share the same API and wire codec in [`drivers/js`](https://github.com/bzync/nextsql/tree/master/drivers/js).

The Node client is published to npm as [`@bzync/nextsql`](https://www.npmjs.com/package/@bzync/nextsql) — MIT, zero runtime dependencies, with bundled TypeScript declarations:

```bash
npm i @bzync/nextsql@0.0.3
```

The Bun client is distributed in the repository under [`drivers/bun/`](https://github.com/bzync/nextsql/tree/master/drivers/bun).

---

## Quick start

```js
const { connect } = require("@bzync/nextsql"); // ESM / TypeScript: import { connect, type Config } from "@bzync/nextsql"

const conn = await connect({
  address: "127.0.0.1:7210",
  database: "default",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  insecureNoTLS: true, // loopback only
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

### Remote TLS & client unlock key

```js
const conn = await connect({
  address: "db.example.com:7210",
  database: "production",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  tls: { ca, servername: "db.example.com" },
});
```

For `--require-client-key`, pass `key` as a 32-byte `Buffer` or `Uint8Array`. Never put keys or passwords in a URL.

---

## Connection pooling & prepared statement LRU cache

Both `Conn` and `Pool` (`createPool`) include an automatic bounded **LRU prepared statement cache** (`statementCacheSize: 50` by default; `0` disables caching). Parameterized queries (`conn.query(sql, params)` or `pool.query(sql, params)`) transparently reuse server-side prepared statements and close evicted statement handles on the server (`Type.CloseStmt`).

```js
import { createPool } from "@bzync/nextsql";

const pool = createPool({
  address: "127.0.0.1:7210",
  database: "default",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  insecureNoTLS: true,
  minSize: 2,
  maxSize: 10,
  statementCacheSize: 50,
});

// Auto-checkout and release:
const res = await pool.query("SELECT id, name FROM users WHERE status = $1", ["active"]);

// Scoped connection checkout:
await pool.withConnection(async (conn) => {
  await conn.exec("INSERT INTO audit_log (event) VALUES ($1)", ["started"]);
});

await pool.close();
```

---

## Typed parameters

Pass native JS values or explicit type descriptors:

* `{ kind: "uuid" | "decimal", value: "…" }`
* `number`, `string`, `boolean`, `Date`, `Uint8Array` / `Buffer`
* `number[]` (dense vectors) or `{ kind: "sparsevector", dim, indices, values }`
* `{ lon, lat }` (`POINT`), `{ west, south, east, north }` (`BOX`), `{ points }` (`LINESTRING`), `{ rings }` (`POLYGON`), `{ kind: "geometry" | "geography", ... }`
* Plain objects (encoded as `JSON`, decoded from binary `NSJB` on read)
* Explicit wrappers for `FLOAT32`/`FLOAT64`, `INT8`..`INT64`, `UINT8`..`UINT64`, `INTERVAL`, `ENUM`, `STRUCT`, `ARRAY`, and `MAP`.

---

## Client-side field encryption

Provide `fieldKeys` (`MemoryFieldKeyring` or durable `FileFieldKeyring`), then use `encryptField` / `decryptField` for randomized `NSCE1` columns and `encryptFieldDeterministic` / `decryptFieldDeterministic` for `ENCRYPTED CLIENT DETERMINISTIC` (`NSCE2`) equality predicates:

```js
import { connect, FileFieldKeyring } from "@bzync/nextsql";

const keyring = new FileFieldKeyring("/etc/nextsql/field-keys.json");
const conn = await connect({
  address: "127.0.0.1:7210",
  database: "default",
  user: "app",
  password: process.env.NEXTSQL_DATABASE_PASS,
  insecureNoTLS: true,
  fieldKeys: keyring,
});

const ct = await conn.encryptField("patients", "ssn", "123-45-6789");
await conn.exec("INSERT INTO patients (id, ssn) VALUES ($1, $2)", [1, ct]);
```

See [Client-side encryption](/docs/client-encryption) for details.

---

## Cluster routing & follower reads

Use `connectCluster` (`ReadConsistency.STRONG`, `ReadConsistency.BOUNDED`, or `ReadConsistency.STALE`) to route read-only queries to healthy followers and writes to the Raft leader. See [High availability](/docs/ha) and [Error codes & taxonomy](/docs/error-codes).
