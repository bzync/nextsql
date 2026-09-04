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
const MAX_ENUM_LABELS = 4096;
const MAX_ENUM_LABEL_BYTES = 255;

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
  Blob: 14,
  Int8: 15,
  Int16: 16,
  Int32: 17,
  Int64: 18,
  Uint8: 19,
  Uint16: 20,
  Uint32: 21,
  Uint64: 22,
  Date: 23,
  Time: 24,
  Char: 25,
  Varchar: 26,
  Timestamp: 27,
  Float32: 28,
  Float64: 29,
  Enum: 30,
  Interval: 31,
  Struct: 32,
  Array: 33,
  Map: 34,
  Geometry: 35,
  Geography: 36,
};

const MAX_NEST_DEPTH = 8;
const MAX_STRUCT_FIELDS = 128;
const MAX_COLLECTION_LEN = 1 << 20;

class NextSQLError extends Error {
  constructor(code, message) {
    super(message || code);
    this.name = 'NextSQLError';
    this.code = code;
  }
}

const {
  FieldType,
  MemoryFieldKeyring,
  FileFieldKeyring,
  decryptField,
  encryptField,
  generateFieldKey,
  inspectField,
} = require('./client-encryption')({
  NextSQLError,
  Kind,
  encodeDecimalString,
  decodeDecimal,
  decodeNSJB,
  formatUUID,
  parseUUID,
});

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
  // BigInt.asUintN wraps a negative value to its unsigned 64-bit two's-
  // complement bit pattern before writeBigUInt64LE, which otherwise throws
  // a RangeError for anything outside [0, 2^64) (unlike the JS/Bun/Deno
  // driver's DataView.setBigUint64, which already wraps automatically per
  // the ECMAScript ToBigUint64 abstract operation). Found and fixed while
  // implementing D6 (INTERVAL's nanosecond component is legitimately
  // negative, e.g. "-1 hour") — this was already a latent, pre-existing bug
  // for any negative epoch-nanosecond value, i.e. every pre-1970
  // TIMESTAMPTZ/TIMESTAMP encoded through this same helper.
  b.writeBigUInt64LE(BigInt.asUintN(64, BigInt(n)), 0);
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

// appendEnumLabels/readEnumLabels carry an ENUM Type's declared label list on
// the wire, matching internal/protocol/value.go's appendEnumLabels/
// readEnumLabels exactly (docs/design-datatypes.md D11): ENUM is the first
// D-track type whose Type needs variable-length metadata beyond the fixed
// 5/6-byte Precision/Scale/VecElem shape every other type fits into.
function appendEnumLabels(labels) {
  const parts = [putU16(labels.length)];
  for (const l of labels) {
    parts.push(appendU16String(l, MAX_ENUM_LABEL_BYTES));
  }
  return Buffer.concat(parts);
}

function readEnumLabels(buf, off) {
  if (off + 2 > buf.length) {
    throw new NextSQLError('protocol', 'truncated enum label count');
  }
  const n = u16(buf, off);
  if (n > MAX_ENUM_LABELS) {
    throw new NextSQLError('protocol', 'enum label count exceeds limit');
  }
  off += 2;
  const labels = [];
  for (let i = 0; i < n; i++) {
    const got = readU16String(buf, off, MAX_ENUM_LABEL_BYTES);
    labels.push(got.value);
    off = got.next;
  }
  return { value: labels, next: off };
}

// --- Collections (STRUCT / ARRAY / MAP), docs/design-collections.md ----------

function readTypeFull(buf, off, depth) {
  if (off + 6 > buf.length) throw new NextSQLError('protocol', 'truncated type');
  const type = {
    kind: buf[off],
    precision: u16(buf, off + 1),
    scale: u16(buf, off + 3),
    elem: buf[off + 5],
  };
  const next = readNestedDescriptor(buf, off + 6, type, depth);
  return { type, next };
}

function readNestedDescriptor(buf, off, type, depth) {
  if (depth > MAX_NEST_DEPTH + 1) {
    throw new NextSQLError('protocol', 'collection type nesting too deep');
  }
  const kind = type.kind;
  if (kind === Kind.Enum) {
    const got = readEnumLabels(buf, off);
    type.labels = got.value;
    return got.next;
  }
  if (kind === Kind.Array) {
    const e = readTypeFull(buf, off, depth + 1);
    type.elemType = e.type;
    return e.next;
  }
  if (kind === Kind.Map) {
    const k = readTypeFull(buf, off, depth + 1);
    const v = readTypeFull(buf, k.next, depth + 1);
    type.keyType = k.type;
    type.elemType = v.type;
    return v.next;
  }
  if (kind === Kind.Struct) {
    if (off + 2 > buf.length) throw new NextSQLError('protocol', 'truncated struct field count');
    const n = u16(buf, off);
    if (n === 0 || n > MAX_STRUCT_FIELDS) {
      throw new NextSQLError('protocol', 'struct field count out of range');
    }
    off += 2;
    const fields = [];
    for (let i = 0; i < n; i++) {
      const name = readU16String(buf, off, 255);
      off = name.next;
      const ft = readTypeFull(buf, off, depth + 1);
      off = ft.next;
      fields.push({ name: name.value, type: ft.type });
    }
    type.fields = fields;
    return off;
  }
  return off;
}

function decodePayload(buf, off, type) {
  const kind = type.kind;
  if (kind === Kind.Struct || kind === Kind.Array || kind === Kind.Map) {
    return decodeCollectionPayload(buf, off, type);
  }
  const header = [Buffer.from([kind, 0]), Buffer.alloc(5)];
  if (kind === Kind.Enum) header.push(appendEnumLabels(type.labels || []));
  const headerBytes = Buffer.concat(header);
  const synthetic = Buffer.concat([headerBytes, buf.subarray(off)]);
  const got = decodeValue(synthetic, 0);
  return { value: got.value, next: off + (got.next - headerBytes.length) };
}

function decodeCollectionPayload(buf, off, type) {
  if (off + 4 > buf.length) throw new NextSQLError('protocol', 'truncated collection');
  const bodyLen = buf.readUInt32LE(off);
  const bodyEnd = off + 4 + bodyLen;
  if (bodyEnd > buf.length) throw new NextSQLError('protocol', 'truncated collection body');
  let p = off + 4;
  const n = buf.readUInt32LE(p);
  p += 4;
  if (n > 2 * MAX_COLLECTION_LEN + 2 || n > bodyLen) {
    throw new NextSQLError('protocol', 'collection member count out of range');
  }
  const nb = (n + 7) >> 3;
  const nulls = buf.subarray(p, p + nb);
  p += nb;
  const memberType = (i) => {
    if (type.kind === Kind.Struct) return type.fields[i].type;
    if (type.kind === Kind.Array) return type.elemType;
    return i % 2 === 0 ? type.keyType : type.elemType;
  };
  const members = [];
  for (let i = 0; i < n; i++) {
    if (nulls[i >> 3] & (1 << (i & 7))) {
      members.push(null);
      continue;
    }
    const got = decodePayload(buf, p, memberType(i));
    p = got.next;
    members.push(got.value);
  }
  let value;
  if (type.kind === Kind.Struct) {
    value = {};
    for (let i = 0; i < type.fields.length; i++) value[type.fields[i].name] = members[i];
  } else if (type.kind === Kind.Array) {
    value = members;
  } else {
    value = new Map();
    for (let i = 0; i + 1 < members.length; i += 2) value.set(members[i], members[i + 1]);
  }
  return { value, next: bodyEnd, kind: type.kind };
}

function encodeTypeFull(type) {
  const parts = [Buffer.from([type.kind]), Buffer.alloc(5)];
  if (type.kind === Kind.Enum) parts.push(appendEnumLabels(type.labels || []));
  else if (type.kind === Kind.Array) parts.push(encodeTypeFull(type.elemType));
  else if (type.kind === Kind.Map) {
    parts.push(encodeTypeFull(type.keyType));
    parts.push(encodeTypeFull(type.elemType));
  } else if (type.kind === Kind.Struct) {
    parts.push(putU16(type.fields.length));
    for (const f of type.fields) {
      parts.push(appendU16String(f.name, 255));
      parts.push(encodeTypeFull(f.type));
    }
  }
  return Buffer.concat(parts);
}

function inferValue(v) {
  if (v === null || v === undefined) return { type: { kind: Kind.String }, payload: null };
  if (Array.isArray(v)) {
    const els = v.map(inferValue);
    const elemType = (els.find((e) => e.payload !== null) || {}).type || { kind: Kind.String };
    return { type: { kind: Kind.Array, elemType }, payload: collectionPayload(els) };
  }
  if (v instanceof Map) {
    const entries = [];
    for (const [k, val] of v) {
      entries.push(inferValue(k));
      entries.push(inferValue(val));
    }
    const keyType = (entries.find((e, i) => i % 2 === 0 && e.payload !== null) || {}).type || { kind: Kind.String };
    const elemType = (entries.find((e, i) => i % 2 === 1 && e.payload !== null) || {}).type || { kind: Kind.String };
    return { type: { kind: Kind.Map, keyType, elemType }, payload: collectionPayload(entries) };
  }
  if (v && typeof v === 'object' && v.__struct) {
    const fields = [];
    const els = [];
    for (const [name, fv] of v.__struct) {
      const iv = inferValue(fv);
      fields.push({ name, type: iv.type });
      els.push(iv);
    }
    return { type: { kind: Kind.Struct, fields }, payload: collectionPayload(els) };
  }
  const enc = encodeParam(v);
  const kind = enc[0];
  let hdr = 7;
  if (kind === Kind.Enum) {
    const lc = u16(enc, 7);
    hdr = 9;
    for (let i = 0; i < lc; i++) hdr += 2 + u16(enc, hdr);
  }
  return { type: { kind }, payload: enc.subarray(hdr) };
}

function collectionPayload(members) {
  const n = members.length;
  const nb = (n + 7) >> 3;
  const nulls = Buffer.alloc(nb);
  const chunks = [];
  members.forEach((m, i) => {
    if (m.payload === null) nulls[i >> 3] |= 1 << (i & 7);
    else chunks.push(m.payload);
  });
  const body = Buffer.concat([putU32(n), nulls, ...chunks]);
  return Buffer.concat([putU32(body.length), body]);
}

function encodeCollectionParam(v) {
  const iv = inferValue(v);
  const typeBody = encodeTypeFull(iv.type).subarray(1);
  return Buffer.concat([Buffer.from([iv.type.kind, 0]), typeBody, iv.payload || Buffer.alloc(0)]);
}

/** Wrap an array of [name, value] pairs as an explicit STRUCT parameter. */
function struct(fields) {
  return { __struct: fields };
}

// --- Spatial: EWKB decode (Spatial track, docs/design-spatial.md) ----------
const EWKB_TYPES = {
  1: 'Point', 2: 'LineString', 3: 'Polygon',
  4: 'MultiPoint', 5: 'MultiLineString', 6: 'MultiPolygon',
  7: 'GeometryCollection',
};
const EWKB_SRID_FLAG = 0x20000000;

function decodeEWKB(buf, off, depth) {
  if (depth > 8) throw new NextSQLError('protocol', 'geometry nesting too deep');
  if (off + 5 > buf.length) throw new NextSQLError('protocol', 'truncated geometry');
  if (buf[off] !== 1) throw new NextSQLError('protocol', 'only little-endian EWKB is supported');
  const tword = buf.readUInt32LE(off + 1);
  const gtype = tword & ~EWKB_SRID_FLAG;
  let p = off + 5;
  let srid = 0;
  if (tword & EWKB_SRID_FLAG) {
    srid = buf.readUInt32LE(p);
    p += 4;
  }
  const f64 = () => { const v = buf.readDoubleLE(p); p += 8; return v; };
  const u32 = () => { const v = buf.readUInt32LE(p); p += 4; return v; };
  const pts = (n) => {
    const out = [];
    for (let i = 0; i < n; i++) out.push([f64(), f64()]);
    return out;
  };
  const name = EWKB_TYPES[gtype];
  if (!name) throw new NextSQLError('protocol', 'unknown geometry type');
  if (gtype === 1) {
    return { g: { type: name, srid, coordinates: [f64(), f64()] }, next: p };
  }
  if (gtype === 2) {
    return { g: { type: name, srid, coordinates: pts(u32()) }, next: p };
  }
  if (gtype === 3) {
    const nr = u32();
    const coordinates = [];
    for (let r = 0; r < nr; r++) coordinates.push(pts(u32()));
    return { g: { type: name, srid, coordinates }, next: p };
  }
  const np = u32();
  const parts = [];
  for (let i = 0; i < np; i++) {
    const sub = decodeEWKB(buf, p, depth + 1);
    p = sub.next;
    parts.push(sub.g);
  }
  const g = { type: name, srid };
  if (gtype === 7) g.geometries = parts;
  else g.coordinates = parts.map((x) => x.coordinates);
  return { g, next: p };
}

// geoToWKT renders a decoded { type, coordinates } object back as WKT.
function geoToWKT(g) {
  const p = (xy) => `${xy[0]} ${xy[1]}`;
  const ring = (r) => `(${r.map(p).join(', ')})`;
  switch (g.type) {
    case 'Point': return `POINT(${p(g.coordinates)})`;
    case 'LineString': return `LINESTRING(${g.coordinates.map(p).join(', ')})`;
    case 'Polygon': return `POLYGON(${g.coordinates.map(ring).join(', ')})`;
    case 'MultiPoint': return `MULTIPOINT(${g.coordinates.map((c) => `(${p(c)})`).join(', ')})`;
    case 'MultiLineString': return `MULTILINESTRING(${g.coordinates.map(ring).join(', ')})`;
    case 'MultiPolygon':
      return `MULTIPOLYGON(${g.coordinates.map((poly) => `(${poly.map(ring).join(', ')})`).join(', ')})`;
    case 'GeometryCollection':
      return `GEOMETRYCOLLECTION(${g.geometries.map(geoToWKT).join(', ')})`;
    default:
      throw new NextSQLError('invalid_argument', 'unsupported geometry type');
  }
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

function encodeBlob(raw) {
  return Buffer.concat([Buffer.from([Kind.Blob, 0]), Buffer.alloc(5), appendU32Bytes(raw, MAX_PACKET)]);
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
  const full = Buffer.alloc(8);
  full.writeBigInt64LE(n, 0);
  return Buffer.concat([Buffer.from([kind, 0]), Buffer.alloc(5), full.subarray(0, width)]);
}

const UINT_RANGES = {
  uint8: [0xffn, Kind.Uint8, 1],
  uint16: [0xffffn, Kind.Uint16, 2],
  uint32: [0xffffffffn, Kind.Uint32, 4],
  uint64: [0xffffffffffffffffn, Kind.Uint64, 8],
};

// encodeUint builds an explicit fixed-width unsigned int parameter (D3,
// Datatype expansion track). Mirrors encodeInt.
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
  const full = Buffer.alloc(8);
  full.writeBigUInt64LE(n, 0);
  return Buffer.concat([Buffer.from([kind, 0]), Buffer.alloc(5), full.subarray(0, width)]);
}

// encodeEnum builds an explicit ENUM parameter (D11, Datatype expansion
// track). Ordinary INSERT/UPDATE params can just pass a plain JS string —
// the server coerces STRING -> ENUM against the destination column, same as
// a SQL string literal. This wrapper exists for explicit round-tripping and
// mirrors encodeInt/encodeUint's precedent.
function encodeEnum(label, labels) {
  const ord = labels.indexOf(label);
  if (ord < 0) {
    throw new NextSQLError('invalid_argument', 'value is not a member of the ENUM label set');
  }
  return Buffer.concat([Buffer.from([Kind.Enum, 0]), Buffer.alloc(5), appendEnumLabels(labels), putU16(ord)]);
}

function encodeTimestamp(ns) {
  return Buffer.concat([Buffer.from([Kind.TimestampTZ, 0]), Buffer.alloc(5), putU64(ns)]);
}

// encodeDate/encodeTime/encodeNaiveTimestamp/encodeFloat32/encodeFloat64 (D5/
// D7/D8, Datatype expansion track): explicit wrappers for types with no
// natural bare-value mapping (a Date is always TimestampTZ; a number always
// defaults to Decimal; JS has no date-only or time-only type). Wire shapes
// mirror internal/sql/types/row.go's encodeScalar exactly.
function encodeDate(dayCount) {
  const b = Buffer.alloc(4);
  b.writeInt32LE(dayCount, 0);
  return Buffer.concat([Buffer.from([Kind.Date, 0]), Buffer.alloc(5), b]);
}

function encodeTime(nanosSinceMidnight) {
  return Buffer.concat([Buffer.from([Kind.Time, 0]), Buffer.alloc(5), putU64(nanosSinceMidnight)]);
}

function encodeNaiveTimestamp(ns) {
  return Buffer.concat([Buffer.from([Kind.Timestamp, 0]), Buffer.alloc(5), putU64(ns)]);
}

// NaN/+-Infinity are valid FLOAT32/FLOAT64 values (unlike the bare-number ->
// Decimal default path, which requires finite) — the server canonicalizes
// -0.0 -> +0.0 and every NaN payload to one value (docs/design-datatypes.md D8).
function encodeFloat32(n) {
  if (typeof n !== 'number') {
    throw new NextSQLError('invalid_argument', 'FLOAT32 value must be a number');
  }
  const b = Buffer.alloc(4);
  b.writeFloatLE(n, 0);
  return Buffer.concat([Buffer.from([Kind.Float32, 0]), Buffer.alloc(5), b]);
}

function encodeFloat64(n) {
  if (typeof n !== 'number') {
    throw new NextSQLError('invalid_argument', 'FLOAT64 value must be a number');
  }
  const b = Buffer.alloc(8);
  b.writeDoubleLE(n, 0);
  return Buffer.concat([Buffer.from([Kind.Float64, 0]), Buffer.alloc(5), b]);
}

// encodeInterval builds an explicit INTERVAL parameter (D6, Datatype
// expansion track): months(i32 LE) + days(i32 LE) + nanos(i64 LE) — a
// plain string still works as an INTERVAL param for INSERT/UPDATE column
// assignment (server-side Coerce) but not inside an arithmetic expression
// like `dur + $1`, which requires the actual wire Kind (see the JS/Bun/Deno
// driver's encodeInterval for the full explanation).
function encodeInterval(months, days, nanos) {
  const mbuf = Buffer.alloc(4);
  mbuf.writeInt32LE(months, 0);
  const dbuf = Buffer.alloc(4);
  dbuf.writeInt32LE(days, 0);
  return Buffer.concat([Buffer.from([Kind.Interval, 0]), Buffer.alloc(5), mbuf, dbuf, putU64(nanos)]);
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
  if (Buffer.isBuffer(v) || v instanceof Uint8Array) {
    // A bare 16-byte buffer keeps its pre-existing meaning (UUID) for
    // compatibility; any other length is a BLOB. A 16-byte BLOB needs the
    // explicit { kind: 'blob', value } wrapper below to disambiguate.
    return v.length === 16 ? encodeUUID(v) : encodeBlob(v);
  }
  if (Array.isArray(v) && v.length > 0 && v.every((x) => typeof x === 'number')) {
    return encodeVector(v);
  }
  if (Array.isArray(v) || v instanceof Map || (v && typeof v === 'object' && v.__struct)) {
    return encodeCollectionParam(v);
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
    if (v.kind === 'blob') {
      return encodeBlob(Buffer.isBuffer(v.value) ? v.value : Buffer.from(v.value));
    }
    if (v.kind === 'int8' || v.kind === 'int16' || v.kind === 'int32' || v.kind === 'int64') {
      return encodeInt(v.kind, v.value);
    }
    if (v.kind === 'uint8' || v.kind === 'uint16' || v.kind === 'uint32' || v.kind === 'uint64') {
      return encodeUint(v.kind, v.value);
    }
    if (v.kind === 'enum') {
      return encodeEnum(v.value, v.labels);
    }
    if (v.kind === 'date') {
      const dayCount = v.value instanceof Date ? Math.floor(v.value.getTime() / 86400000) : Number(v.value);
      return encodeDate(dayCount);
    }
    if (v.kind === 'time') {
      return encodeTime(v.value);
    }
    if (v.kind === 'timestamp') {
      const ns = v.value instanceof Date ? BigInt(v.value.getTime()) * 1000000n : BigInt(v.value);
      return encodeNaiveTimestamp(ns);
    }
    if (v.kind === 'float32') {
      return encodeFloat32(Number(v.value));
    }
    if (v.kind === 'float64') {
      return encodeFloat64(Number(v.value));
    }
    if (v.kind === 'interval') {
      return encodeInterval(v.months | 0, v.days | 0, v.nanos);
    }
    if (v.kind === 'array' || v.kind === 'map' || v.kind === 'struct') {
      let inner = v.value;
      if (v.kind === 'map' && Array.isArray(inner)) inner = new Map(inner);
      if (v.kind === 'struct') inner = { __struct: inner };
      return encodeCollectionParam(inner);
    }
    if (v.kind === 'geometry' || v.kind === 'geography') {
      let wkt = v.wkt != null ? String(v.wkt) : geoToWKT(v);
      if (v.srid != null && !/^SRID=/i.test(wkt)) wkt = `SRID=${v.srid};${wkt}`;
      return encodeString(wkt);
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
  let enumLabels = null;
  let collType = null;
  if (kind === Kind.Enum) {
    const got = readEnumLabels(buf, off);
    enumLabels = got.value;
    off = got.next;
  } else if (kind === Kind.Struct || kind === Kind.Array || kind === Kind.Map) {
    collType = { kind };
    off = readNestedDescriptor(buf, off, collType, 0);
  }
  if (flags & FlagNull) {
    return { value: null, next: off, kind, labels: enumLabels || undefined };
  }
  switch (kind) {
    case Kind.UUID: {
      if (off + 16 > buf.length) {
        throw new NextSQLError('protocol', 'truncated UUID');
      }
      return { value: formatUUID(buf.subarray(off, off + 16)), next: off + 16, kind };
    }
    case Kind.String:
    case Kind.Text:
    case Kind.Char:
    case Kind.Varchar: {
      const got = readU32Bytes(buf, off, MAX_PACKET);
      return { value: got.value.toString('utf8'), next: got.next, kind };
    }
    case Kind.Blob: {
      const got = readU32Bytes(buf, off, MAX_PACKET);
      return { value: Buffer.from(got.value), next: got.next, kind };
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
    case Kind.Timestamp: {
      // Naive/no-timezone: same wire shape as TimestampTZ (docs/design-datatypes.md D7).
      if (off + 8 > buf.length) {
        throw new NextSQLError('protocol', 'truncated timestamp');
      }
      const ns = buf.readBigInt64LE(off);
      return { value: new Date(Number(ns / 1000000n)), next: off + 8, kind };
    }
    case Kind.Date: {
      if (off + 4 > buf.length) {
        throw new NextSQLError('protocol', 'truncated date');
      }
      const dayCount = buf.readInt32LE(off);
      return { value: new Date(dayCount * 86400000), next: off + 4, kind };
    }
    case Kind.Time: {
      if (off + 8 > buf.length) {
        throw new NextSQLError('protocol', 'truncated time');
      }
      const ns = buf.readBigUInt64LE(off);
      return { value: Number(ns), next: off + 8, kind };
    }
    case Kind.Float32: {
      if (off + 4 > buf.length) {
        throw new NextSQLError('protocol', 'truncated float32');
      }
      return { value: buf.readFloatLE(off), next: off + 4, kind };
    }
    case Kind.Float64: {
      if (off + 8 > buf.length) {
        throw new NextSQLError('protocol', 'truncated float64');
      }
      return { value: buf.readDoubleLE(off), next: off + 8, kind };
    }
    case Kind.Interval: {
      if (off + 16 > buf.length) {
        throw new NextSQLError('protocol', 'truncated interval');
      }
      const months = buf.readInt32LE(off);
      const days = buf.readInt32LE(off + 4);
      const nanos = buf.readBigInt64LE(off + 8);
      return { value: { months, days, nanos }, next: off + 16, kind };
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
      return { value: buf.readInt8(off), next: off + 1, kind };
    }
    case Kind.Int16: {
      if (off + 2 > buf.length) {
        throw new NextSQLError('protocol', 'truncated int16');
      }
      return { value: buf.readInt16LE(off), next: off + 2, kind };
    }
    case Kind.Int32: {
      if (off + 4 > buf.length) {
        throw new NextSQLError('protocol', 'truncated int32');
      }
      return { value: buf.readInt32LE(off), next: off + 4, kind };
    }
    case Kind.Int64: {
      if (off + 8 > buf.length) {
        throw new NextSQLError('protocol', 'truncated int64');
      }
      // Exposed as BigInt, not number: the full int64 range does not fit
      // safely in a JS double (see docs/design-datatypes.md D2).
      return { value: buf.readBigInt64LE(off), next: off + 8, kind };
    }
    case Kind.Uint8: {
      if (off + 1 > buf.length) {
        throw new NextSQLError('protocol', 'truncated uint8');
      }
      return { value: buf.readUInt8(off), next: off + 1, kind };
    }
    case Kind.Uint16: {
      if (off + 2 > buf.length) {
        throw new NextSQLError('protocol', 'truncated uint16');
      }
      return { value: buf.readUInt16LE(off), next: off + 2, kind };
    }
    case Kind.Uint32: {
      if (off + 4 > buf.length) {
        throw new NextSQLError('protocol', 'truncated uint32');
      }
      return { value: buf.readUInt32LE(off), next: off + 4, kind };
    }
    case Kind.Uint64: {
      if (off + 8 > buf.length) {
        throw new NextSQLError('protocol', 'truncated uint64');
      }
      // Exposed as BigInt, not number: the full uint64 range does not fit
      // safely in a JS double (see docs/design-datatypes.md D3).
      return { value: buf.readBigUInt64LE(off), next: off + 8, kind };
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
    case Kind.Enum: {
      if (off + 2 > buf.length) {
        throw new NextSQLError('protocol', 'truncated enum');
      }
      const ord = u16(buf, off);
      if (ord >= enumLabels.length) {
        throw new NextSQLError('protocol', 'ENUM ordinal out of range');
      }
      return { value: enumLabels[ord], next: off + 2, kind, labels: enumLabels };
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
    case Kind.Struct:
    case Kind.Array:
    case Kind.Map:
      return decodeCollectionPayload(buf, off, collType);
    case Kind.Geometry:
    case Kind.Geography: {
      const len = buf.readUInt32LE(off);
      const { g } = decodeEWKB(buf, off + 4, 0);
      return { value: g, next: off + 4 + len, kind };
    }
    default:
      throw new NextSQLError('protocol', 'unsupported type');
  }
}

function encodeHello(h) {
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
  return Buffer.concat(parts);
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
    const kind = b[off];
    const col = {
      name: name.value,
      kind,
      precision: u16(b, off + 1),
      scale: u16(b, off + 3),
    };
    off += 6;
    if (kind === Kind.Enum) {
      const got = readEnumLabels(b, off);
      col.labels = got.value;
      off = got.next;
    } else if (kind === Kind.Struct || kind === Kind.Array || kind === Kind.Map) {
      const t = { kind };
      off = readNestedDescriptor(b, off, t, 0);
      col.collType = t;
    }
    columns.push(col);
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
    throw await c.unexpected(msg);
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
      throw await c.unexpected(msg);
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
      realm: this.cfg.realm || '',
    }));
    let msg = await this.wire.readFrame();
    if (msg.type !== Type.HelloOK) {
      throw await this.unexpected(msg);
    }
    const ok = decodeHelloOK(msg.payload);
    this.secret = ok.secret;
    await this.wire.writeFrame(Type.Auth, encodeAuth(this.cfg.password || ''));
    msg = await this.wire.readFrame();
    if (msg.type !== Type.AuthOK) {
      throw await this.unexpected(msg);
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
        throw await this.unexpected(msg);
      }
    }
    msg = await this.wire.readFrame();
    if (msg.type !== Type.Ready) {
      throw await this.unexpected(msg);
    }
  }

  // Decodes an out-of-band Error frame (or reports a genuine protocol
  // violation) for a call site checking "did I get what I expected?".
  // writeErrReady on the server always sends Error then Ready — every call
  // site funnels through here specifically so that trailing Ready is
  // always drained in one place, rather than each of
  // query/prepare/closeStatement/etc. having to remember to do it
  // individually (a per-call-site version of this was exactly the shape of
  // a real bug: _readRows/prepare/close never drained it, leaving the
  // connection permanently desynced after the first query error).
  async unexpected(msg) {
    if (msg.type === Type.Error) {
      const err = decodeError(msg.payload);
      try {
        await this.expectReady();
      } catch {
        // Best-effort: surface the original application error even if
        // draining the trailing Ready itself fails (e.g. the connection
        // is now genuinely broken).
      }
      return err;
    }
    return new NextSQLError('protocol', 'unexpected message type');
  }

  async expectReady() {
    const msg = await this.wire.readFrame();
    if (msg.type !== Type.Ready) {
      throw await this.unexpected(msg);
    }
  }

  // readAck reads a single control acknowledgement: Ready, or Error
  // (unexpected() drains the trailing Ready so the session stays usable).
  async readAck() {
    const msg = await this.wire.readFrame();
    if (msg.type === Type.Ready) {
      return;
    }
    throw await this.unexpected(msg);
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
      throw await this.unexpected(msg);
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
    throw await this.unexpected(msg);
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

  async encryptField(table, column, type, value) {
    return encryptField(this.cfg.fieldKeys, this.cfg.database || '', table, column, type, value);
  }

  async decryptField(table, column, type, ciphertext) {
    return decryptField(this.cfg.fieldKeys, this.cfg.database || '', table, column, type, ciphertext);
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
      throw await this.unexpected(msg);
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
        throw await this.unexpected(msg);
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
  struct,
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
  FieldType,
  MemoryFieldKeyring,
  FileFieldKeyring,
  decryptField,
  encryptField,
  generateFieldKey,
  inspectField,
};
