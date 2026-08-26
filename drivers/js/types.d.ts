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

/** The only supported way to open a connection. Do not put keys in URLs. */
export interface Config {
  address: string;
  database?: string;
  user: string;
  password?: string;
  /** 32-byte client-held root when the server requires it. Never a URL query. */
  key?: string | Uint8Array;
  keyVersion?: number;
  tls?: TLSOptions;
  /** Plaintext is allowed only on loopback. */
  insecureNoTLS?: boolean;
}

export type Point = { lon: number; lat: number };
export type Box = { west: number; south: number; east: number; north: number };
export type LineString = { coords: number[] };
export type Polygon = { rings: number[][] };
export type VectorRef = { ref: true; dim: number };

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
};

export declare function connect(cfg: Config): Promise<Conn>;
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
