// Shared NSQL v1 codec for official JS-family drivers (Node stays
// self-contained CJS; Bun and Deno import this module).
// Keys and passwords are never accepted in a URL.

const te = new TextEncoder();
const td = new TextDecoder();

export const MAGIC = te.encode('NSQL');
export const VERSION = 1;
export const HEADER = 12;
export const MAX_PACKET = 1 << 20;
export const MAX_SQL = 1 << 20;
export const MAX_NAME = 256;
export const MAX_PARAMS = 256;

export const Type = {
  Hello: 1,
  HelloOK: 2,
  Auth: 3,
  AuthOK: 4,
  Query: 5,
  Prepare: 6,
  PrepareOK: 7,
  Execute: 8,
  CloseStmt: 9,
  CloseOK: 10,
  FlowAck: 11,
  Cancel: 12,
  Terminate: 13,
  RowDesc: 14,
  DataBatch: 15,
  CommandComplete: 16,
  Error: 17,
  Ready: 18,
  Unlock: 19,
  UnlockOK: 20,
  IdempotentQuery: 21,
  SetReadConsistency: 22,
  NodeStatus: 23,
  NodeStatusResp: 24,
};

// Read-consistency mode bytes on the wire. They match the server's
// executor.ReadConsistency / replication.ReadConsistency ordering.
export const ReadConsistency = {
  Strong: 0,
  Bounded: 1,
  Stale: 2,
};

export const AuthPassword = 1;
export const AuthPasswordKey = 2;
export const FlagCancel = 1;
export const FlagNull = 0x01;

export const Kind = {
  Invalid: 0,
  UUID: 1,
  String: 2,
  Text: 3,
  Decimal: 4,
  TimestampTZ: 5,
  JSON: 6,
  Vector: 7,
  Bool: 8,
  Null: 9,
  Point: 10,
  Box: 11,
  Line: 12,
  Polygon: 13,
  Blob: 14,
  Int8: 15,
  Int16: 16,
  Int32: 17,
  Int64: 18,
  Uint8: 19,
  Uint16: 20,
  Uint32: 21,
  Uint64: 22,
};

export class NextSQLError extends Error {
  constructor(code, message) {
    super(message || code);
    this.name = 'NextSQLError';
    this.code = code;
  }
}

export function concat(parts) {
  let n = 0;
  for (const p of parts) {
    n += p.length;
  }
  const out = new Uint8Array(n);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

function view(buf, off, n) {
  return new DataView(buf.buffer, buf.byteOffset + off, n);
}

export function u16(buf, off) {
  return view(buf, off, 2).getUint16(0, true);
}

export function u32(buf, off) {
  return view(buf, off, 4).getUint32(0, true);
}

export function u64(buf, off) {
  return view(buf, off, 8).getBigUint64(0, true);
}

export function putU16(n) {
  const b = new Uint8Array(2);
  new DataView(b.buffer).setUint16(0, n & 0xffff, true);
  return b;
}

export function putU32(n) {
  const b = new Uint8Array(4);
  new DataView(b.buffer).setUint32(0, n >>> 0, true);
  return b;
}

export function putU64(n) {
  const b = new Uint8Array(8);
  new DataView(b.buffer).setBigUint64(0, BigInt(n), true);
  return b;
}

export function putF32(n) {
  const b = new Uint8Array(4);
  new DataView(b.buffer).setFloat32(0, n, true);
  return b;
}

export function putF64(n) {
  const b = new Uint8Array(8);
  new DataView(b.buffer).setFloat64(0, n, true);
  return b;
}

export function toBytes(v) {
  if (v instanceof Uint8Array) {
    return v;
  }
  if (typeof v === 'string') {
    return te.encode(v);
  }
  if (v && v.buffer instanceof ArrayBuffer) {
    return new Uint8Array(v.buffer, v.byteOffset, v.byteLength);
  }
  throw new NextSQLError('invalid_argument', 'expected bytes');
}

export function toHex(buf) {
  let s = '';
  for (let i = 0; i < buf.length; i++) {
    s += buf[i].toString(16).padStart(2, '0');
  }
  return s;
}

export function fromHex(hex) {
  hex = String(hex);
  if (hex.length % 2) {
    throw new NextSQLError('invalid_argument', 'odd hex length');
  }
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

export function splitHostPort(addr) {
  if (typeof addr !== 'string' || addr.length === 0) {
    throw new NextSQLError('invalid_argument', 'address is required');
  }
  if (addr.startsWith('[')) {
    const end = addr.indexOf(']');
    if (end < 0) {
      throw new NextSQLError('invalid_argument', 'invalid address');
    }
    const host = addr.slice(1, end);
    if (addr[end + 1] === ':') {
      return { host, port: Number(addr.slice(end + 2)) };
    }
    throw new NextSQLError('invalid_argument', 'address requires a port');
  }
  const i = addr.lastIndexOf(':');
  if (i < 0) {
    throw new NextSQLError('invalid_argument', 'address requires a port');
  }
  return { host: addr.slice(0, i), port: Number(addr.slice(i + 1)) };
}

export function isLoopback(addr) {
  let host = addr;
  try {
    host = splitHostPort(addr).host;
  } catch {
    // bare host
  }
  host = String(host).trim().toLowerCase();
  if (host === 'localhost') {
    return true;
  }
  if (host === '::1' || host === '0:0:0:0:0:0:0:1') {
    return true;
  }
  return /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host);
}

export function validateConfig(cfg) {
  if (!cfg || typeof cfg !== 'object') {
    throw new NextSQLError('invalid_argument', 'config is required');
  }
  if (!cfg.address) {
    throw new NextSQLError('invalid_argument', 'address is required');
  }
  const addr = String(cfg.address).toLowerCase();
  if (addr.includes('://') || addr.includes('key=') || addr.includes('password=')) {
    throw new NextSQLError('invalid_argument', 'keys and credentials must not be passed in a URL');
  }
  if (!cfg.tls && !cfg.insecureNoTLS) {
    throw new NextSQLError('invalid_argument', 'TLS is required for remote connections');
  }
  if (cfg.insecureNoTLS && !isLoopback(cfg.address)) {
    throw new NextSQLError('invalid_argument', 'plaintext is only allowed on loopback');
  }
  if (!cfg.user) {
    throw new NextSQLError('invalid_argument', 'user is required');
  }
}

export function appendU16String(s, max) {
  const buf = te.encode(String(s));
  if (buf.length > max || buf.length > 0xffff) {
    throw new NextSQLError('protocol', 'string exceeds limit');
  }
  return concat([putU16(buf.length), buf]);
}

export function appendU32Bytes(buf, max) {
  if (buf.length > max) {
    throw new NextSQLError('protocol', 'bytes exceed limit');
  }
  return concat([putU32(buf.length), buf]);
}

export function readU16String(buf, off, max) {
  if (off + 2 > buf.length) {
    throw new NextSQLError('protocol', 'truncated string length');
  }
  const n = u16(buf, off);
  if (n > max) {
    throw new NextSQLError('protocol', 'string exceeds limit');
  }
  if (off + 2 + n > buf.length) {
    throw new NextSQLError('protocol', 'truncated string');
  }
  return { value: td.decode(buf.subarray(off + 2, off + 2 + n)), next: off + 2 + n };
}

export function readU32Bytes(buf, off, max) {
  if (off + 4 > buf.length) {
    throw new NextSQLError('protocol', 'truncated bytes length');
  }
  const n = u32(buf, off);
  if (n > max) {
    throw new NextSQLError('protocol', 'bytes exceed limit');
  }
  if (off + 4 + n > buf.length) {
    throw new NextSQLError('protocol', 'truncated bytes');
  }
  return { value: buf.subarray(off + 4, off + 4 + n), next: off + 4 + n };
}

function bigintToBytes(n) {
  if (n === 0n) {
    return new Uint8Array(0);
  }
  let hex = n.toString(16);
  if (hex.length % 2) {
    hex = '0' + hex;
  }
  return fromHex(hex);
}

function bytesToBigint(buf) {
  let n = 0n;
  for (const b of buf) {
    n = (n << 8n) + BigInt(b);
  }
  return n;
}

export function encodeDecimalString(s) {
  s = String(s).trim();
  let neg = false;
  if (s.startsWith('+')) {
    s = s.slice(1);
  }
  if (s.startsWith('-')) {
    neg = true;
    s = s.slice(1);
  }
  if (!/^\d+(\.\d+)?$/.test(s)) {
    throw new NextSQLError('invalid_argument', 'invalid decimal');
  }
  const parts = s.split('.');
  const scale = parts[1] ? parts[1].length : 0;
  const digits = (parts[0] + (parts[1] || '')).replace(/^0+(?=\d)/, '');
  const coef = bigintToBytes(BigInt(digits));
  const body = new Uint8Array(4 + coef.length);
  body[0] = neg ? 1 : 0;
  body[2] = scale & 0xff;
  body[3] = (scale >>> 8) & 0xff;
  body.set(coef, 4);
  return appendU32Bytes(body, MAX_PACKET);
}

export function decodeDecimal(body) {
  if (body.length < 4) {
    throw new NextSQLError('invalid_format', 'truncated decimal');
  }
  const neg = (body[0] & 1) !== 0;
  const scale = u16(body, 2);
  let s = bytesToBigint(body.subarray(4)).toString();
  if (scale > 0) {
    if (s.length <= scale) {
      s = s.padStart(scale + 1, '0');
    }
    s = s.slice(0, s.length - scale) + '.' + s.slice(s.length - scale);
  }
  if (neg && s !== '0' && s !== '0.' + '0'.repeat(scale)) {
    s = '-' + s;
  }
  return s;
}

export function formatUUID(raw) {
  const h = toHex(raw);
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

export function parseUUID(s) {
  const hex = String(s).trim().replace(/-/g, '');
  if (!/^[0-9a-fA-F]{32}$/.test(hex)) {
    throw new NextSQLError('invalid_argument', 'invalid UUID');
  }
  return fromHex(hex);
}

export function decodeNSJB(doc) {
  if (doc.length < 5 || td.decode(doc.subarray(0, 4)) !== 'NSJB' || doc[4] !== 1) {
    throw new NextSQLError('invalid_format', 'not binary JSON');
  }
  const { value, next } = readNSJB(doc, 5);
  if (next !== doc.length) {
    throw new NextSQLError('invalid_format', 'trailing JSON bytes');
  }
  return value;
}

function readNSJB(b, off) {
  if (off >= b.length) {
    throw new NextSQLError('invalid_format', 'truncated JSON');
  }
  switch (b[off]) {
    case 0x00:
      return { value: null, next: off + 1 };
    case 0x01:
      return { value: false, next: off + 1 };
    case 0x02:
      return { value: true, next: off + 1 };
    case 0x03:
      if (off + 9 > b.length) {
        throw new NextSQLError('invalid_format', 'truncated i64');
      }
      return { value: Number(view(b, off + 1, 8).getBigInt64(0, true)), next: off + 9 };
    case 0x04:
    case 0x05: {
      const n = u32(b, off + 1);
      const end = off + 5 + n;
      if (end > b.length) {
        throw new NextSQLError('invalid_format', 'truncated JSON string');
      }
      const s = td.decode(b.subarray(off + 5, end));
      if (b[off] === 0x05) {
        const num = Number(s);
        return { value: Number.isFinite(num) ? num : s, next: end };
      }
      return { value: s, next: end };
    }
    case 0x06: {
      const size = u32(b, off + 1);
      const body = off + 5;
      const end = body + size;
      const count = u32(b, body);
      let cur = body + 4;
      const arr = [];
      for (let i = 0; i < count; i++) {
        const got = readNSJB(b, cur);
        arr.push(got.value);
        cur = got.next;
      }
      if (cur !== end) {
        throw new NextSQLError('invalid_format', 'array size mismatch');
      }
      return { value: arr, next: end };
    }
    case 0x07: {
      const size = u32(b, off + 1);
      const body = off + 5;
      const end = body + size;
      const count = u16(b, body);
      let cur = body + 2;
      const obj = {};
      for (let i = 0; i < count; i++) {
        const klen = u16(b, cur);
        cur += 2;
        const key = td.decode(b.subarray(cur, cur + klen));
        cur += klen;
        const got = readNSJB(b, cur);
        obj[key] = got.value;
        cur = got.next;
      }
      if (cur !== end) {
        throw new NextSQLError('invalid_format', 'object size mismatch');
      }
      return { value: obj, next: end };
    }
    default:
      throw new NextSQLError('invalid_format', 'unknown JSON tag');
  }
}

function encodeTypeMeta(kind, prec, scale, elem) {
  const meta = new Uint8Array(5);
  meta[0] = (prec || 0) & 0xff;
  meta[1] = ((prec || 0) >>> 8) & 0xff;
  meta[2] = (scale || 0) & 0xff;
  meta[3] = ((scale || 0) >>> 8) & 0xff;
  meta[4] = elem || 0;
  return concat([Uint8Array.of(kind, 0), meta]);
}

function encodeNull() {
  return concat([Uint8Array.of(Kind.String, FlagNull), new Uint8Array(5)]);
}

function encodeBool(v) {
  return concat([encodeTypeMeta(Kind.Bool, 0, 0, 0), Uint8Array.of(v ? 1 : 0)]);
}

function encodeString(s) {
  const raw = te.encode(String(s));
  return concat([Uint8Array.of(Kind.String, 0), new Uint8Array(5), appendU32Bytes(raw, MAX_PACKET)]);
}

function encodeUUID(raw) {
  if (raw.length !== 16) {
    throw new NextSQLError('invalid_argument', 'UUID must be 16 bytes');
  }
  return concat([Uint8Array.of(Kind.UUID, 0), new Uint8Array(5), raw]);
}

function encodeBlob(raw) {
  return concat([Uint8Array.of(Kind.Blob, 0), new Uint8Array(5), appendU32Bytes(raw, MAX_PACKET)]);
}

const INT_RANGES = {
  int8: [-0x80n, 0x7fn, Kind.Int8, 1],
  int16: [-0x8000n, 0x7fffn, Kind.Int16, 2],
  int32: [-0x80000000n, 0x7fffffffn, Kind.Int32, 4],
  int64: [-0x8000000000000000n, 0x7fffffffffffffffn, Kind.Int64, 8],
};

// encodeInt builds an explicit fixed-width int parameter (D2, Datatype
// expansion track). A bare JS number/bigint still defaults to Kind.Decimal
// (see encodeParam) and coerces server-side into any numeric column, so
// this wrapper is only needed to pin an exact wire width.
function encodeInt(which, value) {
  const range = INT_RANGES[which];
  if (!range) {
    throw new NextSQLError('invalid_argument', 'unknown int kind: ' + which);
  }
  const [lo, hi, kind, width] = range;
  const n = BigInt(value);
  if (n < lo || n > hi) {
    throw new NextSQLError('invalid_argument', which + ' out of range');
  }
  // Little-endian: the low `width` bytes of the 8-byte encoding are exactly
  // the narrow two's-complement encoding for any value in range.
  const full = new Uint8Array(8);
  new DataView(full.buffer).setBigInt64(0, n, true);
  return concat([Uint8Array.of(kind, 0), new Uint8Array(5), full.subarray(0, width)]);
}

const UINT_RANGES = {
  uint8: [0xffn, Kind.Uint8, 1],
  uint16: [0xffffn, Kind.Uint16, 2],
  uint32: [0xffffffffn, Kind.Uint32, 4],
  uint64: [0xffffffffffffffffn, Kind.Uint64, 8],
};

// encodeUint builds an explicit fixed-width unsigned int parameter (D3,
// Datatype expansion track). Mirrors encodeInt; a bare JS number/bigint
// still defaults to Kind.Decimal (see encodeParam).
function encodeUint(which, value) {
  const range = UINT_RANGES[which];
  if (!range) {
    throw new NextSQLError('invalid_argument', 'unknown uint kind: ' + which);
  }
  const [hi, kind, width] = range;
  const n = BigInt(value);
  if (n < 0n || n > hi) {
    throw new NextSQLError('invalid_argument', which + ' out of range');
  }
  const full = new Uint8Array(8);
  new DataView(full.buffer).setBigUint64(0, n, true);
  return concat([Uint8Array.of(kind, 0), new Uint8Array(5), full.subarray(0, width)]);
}

function encodeTimestamp(ns) {
  return concat([Uint8Array.of(Kind.TimestampTZ, 0), new Uint8Array(5), putU64(ns)]);
}

function encodePoint(lon, lat) {
  return concat([Uint8Array.of(Kind.Point, 0), new Uint8Array(5), putF64(lon), putF64(lat)]);
}

function encodeBox(w, s, e, n) {
  return concat([Uint8Array.of(Kind.Box, 0), new Uint8Array(5), putF64(w), putF64(s), putF64(e), putF64(n)]);
}

function encodeVector(arr) {
  const dim = arr.length;
  const body = new Uint8Array(3 + dim * 4);
  body[0] = dim & 0xff;
  body[1] = (dim >>> 8) & 0xff;
  body[2] = 0;
  for (let i = 0; i < dim; i++) {
    const f = arr[i];
    if (!Number.isFinite(f)) {
      throw new NextSQLError('invalid_argument', 'VECTOR element is not finite');
    }
    body.set(putF32(f), 3 + i * 4);
  }
  const hdr = new Uint8Array(7);
  hdr[0] = Kind.Vector;
  hdr[2] = dim & 0xff;
  hdr[3] = (dim >>> 8) & 0xff;
  hdr[6] = 1; // VecF32
  return concat([hdr, body]);
}

export function encodeParam(v) {
  if (v === null || v === undefined) {
    return encodeNull();
  }
  if (typeof v === 'boolean') {
    return encodeBool(v);
  }
  if (typeof v === 'number') {
    if (!Number.isFinite(v)) {
      throw new NextSQLError('invalid_argument', 'parameter is not finite');
    }
    return concat([Uint8Array.of(Kind.Decimal, 0), new Uint8Array(5), encodeDecimalString(String(v))]);
  }
  if (typeof v === 'bigint') {
    return concat([Uint8Array.of(Kind.Decimal, 0), new Uint8Array(5), encodeDecimalString(v.toString())]);
  }
  if (typeof v === 'string') {
    return encodeString(v);
  }
  if (v instanceof Date) {
    return encodeTimestamp(BigInt(v.getTime()) * 1000000n);
  }
  if (v instanceof Uint8Array) {
    // A bare 16-byte Uint8Array keeps its pre-existing meaning (UUID) for
    // compatibility; any other length is a BLOB. A 16-byte BLOB needs the
    // explicit { kind: 'blob', value } wrapper below to disambiguate.
    return v.length === 16 ? encodeUUID(v) : encodeBlob(v);
  }
  if (Array.isArray(v) && v.length > 0 && v.every((x) => typeof x === 'number')) {
    return encodeVector(v);
  }
  if (v && typeof v === 'object') {
    if ('lon' in v && 'lat' in v) {
      return encodePoint(Number(v.lon), Number(v.lat));
    }
    if ('west' in v && 'south' in v && 'east' in v && 'north' in v) {
      return encodeBox(Number(v.west), Number(v.south), Number(v.east), Number(v.north));
    }
    if (v.kind === 'uuid') {
      return encodeUUID(parseUUID(v.value));
    }
    if (v.kind === 'decimal') {
      return concat([Uint8Array.of(Kind.Decimal, 0), new Uint8Array(5), encodeDecimalString(v.value)]);
    }
    if (v.kind === 'blob') {
      return encodeBlob(v.value instanceof Uint8Array ? v.value : Uint8Array.from(v.value));
    }
    if (v.kind === 'int8' || v.kind === 'int16' || v.kind === 'int32' || v.kind === 'int64') {
      return encodeInt(v.kind, v.value);
    }
    if (v.kind === 'uint8' || v.kind === 'uint16' || v.kind === 'uint32' || v.kind === 'uint64') {
      return encodeUint(v.kind, v.value);
    }
    return encodeString(JSON.stringify(v));
  }
  throw new NextSQLError('invalid_argument', 'unsupported parameter type');
}

export function decodeValue(buf, off) {
  if (off + 7 > buf.length) {
    throw new NextSQLError('protocol', 'truncated value header');
  }
  const kind = buf[off];
  const flags = buf[off + 1];
  off += 7;
  if (flags & FlagNull) {
    return { value: null, next: off, kind };
  }
  switch (kind) {
    case Kind.UUID: {
      if (off + 16 > buf.length) {
        throw new NextSQLError('protocol', 'truncated UUID');
      }
      return { value: formatUUID(buf.subarray(off, off + 16)), next: off + 16, kind };
    }
    case Kind.String:
    case Kind.Text: {
      const got = readU32Bytes(buf, off, MAX_PACKET);
      return { value: td.decode(got.value), next: got.next, kind };
    }
    case Kind.Blob: {
      const got = readU32Bytes(buf, off, MAX_PACKET);
      return { value: got.value, next: got.next, kind };
    }
    case Kind.JSON: {
      const got = readU32Bytes(buf, off, MAX_PACKET);
      return { value: decodeNSJB(got.value), next: got.next, kind };
    }
    case Kind.Decimal: {
      const got = readU32Bytes(buf, off, MAX_PACKET);
      return { value: decodeDecimal(got.value), next: got.next, kind };
    }
    case Kind.TimestampTZ: {
      if (off + 8 > buf.length) {
        throw new NextSQLError('protocol', 'truncated timestamp');
      }
      const ns = view(buf, off, 8).getBigInt64(0, true);
      return { value: new Date(Number(ns / 1000000n)), next: off + 8, kind };
    }
    case Kind.Bool: {
      if (off >= buf.length) {
        throw new NextSQLError('protocol', 'truncated bool');
      }
      return { value: buf[off] !== 0, next: off + 1, kind };
    }
    case Kind.Int8: {
      if (off + 1 > buf.length) {
        throw new NextSQLError('protocol', 'truncated int8');
      }
      const raw = buf[off];
      return { value: raw >= 0x80 ? raw - 0x100 : raw, next: off + 1, kind };
    }
    case Kind.Int16: {
      if (off + 2 > buf.length) {
        throw new NextSQLError('protocol', 'truncated int16');
      }
      return { value: view(buf, off, 2).getInt16(0, true), next: off + 2, kind };
    }
    case Kind.Int32: {
      if (off + 4 > buf.length) {
        throw new NextSQLError('protocol', 'truncated int32');
      }
      return { value: view(buf, off, 4).getInt32(0, true), next: off + 4, kind };
    }
    case Kind.Int64: {
      if (off + 8 > buf.length) {
        throw new NextSQLError('protocol', 'truncated int64');
      }
      // Exposed as BigInt, not number: the full int64 range does not fit
      // safely in a JS double (see docs/design-datatypes.md D2).
      return { value: view(buf, off, 8).getBigInt64(0, true), next: off + 8, kind };
    }
    case Kind.Uint8: {
      if (off + 1 > buf.length) {
        throw new NextSQLError('protocol', 'truncated uint8');
      }
      return { value: buf[off], next: off + 1, kind };
    }
    case Kind.Uint16: {
      if (off + 2 > buf.length) {
        throw new NextSQLError('protocol', 'truncated uint16');
      }
      return { value: view(buf, off, 2).getUint16(0, true), next: off + 2, kind };
    }
    case Kind.Uint32: {
      if (off + 4 > buf.length) {
        throw new NextSQLError('protocol', 'truncated uint32');
      }
      return { value: view(buf, off, 4).getUint32(0, true), next: off + 4, kind };
    }
    case Kind.Uint64: {
      if (off + 8 > buf.length) {
        throw new NextSQLError('protocol', 'truncated uint64');
      }
      // Exposed as BigInt, not number: the full uint64 range does not fit
      // safely in a JS double (see docs/design-datatypes.md D3).
      return { value: view(buf, off, 8).getBigUint64(0, true), next: off + 8, kind };
    }
    case Kind.Vector: {
      const dim = u16(buf, off);
      const flag = buf[off + 2];
      if (flag & 1) {
        return { value: { ref: true, dim }, next: off + 3, kind };
      }
      if (flag & 2) {
        const nnz = view(buf, off + 3, 4).getUint32(0, true);
        const indices = [];
        const values = [];
        for (let i = 0; i < nnz; i++) {
          indices.push(view(buf, off + 7 + i * 8, 4).getUint32(0, true));
          values.push(view(buf, off + 11 + i * 8, 4).getFloat32(0, true));
        }
        return { value: { dim, indices, values }, next: off + 7 + nnz * 8, kind };
      }
      const need = dim * 4;
      const out = [];
      for (let i = 0; i < dim; i++) {
        out.push(view(buf, off + 3 + i * 4, 4).getFloat32(0, true));
      }
      return { value: out, next: off + 3 + need, kind };
    }
    case Kind.Point: {
      return {
        value: {
          lon: view(buf, off, 8).getFloat64(0, true),
          lat: view(buf, off + 8, 8).getFloat64(0, true),
        },
        next: off + 16,
        kind,
      };
    }
    case Kind.Box: {
      return {
        value: {
          west: view(buf, off, 8).getFloat64(0, true),
          south: view(buf, off + 8, 8).getFloat64(0, true),
          east: view(buf, off + 16, 8).getFloat64(0, true),
          north: view(buf, off + 24, 8).getFloat64(0, true),
        },
        next: off + 32,
        kind,
      };
    }
    case Kind.Line: {
      const n = u16(buf, off);
      const coords = [];
      let p = off + 2;
      for (let i = 0; i < n * 2; i++) {
        coords.push(view(buf, p, 8).getFloat64(0, true));
        p += 8;
      }
      return { value: { coords }, next: p, kind };
    }
    case Kind.Polygon: {
      const nr = u16(buf, off);
      let p = off + 2;
      const rings = [];
      for (let r = 0; r < nr; r++) {
        const np = u16(buf, p);
        p += 2;
        const ring = [];
        for (let i = 0; i < np * 2; i++) {
          ring.push(view(buf, p, 8).getFloat64(0, true));
          p += 8;
        }
        rings.push(ring);
      }
      return { value: { rings }, next: p, kind };
    }
    default:
      throw new NextSQLError('protocol', 'unsupported type');
  }
}

export function encodeHello(h) {
  const parts = [
    putU16(h.version),
    putU16(h.flags || 0),
    putU64(h.secret || 0n),
    appendU16String(h.database || '', MAX_NAME),
    appendU16String(h.user || '', MAX_NAME),
  ];
  // Realm is an optional trailing field (M2-2): emitted only when
  // selected, so a Hello with no realm is byte-identical to the pre-realm
  // wire shape.
  if (h.realm) {
    parts.push(appendU16String(h.realm, MAX_NAME));
  }
  return concat(parts);
}

export function decodeHelloOK(b) {
  if (b.length !== 11) {
    throw new NextSQLError('protocol', 'bad hello-ok length');
  }
  return { version: u16(b, 0), authMethod: b[2], secret: u64(b, 3) };
}

export function encodeAuth(password) {
  return appendU16String(password || '', MAX_NAME);
}

export function encodeQuery(sql, params) {
  if (params.length > MAX_PARAMS) {
    throw new NextSQLError('protocol', 'too many parameters');
  }
  const sqlBuf = te.encode(sql);
  if (sqlBuf.length > MAX_SQL) {
    throw new NextSQLError('protocol', 'SQL exceeds limit');
  }
  const parts = [appendU32Bytes(sqlBuf, MAX_SQL), putU16(params.length)];
  for (const p of params) {
    parts.push(appendU16String('', MAX_NAME));
    parts.push(encodeParam(p));
  }
  return concat(parts);
}

export function encodePrepare(sql) {
  return appendU32Bytes(te.encode(sql), MAX_SQL);
}

export function encodeExecute(id, params) {
  if (params.length > MAX_PARAMS) {
    throw new NextSQLError('protocol', 'too many parameters');
  }
  const parts = [putU32(id), putU16(params.length)];
  for (const p of params) {
    parts.push(appendU16String('', MAX_NAME));
    parts.push(encodeParam(p));
  }
  return concat(parts);
}

export function decodeRowDesc(b) {
  const n = u16(b, 0);
  let off = 2;
  const columns = [];
  for (let i = 0; i < n; i++) {
    const name = readU16String(b, off, MAX_NAME);
    off = name.next;
    if (off + 6 > b.length) {
      throw new NextSQLError('protocol', 'truncated type');
    }
    columns.push({
      name: name.value,
      kind: b[off],
      precision: u16(b, off + 1),
      scale: u16(b, off + 3),
    });
    off += 6;
  }
  return columns;
}

export function decodeDataBatch(b) {
  const nrows = u32(b, 0);
  let off = 4;
  const rows = [];
  for (let i = 0; i < nrows; i++) {
    const ncols = u16(b, off);
    off += 2;
    const row = [];
    for (let j = 0; j < ncols; j++) {
      const got = decodeValue(b, off);
      row.push(got.value);
      off = got.next;
    }
    rows.push(row);
  }
  return rows;
}

export function decodeError(b) {
  const code = readU16String(b, 0, MAX_NAME);
  const msg = readU16String(b, code.next, MAX_NAME);
  return new NextSQLError(code.value, msg.value);
}

export function decodeCommandComplete(b) {
  if (b.length !== 8) {
    throw new NextSQLError('protocol', 'bad command-complete length');
  }
  return Number(view(b, 0, 8).getBigUint64(0, true));
}

export function encodeSetReadConsistency(mode, maxStalenessMs) {
  if (mode !== ReadConsistency.Strong && mode !== ReadConsistency.Bounded && mode !== ReadConsistency.Stale) {
    throw new NextSQLError('invalid_argument', 'unknown read consistency mode');
  }
  let ms = 0;
  if (maxStalenessMs && maxStalenessMs > 0) {
    // Keep a sub-millisecond bound positive so the server does not read it as
    // "use the default window" (0). Real staleness bounds are seconds.
    ms = Math.floor(maxStalenessMs);
    if (ms === 0) {
      ms = 1;
    }
  }
  const buf = new Uint8Array(9);
  buf[0] = mode;
  new DataView(buf.buffer).setBigUint64(1, BigInt(ms), true);
  return buf;
}

export function decodeNodeStatus(b) {
  const role = readU16String(b, 0, MAX_NAME);
  const off = role.next;
  if (b.length - off !== 25) {
    throw new NextSQLError('protocol', 'bad node-status length');
  }
  const flags = b[off];
  const dv = view(b, off + 1, 24);
  return {
    role: role.value,
    hasLeader: (flags & 1) !== 0,
    healthy: (flags & 2) !== 0,
    appliedLSN: dv.getBigUint64(0, true),
    lastContactMs: dv.getBigInt64(8, true),
    applyBacklog: dv.getBigUint64(16, true),
  };
}

export function encodeFrame(typ, payload) {
  payload = payload || new Uint8Array(0);
  if (payload.length > MAX_PACKET) {
    throw new NextSQLError('protocol', 'payload exceeds packet limit');
  }
  const hdr = new Uint8Array(HEADER);
  hdr.set(MAGIC, 0);
  hdr[4] = VERSION & 0xff;
  hdr[5] = (VERSION >>> 8) & 0xff;
  hdr[6] = typ;
  hdr[8] = payload.length & 0xff;
  hdr[9] = (payload.length >>> 8) & 0xff;
  hdr[10] = (payload.length >>> 16) & 0xff;
  hdr[11] = (payload.length >>> 24) & 0xff;
  return concat([hdr, payload]);
}

export function decodeFrameHeader(hdr) {
  if (hdr.length < HEADER) {
    throw new NextSQLError('protocol', 'truncated header');
  }
  if (td.decode(hdr.subarray(0, 4)) !== 'NSQL') {
    throw new NextSQLError('protocol', 'bad magic');
  }
  if (u16(hdr, 4) !== VERSION) {
    throw new NextSQLError('protocol', 'unsupported protocol version');
  }
  const typ = hdr[6];
  if (typ === 0) {
    throw new NextSQLError('protocol', 'invalid message type');
  }
  const n = u32(hdr, 8);
  if (n > MAX_PACKET) {
    throw new NextSQLError('protocol', 'packet exceeds limit');
  }
  return { type: typ, length: n };
}
