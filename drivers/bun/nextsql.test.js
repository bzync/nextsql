import { expect, test } from 'bun:test';
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
} from './nextsql.js';

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

test('round-trip string and bool params', () => {
  const s = encodeParam('hello');
  const d = decodeValue(s, 0);
  expect(d.value).toBe('hello');
  expect(d.kind).toBe(Kind.String);
  expect(decodeValue(encodeParam(true), 0).value).toBe(true);
  expect(decodeValue(encodeParam(null), 0).value).toBe(null);
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
