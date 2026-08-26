// Official TypeScript entry for the Deno driver. Speaks NSQL v1.
// Encryption keys and passwords are never accepted in a URL.

export {
  Kind,
  NextSQLError,
  Type,
  connect,
  decodeDecimal,
  decodeHelloOK,
  decodeNSJB,
  decodeValue,
  encodeDecimalString,
  encodeHello,
  encodeParam,
  formatUUID,
  isLoopback,
  validateConfig,
} from './mod.js';

export type {
  Box,
  Column,
  Config,
  Conn,
  DecodeValue,
  ExecResult,
  Hello,
  HelloOK,
  LineString,
  Param,
  Point,
  Polygon,
  Rows,
  Stmt,
  TLSOptions,
  Value,
  VectorRef,
} from '../js/types.d.ts';
