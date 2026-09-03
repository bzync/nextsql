import { expect, test } from 'bun:test';
import { mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  Kind,
  NextSQLError,
  ReadConsistency,
  connect,
  connectCluster,
  decodeDecimal,
  decodeHelloOK,
  decodeNSJB,
  decodeNodeStatus,
  decodeValue,
  encodeDecimalString,
  encodeHello,
  encodeParam,
  encodeSetReadConsistency,
  formatUUID,
  isLoopback,
  isReadOnlySQL,
  txnControl,
  validateConfig,
  FieldType,
  MemoryFieldKeyring,
  FileFieldKeyring,
  decryptField,
  encryptField,
} from './nextsql.js';

function fieldKey(id, fill) {
  return { id, material: new Uint8Array(32).fill(fill) };
}

test('NSCE1 field encryption round-trip, rotation, and revocation', async () => {
  const v1 = fieldKey('v1', 1);
  const ring = new MemoryFieldKeyring(v1);
  const values = [
    [FieldType.UUID, '00112233-4455-6677-8899-aabbccddeeff'],
    [FieldType.String, 'secret'],
    [FieldType.Decimal(8, 2), '-12.50'],
    [FieldType.TimestampTZ, 1234567890123456789n],
    [FieldType.JSON, { z: [true, null], a: 7 }],
    [FieldType.Bool, true],
    [FieldType.Blob, new Uint8Array([0x00, 0xff, 0xde, 0xad, 0xbe, 0xef])],
    [FieldType.Int8, -128],
    [FieldType.Int8, 127],
    [FieldType.Int16, -32768],
    [FieldType.Int32, -2147483648],
    [FieldType.Int64, -9223372036854775808n],
    [FieldType.Int64, 9223372036854775807n],
    [FieldType.Uint8, 255],
    [FieldType.Uint16, 65535],
    [FieldType.Uint32, 4294967295],
    [FieldType.Uint64, 18446744073709551615n],
  ];
  for (const [type, value] of values) {
    const sealed = await encryptField(ring, 'app', 'accounts', 'secret', type, value);
    expect(sealed.startsWith('NSCE1.')).toBe(true);
    expect(await decryptField(ring, 'app', 'accounts', 'secret', type, sealed)).toEqual(value);
  }
  const old = await encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, 'old');
  const again = await encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, 'old');
  expect(old).not.toBe(again);
  ring.rotate(fieldKey('v2', 2));
  expect(await decryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, old)).toBe('old');
  ring.revoke('v1');
  await expect(decryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, old)).rejects.toBeInstanceOf(NextSQLError);
  await expect(decryptField(new MemoryFieldKeyring(v1), 'app', 'accounts', 'other', FieldType.Text, old)).rejects.toBeInstanceOf(NextSQLError);
  await expect(encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, 'x'.repeat(1 << 20))).rejects.toBeInstanceOf(NextSQLError);
  const goCiphertext = 'NSCE1.AQECdjEDAAAAAABEeyxf_quGP5And9z0FmNijEp3uSiDspby_y1zIxe9L1R-llGtWQxh';
  expect(await decryptField(new MemoryFieldKeyring(v1), 'app', 'accounts', 'secret', FieldType.Text, goCiphertext)).toBe('portable');
});

test('FileFieldKeyring persists rotation and revocation across reopen', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'nextsql-fk-'));
  const path = join(dir, 'keyring.nsfk');
  const kr = await FileFieldKeyring.create(path, fieldKey('v1', 1));
  const old = await encryptField(kr, 'app', 'accounts', 'secret', FieldType.Text, 'old');

  await expect(FileFieldKeyring.create(path, fieldKey('v2', 2))).rejects.toBeInstanceOf(NextSQLError);

  await kr.rotate(fieldKey('v2', 2));
  const reopenedAfterRotate = await FileFieldKeyring.open(path);
  expect(await decryptField(reopenedAfterRotate, 'app', 'accounts', 'secret', FieldType.Text, old)).toBe('old');
  const fresh = await encryptField(reopenedAfterRotate, 'app', 'accounts', 'secret', FieldType.Text, 'new');

  await expect(kr.revoke('v1')).resolves.toBeUndefined();
  const reopenedAfterRevoke = await FileFieldKeyring.open(path);
  await expect(decryptField(reopenedAfterRevoke, 'app', 'accounts', 'secret', FieldType.Text, old)).rejects.toBeInstanceOf(NextSQLError);
  expect(await decryptField(reopenedAfterRevoke, 'app', 'accounts', 'secret', FieldType.Text, fresh)).toBe('new');

  const list = reopenedAfterRevoke.list();
  expect(list.find((r) => r.id === 'v1')).toEqual(expect.objectContaining({ current: false, revoked: true }));
  expect(list.find((r) => r.id === 'v2')).toEqual(expect.objectContaining({ current: true, revoked: false }));

  await expect(kr.revoke('v2')).rejects.toBeInstanceOf(NextSQLError);
  await expect(kr.rotate(fieldKey('v1', 9))).rejects.toBeInstanceOf(NextSQLError);

  const raw = await readFile(path);
  await writeFile(path, 'not a keyring');
  await expect(kr.reload()).rejects.toBeInstanceOf(NextSQLError);
  expect(await kr.currentFieldKey()).toEqual({ id: 'v2', material: fieldKey('v2', 2).material });
  await writeFile(path, raw);
});

test('rejects keys and passwords in a URL', () => {
  expect(() => validateConfig({
    address: 'nextsql://app:secret@db.example.com:7210/prod?key=deadbeef',
    user: 'app',
    password: 'x',
    tls: {},
  })).toThrow(NextSQLError);
});

test('requires TLS off loopback', () => {
  expect(() => validateConfig({
    address: 'db.example.com:7210',
    user: 'app',
    password: 'x',
    insecureNoTLS: true,
  })).toThrow(NextSQLError);
  validateConfig({
    address: '127.0.0.1:7210',
    user: 'app',
    insecureNoTLS: true,
  });
});

test('loopback detection', () => {
  expect(isLoopback('127.0.0.1:7210')).toBe(true);
  expect(isLoopback('localhost:7210')).toBe(true);
  expect(isLoopback('[::1]:7210')).toBe(true);
  expect(isLoopback('db.example.com:7210')).toBe(false);
});

test('connect requires address and user', async () => {
  await expect(connect({ user: 'app', tls: {} })).rejects.toBeInstanceOf(NextSQLError);
  await expect(connect({ address: '127.0.0.1:1', tls: {} })).rejects.toBeInstanceOf(NextSQLError);
});

test('hello encode / decode', () => {
  const payload = encodeHello({ version: 1, database: 'prod', user: 'app' });
  expect(payload[0] | (payload[1] << 8)).toBe(1);
  expect(payload[2] | (payload[3] << 8)).toBe(0);
  const ok = new Uint8Array(11);
  ok[0] = 1;
  ok[2] = 1;
  ok[3] = 99;
  const got = decodeHelloOK(ok);
  expect(got.authMethod).toBe(1);
  expect(got.secret).toBe(99n);
});

test('hello realm is an opt-in trailing field (M2-2)', () => {
  const noRealm = encodeHello({ version: 1, database: 'prod', user: 'app' });
  const emptyRealm = encodeHello({ version: 1, database: 'prod', user: 'app', realm: '' });
  expect(noRealm.length).toBe(emptyRealm.length);
  expect(Buffer.from(noRealm).equals(Buffer.from(emptyRealm))).toBe(true);
  const withRealm = encodeHello({ version: 1, database: 'prod', user: 'app', realm: 'tenant-a' });
  expect(withRealm.length).toBe(noRealm.length + 2 + 'tenant-a'.length);
  expect(Buffer.from(withRealm.slice(0, noRealm.length)).equals(Buffer.from(noRealm))).toBe(true);
});

test('round-trip string and bool params', () => {
  const s = encodeParam('hello');
  const d = decodeValue(s, 0);
  expect(d.value).toBe('hello');
  expect(d.kind).toBe(Kind.String);
  expect(decodeValue(encodeParam(true), 0).value).toBe(true);
  expect(decodeValue(encodeParam(null), 0).value).toBe(null);
});

test('blob param round-trip (D1)', () => {
  const raw = new Uint8Array([0x00, 0xff, 0xfe, 0x00, 0xde, 0xad, 0xbe, 0xef]);
  const enc = encodeParam(raw);
  const dec = decodeValue(enc, 0);
  expect(dec.kind).toBe(Kind.Blob);
  expect(dec.value).toEqual(raw);

  // A 16-byte Uint8Array keeps its pre-existing UUID meaning; use the
  // explicit wrapper to force BLOB for that length.
  const raw16 = new Uint8Array(16).fill(7);
  expect(decodeValue(encodeParam(raw16), 0).kind).toBe(Kind.UUID);
  const wrapped = decodeValue(encodeParam({ kind: 'blob', value: raw16 }), 0);
  expect(wrapped.kind).toBe(Kind.Blob);
  expect(wrapped.value).toEqual(raw16);

  const empty = decodeValue(encodeParam(new Uint8Array(0)), 0);
  expect(empty.kind).toBe(Kind.Blob);
  expect(empty.value).toEqual(new Uint8Array(0));
});

test('fixed-width int param round-trip (D2)', () => {
  const cases = [
    ['int8', -128, Kind.Int8],
    ['int8', 127, Kind.Int8],
    ['int16', -32768, Kind.Int16],
    ['int16', 32767, Kind.Int16],
    ['int32', -2147483648, Kind.Int32],
    ['int32', 2147483647, Kind.Int32],
    ['int64', -9223372036854775808n, Kind.Int64],
    ['int64', 9223372036854775807n, Kind.Int64],
  ];
  for (const [which, value, kind] of cases) {
    const dec = decodeValue(encodeParam({ kind: which, value }), 0);
    expect(dec.kind).toBe(kind);
    expect(dec.value.toString()).toBe(value.toString());
  }
  expect(() => encodeParam({ kind: 'int8', value: 128 })).toThrow();
  expect(() => encodeParam({ kind: 'int8', value: -129 })).toThrow();
  expect(decodeValue(encodeParam(42), 0).kind).toBe(Kind.Decimal);
  expect(typeof decodeValue(encodeParam({ kind: 'int64', value: 5n }), 0).value).toBe('bigint');
});

test('fixed-width uint param round-trip (D3)', () => {
  const cases = [
    ['uint8', 0, Kind.Uint8],
    ['uint8', 255, Kind.Uint8],
    ['uint16', 0, Kind.Uint16],
    ['uint16', 65535, Kind.Uint16],
    ['uint32', 0, Kind.Uint32],
    ['uint32', 4294967295, Kind.Uint32],
    ['uint64', 0n, Kind.Uint64],
    ['uint64', 18446744073709551615n, Kind.Uint64],
  ];
  for (const [which, value, kind] of cases) {
    const dec = decodeValue(encodeParam({ kind: which, value }), 0);
    expect(dec.kind).toBe(kind);
    expect(dec.value.toString()).toBe(value.toString());
  }
  expect(() => encodeParam({ kind: 'uint8', value: 256 })).toThrow();
  expect(() => encodeParam({ kind: 'uint8', value: -1 })).toThrow();
  expect(decodeValue(encodeParam(42), 0).kind).toBe(Kind.Decimal);
  expect(typeof decodeValue(encodeParam({ kind: 'uint64', value: 5n }), 0).value).toBe('bigint');
});

test('ENUM param round-trip (D11)', () => {
  const labels = ['small', 'medium', 'large'];
  const dec = decodeValue(encodeParam({ kind: 'enum', value: 'medium', labels }), 0);
  expect(dec.kind).toBe(Kind.Enum);
  expect(dec.value).toBe('medium');
  expect(dec.labels).toEqual(labels);
  expect(() => encodeParam({ kind: 'enum', value: 'huge', labels })).toThrow();
});

test('DATE/TIME/TIMESTAMP/FLOAT32/FLOAT64 param round-trip (D5/D7/D8)', () => {
  const d = new Date(Date.UTC(2024, 0, 15));
  const decDate = decodeValue(encodeParam({ kind: 'date', value: d }), 0);
  expect(decDate.kind).toBe(Kind.Date);
  expect(decDate.value.getTime()).toBe(d.getTime());

  const nsInDay = (23 * 3600 + 59 * 60 + 59) * 1_000_000_000 + 999_000_000;
  const decTime = decodeValue(encodeParam({ kind: 'time', value: nsInDay }), 0);
  expect(decTime.kind).toBe(Kind.Time);
  expect(decTime.value).toBe(nsInDay);

  const ts = new Date('2024-06-15T10:30:00.000Z');
  const decTs = decodeValue(encodeParam({ kind: 'timestamp', value: ts }), 0);
  expect(decTs.kind).toBe(Kind.Timestamp);
  expect(decTs.value.getTime()).toBe(ts.getTime());
  expect(decodeValue(encodeParam(ts), 0).kind).toBe(Kind.TimestampTZ);

  const decF32 = decodeValue(encodeParam({ kind: 'float32', value: 1.5 }), 0);
  expect(decF32.kind).toBe(Kind.Float32);
  expect(decF32.value).toBe(1.5);
  const decF64Nan = decodeValue(encodeParam({ kind: 'float64', value: NaN }), 0);
  expect(decF64Nan.kind).toBe(Kind.Float64);
  expect(Number.isNaN(decF64Nan.value)).toBe(true);
});

test('INTERVAL param round-trip, including negative nanos (D6)', () => {
  const dec = decodeValue(encodeParam({ kind: 'interval', months: 14, days: 3, nanos: 4n * 3_600_000_000_000n }), 0);
  expect(dec.kind).toBe(Kind.Interval);
  expect(dec.value).toEqual({ months: 14, days: 3, nanos: 4n * 3_600_000_000_000n });
  const negDec = decodeValue(encodeParam({ kind: 'interval', months: 0, days: 0, nanos: -3_600_000_000_000n }), 0);
  expect(negDec.value.nanos).toBe(-3_600_000_000_000n);
});

test('decimal encode / decode', () => {
  const body = encodeDecimalString('-12.50');
  const raw = body.subarray(4);
  expect(decodeDecimal(raw)).toBe('-12.50');
  const enc = encodeParam({ kind: 'decimal', value: '1000.5' });
  expect(decodeValue(enc, 0).value).toBe('1000.5');
});

test('uuid format', () => {
  const raw = new Uint8Array([
    0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
    0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
  ]);
  expect(formatUUID(raw)).toBe('00112233-4455-6677-8899-aabbccddeeff');
});

test('NSJB true / object', () => {
  const t = new Uint8Array([0x4e, 0x53, 0x4a, 0x42, 1, 0x02]);
  const n = new Uint8Array([0x4e, 0x53, 0x4a, 0x42, 1, 0x00]);
  expect(decodeNSJB(t)).toBe(true);
  expect(decodeNSJB(n)).toBe(null);
});

test('point param', () => {
  const enc = encodeParam({ lon: -73.98, lat: 40.75 });
  const got = decodeValue(enc, 0);
  expect(got.kind).toBe(Kind.Point);
  expect(Math.abs(got.value.lon + 73.98)).toBeLessThan(1e-9);
  expect(Math.abs(got.value.lat - 40.75)).toBeLessThan(1e-9);
});

test('follower-read routing classifiers', () => {
  for (const s of ['SELECT 1', '  select n from t', '\n-- c\nSELECT n FROM t', 'SHOW TASKS', 'WITH c AS (SELECT n FROM t) SELECT n FROM c']) {
    expect(isReadOnlySQL(s)).toBe(true);
  }
  for (const s of ["INSERT INTO t (n) VALUES ('x')", 'UPDATE t SET n = 1', 'DELETE FROM t', "UPSERT INTO t (id) VALUES ('x')", 'EXPLAIN ANALYZE SELECT n FROM t', 'BEGIN']) {
    expect(isReadOnlySQL(s)).toBe(false);
  }
  expect(txnControl('  begin transaction ')).toEqual({ begin: true, end: false });
  expect(txnControl('rollback')).toEqual({ begin: false, end: true });
  expect(txnControl('SELECT 1')).toEqual({ begin: false, end: false });
});

test('SetReadConsistency / NodeStatus wire codec', () => {
  const b = encodeSetReadConsistency(ReadConsistency.Bounded, 2500);
  expect(b.length).toBe(9);
  expect(b[0]).toBe(1);
  expect(new DataView(b.buffer, b.byteOffset).getBigUint64(1, true)).toBe(2500n);
  expect(() => encodeSetReadConsistency(9, 0)).toThrow(NextSQLError);

  const role = new TextEncoder().encode('follower');
  const buf = new Uint8Array(2 + role.length + 25);
  const dv = new DataView(buf.buffer);
  dv.setUint16(0, role.length, true);
  buf.set(role, 2);
  const off = 2 + role.length;
  buf[off] = 0x03;
  dv.setBigUint64(off + 1, 99n, true);
  dv.setBigInt64(off + 9, 4n, true);
  dv.setBigUint64(off + 17, 0n, true);
  const st = decodeNodeStatus(buf);
  expect(st.role).toBe('follower');
  expect(st.hasLeader).toBe(true);
  expect(st.healthy).toBe(true);
  expect(st.appliedLSN).toBe(99n);
  expect(st.lastContactMs).toBe(4n);
});

test('connectCluster requires an address', async () => {
  await expect(connectCluster({ user: 'app', tls: {} })).rejects.toBeInstanceOf(NextSQLError);
});
