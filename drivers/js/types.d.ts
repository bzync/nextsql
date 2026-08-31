// Shared TypeScript surface for official JS-family NextSQL drivers.
// Keys and passwords are never accepted in a URL.

export interface TLSOptions {
  /** PEM or DER trust material. */
  ca?: string | Uint8Array;
  /** Deno-style extra trust anchors. */
  caCerts?: string[];
  /** SNI and certificate hostname (may differ from the TCP host). */
  servername?: string;
  /** Explicitly disable verification. Remote production must not set this. */
  rejectUnauthorized?: boolean;
}

/** How a read observes replicated state. Values match the wire byte ordering. */
export declare const ReadConsistency: {
  /** Every acknowledged write; leader only, behind a Raft read barrier. */
  readonly Strong: 0;
  /** Any member within maxStalenessMs of the leader. */
  readonly Bounded: 1;
  /** Local applied state, no freshness bound. */
  readonly Stale: 2;
};
export type ReadConsistencyMode = 0 | 1 | 2;

/** The only supported way to open a connection. Do not put keys in URLs. */
export interface Config {
  /** Single-node entry point. Required for connect(); optional when nodes is set. */
  address?: string;
  database?: string;
  user: string;
  password?: string;
  /** 32-byte client-held root when the server requires it. Never a URL query. */
  key?: string | Uint8Array;
  keyVersion?: number;
  tls?: TLSOptions;
  /** Plaintext is allowed only on loopback. */
  insecureNoTLS?: boolean;
  /** Every cluster member address, for connectCluster routing. */
  nodes?: string[];
  /** Read-consistency mode applied to routed reads / the plain connection. */
  readConsistency?: ReadConsistencyMode;
  /** Bounded-read staleness window in milliseconds. 0 selects the server default. */
  maxStalenessMs?: number;
}

/** Key-free replication health snapshot for follower-read routing. */
export interface NodeStatus {
  role: string;
  hasLeader: boolean;
  healthy: boolean;
  appliedLSN: bigint;
  lastContactMs: bigint;
  applyBacklog: bigint;
}

export type Point = { lon: number; lat: number };
export type Box = { west: number; south: number; east: number; north: number };
export type LineString = { coords: number[] };
export type Polygon = { rings: number[][] };
export type VectorRef = { ref: true; dim: number };
export type SparseVector = { dim: number; indices: number[]; values: number[] };

export type Param =
  | null
  | undefined
  | boolean
  | number
  | bigint
  | string
  | Date
  | Uint8Array
  | number[]
  | Point
  | Box
  | { kind: 'uuid'; value: string }
  | { kind: 'decimal'; value: string }
  | Record<string, unknown>;

export type Value =
  | null
  | boolean
  | string
  | number
  | Date
  | number[]
  | Point
  | Box
  | LineString
  | Polygon
  | VectorRef
  | SparseVector
  | Record<string, unknown>
  | unknown[];

export interface Column {
  name: string;
  kind: number;
  precision: number;
  scale: number;
}

export interface ExecResult {
  columns: string[];
  rows: Value[][];
  affected: number;
}

export interface Rows extends AsyncIterable<Value[]> {
  columns: string[];
  columnTypes: Column[];
  affected: number;
  next(): Promise<boolean>;
  values(): Value[] | null;
  close(): Promise<void>;
}

export interface Stmt {
  query(params?: Param[]): Promise<Rows>;
  exec(params?: Param[]): Promise<ExecResult>;
  close(): Promise<void>;
}

export interface Conn {
  query(sql: string, params?: Param[]): Promise<Rows>;
  exec(sql: string, params?: Param[]): Promise<ExecResult>;
  prepare(sql: string): Promise<Stmt>;
  cancel(): Promise<void>;
  close(): Promise<void>;
  /** Set this connection's read-consistency mode for subsequent statements. */
  setReadConsistency(mode: ReadConsistencyMode, maxStalenessMs?: number): Promise<void>;
  /** This server node's key-free replication health. */
  nodeStatus(): Promise<NodeStatus>;
}

/** Routing client over every node of a NextSQL HA cluster. */
export interface Cluster {
  query(sql: string, params?: Param[]): Promise<Rows>;
  exec(sql: string, params?: Param[]): Promise<ExecResult>;
  /** Last observed status of every reachable node. */
  nodes(): Promise<NodeStatus[]>;
  close(): Promise<void>;
}

export interface Hello {
  version: number;
  flags?: number;
  secret?: bigint;
  database?: string;
  user?: string;
}

export interface HelloOK {
  version: number;
  authMethod: number;
  secret: bigint;
}

export interface DecodeValue {
  value: Value;
  next: number;
  kind: number;
}

export declare class NextSQLError extends Error {
  readonly code: string;
  constructor(code: string, message?: string);
}

export declare const Kind: {
  readonly Invalid: 0;
  readonly UUID: 1;
  readonly String: 2;
  readonly Text: 3;
  readonly Decimal: 4;
  readonly TimestampTZ: 5;
  readonly JSON: 6;
  readonly Vector: 7;
  readonly Bool: 8;
  readonly Null: 9;
  readonly Point: 10;
  readonly Box: 11;
  readonly Line: 12;
  readonly Polygon: 13;
};

export declare const Type: {
  readonly Hello: 1;
  readonly HelloOK: 2;
  readonly Auth: 3;
  readonly AuthOK: 4;
  readonly Query: 5;
  readonly Prepare: 6;
  readonly PrepareOK: 7;
  readonly Execute: 8;
  readonly CloseStmt: 9;
  readonly CloseOK: 10;
  readonly FlowAck: 11;
  readonly Cancel: 12;
  readonly Terminate: 13;
  readonly RowDesc: 14;
  readonly DataBatch: 15;
  readonly CommandComplete: 16;
  readonly Error: 17;
  readonly Ready: 18;
  readonly Unlock: 19;
  readonly UnlockOK: 20;
  readonly IdempotentQuery: 21;
  readonly SetReadConsistency: 22;
  readonly NodeStatus: 23;
  readonly NodeStatusResp: 24;
};

export declare function connect(cfg: Config): Promise<Conn>;
export declare function connectCluster(cfg: Config): Promise<Cluster>;
export declare function validateConfig(cfg: Config): void;
export declare function isLoopback(addr: string): boolean;
export declare function encodeParam(v: Param): Uint8Array;
export declare function decodeValue(buf: Uint8Array, off: number): DecodeValue;
export declare function encodeHello(h: Hello): Uint8Array;
export declare function decodeHelloOK(b: Uint8Array): HelloOK;
export declare function encodeDecimalString(s: string): Uint8Array;
export declare function decodeDecimal(body: Uint8Array): string;
export declare function decodeNSJB(doc: Uint8Array): unknown;
export declare function formatUUID(raw: Uint8Array): string;
