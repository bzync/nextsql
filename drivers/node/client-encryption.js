'use strict';

// Node-specific NSCE1 implementation. The dependency injection keeps this
// module on the driver's single public NextSQLError/Kind surface.
const { createCipheriv, createDecipheriv, randomBytes } = require('node:crypto');
const fs = require('node:fs/promises');

module.exports = function createClientEncryption(deps) {
  const { NextSQLError, Kind, encodeDecimalString, decodeDecimal, decodeNSJB, formatUUID, parseUUID } = deps;
  const PREFIX = 'NSCE1.';
  const MAX_PLAINTEXT = 1 << 20;
  const MAX_KEY_ID = 64;
  const MAX_KEYS = 64;
  const NONCE_SIZE = 12;
  const TAG_SIZE = 16;

  const FieldType = Object.freeze({
    UUID: Object.freeze({ kind: Kind.UUID, precision: 0, scale: 0, vecElem: 0 }),
    String: Object.freeze({ kind: Kind.String, precision: 0, scale: 0, vecElem: 0 }),
    Text: Object.freeze({ kind: Kind.Text, precision: 0, scale: 0, vecElem: 0 }),
    TimestampTZ: Object.freeze({ kind: Kind.TimestampTZ, precision: 0, scale: 0, vecElem: 0 }),
    JSON: Object.freeze({ kind: Kind.JSON, precision: 0, scale: 0, vecElem: 0 }),
    Bool: Object.freeze({ kind: Kind.Bool, precision: 0, scale: 0, vecElem: 0 }),
    Blob: Object.freeze({ kind: Kind.Blob, precision: 0, scale: 0, vecElem: 0 }),
    Int8: Object.freeze({ kind: Kind.Int8, precision: 0, scale: 0, vecElem: 0 }),
    Int16: Object.freeze({ kind: Kind.Int16, precision: 0, scale: 0, vecElem: 0 }),
    Int32: Object.freeze({ kind: Kind.Int32, precision: 0, scale: 0, vecElem: 0 }),
    Int64: Object.freeze({ kind: Kind.Int64, precision: 0, scale: 0, vecElem: 0 }),
    Uint8: Object.freeze({ kind: Kind.Uint8, precision: 0, scale: 0, vecElem: 0 }),
    Uint16: Object.freeze({ kind: Kind.Uint16, precision: 0, scale: 0, vecElem: 0 }),
    Uint32: Object.freeze({ kind: Kind.Uint32, precision: 0, scale: 0, vecElem: 0 }),
    Uint64: Object.freeze({ kind: Kind.Uint64, precision: 0, scale: 0, vecElem: 0 }),
    Decimal(precision, scale) {
      const type = normalizeType({ kind: Kind.Decimal, precision, scale, vecElem: 0 });
      return Object.freeze(type);
    },
  });

  function fail(code, message) { throw new NextSQLError(code, message); }

  function validKeyID(id) {
    return typeof id === 'string' && id.length >= 1 && id.length <= MAX_KEY_ID &&
      /^[A-Za-z0-9._-]+$/.test(id) && Buffer.byteLength(id) === id.length;
  }

  function keyMaterial(key, expectedID) {
    if (!key || !validKeyID(key.id) || (expectedID !== undefined && key.id !== expectedID)) {
      fail('crypto', 'field key unavailable or revoked');
    }
    const material = Buffer.from(key.material || []);
    if (material.length !== 32 || !material.some((b) => b !== 0)) fail('crypto', 'invalid field key');
    return Buffer.from(material);
  }

  function normalizeType(type, code = 'invalid_argument') {
    const t = {
      kind: Number(type && type.kind),
      precision: Number((type && type.precision) || 0),
      scale: Number((type && type.scale) || 0),
      vecElem: Number((type && type.vecElem) || 0),
    };
    const scalar = [
      Kind.UUID, Kind.String, Kind.Text, Kind.Blob, Kind.TimestampTZ, Kind.JSON, Kind.Bool,
      Kind.Int8, Kind.Int16, Kind.Int32, Kind.Int64, Kind.Uint8, Kind.Uint16, Kind.Uint32, Kind.Uint64,
    ].includes(t.kind);
    const decimal = t.kind === Kind.Decimal && Number.isInteger(t.precision) && Number.isInteger(t.scale) &&
      t.precision >= 1 && t.precision <= 38 && t.scale >= 0 && t.scale <= t.precision;
    if ((!scalar && !decimal) || !Number.isInteger(t.kind) || !Number.isInteger(t.precision) ||
        !Number.isInteger(t.scale) || !Number.isInteger(t.vecElem) ||
        (scalar && (t.precision !== 0 || t.scale !== 0)) || t.vecElem !== 0) {
      fail(code, 'unsupported client-encrypted type');
    }
    return t;
  }

  function sameType(a, b) {
    return a.kind === b.kind && a.precision === b.precision && a.scale === b.scale && a.vecElem === b.vecElem;
  }

  function validateName(name) {
    const raw = Buffer.from(typeof name === 'string' ? name : '', 'utf8');
    if (raw.length < 1 || raw.length > 0xffff) fail('invalid_argument', 'database, table, and column are required and bounded');
    return raw;
  }

  function u16(n) { const b = Buffer.alloc(2); b.writeUInt16LE(n); return b; }
  function u32(n) { const b = Buffer.alloc(4); b.writeUInt32LE(n); return b; }
  function lengthBytes(raw) { return Buffer.concat([u32(raw.length), raw]); }

  function readLengthBytes(raw) {
    if (raw.length < 4) fail('invalid_format', 'invalid encrypted value');
    const n = raw.readUInt32LE(0);
    if (n > MAX_PLAINTEXT || n + 4 !== raw.length) fail('invalid_format', 'invalid encrypted value');
    return raw.subarray(4);
  }

  function encodeScalar(type, value) {
    switch (type.kind) {
      case Kind.UUID:
        return Buffer.from(parseUUID(value));
      case Kind.String:
      case Kind.Text:
        if (typeof value !== 'string') fail('invalid_argument', 'encrypted string value must be a string');
        return lengthBytes(Buffer.from(value, 'utf8'));
      case Kind.Blob:
        if (!Buffer.isBuffer(value) && !(value instanceof Uint8Array)) {
          fail('invalid_argument', 'encrypted BLOB value must be a Buffer or Uint8Array');
        }
        return lengthBytes(Buffer.from(value));
      case Kind.Decimal: {
        const normalized = normalizeDecimalValue(type, value);
        const raw = Buffer.from(encodeDecimalString(normalized));
        if (raw.readUInt16LE(6) !== type.scale) fail('invalid_argument', 'decimal scale does not match encrypted column type');
        return raw;
      }
      case Kind.TimestampTZ: {
        const ns = typeof value === 'bigint' ? value :
          value instanceof Date && Number.isFinite(value.getTime()) ? BigInt(value.getTime()) * 1000000n : null;
        if (ns === null || ns < -(1n << 63n) || ns >= (1n << 63n)) fail('invalid_argument', 'TIMESTAMPTZ must be a Date or unix-nanosecond bigint');
        const out = Buffer.alloc(8);
        out.writeBigInt64LE(ns);
        return out;
      }
      case Kind.JSON:
        return lengthBytes(encodeNSJB(value));
      case Kind.Bool:
        if (typeof value !== 'boolean') fail('invalid_argument', 'encrypted BOOL value must be boolean');
        return Buffer.from([value ? 1 : 0]);
      case Kind.Int8:
      case Kind.Int16:
      case Kind.Int32:
      case Kind.Int64:
        return encodeInt(type.kind, value);
      case Kind.Uint8:
      case Kind.Uint16:
      case Kind.Uint32:
      case Kind.Uint64:
        return encodeUint(type.kind, value);
      default:
        fail('invalid_argument', 'unsupported client-encrypted type');
    }
  }

  // encodeInt/decodeInt/encodeUint/decodeUint (D2/D3, Datatype expansion
  // track) use the same fixed-width raw-byte plaintext shape as the server's
  // row encoding (internal/sql/types/row.go encodeScalar) — not the
  // length-prefixed shape STRING/BLOB/DECIMAL use — so any official driver
  // can decrypt a field another driver encrypted.
  const INT_ENC_RANGES = {
    [Kind.Int8]: [-0x80n, 0x7fn, 1],
    [Kind.Int16]: [-0x8000n, 0x7fffn, 2],
    [Kind.Int32]: [-0x80000000n, 0x7fffffffn, 4],
    [Kind.Int64]: [-0x8000000000000000n, 0x7fffffffffffffffn, 8],
  };

  function encodeInt(kind, value) {
    const [lo, hi, width] = INT_ENC_RANGES[kind];
    let n;
    try {
      n = BigInt(value);
    } catch {
      fail('invalid_argument', 'encrypted int value must be an integer');
    }
    if (n < lo || n > hi) fail('invalid_argument', 'encrypted int value out of range');
    const full = Buffer.alloc(8);
    full.writeBigInt64LE(n, 0);
    return full.subarray(0, width);
  }

  function decodeInt(kind, raw) {
    const [, , width] = INT_ENC_RANGES[kind];
    if (raw.length !== width) fail('invalid_format', 'invalid encrypted value');
    const full = Buffer.alloc(8);
    raw.copy(full, 0, 0, width);
    if (width < 8 && (raw[width - 1] & 0x80) !== 0) full.fill(0xff, width);
    const n = full.readBigInt64LE(0);
    return width === 8 ? n : Number(n);
  }

  const UINT_ENC_RANGES = {
    [Kind.Uint8]: [0xffn, 1],
    [Kind.Uint16]: [0xffffn, 2],
    [Kind.Uint32]: [0xffffffffn, 4],
    [Kind.Uint64]: [0xffffffffffffffffn, 8],
  };

  function encodeUint(kind, value) {
    const [hi, width] = UINT_ENC_RANGES[kind];
    let n;
    try {
      n = BigInt(value);
    } catch {
      fail('invalid_argument', 'encrypted uint value must be an integer');
    }
    if (n < 0n || n > hi) fail('invalid_argument', 'encrypted uint value out of range');
    const full = Buffer.alloc(8);
    full.writeBigUInt64LE(n, 0);
    return full.subarray(0, width);
  }

  function decodeUint(kind, raw) {
    const [, width] = UINT_ENC_RANGES[kind];
    if (raw.length !== width) fail('invalid_format', 'invalid encrypted value');
    const full = Buffer.alloc(8);
    raw.copy(full, 0, 0, width);
    const n = full.readBigUInt64LE(0);
    return width === 8 ? n : Number(n);
  }

  function normalizeDecimalValue(type, value) {
    const original = String(value).trim();
    const sign = original.startsWith('-') ? '-' : '';
    let text = original;
    if (text.startsWith('+') || text.startsWith('-')) text = text.slice(1);
    if (!/^\d+(\.\d+)?$/.test(text)) fail('invalid_argument', 'invalid decimal');
    const [whole, inputFraction = ''] = text.split('.');
    let fraction = inputFraction;
    if (fraction.length > type.scale) {
      if (!/^0*$/.test(fraction.slice(type.scale))) fail('invalid_argument', 'decimal would lose scale');
      fraction = fraction.slice(0, type.scale);
    }
    fraction = fraction.padEnd(type.scale, '0');
    const digits = (whole + fraction).replace(/^0+/, '') || '0';
    if (digits.length > type.precision) fail('invalid_argument', 'decimal exceeds encrypted column precision');
    return sign + whole + (type.scale > 0 ? '.' + fraction : '');
  }

  function decodeScalar(type, raw) {
    try {
      switch (type.kind) {
        case Kind.UUID:
          if (raw.length !== 16) fail('invalid_format', 'invalid encrypted value');
          return formatUUID(raw);
        case Kind.String:
        case Kind.Text:
          return new TextDecoder('utf-8', { fatal: true }).decode(readLengthBytes(raw));
        case Kind.Blob:
          return Buffer.from(readLengthBytes(raw));
        case Kind.Decimal:
          return decodeDecimal(readLengthBytes(raw));
        case Kind.TimestampTZ: {
          if (raw.length !== 8) fail('invalid_format', 'invalid encrypted value');
          const ns = raw.readBigInt64LE(0);
          return ns % 1000000n === 0n ? new Date(Number(ns / 1000000n)) : ns;
        }
        case Kind.JSON:
          return decodeNSJB(readLengthBytes(raw));
        case Kind.Bool:
          if (raw.length !== 1 || raw[0] > 1) fail('invalid_format', 'invalid encrypted value');
          return raw[0] === 1;
        case Kind.Int8:
        case Kind.Int16:
        case Kind.Int32:
        case Kind.Int64:
          return decodeInt(type.kind, raw);
        case Kind.Uint8:
        case Kind.Uint16:
        case Kind.Uint32:
        case Kind.Uint64:
          return decodeUint(type.kind, raw);
        default:
          fail('invalid_format', 'unsupported encrypted logical type');
      }
    } catch (err) {
      if (err instanceof NextSQLError) throw err;
      fail('invalid_format', 'invalid encrypted value');
    }
  }

  function encodeNSJB(value) {
    const body = encodeJSONValue(value, 0);
    const out = Buffer.concat([Buffer.from('NSJB\x01', 'binary'), body]);
    if (out.length > MAX_PLAINTEXT) fail('exhausted', 'JSON exceeds field limit');
    return out;
  }

  function jsonUTF8(value) {
    const raw = Buffer.from(value, 'utf8');
    if (raw.toString('utf8') !== value) fail('invalid_argument', 'JSON string is not valid Unicode');
    return raw;
  }

  function encodeJSONValue(value, depth) {
    if (depth > 32) fail('exhausted', 'JSON exceeds depth limit');
    if (value === null) return Buffer.from([0]);
    if (value === false) return Buffer.from([1]);
    if (value === true) return Buffer.from([2]);
    if (typeof value === 'string') return jsonTagged(4, jsonUTF8(value));
    if (typeof value === 'bigint') {
      if (value < -(1n << 63n) || value >= (1n << 63n)) fail('invalid_argument', 'JSON integer is out of range');
      const b = Buffer.alloc(9); b[0] = 3; b.writeBigInt64LE(value, 1); return b;
    }
    if (typeof value === 'number') {
      if (!Number.isFinite(value)) fail('invalid_argument', 'JSON number is not finite');
      if (Number.isSafeInteger(value)) {
        const b = Buffer.alloc(9); b[0] = 3; b.writeBigInt64LE(BigInt(value), 1); return b;
      }
      return jsonTagged(5, Buffer.from(String(value), 'utf8'));
    }
    if (Array.isArray(value)) {
      if (value.length > (1 << 20)) fail('exhausted', 'JSON array exceeds limit');
      const vals = [];
      let size = 4;
      for (const entry of value) {
        const encoded = encodeJSONValue(entry, depth + 1);
        size += encoded.length;
        if (size > MAX_PLAINTEXT) fail('exhausted', 'JSON exceeds field limit');
        vals.push(encoded);
      }
      const body = Buffer.concat([u32(vals.length), ...vals]);
      return Buffer.concat([Buffer.from([6]), u32(body.length), body]);
    }
    if (value && typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype) {
      const keys = Object.keys(value).sort((a, b) => Buffer.compare(jsonUTF8(a), jsonUTF8(b)));
      if (keys.length > 0xffff) fail('exhausted', 'JSON object exceeds limit');
      const entries = [u16(keys.length)];
      let size = 2;
      for (const key of keys) {
        const k = jsonUTF8(key);
        if (k.length > 0xffff) fail('exhausted', 'JSON key exceeds limit');
        const encoded = encodeJSONValue(value[key], depth + 1);
        size += 2 + k.length + encoded.length;
        if (size > MAX_PLAINTEXT) fail('exhausted', 'JSON exceeds field limit');
        entries.push(u16(k.length), k, encoded);
      }
      const body = Buffer.concat(entries);
      return Buffer.concat([Buffer.from([7]), u32(body.length), body]);
    }
    fail('invalid_argument', 'unsupported JSON value');
  }

  function jsonTagged(tag, raw) {
    if (raw.length > MAX_PLAINTEXT) fail('exhausted', 'JSON string exceeds limit');
    return Buffer.concat([Buffer.from([tag]), u32(raw.length), raw]);
  }

  function makeHeader(keyID, type, nonce) {
    const id = Buffer.from(keyID, 'ascii');
    const out = Buffer.alloc(3 + id.length + 6 + NONCE_SIZE);
    out[0] = 1; out[1] = 1; out[2] = id.length; id.copy(out, 3);
    const off = 3 + id.length;
    out[off] = type.kind; out.writeUInt16LE(type.precision, off + 1); out.writeUInt16LE(type.scale, off + 3); out[off + 5] = type.vecElem;
    nonce.copy(out, off + 6);
    return out;
  }

  function aad(database, table, column, publicHeader) {
    const parts = [Buffer.from(PREFIX, 'ascii')];
    for (const name of [database, table, column]) {
      const raw = validateName(name);
      parts.push(u16(raw.length), raw);
    }
    parts.push(publicHeader);
    return Buffer.concat(parts);
  }

  function inspectField(ciphertext) {
    if (typeof ciphertext !== 'string' || !ciphertext.startsWith(PREFIX)) fail('invalid_format', 'invalid client ciphertext prefix');
    const enc = ciphertext.slice(PREFIX.length);
    if (!enc || enc.length % 4 === 1 || !/^[A-Za-z0-9_-]+$/.test(enc) || enc.length > Math.ceil((MAX_PLAINTEXT + 101) * 4 / 3)) {
      fail('invalid_format', 'client ciphertext length out of range');
    }
    const body = Buffer.from(enc, 'base64url');
    if (body.toString('base64url') !== enc || body.length < 38 || body[0] !== 1 || body[1] !== 1) fail('invalid_format', 'unsupported or truncated client ciphertext');
    const n = body[2];
    if (n < 1 || n > MAX_KEY_ID || body.length < 3 + n + 6 + NONCE_SIZE + TAG_SIZE) fail('invalid_format', 'invalid field key id length');
    const keyID = body.subarray(3, 3 + n).toString('ascii');
    if (!validKeyID(keyID)) fail('invalid_format', 'invalid field key id');
    const off = 3 + n;
    const type = normalizeType({ kind: body[off], precision: body.readUInt16LE(off + 1), scale: body.readUInt16LE(off + 3), vecElem: body[off + 5] }, 'invalid_format');
    return { keyID, type, body, headerLength: off + 6 + NONCE_SIZE };
  }

  async function encryptField(provider, database, table, column, type, value) {
    if (value === null || value === undefined) return null;
    if (!provider || typeof provider.currentFieldKey !== 'function') fail('invalid_argument', 'field key provider is required');
    const t = normalizeType(type);
    const plain = encodeScalar(t, value);
    if (plain.length > MAX_PLAINTEXT) fail('exhausted', 'plaintext exceeds field limit');
    let key;
    try { key = await provider.currentFieldKey(database, table, column); } catch { fail('crypto', 'field key unavailable'); }
    const material = keyMaterial(key);
    const nonce = randomBytes(NONCE_SIZE);
    const header = makeHeader(key.id, t, nonce);
    const publicHeader = header.subarray(0, header.length - NONCE_SIZE);
    try {
      const cipher = createCipheriv('aes-256-gcm', material, nonce, { authTagLength: TAG_SIZE });
      cipher.setAAD(aad(database, table, column, publicHeader));
      const sealed = Buffer.concat([cipher.update(plain), cipher.final(), cipher.getAuthTag()]);
      return PREFIX + Buffer.concat([header, sealed]).toString('base64url');
    } catch { fail('crypto', 'field encryption failed'); }
  }

  async function decryptField(provider, database, table, column, expectedType, ciphertext) {
    const expected = normalizeType(expectedType);
    if (ciphertext === null || ciphertext === undefined) return null;
    if (!provider || typeof provider.fieldKey !== 'function') fail('invalid_argument', 'field key provider is required');
    const parsed = inspectField(ciphertext);
    if (!sameType(expected, parsed.type)) fail('invalid_format', 'encrypted logical type mismatch');
    let key;
    try { key = await provider.fieldKey(database, table, column, parsed.keyID); } catch { fail('crypto', 'field key unavailable or revoked'); }
    const material = keyMaterial(key, parsed.keyID);
    const nonce = parsed.body.subarray(parsed.headerLength - NONCE_SIZE, parsed.headerLength);
    const payload = parsed.body.subarray(parsed.headerLength);
    try {
      const decipher = createDecipheriv('aes-256-gcm', material, nonce, { authTagLength: TAG_SIZE });
      decipher.setAAD(aad(database, table, column, parsed.body.subarray(0, parsed.headerLength - NONCE_SIZE)));
      decipher.setAuthTag(payload.subarray(payload.length - TAG_SIZE));
      const plain = Buffer.concat([decipher.update(payload.subarray(0, payload.length - TAG_SIZE)), decipher.final()]);
      if (plain.length > MAX_PLAINTEXT) fail('invalid_format', 'plaintext exceeds field limit');
      return decodeScalar(expected, plain);
    } catch (err) {
      if (err instanceof NextSQLError) throw err;
      fail('crypto', 'ciphertext authentication failed');
    }
  }

  function generateFieldKey(id) {
    if (!validKeyID(id)) fail('invalid_argument', 'invalid field key id');
    return { id, material: randomBytes(32) };
  }

  class MemoryFieldKeyring {
    constructor(current, ...overlap) {
      const keys = [current, ...overlap];
      if (keys.length > MAX_KEYS) fail('invalid_argument', 'too many field keys');
      this._keys = new Map();
      for (const key of keys) {
        const material = keyMaterial(key);
        if (this._keys.has(key.id)) fail('invalid_argument', 'duplicate field key id');
        this._keys.set(key.id, { id: key.id, material });
      }
      this._current = current.id;
    }
    async currentFieldKey() {
      const key = this._keys.get(this._current);
      if (!key) fail('crypto', 'current field key unavailable');
      return { id: key.id, material: Buffer.from(key.material) };
    }
    async fieldKey(_database, _table, _column, id) {
      const key = this._keys.get(id);
      if (!key) fail('crypto', 'field key unavailable or revoked');
      return { id: key.id, material: Buffer.from(key.material) };
    }
    rotate(key) {
      const material = keyMaterial(key);
      if (!this._keys.has(key.id) && this._keys.size >= MAX_KEYS) fail('exhausted', 'field key limit reached');
      this._keys.set(key.id, { id: key.id, material });
      this._current = key.id;
    }
    revoke(id) {
      if (id === this._current) fail('conflict', 'cannot revoke current field key');
      this._keys.delete(id);
    }
  }

  // NSFK1 is the durable field-keyring format used by FileFieldKeyring (see
  // drivers/js/client-encryption.mjs for the identical Bun/Deno codec and
  // drivers/go/nextsql.go for the Go implementation):
  //
  //   magic "NSFK" (4) | version u16=1 | count u16
  //   per record: idLen u8 | id bytes | created u64 (unix seconds) |
  //     flags u8 (bit0=current, bit1=revoked) | material [32]byte
  //     (all-zero when revoked)
  const FIELD_KEYRING_MAGIC = 'NSFK';
  const FIELD_KEYRING_VERSION = 1;
  const FK_FLAG_CURRENT = 1 << 0;
  const FK_FLAG_REVOKED = 1 << 1;

  function u16(n) {
    const b = Buffer.alloc(2);
    b.writeUInt16LE(n);
    return b;
  }

  function encodeFieldKeyring(records) {
    if (records.length > MAX_KEYS) fail('invalid_argument', 'too many field keys');
    const parts = [Buffer.from(FIELD_KEYRING_MAGIC, 'ascii'), u16(FIELD_KEYRING_VERSION), u16(records.length)];
    for (const rec of records) {
      if (!validKeyID(rec.id)) fail('invalid_format', 'invalid field key id length');
      const idBuf = Buffer.from(rec.id, 'ascii');
      const material = Buffer.from(rec.material || []);
      if (material.length !== 32) fail('invalid_format', 'invalid field key material size');
      let flags = 0;
      if (rec.current) flags |= FK_FLAG_CURRENT;
      if (rec.revoked) flags |= FK_FLAG_REVOKED;
      const created = Buffer.alloc(8);
      created.writeBigUInt64LE(BigInt(Math.trunc(rec.created)));
      parts.push(Buffer.from([idBuf.length]), idBuf, created, Buffer.from([flags]), material);
    }
    return Buffer.concat(parts);
  }

  function decodeFieldKeyring(input) {
    const raw = Buffer.isBuffer(input) ? input : Buffer.from(input);
    const bad = (msg) => fail('invalid_format', msg);
    if (raw.length < 8) bad('truncated keyring');
    if (raw.toString('ascii', 0, 4) !== FIELD_KEYRING_MAGIC) bad('bad keyring magic');
    if (raw.readUInt16LE(4) !== FIELD_KEYRING_VERSION) bad('unsupported keyring version');
    const count = raw.readUInt16LE(6);
    if (count > MAX_KEYS) bad('key count exceeds limit');
    const records = [];
    const seen = new Set();
    let off = 8;
    let currentCount = 0;
    for (let i = 0; i < count; i++) {
      if (off >= raw.length) bad('truncated id length');
      const idLen = raw[off];
      off += 1;
      if (idLen < 1 || idLen > MAX_KEY_ID) bad('invalid field key id length');
      if (off + idLen > raw.length) bad('truncated field key id');
      const id = raw.toString('ascii', off, off + idLen);
      off += idLen;
      if (!validKeyID(id)) bad('invalid field key id');
      if (off + 8 > raw.length) bad('truncated created time');
      const created = Number(raw.readBigUInt64LE(off));
      off += 8;
      if (off >= raw.length) bad('truncated flags');
      const flags = raw[off];
      off += 1;
      if (off + 32 > raw.length) bad('truncated field key material');
      const material = Buffer.from(raw.subarray(off, off + 32));
      off += 32;
      if (seen.has(id)) bad('duplicate field key id');
      seen.add(id);
      const current = (flags & FK_FLAG_CURRENT) !== 0;
      const revoked = (flags & FK_FLAG_REVOKED) !== 0;
      if (current && revoked) bad('current field key cannot be revoked');
      const allZero = !material.some((b) => b !== 0);
      if (revoked) {
        if (!allZero) bad('revoked field key retains material');
      } else if (allZero) {
        bad('empty field key material');
      }
      if (current) currentCount++;
      records.push({ id, created, current, revoked, material });
    }
    if (off !== raw.length) bad('trailing keyring bytes');
    if (records.length === 0) bad('keyring has no keys');
    if (currentCount !== 1) bad('keyring must have exactly one current key');
    return records;
  }

  // FileFieldKeyring is a durable, atomic, file-backed FieldKeyProvider:
  // rotation and revocation persist across process restarts, unlike
  // MemoryFieldKeyring. Production applications with an existing secret
  // manager or KMS should implement the provider contract directly against
  // that system instead.
  class FileFieldKeyring {
    constructor(path, records) {
      this._path = path;
      this._records = records;
    }

    static async create(path, current) {
      try {
        await fs.stat(path);
        fail('already_exists', 'keyring file exists');
      } catch (err) {
        if (err.code !== 'ENOENT') throw err;
      }
      const material = keyMaterial(current);
      const record = { id: current.id, created: Math.floor(Date.now() / 1000), current: true, revoked: false, material };
      const kr = new FileFieldKeyring(path, [record]);
      await kr._persist();
      return kr;
    }

    static async open(path) {
      const raw = await fs.readFile(path);
      const records = decodeFieldKeyring(raw);
      return new FileFieldKeyring(path, records);
    }

    get path() {
      return this._path;
    }

    async currentFieldKey() {
      const rec = this._records.find((r) => r.current && !r.revoked);
      if (!rec) fail('crypto', 'current field key unavailable');
      return { id: rec.id, material: Buffer.from(rec.material) };
    }

    async fieldKey(_database, _table, _column, id) {
      const rec = this._records.find((r) => r.id === id);
      if (!rec || rec.revoked) fail('crypto', 'field key unavailable or revoked');
      return { id: rec.id, material: Buffer.from(rec.material) };
    }

    async rotate(key) {
      const material = keyMaterial(key);
      let rec = this._records.find((r) => r.id === key.id);
      if (rec && rec.revoked) fail('conflict', 'cannot reuse a revoked field key id');
      if (!rec) {
        if (this._records.length >= MAX_KEYS) fail('exhausted', 'field key limit reached');
        rec = { id: key.id, created: Math.floor(Date.now() / 1000), current: false, revoked: false, material };
        this._records.push(rec);
      }
      for (const r of this._records) r.current = false;
      rec.current = true;
      rec.material = material;
      await this._persist();
    }

    async revoke(id) {
      const rec = this._records.find((r) => r.id === id);
      if (!rec) fail('not_found', 'unknown field key id');
      if (rec.current) fail('conflict', 'cannot revoke the current field key');
      if (rec.revoked) return;
      rec.revoked = true;
      rec.material = Buffer.alloc(32);
      await this._persist();
    }

    async reload() {
      const raw = await fs.readFile(this._path);
      this._records = decodeFieldKeyring(raw);
    }

    list() {
      return this._records.map((r) => ({ id: r.id, created: r.created, current: r.current, revoked: r.revoked }));
    }

    async _persist() {
      const raw = encodeFieldKeyring(this._records);
      const tmp = `${this._path}.tmp`;
      await fs.writeFile(tmp, raw, { mode: 0o600 });
      await fs.rename(tmp, this._path);
    }
  }

  return { FieldType, MemoryFieldKeyring, FileFieldKeyring, decryptField, encryptField, generateFieldKey, inspectField };
};
