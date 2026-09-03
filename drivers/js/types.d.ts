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
  /** Selects which hosted realm this connection targets (M2-2). Optional:
   * an unset realm sends the exact pre-realm Hello and connects to the
   * server's configured default. */
  realm?: string;
  user: string;
  password?: string;
  /** 32-byte client-held root when the server requires it. Never a URL query. */
  key?: string | Uint8Array;
  keyVersion?: number;
  /** Client-only provider for ENCRYPTED CLIENT field keys. Never sent over NSQL. */
  fieldKeys?: FieldKeyProvider;
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

export interface FieldKey {
  /** Public NSCE1 key identifier (1..64 ASCII letters/digits/._-). */
  id: string;
  /** Exactly 32 bytes of AES-256 key material. */
  material: Uint8Array;
}

export interface FieldKeyProvider {
  currentFieldKey(database: string, table: string, column: string): FieldKey | Promise<FieldKey>;
  fieldKey(database: string, table: string, column: string, keyID: string): FieldKey | Promise<FieldKey>;
}

export interface FieldTypeDescriptor {
  kind: number;
  precision: number;
  scale: number;
  vecElem: number;
}

export declare const FieldType: {
  readonly UUID: FieldTypeDescriptor;
  readonly String: FieldTypeDescriptor;
  readonly Text: FieldTypeDescriptor;
  readonly TimestampTZ: FieldTypeDescriptor;
  readonly JSON: FieldTypeDescriptor;
  readonly Bool: FieldTypeDescriptor;
  Decimal(precision: number, scale: number): FieldTypeDescriptor;
};

export declare class MemoryFieldKeyring implements FieldKeyProvider {
  constructor(current: FieldKey, ...overlap: FieldKey[]);
  currentFieldKey(database: string, table: string, column: string): Promise<FieldKey>;
  fieldKey(database: string, table: string, column: string, keyID: string): Promise<FieldKey>;
  rotate(key: FieldKey): void;
  revoke(keyID: string): void;
}

/** Material-free summary of one FileFieldKeyring record. */
export interface FieldKeyInfo {
  id: string;
  /** Unix seconds. */
  created: number;
  current: boolean;
  revoked: boolean;
}

/**
 * Durable, atomic, file-backed FieldKeyProvider. Unlike MemoryFieldKeyring,
 * rotation and revocation persist across process restarts in the versioned
 * NSFK1 on-disk format (see docs/client-encryption.md). A revoked key's
 * material is overwritten with zeros on disk and its id can never be reused.
 * Production applications with an existing secret manager or KMS should
 * still prefer implementing FieldKeyProvider directly against that system.
 */
export declare class FileFieldKeyring implements FieldKeyProvider {
  private constructor();
  static create(path: string, current: FieldKey): Promise<FileFieldKeyring>;
  static open(path: string): Promise<FileFieldKeyring>;
  readonly path: string;
  currentFieldKey(database: string, table: string, column: string): Promise<FieldKey>;
  fieldKey(database: string, table: string, column: string, keyID: string): Promise<FieldKey>;
  rotate(key: FieldKey): Promise<void>;
  revoke(keyID: string): Promise<void>;
  /** Re-reads the keyring file; on error the in-memory keyring is unchanged. */
  reload(): Promise<void>;
  list(): FieldKeyInfo[];
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
  /** Seal a logical value into an opaque NSCE1 STRING; null passes through. */
  encryptField(table: string, column: string, type: FieldTypeDescriptor, value: unknown): Promise<string | null>;
  /** Authenticate and decode an opaque NSCE1 STRING; null passes through. */
  decryptField(table: string, column: string, type: FieldTypeDescriptor, ciphertext: string | null): Promise<unknown>;
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
  realm?: string;
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
  readonly Blob: 14;
  readonly Int8: 15;
  readonly Int16: 16;
  readonly Int32: 17;
  readonly Int64: 18;
  readonly Uint8: 19;
  readonly Uint16: 20;
  readonly Uint32: 21;
  readonly Uint64: 22;
  readonly Date: 23;
  readonly Time: 24;
  readonly Char: 25;
  readonly Varchar: 26;
  readonly Timestamp: 27;
  readonly Float32: 28;
  readonly Float64: 29;
  readonly Enum: 30;
  readonly Interval: 31;
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
export declare function generateFieldKey(id: string): FieldKey;
export declare function inspectField(ciphertext: string): { keyID: string; type: FieldTypeDescriptor };
export declare function encryptField(provider: FieldKeyProvider, database: string, table: string, column: string, type: FieldTypeDescriptor, value: unknown): Promise<string | null>;
export declare function decryptField(provider: FieldKeyProvider, database: string, table: string, column: string, type: FieldTypeDescriptor, ciphertext: string | null): Promise<unknown>;
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
