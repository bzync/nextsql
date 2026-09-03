// Portable NSCE1 client-field encryption for the Bun and Deno drivers.
// Field keys remain in the caller-supplied provider and never cross NSQL.

import {
  decodeDecimal,
  decodeNSJB,
  encodeDecimalString,
  formatUUID,
  Kind,
  NextSQLError,
  parseUUID,
  putU64,
} from "./protocol.mjs";

export const FieldType = Object.freeze({
  UUID: Object.freeze({ kind: Kind.UUID, precision: 0, scale: 0, vecElem: 0 }),
  String: Object.freeze({
    kind: Kind.String,
    precision: 0,
    scale: 0,
    vecElem: 0,
  }),
  Text: Object.freeze({ kind: Kind.Text, precision: 0, scale: 0, vecElem: 0 }),
  TimestampTZ: Object.freeze({
    kind: Kind.TimestampTZ,
    precision: 0,
    scale: 0,
    vecElem: 0,
  }),
  JSON: Object.freeze({ kind: Kind.JSON, precision: 0, scale: 0, vecElem: 0 }),
  Bool: Object.freeze({ kind: Kind.Bool, precision: 0, scale: 0, vecElem: 0 }),
  Blob: Object.freeze({ kind: Kind.Blob, precision: 0, scale: 0, vecElem: 0 }),
  Int8: Object.freeze({ kind: Kind.Int8, precision: 0, scale: 0, vecElem: 0 }),
  Int16: Object.freeze({
    kind: Kind.Int16,
    precision: 0,
    scale: 0,
    vecElem: 0,
  }),
  Int32: Object.freeze({
    kind: Kind.Int32,
    precision: 0,
    scale: 0,
    vecElem: 0,
  }),
  Int64: Object.freeze({
    kind: Kind.Int64,
    precision: 0,
    scale: 0,
    vecElem: 0,
  }),
  Uint8: Object.freeze({
    kind: Kind.Uint8,
    precision: 0,
    scale: 0,
    vecElem: 0,
  }),
  Uint16: Object.freeze({
    kind: Kind.Uint16,
    precision: 0,
    scale: 0,
    vecElem: 0,
  }),
  Uint32: Object.freeze({
    kind: Kind.Uint32,
    precision: 0,
    scale: 0,
    vecElem: 0,
  }),
  Uint64: Object.freeze({
    kind: Kind.Uint64,
    precision: 0,
    scale: 0,
    vecElem: 0,
  }),
  Decimal(precision, scale) {
    const t = {
      kind: Kind.Decimal,
      precision: Number(precision),
      scale: Number(scale),
      vecElem: 0,
    };
    validateType(t, "invalid_argument");
    return Object.freeze(t);
  },
});

const PREFIX = "NSCE1.";
const VERSION = 1;
const SUITE_AES_256_GCM = 1;
const KEY_SIZE = 32;
const MAX_KEY_ID = 64;
const MAX_KEYS = 64;
const MAX_PLAINTEXT = 1 << 20;
const NONCE_SIZE = 12;
const TAG_SIZE = 16;
const te = new TextEncoder();
const td = new TextDecoder("utf-8", { fatal: true });

function fail(code, message) {
  throw new NextSQLError(code, message);
}

function cryptoAPI() {
  const c = globalThis.crypto;
  if (!c || !c.subtle || typeof c.getRandomValues !== "function") {
    fail("crypto", "Web Crypto AES-GCM is unavailable");
  }
  return c;
}

function validKeyID(id) {
  return typeof id === "string" && id.length >= 1 && id.length <= MAX_KEY_ID &&
    /^[A-Za-z0-9._-]+$/.test(id) && te.encode(id).length === id.length;
}

function keyMaterial(key, expectedID) {
  if (
    !key || !validKeyID(key.id) ||
    (expectedID !== undefined && key.id !== expectedID)
  ) {
    fail("crypto", "field key unavailable or revoked");
  }
  const material = key.material instanceof Uint8Array
    ? key.material
    : new Uint8Array(key.material || []);
  if (material.length !== KEY_SIZE || !material.some((b) => b !== 0)) {
    fail("crypto", "invalid field key");
  }
  return material.slice();
}

function validateName(value) {
  if (typeof value !== "string") {
    fail(
      "invalid_argument",
      "database, table, and column are required and bounded",
    );
  }
  const raw = te.encode(value);
  if (raw.length < 1 || raw.length > 0xffff) {
    fail(
      "invalid_argument",
      "database, table, and column are required and bounded",
    );
  }
  return raw;
}

function normalizeType(type, code = "invalid_argument") {
  const t = {
    kind: Number(type && type.kind),
    precision: Number((type && type.precision) || 0),
    scale: Number((type && type.scale) || 0),
    vecElem: Number((type && type.vecElem) || 0),
  };
  validateType(t, code);
  return t;
}

function validateType(t, code) {
  const scalar = t.kind === Kind.UUID || t.kind === Kind.String ||
    t.kind === Kind.Text || t.kind === Kind.Blob ||
    t.kind === Kind.TimestampTZ || t.kind === Kind.JSON ||
    t.kind === Kind.Bool || t.kind === Kind.Int8 || t.kind === Kind.Int16 ||
    t.kind === Kind.Int32 || t.kind === Kind.Int64 || t.kind === Kind.Uint8 ||
    t.kind === Kind.Uint16 || t.kind === Kind.Uint32 || t.kind === Kind.Uint64;
  const decimal = t.kind === Kind.Decimal && Number.isInteger(t.precision) &&
    Number.isInteger(t.scale) &&
    t.precision >= 1 && t.precision <= 38 && t.scale >= 0 &&
    t.scale <= t.precision;
  if (
    (!scalar && !decimal) || !Number.isInteger(t.kind) ||
    !Number.isInteger(t.precision) ||
    !Number.isInteger(t.scale) || !Number.isInteger(t.vecElem) ||
    (scalar && (t.precision !== 0 || t.scale !== 0)) || t.vecElem !== 0
  ) {
    fail(code, "unsupported client-encrypted type");
  }
}

function sameType(a, b) {
  return a.kind === b.kind && a.precision === b.precision &&
    a.scale === b.scale && a.vecElem === b.vecElem;
}

function putU16(n) {
  const b = new Uint8Array(2);
  new DataView(b.buffer).setUint16(0, n, true);
  return b;
}

function putU32(n) {
  const b = new Uint8Array(4);
  new DataView(b.buffer).setUint32(0, n, true);
  return b;
}

function concat(parts) {
  const n = parts.reduce((sum, p) => sum + p.length, 0);
  const out = new Uint8Array(n);
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

function lengthBytes(raw) {
  return concat([putU32(raw.length), raw]);
}

function readLengthBytes(raw) {
  if (raw.length < 4) {
    fail("invalid_format", "invalid encrypted value");
  }
  const n = new DataView(raw.buffer, raw.byteOffset, 4).getUint32(0, true);
  if (n > MAX_PLAINTEXT || n + 4 !== raw.length) {
    fail("invalid_format", "invalid encrypted value");
  }
  return raw.subarray(4);
}

function timestampNanos(value) {
  if (typeof value === "bigint") {
    return value;
  }
  if (value instanceof Date && Number.isFinite(value.getTime())) {
    return BigInt(value.getTime()) * 1000000n;
  }
  fail(
    "invalid_argument",
    "TIMESTAMPTZ must be a Date or unix-nanosecond bigint",
  );
}

function encodeScalar(type, value) {
  switch (type.kind) {
    case Kind.UUID:
      return parseUUID(value);
    case Kind.String:
    case Kind.Text:
      if (typeof value !== "string") {
        fail("invalid_argument", "encrypted string value must be a string");
      }
      return lengthBytes(te.encode(value));
    case Kind.Blob:
      if (!(value instanceof Uint8Array)) {
        fail("invalid_argument", "encrypted BLOB value must be a Uint8Array");
      }
      return lengthBytes(value);
    case Kind.Decimal: {
      const normalized = normalizeDecimalValue(type, value);
      const raw = encodeDecimalString(normalized);
      if (
        raw[6] !== (type.scale & 0xff) || raw[7] !== ((type.scale >>> 8) & 0xff)
      ) {
        fail(
          "invalid_argument",
          "decimal scale does not match encrypted column type",
        );
      }
      return raw;
    }
    case Kind.TimestampTZ: {
      const ns = timestampNanos(value);
      if (ns < -(1n << 63n) || ns >= (1n << 63n)) {
        fail("invalid_argument", "timestamp is out of range");
      }
      const out = new Uint8Array(8);
      new DataView(out.buffer).setBigInt64(0, ns, true);
      return out;
    }
    case Kind.JSON:
      return lengthBytes(encodeNSJB(value));
    case Kind.Bool:
      if (typeof value !== "boolean") {
        fail("invalid_argument", "encrypted BOOL value must be boolean");
      }
      return Uint8Array.of(value ? 1 : 0);
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
      fail("invalid_argument", "unsupported client-encrypted type");
  }
}

// encodeInt/decodeInt (D2, Datatype expansion track) use the same
// fixed-width raw-byte plaintext shape as the server's row encoding
// (internal/sql/types/row.go encodeScalar) — not the length-prefixed shape
// STRING/BLOB/DECIMAL use — so any official driver can decrypt a field
// another driver encrypted. encodeUint/decodeUint (D3) mirror this exactly.
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
    fail("invalid_argument", "encrypted int value must be an integer");
  }
  if (n < lo || n > hi) {
    fail("invalid_argument", "encrypted int value out of range");
  }
  const full = new Uint8Array(8);
  new DataView(full.buffer).setBigInt64(0, n, true);
  return full.subarray(0, width);
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
    fail("invalid_argument", "encrypted uint value must be an integer");
  }
  if (n < 0n || n > hi) {
    fail("invalid_argument", "encrypted uint value out of range");
  }
  const full = new Uint8Array(8);
  new DataView(full.buffer).setBigUint64(0, n, true);
  return full.subarray(0, width);
}

function decodeInt(kind, raw) {
  const [, , width] = INT_ENC_RANGES[kind];
  if (raw.length !== width) {
    fail("invalid_format", "invalid encrypted value");
  }
  const full = new Uint8Array(8);
  full.set(raw);
  // Sign-extend: replicate the top bit of the narrow value into the
  // padding bytes before reading as a full 64-bit value.
  if (width < 8 && (raw[width - 1] & 0x80) !== 0) {
    full.fill(0xff, width);
  }
  const n = new DataView(full.buffer).getBigInt64(0, true);
  return width === 8 ? n : Number(n);
}

function decodeUint(kind, raw) {
  const [, width] = UINT_ENC_RANGES[kind];
  if (raw.length !== width) {
    fail("invalid_format", "invalid encrypted value");
  }
  const full = new Uint8Array(8);
  full.set(raw);
  const n = new DataView(full.buffer).getBigUint64(0, true);
  return width === 8 ? n : Number(n);
}

function normalizeDecimalValue(type, value) {
  const original = String(value).trim();
  const sign = original.startsWith("-") ? "-" : "";
  let text = original;
  if (text.startsWith("+") || text.startsWith("-")) text = text.slice(1);
  if (!/^\d+(\.\d+)?$/.test(text)) fail("invalid_argument", "invalid decimal");
  const [whole, inputFraction = ""] = text.split(".");
  let fraction = inputFraction;
  if (fraction.length > type.scale) {
    if (!/^0*$/.test(fraction.slice(type.scale))) {
      fail("invalid_argument", "decimal would lose scale");
    }
    fraction = fraction.slice(0, type.scale);
  }
  fraction = fraction.padEnd(type.scale, "0");
  const digits = (whole + fraction).replace(/^0+/, "") || "0";
  if (digits.length > type.precision) {
    fail("invalid_argument", "decimal exceeds encrypted column precision");
  }
  return sign + whole + (type.scale > 0 ? "." + fraction : "");
}

function decodeScalar(type, raw) {
  try {
    switch (type.kind) {
      case Kind.UUID:
        if (raw.length !== 16) {
          fail("invalid_format", "invalid encrypted value");
        }
        return formatUUID(raw);
      case Kind.String:
      case Kind.Text:
        return td.decode(readLengthBytes(raw));
      case Kind.Blob:
        return readLengthBytes(raw);
      case Kind.Decimal:
        return decodeDecimal(readLengthBytes(raw));
      case Kind.TimestampTZ: {
        if (raw.length !== 8) fail("invalid_format", "invalid encrypted value");
        const ns = new DataView(raw.buffer, raw.byteOffset, 8).getBigInt64(
          0,
          true,
        );
        return ns % 1000000n === 0n ? new Date(Number(ns / 1000000n)) : ns;
      }
      case Kind.JSON:
        return decodeNSJB(readLengthBytes(raw));
      case Kind.Bool:
        if (raw.length !== 1 || raw[0] > 1) {
          fail("invalid_format", "invalid encrypted value");
        }
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
        fail("invalid_format", "unsupported encrypted logical type");
    }
  } catch (err) {
    if (err instanceof NextSQLError) throw err;
    fail("invalid_format", "invalid encrypted value");
  }
}

function encodeNSJB(value) {
  const body = encodeJSONValue(value, 0);
  const out = new Uint8Array(5 + body.length);
  out.set(te.encode("NSJB"), 0);
  out[4] = 1;
  out.set(body, 5);
  if (out.length > MAX_PLAINTEXT) fail("exhausted", "JSON exceeds field limit");
  return out;
}

function jsonUTF8(value) {
  const raw = te.encode(value);
  if (td.decode(raw) !== value) {
    fail("invalid_argument", "JSON string is not valid Unicode");
  }
  return raw;
}

function encodeJSONValue(value, depth) {
  if (depth > 32) fail("exhausted", "JSON exceeds depth limit");
  if (value === null) return Uint8Array.of(0);
  if (value === false) return Uint8Array.of(1);
  if (value === true) return Uint8Array.of(2);
  if (typeof value === "string") return jsonTagged(4, jsonUTF8(value));
  if (typeof value === "bigint") {
    if (value < -(1n << 63n) || value >= (1n << 63n)) {
      fail("invalid_argument", "JSON integer is out of range");
    }
    const b = new Uint8Array(9);
    b[0] = 3;
    new DataView(b.buffer).setBigInt64(1, value, true);
    return b;
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      fail("invalid_argument", "JSON number is not finite");
    }
    if (Number.isSafeInteger(value)) {
      const b = new Uint8Array(9);
      b[0] = 3;
      new DataView(b.buffer).setBigInt64(1, BigInt(value), true);
      return b;
    }
    return jsonTagged(5, te.encode(String(value)));
  }
  if (Array.isArray(value)) {
    if (value.length > (1 << 20)) fail("exhausted", "JSON array exceeds limit");
    const vals = [];
    let size = 4;
    for (const entry of value) {
      const encoded = encodeJSONValue(entry, depth + 1);
      size += encoded.length;
      if (size > MAX_PLAINTEXT) fail("exhausted", "JSON exceeds field limit");
      vals.push(encoded);
    }
    const body = concat([putU32(vals.length), ...vals]);
    return concat([Uint8Array.of(6), putU32(body.length), body]);
  }
  if (
    value && typeof value === "object" &&
    Object.getPrototypeOf(value) === Object.prototype
  ) {
    const keys = Object.keys(value).sort((a, b) => {
      const aa = jsonUTF8(a);
      const bb = jsonUTF8(b);
      const n = Math.min(aa.length, bb.length);
      for (let i = 0; i < n; i++) if (aa[i] !== bb[i]) return aa[i] - bb[i];
      return aa.length - bb.length;
    });
    if (keys.length > 0xffff) fail("exhausted", "JSON object exceeds limit");
    const entries = [putU16(keys.length)];
    let size = 2;
    for (const key of keys) {
      const k = jsonUTF8(key);
      if (k.length > 0xffff) fail("exhausted", "JSON key exceeds limit");
      const encoded = encodeJSONValue(value[key], depth + 1);
      size += 2 + k.length + encoded.length;
      if (size > MAX_PLAINTEXT) fail("exhausted", "JSON exceeds field limit");
      entries.push(putU16(k.length), k, encoded);
    }
    const body = concat(entries);
    return concat([Uint8Array.of(7), putU32(body.length), body]);
  }
  fail("invalid_argument", "unsupported JSON value");
}

function jsonTagged(tag, raw) {
  if (raw.length > MAX_PLAINTEXT) {
    fail("exhausted", "JSON string exceeds limit");
  }
  return concat([Uint8Array.of(tag), putU32(raw.length), raw]);
}

function header(keyID, type, nonce) {
  const id = te.encode(keyID);
  const out = new Uint8Array(3 + id.length + 6 + NONCE_SIZE);
  out[0] = VERSION;
  out[1] = SUITE_AES_256_GCM;
  out[2] = id.length;
  out.set(id, 3);
  const off = 3 + id.length;
  out[off] = type.kind;
  new DataView(out.buffer).setUint16(off + 1, type.precision, true);
  new DataView(out.buffer).setUint16(off + 3, type.scale, true);
  out[off + 5] = type.vecElem;
  out.set(nonce, off + 6);
  return out;
}

function aad(database, table, column, publicHeader) {
  const parts = [te.encode(PREFIX)];
  for (const name of [database, table, column]) {
    const raw = validateName(name);
    parts.push(putU16(raw.length), raw);
  }
  parts.push(publicHeader);
  return concat(parts);
}

function base64url(raw) {
  const alphabet =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
  let out = "";
  for (let i = 0; i < raw.length; i += 3) {
    const a = raw[i];
    const b = i + 1 < raw.length ? raw[i + 1] : 0;
    const c = i + 2 < raw.length ? raw[i + 2] : 0;
    out += alphabet[a >>> 2] + alphabet[((a & 3) << 4) | (b >>> 4)];
    if (i + 1 < raw.length) out += alphabet[((b & 15) << 2) | (c >>> 6)];
    if (i + 2 < raw.length) out += alphabet[c & 63];
  }
  return out;
}

function unbase64url(text) {
  if (
    typeof text !== "string" || text.length === 0 || text.length % 4 === 1 ||
    !/^[A-Za-z0-9_-]+$/.test(text)
  ) {
    fail("invalid_format", "invalid client ciphertext encoding");
  }
  const table = new Int16Array(128).fill(-1);
  const alphabet =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
  for (let i = 0; i < alphabet.length; i++) table[alphabet.charCodeAt(i)] = i;
  const out = new Uint8Array(Math.floor(text.length * 3 / 4));
  let oi = 0;
  for (let i = 0; i < text.length; i += 4) {
    const n = Math.min(4, text.length - i);
    let bits = 0;
    for (let j = 0; j < n; j++) {
      bits = (bits << 6) | table[text.charCodeAt(i + j)];
    }
    bits <<= (4 - n) * 6;
    out[oi++] = (bits >>> 16) & 0xff;
    if (n >= 3) out[oi++] = (bits >>> 8) & 0xff;
    if (n === 4) out[oi++] = bits & 0xff;
  }
  if (base64url(out) !== text) {
    fail("invalid_format", "non-canonical client ciphertext encoding");
  }
  return out;
}

export function inspectField(ciphertext) {
  if (typeof ciphertext !== "string" || !ciphertext.startsWith(PREFIX)) {
    fail("invalid_format", "invalid client ciphertext prefix");
  }
  const encoded = ciphertext.slice(PREFIX.length);
  if (
    encoded.length >
      Math.ceil(
        (MAX_PLAINTEXT + 3 + MAX_KEY_ID + 6 + NONCE_SIZE + TAG_SIZE) * 4 / 3,
      )
  ) {
    fail("invalid_format", "client ciphertext length out of range");
  }
  const body = unbase64url(encoded);
  if (
    body.length < 3 + 1 + 6 + NONCE_SIZE + TAG_SIZE || body[0] !== VERSION ||
    body[1] !== SUITE_AES_256_GCM
  ) {
    fail("invalid_format", "unsupported or truncated client ciphertext");
  }
  const n = body[2];
  if (
    n < 1 || n > MAX_KEY_ID || body.length < 3 + n + 6 + NONCE_SIZE + TAG_SIZE
  ) {
    fail("invalid_format", "invalid field key id length");
  }
  let keyID;
  try {
    keyID = td.decode(body.subarray(3, 3 + n));
  } catch {
    fail("invalid_format", "invalid field key id");
  }
  if (!validKeyID(keyID)) fail("invalid_format", "invalid field key id");
  const off = 3 + n;
  const type = normalizeType({
    kind: body[off],
    precision: new DataView(body.buffer, body.byteOffset).getUint16(
      off + 1,
      true,
    ),
    scale: new DataView(body.buffer, body.byteOffset).getUint16(off + 3, true),
    vecElem: body[off + 5],
  }, "invalid_format");
  return { keyID, type, body, headerLength: off + 6 + NONCE_SIZE };
}

export async function encryptField(
  provider,
  database,
  table,
  column,
  type,
  value,
) {
  if (value === null || value === undefined) return null;
  if (!provider || typeof provider.currentFieldKey !== "function") {
    fail("invalid_argument", "field key provider is required");
  }
  const t = normalizeType(type);
  const plain = encodeScalar(t, value);
  if (plain.length > MAX_PLAINTEXT) {
    fail("exhausted", "plaintext exceeds field limit");
  }
  let key;
  try {
    key = await provider.currentFieldKey(database, table, column);
  } catch {
    fail("crypto", "field key unavailable");
  }
  const material = keyMaterial(key);
  const nonce = new Uint8Array(NONCE_SIZE);
  cryptoAPI().getRandomValues(nonce);
  const hdr = header(key.id, t, nonce);
  const publicHeader = hdr.subarray(0, hdr.length - NONCE_SIZE);
  let sealed;
  try {
    const imported = await cryptoAPI().subtle.importKey(
      "raw",
      material,
      "AES-GCM",
      false,
      ["encrypt"],
    );
    sealed = new Uint8Array(
      await cryptoAPI().subtle.encrypt(
        {
          name: "AES-GCM",
          iv: nonce,
          additionalData: aad(database, table, column, publicHeader),
          tagLength: 128,
        },
        imported,
        plain,
      ),
    );
  } catch {
    fail("crypto", "field encryption failed");
  }
  return PREFIX + base64url(concat([hdr, sealed]));
}

export async function decryptField(
  provider,
  database,
  table,
  column,
  expectedType,
  ciphertext,
) {
  const expected = normalizeType(expectedType);
  if (ciphertext === null || ciphertext === undefined) return null;
  if (!provider || typeof provider.fieldKey !== "function") {
    fail("invalid_argument", "field key provider is required");
  }
  const parsed = inspectField(ciphertext);
  if (!sameType(expected, parsed.type)) {
    fail("invalid_format", "encrypted logical type mismatch");
  }
  let key;
  try {
    key = await provider.fieldKey(database, table, column, parsed.keyID);
  } catch {
    fail("crypto", "field key unavailable or revoked");
  }
  const material = keyMaterial(key, parsed.keyID);
  const nonce = parsed.body.subarray(
    parsed.headerLength - NONCE_SIZE,
    parsed.headerLength,
  );
  const publicHeader = parsed.body.subarray(
    0,
    parsed.headerLength - NONCE_SIZE,
  );
  let plain;
  try {
    const imported = await cryptoAPI().subtle.importKey(
      "raw",
      material,
      "AES-GCM",
      false,
      ["decrypt"],
    );
    plain = new Uint8Array(
      await cryptoAPI().subtle.decrypt(
        {
          name: "AES-GCM",
          iv: nonce,
          additionalData: aad(database, table, column, publicHeader),
          tagLength: 128,
        },
        imported,
        parsed.body.subarray(parsed.headerLength),
      ),
    );
  } catch {
    fail("crypto", "ciphertext authentication failed");
  }
  if (plain.length > MAX_PLAINTEXT) {
    fail("invalid_format", "plaintext exceeds field limit");
  }
  return decodeScalar(expected, plain);
}

export function generateFieldKey(id) {
  if (!validKeyID(id)) fail("invalid_argument", "invalid field key id");
  const material = new Uint8Array(KEY_SIZE);
  cryptoAPI().getRandomValues(material);
  return { id, material };
}

export class MemoryFieldKeyring {
  constructor(current, ...overlap) {
    const keys = [current, ...overlap];
    if (keys.length > MAX_KEYS) fail("invalid_argument", "too many field keys");
    this._keys = new Map();
    for (const key of keys) {
      const material = keyMaterial(key);
      if (this._keys.has(key.id)) {
        fail("invalid_argument", "duplicate field key id");
      }
      this._keys.set(key.id, { id: key.id, material });
    }
    this._current = current.id;
  }

  async currentFieldKey() {
    const key = this._keys.get(this._current);
    if (!key) fail("crypto", "current field key unavailable");
    return { id: key.id, material: key.material.slice() };
  }

  async fieldKey(_database, _table, _column, id) {
    const key = this._keys.get(id);
    if (!key) fail("crypto", "field key unavailable or revoked");
    return { id: key.id, material: key.material.slice() };
  }

  rotate(key) {
    const material = keyMaterial(key);
    if (!this._keys.has(key.id) && this._keys.size >= MAX_KEYS) {
      fail("exhausted", "field key limit reached");
    }
    this._keys.set(key.id, { id: key.id, material });
    this._current = key.id;
  }

  revoke(id) {
    if (id === this._current) {
      fail("conflict", "cannot revoke current field key");
    }
    this._keys.delete(id);
  }
}

// NSFK1 is the durable field-keyring format used by FileFieldKeyring
// (implemented per runtime in drivers/bun, drivers/deno, drivers/node since
// each has its own native file API). This module owns the pure, I/O-free
// codec so every runtime encodes/decodes an identical byte format:
//
//   magic "NSFK" (4) | version u16=1 | count u16
//   per record: idLen u8 | id bytes | created u64 (unix seconds) |
//     flags u8 (bit0=current, bit1=revoked) | material [32]byte
//     (all-zero when revoked)
//
// Records are {id, created, current, revoked, material}; created is a
// Number of unix seconds and material is a 32-byte Uint8Array.
const FIELD_KEYRING_MAGIC = [0x4e, 0x53, 0x46, 0x4b]; // "NSFK"
const FIELD_KEYRING_VERSION = 1;
const MAX_KEYRING_KEYS = MAX_KEYS;
const FK_FLAG_CURRENT = 1 << 0;
const FK_FLAG_REVOKED = 1 << 1;

export function encodeFieldKeyring(records) {
  if (records.length > MAX_KEYRING_KEYS) {
    fail("invalid_argument", "too many field keys");
  }
  const parts = [
    Uint8Array.from(FIELD_KEYRING_MAGIC),
    putU16(FIELD_KEYRING_VERSION),
    putU16(records.length),
  ];
  for (const rec of records) {
    if (!validKeyID(rec.id)) {
      fail("invalid_format", "invalid field key id length");
    }
    const idBytes = te.encode(rec.id);
    const material = rec.material instanceof Uint8Array
      ? rec.material
      : new Uint8Array(rec.material || []);
    if (material.length !== KEY_SIZE) {
      fail("invalid_format", "invalid field key material size");
    }
    let flags = 0;
    if (rec.current) flags |= FK_FLAG_CURRENT;
    if (rec.revoked) flags |= FK_FLAG_REVOKED;
    parts.push(
      Uint8Array.of(idBytes.length),
      idBytes,
      putU64(Math.trunc(rec.created)),
      Uint8Array.of(flags),
      material,
    );
  }
  return concat(parts);
}

export function decodeFieldKeyring(input) {
  const raw = input instanceof Uint8Array ? input : new Uint8Array(input);
  const bad = (msg) => fail("invalid_format", msg);
  if (raw.length < 8) bad("truncated keyring");
  if (
    raw[0] !== FIELD_KEYRING_MAGIC[0] || raw[1] !== FIELD_KEYRING_MAGIC[1] ||
    raw[2] !== FIELD_KEYRING_MAGIC[2] || raw[3] !== FIELD_KEYRING_MAGIC[3]
  ) {
    bad("bad keyring magic");
  }
  const dv = new DataView(raw.buffer, raw.byteOffset, raw.byteLength);
  if (dv.getUint16(4, true) !== FIELD_KEYRING_VERSION) {
    bad("unsupported keyring version");
  }
  const count = dv.getUint16(6, true);
  if (count > MAX_KEYRING_KEYS) bad("key count exceeds limit");
  const records = [];
  const seen = new Set();
  let off = 8;
  let currentCount = 0;
  for (let i = 0; i < count; i++) {
    if (off >= raw.length) bad("truncated id length");
    const idLen = raw[off];
    off += 1;
    if (idLen < 1 || idLen > MAX_KEY_ID) bad("invalid field key id length");
    if (off + idLen > raw.length) bad("truncated field key id");
    let id;
    try {
      id = td.decode(raw.subarray(off, off + idLen));
    } catch {
      bad("invalid field key id");
    }
    off += idLen;
    if (!validKeyID(id)) bad("invalid field key id");
    if (off + 8 > raw.length) bad("truncated created time");
    const created = Number(
      new DataView(raw.buffer, raw.byteOffset + off, 8).getBigUint64(0, true),
    );
    off += 8;
    if (off >= raw.length) bad("truncated flags");
    const flags = raw[off];
    off += 1;
    if (off + KEY_SIZE > raw.length) bad("truncated field key material");
    const material = raw.slice(off, off + KEY_SIZE);
    off += KEY_SIZE;
    if (seen.has(id)) bad("duplicate field key id");
    seen.add(id);
    const current = (flags & FK_FLAG_CURRENT) !== 0;
    const revoked = (flags & FK_FLAG_REVOKED) !== 0;
    if (current && revoked) bad("current field key cannot be revoked");
    const allZero = !material.some((b) => b !== 0);
    if (revoked) {
      if (!allZero) bad("revoked field key retains material");
    } else if (allZero) {
      bad("empty field key material");
    }
    if (current) currentCount++;
    records.push({ id, created, current, revoked, material });
  }
  if (off !== raw.length) bad("trailing keyring bytes");
  if (records.length === 0) bad("keyring has no keys");
  if (currentCount !== 1) bad("keyring must have exactly one current key");
  return records;
}
