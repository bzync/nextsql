// Type-only usage fixture for the official Bun TypeScript entry.
import {
  Kind,
  NextSQLError,
  connect,
  encodeParam,
  isLoopback,
  validateConfig,
  type Config,
  type Param,
} from './nextsql.js';

const cfg: Config = {
  address: '127.0.0.1:7210',
  user: 'app',
  insecureNoTLS: true,
};

validateConfig(cfg);
const _loop: boolean = isLoopback(cfg.address);
const _kind: number = Kind.String;
const _p: Param = { lon: -73.98, lat: 40.75 };
const _enc: Uint8Array = encodeParam(_p);

export async function openLoopback(): Promise<void> {
  const conn = await connect(cfg);
  const rows = await conn.query('SELECT 1');
  for await (const row of rows) {
    void row[0];
  }
  await conn.close();
}

export function expectTypedError(): NextSQLError {
  return new NextSQLError('invalid_argument', 'keys and credentials must not be passed in a URL');
}

void _loop;
void _kind;
void _enc;
