'use strict';

// Official NextSQL Node.js driver. Speak the native NSQL v1 protocol.
// Encryption keys and passwords are never accepted in a URL.

const net = require('net');
const tls = require('tls');
const { Buffer } = require('buffer');

const MAGIC = Buffer.from('NSQL');
const VERSION = 1;
const HEADER = 12;
const MAX_PACKET = 1 << 20;
const MAX_SQL = 1 << 20;
const MAX_NAME = 256;
const MAX_PARAMS = 256;

const Type = {
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
const ReadConsistency = {
  Strong: 0,
  Bounded: 1,
  Stale: 2,
};

const STATUS_TTL_MS = 500;

const AuthPassword = 1;
const AuthPasswordKey = 2;
const FlagCancel = 1;
const FlagNull = 0x01;

const Kind = {
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
};

class NextSQLError extends Error {
  constructor(code, message) {
    super(message || code);
    this.name = 'NextSQLError';
    this.code = code;
  }
}

function splitHostPort(addr) {
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

function isLoopback(addr) {
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
  const m = /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.exec(host);
  return Boolean(m);
}

function validateConfig(cfg) {
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

function u16(buf, off) {
  return buf.readUInt16LE(off);
}

function u32(buf, off) {
  return buf.readUInt32LE(off);
}

function u64(buf, off) {
  return buf.readBigUInt64LE(off);
}

function putU16(n) {
  const b = Buffer.alloc(2);
  b.writeUInt16LE(n, 0);
  return b;
}

function putU32(n) {
  const b = Buffer.alloc(4);
  b.writeUInt32LE(n >>> 0, 0);
  return b;
}

function putU64(n) {
  const b = Buffer.alloc(8);
  b.writeBigUInt64LE(BigInt(n), 0);
  return b;
}

function appendU16String(s, max) {
  const buf = Buffer.from(String(s), 'utf8');
  if (buf.length > max || buf.length > 0xffff) {
    throw new NextSQLError('protocol', 'string exceeds limit');
  }
  return Buffer.concat([putU16(buf.length), buf]);
}

function appendU32Bytes(buf, max) {
  if (buf.length > max) {
    throw new NextSQLError('protocol', 'bytes exceed limit');
  }
  return Buffer.concat([putU32(buf.length), buf]);
}

function readU16String(buf, off, max) {
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
  return { value: buf.subarray(off + 2, off + 2 + n).toString('utf8'), next: off + 2 + n };
}

function readU32Bytes(buf, off, max) {
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
    return Buffer.alloc(0);
  }
  let hex = n.toString(16);
  if (hex.length % 2) {
    hex = '0' + hex;
  }
  return Buffer.from(hex, 'hex');
}

function bytesToBigint(buf) {
  let n = 0n;
  for (const b of buf) {
    n = (n << 8n) + BigInt(b);
  }
  return n;
}

function encodeDecimalString(s) {
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
  const body = Buffer.alloc(4 + coef.length);
  body[0] = neg ? 1 : 0;
  body.writeUInt16LE(scale, 2);
  coef.copy(body, 4);
  return appendU32Bytes(body, MAX_PACKET);
}

function decodeDecimal(body) {
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

function formatUUID(raw) {
  const h = raw.toString('hex');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

function parseUUID(s) {
  const hex = String(s).trim().replace(/-/g, '');
  if (!/^[0-9a-fA-F]{32}$/.test(hex)) {
    throw new NextSQLError('invalid_argument', 'invalid UUID');
  }
  return Buffer.from(hex, 'hex');
}

function decodeNSJB(doc) {
  if (doc.length < 5 || doc.subarray(0, 4).toString() !== 'NSJB' || doc[4] !== 1) {
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
      return { value: Number(b.readBigInt64LE(off + 1)), next: off + 9 };
    case 0x04:
    case 0x05: {
      const n = u32(b, off + 1);
      const end = off + 5 + n;
      if (end > b.length) {
        throw new NextSQLError('invalid_format', 'truncated JSON string');
      }
      const s = b.subarray(off + 5, end).toString('utf8');
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
        const key = b.subarray(cur, cur + klen).toString('utf8');
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
  const meta = Buffer.alloc(5);
  meta.writeUInt16LE(prec || 0, 0);
  meta.writeUInt16LE(scale || 0, 2);
  meta[4] = elem || 0;
  return Buffer.from([kind, 0, ...meta]);
}

function encodeNull() {
  const meta = Buffer.alloc(5);
  return Buffer.from([Kind.String, FlagNull, ...meta]);
}

function encodeBool(v) {
  return Buffer.concat([encodeTypeMeta(Kind.Bool, 0, 0, 0), Buffer.from([v ? 1 : 0])]);
}

function encodeString(s) {
  const raw = Buffer.from(String(s), 'utf8');
  return Buffer.concat([Buffer.from([Kind.String, 0]), Buffer.alloc(5), appendU32Bytes(raw, MAX_PACKET)]);
}

function encodeUUID(raw) {
  if (raw.length !== 16) {
    throw new NextSQLError('invalid_argument', 'UUID must be 16 bytes');
  }
  return Buffer.concat([Buffer.from([Kind.UUID, 0]), Buffer.alloc(5), raw]);
}

function encodeTimestamp(ns) {
  return Buffer.concat([Buffer.from([Kind.TimestampTZ, 0]), Buffer.alloc(5), putU64(ns)]);
}

function encodePoint(lon, lat) {
  const b = Buffer.alloc(16);
  b.writeDoubleLE(lon, 0);
  b.writeDoubleLE(lat, 8);
  return Buffer.concat([Buffer.from([Kind.Point, 0]), Buffer.alloc(5), b]);
}

function encodeBox(w, s, e, n) {
  const b = Buffer.alloc(32);
  b.writeDoubleLE(w, 0);
  b.writeDoubleLE(s, 8);
  b.writeDoubleLE(e, 16);
  b.writeDoubleLE(n, 24);
  return Buffer.concat([Buffer.from([Kind.Box, 0]), Buffer.alloc(5), b]);
}

function encodeVector(arr) {
  const dim = arr.length;
  const body = Buffer.alloc(3 + dim * 4);
  body.writeUInt16LE(dim, 0);
  body[2] = 0;
  for (let i = 0; i < dim; i++) {
    const f = arr[i];
    if (!Number.isFinite(f)) {
      throw new NextSQLError('invalid_argument', 'VECTOR element is not finite');
    }
    body.writeFloatLE(f, 3 + i * 4);
  }
  const hdr = Buffer.alloc(7);
  hdr[0] = Kind.Vector;
  hdr.writeUInt16LE(dim, 2);
  hdr[6] = 1; // VecF32
  return Buffer.concat([hdr, body]);
}

function encodeParam(v) {
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
    return Buffer.concat([Buffer.from([Kind.Decimal, 0]), Buffer.alloc(5), encodeDecimalString(Number.isInteger(v) ? String(v) : String(v))]);
  }
  if (typeof v === 'bigint') {
    return Buffer.concat([Buffer.from([Kind.Decimal, 0]), Buffer.alloc(5), encodeDecimalString(v.toString())]);
  }
  if (typeof v === 'string') {
    return encodeString(v);
  }
  if (v instanceof Date) {
    return encodeTimestamp(BigInt(v.getTime()) * 1000000n);
  }
  if (Buffer.isBuffer(v) && v.length === 16) {
    return encodeUUID(v);
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
      return Buffer.concat([Buffer.from([Kind.Decimal, 0]), Buffer.alloc(5), encodeDecimalString(v.value)]);
    }
    // JSON object → UTF-8 text; the server coerces STRING to JSON.
    return encodeString(JSON.stringify(v));
  }
  throw new NextSQLError('invalid_argument', 'unsupported parameter type');
}

function decodeValue(buf, off) {
  if (off + 7 > buf.length) {
    throw new NextSQLError('protocol', 'truncated value header');
  }
  const kind = buf[off];
  const flags = buf[off + 1];
  const prec = u16(buf, off + 2);
  const scale = u16(buf, off + 4);
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
      return { value: got.value.toString('utf8'), next: got.next, kind };
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
      const ns = buf.readBigInt64LE(off);
      return { value: new Date(Number(ns / 1000000n)), next: off + 8, kind };
    }
    case Kind.Bool: {
      if (off >= buf.length) {
        throw new NextSQLError('protocol', 'truncated bool');
      }
      return { value: buf[off] !== 0, next: off + 1, kind };
    }
    case Kind.Vector: {
      const dim = u16(buf, off);
      const flag = buf[off + 2];
      if (flag & 1) {
        return { value: { ref: true, dim }, next: off + 3, kind };
      }
      if (flag & 2) {
        const nnz = buf.readUInt32LE(off + 3);
        const indices = [];
        const values = [];
        for (let i = 0; i < nnz; i++) {
          indices.push(buf.readUInt32LE(off + 7 + i * 8));
          values.push(buf.readFloatLE(off + 11 + i * 8));
        }
        return { value: { dim, indices, values }, next: off + 7 + nnz * 8, kind };
      }
      const need = dim * 4;
      const out = [];
      for (let i = 0; i < dim; i++) {
        out.push(buf.readFloatLE(off + 3 + i * 4));
      }
      return { value: out, next: off + 3 + need, kind };
    }
    case Kind.Point: {
      return {
        value: { lon: buf.readDoubleLE(off), lat: buf.readDoubleLE(off + 8) },
        next: off + 16,
        kind,
      };
    }
    case Kind.Box: {
      return {
        value: {
          west: buf.readDoubleLE(off),
          south: buf.readDoubleLE(off + 8),
          east: buf.readDoubleLE(off + 16),
          north: buf.readDoubleLE(off + 24),
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
        coords.push(buf.readDoubleLE(p));
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
          ring.push(buf.readDoubleLE(p));
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

function encodeHello(h) {
  return Buffer.concat([
    putU16(h.version),
    putU16(h.flags || 0),
    putU64(h.secret || 0n),
    appendU16String(h.database || '', MAX_NAME),
    appendU16String(h.user || '', MAX_NAME),
  ]);
}

function decodeHelloOK(b) {
  if (b.length !== 11) {
    throw new NextSQLError('protocol', 'bad hello-ok length');
  }
  return { version: u16(b, 0), authMethod: b[2], secret: u64(b, 3) };
}

function encodeAuth(password) {
  return appendU16String(password || '', MAX_NAME);
}

function encodeQuery(sql, params) {
  if (params.length > MAX_PARAMS) {
    throw new NextSQLError('protocol', 'too many parameters');
  }
  const sqlBuf = Buffer.from(sql, 'utf8');
  if (sqlBuf.length > MAX_SQL) {
    throw new NextSQLError('protocol', 'SQL exceeds limit');
  }
  const parts = [appendU32Bytes(sqlBuf, MAX_SQL), putU16(params.length)];
  for (const p of params) {
    parts.push(appendU16String('', MAX_NAME));
    parts.push(encodeParam(p));
  }
  return Buffer.concat(parts);
}

function encodePrepare(sql) {
  return appendU32Bytes(Buffer.from(sql, 'utf8'), MAX_SQL);
}

function encodeExecute(id, params) {
  if (params.length > MAX_PARAMS) {
    throw new NextSQLError('protocol', 'too many parameters');
  }
  const parts = [putU32(id), putU16(params.length)];
  for (const p of params) {
    parts.push(appendU16String('', MAX_NAME));
    parts.push(encodeParam(p));
  }
  return Buffer.concat(parts);
}

function decodeRowDesc(b) {
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

function decodeDataBatch(b) {
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

function decodeError(b) {
  const code = readU16String(b, 0, MAX_NAME);
  const msg = readU16String(b, code.next, MAX_NAME);
  return new NextSQLError(code.value, msg.value);
}

function decodeCommandComplete(b) {
  if (b.length !== 8) {
    throw new NextSQLError('protocol', 'bad command-complete length');
  }
  return Number(b.readBigUInt64LE(0));
}

function encodeSetReadConsistency(mode, maxStalenessMs) {
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
  const buf = Buffer.alloc(9);
  buf[0] = mode;
  buf.writeBigUInt64LE(BigInt(ms), 1);
  return buf;
}

function decodeNodeStatus(b) {
  const role = readU16String(b, 0, MAX_NAME);
  const off = role.next;
  if (b.length - off !== 25) {
    throw new NextSQLError('protocol', 'bad node-status length');
  }
  const flags = b[off];
  return {
    role: role.value,
    hasLeader: (flags & 1) !== 0,
    healthy: (flags & 2) !== 0,
    appliedLSN: b.readBigUInt64LE(off + 1),
    lastContactMs: b.readBigInt64LE(off + 9),
    applyBacklog: b.readBigUInt64LE(off + 17),
  };
}

function txnControl(sql) {
  const up = String(sql).replace(/^[\s(]+/, '').toUpperCase();
  const begin = up.startsWith('BEGIN') || up.startsWith('START TRANSACTION');
  const end = up.startsWith('COMMIT') || up.startsWith('ROLLBACK');
  return { begin, end };
}

// isReadOnlySQL is a conservative check: a false negative only costs a leader
// round trip, and a false positive on a write self-corrects (the follower
// rejects it as not-leader and the caller retries on the leader). EXPLAIN is
// excluded because EXPLAIN ANALYZE executes its statement.
function isReadOnlySQL(sql) {
  let s = String(sql).replace(/^[\s(]+/, '');
  while (s.startsWith('--')) {
    const i = s.indexOf('\n');
    if (i < 0) {
      return false;
    }
    s = s.slice(i + 1).replace(/^[\s(]+/, '');
  }
  const up = s.toUpperCase();
  if (up.startsWith('SELECT') || up.startsWith('SHOW')) {
    return true;
  }
  if (up.startsWith('WITH')) {
    return !up.includes('INSERT') && !up.includes('UPDATE') &&
      !up.includes('DELETE') && !up.includes('UPSERT');
  }
  return false;
}

class Wire {
  constructor(socket) {
    this.socket = socket;
    this.buf = Buffer.alloc(0);
    this.closed = false;
    this.err = null;
    this.wait = null;
    socket.on('data', (chunk) => {
      this.buf = Buffer.concat([this.buf, chunk]);
      this.pump();
    });
    socket.on('error', (err) => {
      this.err = err;
      this.pump();
    });
    socket.on('close', () => {
      this.closed = true;
      this.pump();
    });
  }

  pump() {
    if (!this.wait) {
      return;
    }
    if (this.err) {
      const { reject } = this.wait;
      this.wait = null;
      reject(new NextSQLError('io', this.err.message));
      return;
    }
    if (this.buf.length >= this.wait.n) {
      const out = this.buf.subarray(0, this.wait.n);
      this.buf = this.buf.subarray(this.wait.n);
      const { resolve } = this.wait;
      this.wait = null;
      resolve(out);
      return;
    }
    if (this.closed) {
      const { reject } = this.wait;
      this.wait = null;
      reject(new NextSQLError('unavailable', 'connection closed'));
    }
  }

  readExact(n) {
    if (this.wait) {
      return Promise.reject(new NextSQLError('internal', 'overlapping read'));
    }
    return new Promise((resolve, reject) => {
      this.wait = { n, resolve, reject };
      this.pump();
    });
  }

  async readFrame() {
    const hdr = await this.readExact(HEADER);
    if (!hdr.subarray(0, 4).equals(MAGIC)) {
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
    const payload = n === 0 ? Buffer.alloc(0) : await this.readExact(n);
    return { type: typ, payload };
  }

  writeFrame(typ, payload) {
    payload = payload || Buffer.alloc(0);
    if (payload.length > MAX_PACKET) {
      throw new NextSQLError('protocol', 'payload exceeds packet limit');
    }
    const hdr = Buffer.alloc(HEADER);
    MAGIC.copy(hdr, 0);
    hdr.writeUInt16LE(VERSION, 4);
    hdr[6] = typ;
    hdr.writeUInt32LE(payload.length, 8);
    return new Promise((resolve, reject) => {
      this.socket.write(Buffer.concat([hdr, payload]), (err) => {
        if (err) {
          reject(new NextSQLError('io', err.message));
        } else {
          resolve();
        }
      });
    });
  }

  close() {
    this.socket.destroy();
  }
}

function connectSocket(cfg) {
  const { host, port } = splitHostPort(cfg.address);
  return new Promise((resolve, reject) => {
    const onErr = (err) => reject(new NextSQLError('io', err.message));
    if (cfg.tls) {
      const opts = {
        host,
        port,
        minVersion: 'TLSv1.3',
        servername: cfg.tls.servername || host,
      };
      if (cfg.tls.ca) {
        opts.ca = cfg.tls.ca;
      }
      if (cfg.tls.rejectUnauthorized === false) {
        opts.rejectUnauthorized = false;
      }
      const sock = tls.connect(opts, () => resolve(sock));
      sock.on('error', onErr);
      return;
    }
    const sock = net.connect({ host, port }, () => resolve(sock));
    sock.on('error', onErr);
  });
}

class Rows {
  constructor(conn, columns) {
    this.conn = conn;
    this.columns = columns ? columns.map((c) => c.name) : [];
    this.columnTypes = columns || [];
    this.affected = 0;
    this._batch = [];
    this._i = 0;
    this._done = columns == null;
    this._closed = columns == null;
    this._err = null;
  }

  async next() {
    if (this._closed || this._err) {
      return false;
    }
    if (this._i < this._batch.length) {
      this._i++;
      return true;
    }
    if (this._done) {
      return false;
    }
    try {
      await this._fill();
    } catch (err) {
      this._err = err;
      return false;
    }
    if (this._i < this._batch.length) {
      this._i++;
      return true;
    }
    return false;
  }

  values() {
    if (this._i <= 0 || this._i > this._batch.length) {
      return null;
    }
    return this._batch[this._i - 1];
  }

  async _fill() {
    const c = this.conn;
    if (!this._done && this._batch.length > 0) {
      await c.wire.writeFrame(Type.FlowAck, null);
    }
    const msg = await c.wire.readFrame();
    if (msg.type === Type.DataBatch) {
      this._batch = decodeDataBatch(msg.payload);
      this._i = 0;
      return;
    }
    if (msg.type === Type.CommandComplete) {
      this.affected = decodeCommandComplete(msg.payload);
      this._done = true;
      this._batch = [];
      this._i = 0;
      await c.expectReady();
      this._finish();
      return;
    }
    throw c.unexpected(msg);
  }

  async close() {
    while (await this.next()) {
      // drain so the session returns to Ready
    }
    if (!this._closed) {
      this._finish();
    }
    if (this._err) {
      throw this._err;
    }
  }

  _finish() {
    if (this.conn && !this._closed) {
      this.conn.busy = false;
    }
    this._closed = true;
  }

  async *[Symbol.asyncIterator]() {
    try {
      while (await this.next()) {
        yield this.values();
      }
      if (this._err) {
        throw this._err;
      }
    } finally {
      if (!this._closed) {
        try {
          await this.close();
        } catch {
          // already surfaced via _err
        }
      }
    }
  }
}

class Stmt {
  constructor(conn, id) {
    this.conn = conn;
    this.id = id;
  }

  async query(params) {
    return this.conn._execute(this.id, params || []);
  }

  async exec(params) {
    const rows = await this.query(params);
    return collect(rows);
  }

  async close() {
    const c = this.conn;
    if (!c.wire || !this.id) {
      return;
    }
    if (c.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    await c.wire.writeFrame(Type.CloseStmt, putU32(this.id));
    const msg = await c.wire.readFrame();
    if (msg.type !== Type.CloseOK) {
      throw c.unexpected(msg);
    }
    await c.expectReady();
    this.id = 0;
  }
}

class Conn {
  constructor(cfg, wire) {
    this.cfg = cfg;
    this.wire = wire;
    this.secret = 0n;
    this.busy = false;
  }

  async handshake() {
    await this.wire.writeFrame(Type.Hello, encodeHello({
      version: VERSION,
      database: this.cfg.database || '',
      user: this.cfg.user,
    }));
    let msg = await this.wire.readFrame();
    if (msg.type !== Type.HelloOK) {
      throw this.unexpected(msg);
    }
    const ok = decodeHelloOK(msg.payload);
    this.secret = ok.secret;
    await this.wire.writeFrame(Type.Auth, encodeAuth(this.cfg.password || ''));
    msg = await this.wire.readFrame();
    if (msg.type !== Type.AuthOK) {
      throw this.unexpected(msg);
    }
    if (ok.authMethod === AuthPasswordKey) {
      if (!this.cfg.key) {
        throw new NextSQLError('unauthorized', 'server requires a client-held key');
      }
      const key = Buffer.isBuffer(this.cfg.key) ? this.cfg.key : Buffer.from(this.cfg.key);
      if (key.length !== 32) {
        throw new NextSQLError('invalid_argument', 'client key must be 32 bytes');
      }
      const mat = Buffer.alloc(36);
      mat.writeUInt32LE(this.cfg.keyVersion || 1, 0);
      key.copy(mat, 4);
      await this.wire.writeFrame(Type.Unlock, mat);
      msg = await this.wire.readFrame();
      if (msg.type !== Type.UnlockOK) {
        throw this.unexpected(msg);
      }
    }
    msg = await this.wire.readFrame();
    if (msg.type !== Type.Ready) {
      throw this.unexpected(msg);
    }
  }

  unexpected(msg) {
    if (msg.type === Type.Error) {
      return decodeError(msg.payload);
    }
    return new NextSQLError('protocol', 'unexpected message type');
  }

  async expectReady() {
    const msg = await this.wire.readFrame();
    if (msg.type !== Type.Ready) {
      throw this.unexpected(msg);
    }
  }

  // readAck reads a single control acknowledgement: Ready, or Error followed by
  // Ready (which is drained so the session stays usable).
  async readAck() {
    const msg = await this.wire.readFrame();
    if (msg.type === Type.Ready) {
      return;
    }
    const err = this.unexpected(msg);
    if (msg.type === Type.Error) {
      await this.expectReady();
    }
    throw err;
  }

  // setReadConsistency sets this connection's read-consistency mode for
  // subsequent statements. maxStalenessMs applies only to Bounded (0 or omitted
  // selects the server default window).
  async setReadConsistency(mode, maxStalenessMs) {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    await this.wire.writeFrame(Type.SetReadConsistency, encodeSetReadConsistency(mode, maxStalenessMs));
    await this.readAck();
  }

  // nodeStatus asks the connected server for its key-free replication health.
  async nodeStatus() {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    await this.wire.writeFrame(Type.NodeStatus, null);
    const msg = await this.wire.readFrame();
    if (msg.type !== Type.NodeStatusResp) {
      const err = this.unexpected(msg);
      if (msg.type === Type.Error) {
        await this.expectReady();
      }
      throw err;
    }
    const st = decodeNodeStatus(msg.payload);
    await this.expectReady();
    return st;
  }

  async _readRows() {
    const msg = await this.wire.readFrame();
    if (msg.type === Type.RowDesc) {
      return new Rows(this, decodeRowDesc(msg.payload));
    }
    if (msg.type === Type.CommandComplete) {
      const rows = new Rows(this, null);
      rows.affected = decodeCommandComplete(msg.payload);
      await this.expectReady();
      this.busy = false;
      rows._closed = true;
      rows._done = true;
      return rows;
    }
    this.busy = false;
    throw this.unexpected(msg);
  }

  async query(sql, params) {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    this.busy = true;
    try {
      await this.wire.writeFrame(Type.Query, encodeQuery(sql, params || []));
      return await this._readRows();
    } catch (err) {
      this.busy = false;
      throw err;
    }
  }

  async exec(sql, params) {
    const rows = await this.query(sql, params);
    return collect(rows);
  }

  async prepare(sql) {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    await this.wire.writeFrame(Type.Prepare, encodePrepare(sql));
    const msg = await this.wire.readFrame();
    if (msg.type !== Type.PrepareOK) {
      throw this.unexpected(msg);
    }
    if (msg.payload.length !== 4) {
      throw new NextSQLError('protocol', 'bad prepare-ok length');
    }
    const id = u32(msg.payload, 0);
    await this.expectReady();
    return new Stmt(this, id);
  }

  async _execute(id, params) {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    this.busy = true;
    try {
      await this.wire.writeFrame(Type.Execute, encodeExecute(id, params));
      return await this._readRows();
    } catch (err) {
      this.busy = false;
      throw err;
    }
  }

  async cancel() {
    if (!this.secret) {
      throw new NextSQLError('unavailable', 'not connected');
    }
    const sock = await connectSocket(this.cfg);
    const side = new Wire(sock);
    try {
      await side.writeFrame(Type.Hello, encodeHello({
        version: VERSION,
        flags: FlagCancel,
        secret: this.secret,
        database: '',
        user: '',
      }));
      const msg = await side.readFrame();
      if (msg.type !== Type.Ready) {
        throw this.unexpected(msg);
      }
    } finally {
      side.close();
    }
  }

  async close() {
    if (!this.wire) {
      return;
    }
    try {
      await this.wire.writeFrame(Type.Terminate, null);
    } catch {
      // ignore
    }
    this.wire.close();
    this.wire = null;
  }
}

async function collect(rows) {
  const out = { columns: rows.columns, rows: [], affected: 0 };
  try {
    while (await rows.next()) {
      out.rows.push(rows.values());
    }
    if (rows._err) {
      throw rows._err;
    }
  } finally {
    if (!rows._closed) {
      await rows.close();
    }
  }
  out.affected = rows.affected;
  return out;
}

async function openConn(cfg) {
  const sock = await connectSocket(cfg);
  const wire = new Wire(sock);
  const conn = new Conn(cfg, wire);
  try {
    await conn.handshake();
    const mode = cfg.readConsistency;
    if (mode !== undefined && mode !== null && mode !== ReadConsistency.Strong) {
      await conn.setReadConsistency(mode, cfg.maxStalenessMs);
    }
  } catch (err) {
    wire.close();
    throw err;
  }
  return conn;
}

async function connect(cfg) {
  validateConfig(cfg);
  return openConn(cfg);
}

// Cluster is a routing client over every node of a NextSQL HA cluster.
//
// With cfg.readConsistency set to Bounded or Stale it sends eligible read-only
// statements to a healthy follower and everything else — writes, DDL,
// transaction control, and Strong reads — to the leader. With the default
// Strong consistency every statement goes to the leader and Cluster is just a
// leader-failover wrapper. A Cluster is for sequential use.
class Cluster {
  constructor(cfg, conns) {
    this.cfg = cfg;
    this._conns = conns; // [{ addr, conn, status, seen }]
    this._rr = 0;
    this._inTxn = false;
  }

  async close() {
    let err = null;
    for (const cc of this._conns) {
      try {
        await cc.conn.close();
      } catch (e) {
        if (!err) {
          err = e;
        }
      }
    }
    if (err) {
      throw err;
    }
  }

  async nodes() {
    await this._refresh();
    return this._conns.map((cc) => cc.status).filter((s) => s != null);
  }

  async exec(sql, params) {
    const rows = await this.query(sql, params);
    return collect(rows);
  }

  async query(sql, params) {
    const { begin, end } = txnControl(sql);
    const routable = !this._inTxn && !begin && !end &&
      this.cfg.readConsistency !== undefined &&
      this.cfg.readConsistency !== null &&
      this.cfg.readConsistency !== ReadConsistency.Strong &&
      isReadOnlySQL(sql);

    if (routable) {
      const fc = await this._followerConn();
      if (fc) {
        try {
          return await fc.query(sql, params || []);
        } catch (err) {
          if (!(err instanceof NextSQLError) || err.code !== 'unavailable') {
            throw err;
          }
          // Follower lost the leader or fell outside the bound; fall through.
        }
      }
    }

    const lc = await this._leaderConn();
    const rows = await lc.query(sql, params || []);
    if (begin || end) {
      this._inTxn = begin;
    }
    return rows;
  }

  async _refresh() {
    const now = Date.now();
    const targets = this._conns.filter((cc) => now - (cc.seen || 0) >= STATUS_TTL_MS);
    for (const cc of targets) {
      try {
        cc.status = await cc.conn.nodeStatus();
        cc.seen = Date.now();
      } catch {
        // keep the last known status
      }
    }
  }

  async _leaderConn() {
    await this._refresh();
    for (const cc of this._conns) {
      const role = cc.status && cc.status.role;
      if (role === 'leader' || role === 'standalone') {
        return cc.conn;
      }
    }
    throw new NextSQLError('unavailable', 'no reachable leader');
  }

  async _followerConn() {
    await this._refresh();
    const followers = [];
    const others = [];
    for (const cc of this._conns) {
      if (!cc.status || !cc.status.healthy) {
        continue;
      }
      if (cc.status.role === 'follower') {
        followers.push(cc);
      } else if (cc.status.role === 'leader' || cc.status.role === 'standalone') {
        others.push(cc);
      }
    }
    const pick = followers.length > 0 ? followers : others;
    if (pick.length === 0) {
      return null;
    }
    const cc = pick[this._rr % pick.length];
    this._rr++;
    return cc.conn;
  }
}

async function connectCluster(cfg) {
  let addrs = Array.isArray(cfg && cfg.nodes) ? cfg.nodes.slice() : [];
  if (addrs.length === 0 && cfg && cfg.address) {
    addrs = [cfg.address];
  }
  if (addrs.length === 0) {
    throw new NextSQLError('invalid_argument', 'at least one node address is required');
  }
  for (const a of addrs) {
    validateConfig({ ...cfg, address: a, nodes: undefined });
  }
  const conns = [];
  let firstErr = null;
  for (const a of addrs) {
    try {
      const conn = await openConn({ ...cfg, address: a, nodes: undefined });
      conns.push({ addr: a, conn, status: null, seen: 0 });
    } catch (err) {
      if (!firstErr) {
        firstErr = err;
      }
    }
  }
  if (conns.length === 0) {
    throw firstErr;
  }
  return new Cluster(cfg, conns);
}

module.exports = {
  connect,
  connectCluster,
  Cluster,
  NextSQLError,
  Kind,
  Type,
  ReadConsistency,
  validateConfig,
  isLoopback,
  isReadOnlySQL,
  txnControl,
  encodeParam,
  decodeValue,
  encodeHello,
  decodeHelloOK,
  encodeQuery,
  encodeSetReadConsistency,
  decodeNodeStatus,
  decodeRowDesc,
  decodeDataBatch,
  decodeError,
  decodeNSJB,
  formatUUID,
  encodeDecimalString,
  decodeDecimal,
};
