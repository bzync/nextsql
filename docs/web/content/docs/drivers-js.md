# Node, Bun, and Deno

Shared TypeScript types live in [`drivers/js/types.d.ts`](https://github.com/bzync/nextsql/blob/main/drivers/js/types.d.ts). The Node and Bun clients have the same shape.

## Node.js / Bun

```js
const { connect } = require("./drivers/node/nextsql"); // Bun: drivers/bun/nextsql.js

const conn = await connect({
  address: "127.0.0.1:7210",
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

TypeScript: `import { connect, type Config } from "./drivers/node/nextsql"`.

## Deno

```ts
import { connect } from "./drivers/deno/mod.ts";

const conn = await connect({
  address: "127.0.0.1:7210",
  user: "app",
  password: Deno.env.get("NEXTSQL_DATABASE_PASS"),
  insecureNoTLS: true,
});
const res = await conn.exec("SELECT 1");
await conn.close();
```

## Typed parameters

`{ kind: "uuid" | "decimal", value: "…" }`, numbers, strings, booleans, `Date`, `number[]` (vectors), `{ lon, lat }` (points), `{ west, south, east, north }` (boxes), or a plain object (JSON).

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
