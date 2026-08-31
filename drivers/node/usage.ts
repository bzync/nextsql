// Type-only usage fixture for the official Node driver.
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
} from './nextsql';

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
  const res = await cluster.exec('SELECT 1');
  void res.affected;
  await cluster.close();
}

const cfg: Config = {
  address: '127.0.0.1:7210',
  user: 'app',
  insecureNoTLS: true,
};

validateConfig(cfg);
const _loop: boolean = isLoopback(cfg.address ?? "");
const _kind: number = Kind.String;
const _p: Param = { kind: 'decimal', value: '1.5' };
const _enc: Uint8Array = encodeParam(_p);

export async function openLoopback(): Promise<void> {
  const conn = await connect(cfg);
  const res = await conn.exec('SELECT 1');
  const _n: number = res.affected;
  await conn.close();
}

export function expectTypedError(): NextSQLError {
  return new NextSQLError('invalid_argument', 'keys and credentials must not be passed in a URL');
}

void _loop;
void _kind;
void _enc;
