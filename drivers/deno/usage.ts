// Type-only usage fixture for the official Deno TypeScript entry.
import {
  Kind,
  NextSQLError,
  ReadConsistency,
  connect,
  connectCluster,
  encodeParam,
  isLoopback,
  validateConfig,
  type Cluster,
  type Config,
  type NodeStatus,
  type Param,
} from './mod.ts';

const cfg: Config = {
  address: '127.0.0.1:7210',
  user: 'app',
  insecureNoTLS: true,
};

export async function routedReads(): Promise<void> {
  const cluster: Cluster = await connectCluster({
    nodes: ['127.0.0.1:7210', '127.0.0.1:7211', '127.0.0.1:7212'],
    user: 'app',
    insecureNoTLS: true,
    readConsistency: ReadConsistency.Bounded,
    maxStalenessMs: 5000,
  });
  const health: NodeStatus[] = await cluster.nodes();
  void health;
  const res = await cluster.exec('SELECT sku FROM items');
  void res.rows;
  await cluster.close();
}

validateConfig(cfg);
const _loop: boolean = isLoopback(cfg.address ?? "");
const _kind: number = Kind.Point;
const _p: Param = { kind: 'uuid', value: '00112233-4455-6677-8899-aabbccddeeff' };
const _enc: Uint8Array = encodeParam(_p);

export async function openLoopback(): Promise<void> {
  const conn = await connect(cfg);
  const st = await conn.prepare('SELECT sku FROM items WHERE sku = $1');
  const res = await st.exec(['A-1']);
  void res.rows;
  await st.close();
  await conn.close();
}

export function expectTypedError(): NextSQLError {
  return new NextSQLError('invalid_argument', 'keys and credentials must not be passed in a URL');
}

void _loop;
void _kind;
void _enc;
