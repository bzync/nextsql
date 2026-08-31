// Live TypeScript check invoked by tests/integration/drivers_test.go.
// Env: NEXTSQL_ADDR, NEXTSQL_CA, NEXTSQL_DATABASE_USER, NEXTSQL_DATABASE_PASS

import fs from 'node:fs';
import { connect, ReadConsistency, type Config } from './nextsql.js';

async function main(): Promise<void> {
  const addr = process.env.NEXTSQL_ADDR;
  const caPath = process.env.NEXTSQL_CA;
  if (!addr || !caPath) {
    throw new Error('NEXTSQL_ADDR and NEXTSQL_CA are required');
  }
  const cfg: Config = {
    address: addr,
    database: 'production',
    user: process.env.NEXTSQL_DATABASE_USER || 'app',
    password: process.env.NEXTSQL_DATABASE_PASS || 's3cret',
    tls: {
      ca: fs.readFileSync(caPath),
      servername: 'localhost',
    },
  };
  const conn = await connect(cfg);
  try {
    await conn.exec(`CREATE TABLE items (
      id UUID PRIMARY KEY DEFAULT UUID(),
      sku STRING NOT NULL,
      qty DECIMAL(10,0)
    )`);
    const ins = await conn.exec(`INSERT INTO items (sku, qty) VALUES ('A-1', 3), ('B-2', 9)`);
    if (ins.affected !== 2) {
      throw new Error('inserted ' + ins.affected);
    }
    const sel = await conn.exec(`SELECT sku, qty FROM items WHERE sku = $1`, ['B-2']);
    if (sel.rows.length !== 1 || sel.rows[0][0] !== 'B-2') {
      throw new Error('select mismatch ' + JSON.stringify(sel.rows));
    }
    const qty = sel.rows[0][1];
    if (qty !== '9' && qty !== '9.0') {
      throw new Error('qty ' + String(qty));
    }
    const st = await conn.prepare(`SELECT sku FROM items WHERE sku = $1`);
    const pres = await st.exec(['A-1']);
    if (pres.rows.length !== 1 || pres.rows[0][0] !== 'A-1') {
      throw new Error('prepared mismatch');
    }
    await st.close();
    let n = 0;
    for await (const row of await conn.query(`SELECT sku FROM items`)) {
      if (!row[0]) {
        throw new Error('empty row');
      }
      n++;
    }
    if (n !== 2) {
      throw new Error('streamed ' + n);
    }
    const status = await conn.nodeStatus();
    if (status.role !== 'standalone' || !status.healthy) {
      throw new Error('node status ' + JSON.stringify(status));
    }
    await conn.setReadConsistency(ReadConsistency.Bounded, 5000);
    const bounded = await conn.exec(`SELECT sku FROM items WHERE sku = $1`, ['A-1']);
    if (bounded.rows.length !== 1) {
      throw new Error('bounded read mismatch');
    }
    await conn.setReadConsistency(ReadConsistency.Strong);
  } finally {
    await conn.close();
  }
}

main().catch((err: unknown) => {
  console.error(err);
  process.exit(1);
});
