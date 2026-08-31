'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const {
  connect,
  connectCluster,
  NextSQLError,
  Kind,
  ReadConsistency,
  validateConfig,
  isLoopback,
  isReadOnlySQL,
  txnControl,
  encodeParam,
  decodeValue,
  encodeHello,
  decodeHelloOK,
  encodeSetReadConsistency,
  decodeNodeStatus,
  decodeNSJB,
  formatUUID,
  decodeDecimal,
  encodeDecimalString,
} = require('./nextsql');

test('rejects keys and passwords in a URL', () => {
  assert.throws(
    () => validateConfig({
      address: 'nextsql://app:secret@db.example.com:7210/prod?key=deadbeef',
      user: 'app',
      password: 'x',
      tls: {},
    }),
    (err) => err instanceof NextSQLError && err.code === 'invalid_argument',
  );
});

test('requires TLS off loopback', () => {
  assert.throws(
    () => validateConfig({
      address: 'db.example.com:7210',
      user: 'app',
      password: 'x',
      insecureNoTLS: true,
    }),
    (err) => err instanceof NextSQLError && err.code === 'invalid_argument',
  );
  validateConfig({
    address: '127.0.0.1:7210',
    user: 'app',
    insecureNoTLS: true,
  });
});

test('loopback detection', () => {
  assert.equal(isLoopback('127.0.0.1:7210'), true);
  assert.equal(isLoopback('localhost:7210'), true);
  assert.equal(isLoopback('[::1]:7210'), true);
  assert.equal(isLoopback('db.example.com:7210'), false);
});

test('connect requires address and user', async () => {
  await assert.rejects(() => connect({ user: 'app', tls: {} }), NextSQLError);
  await assert.rejects(() => connect({ address: '127.0.0.1:1', tls: {} }), NextSQLError);
});

test('hello encode / decode', () => {
  const payload = encodeHello({ version: 1, database: 'prod', user: 'app' });
  assert.equal(payload.readUInt16LE(0), 1);
  assert.equal(payload.readUInt16LE(2), 0);
  const ok = Buffer.alloc(11);
  ok.writeUInt16LE(1, 0);
  ok[2] = 1;
  ok.writeBigUInt64LE(99n, 3);
  const got = decodeHelloOK(ok);
  assert.equal(got.authMethod, 1);
  assert.equal(got.secret, 99n);
});

test('round-trip string and bool params', () => {
  const s = encodeParam('hello');
  const d = decodeValue(s, 0);
  assert.equal(d.value, 'hello');
  assert.equal(d.kind, Kind.String);
  const b = encodeParam(true);
  assert.equal(decodeValue(b, 0).value, true);
  const n = encodeParam(null);
  assert.equal(decodeValue(n, 0).value, null);
});

test('decimal encode / decode', () => {
  const body = encodeDecimalString('-12.50');
  // strip u32 length
  const raw = body.subarray(4);
  assert.equal(decodeDecimal(raw), '-12.50');
  const enc = encodeParam({ kind: 'decimal', value: '1000.5' });
  assert.equal(decodeValue(enc, 0).value, '1000.5');
});

test('uuid format', () => {
  const raw = Buffer.from('00112233445566778899aabbccddeeff', 'hex');
  assert.equal(formatUUID(raw), '00112233-4455-6677-8899-aabbccddeeff');
});

test('NSJB true / object', () => {
  assert.equal(decodeNSJB(Buffer.from('NSJB\x01\x02')), true);
  assert.equal(decodeNSJB(Buffer.from('NSJB\x01\x00')), null);
});

test('point param', () => {
  const enc = encodeParam({ lon: -73.98, lat: 40.75 });
  const got = decodeValue(enc, 0);
  assert.equal(got.kind, Kind.Point);
  assert.ok(Math.abs(got.value.lon + 73.98) < 1e-9);
  assert.ok(Math.abs(got.value.lat - 40.75) < 1e-9);
});

test('follower-read routing classifiers', () => {
  for (const s of [
    'SELECT 1',
    '  select n from t',
    '\n-- comment\nSELECT n FROM t',
    '(SELECT n FROM t) UNION (SELECT n FROM u)',
    'SHOW TASKS',
    'WITH c AS (SELECT n FROM t) SELECT n FROM c',
  ]) {
    assert.equal(isReadOnlySQL(s), true, s);
  }
  for (const s of [
    "INSERT INTO t (n) VALUES ('x')",
    'UPDATE t SET n = 1',
    'DELETE FROM t',
    "UPSERT INTO t (id) VALUES ('x')",
    'CREATE TABLE t (id STRING PRIMARY KEY)',
    'EXPLAIN ANALYZE SELECT n FROM t',
    'WITH c AS (SELECT 1) INSERT INTO t SELECT * FROM c',
    'BEGIN',
  ]) {
    assert.equal(isReadOnlySQL(s), false, s);
  }
  for (const [sql, begin, end] of [
    ['BEGIN', true, false],
    ['  begin transaction ', true, false],
    ['START TRANSACTION', true, false],
    ['COMMIT', false, true],
    ['rollback', false, true],
    ['SELECT 1', false, false],
  ]) {
    const got = txnControl(sql);
    assert.equal(got.begin, begin, sql);
    assert.equal(got.end, end, sql);
  }
});

test('SetReadConsistency wire encoding', () => {
  const strong = encodeSetReadConsistency(ReadConsistency.Strong, 0);
  assert.equal(strong.length, 9);
  assert.equal(strong[0], 0);
  assert.equal(strong.readBigUInt64LE(1), 0n);
  const bounded = encodeSetReadConsistency(ReadConsistency.Bounded, 2500);
  assert.equal(bounded[0], 1);
  assert.equal(bounded.readBigUInt64LE(1), 2500n);
  // sub-millisecond bound stays positive (not "server default")
  assert.equal(encodeSetReadConsistency(ReadConsistency.Bounded, 0.4).readBigUInt64LE(1), 1n);
  assert.throws(() => encodeSetReadConsistency(9, 0), NextSQLError);
});

test('NodeStatus round-trips the server encoding', () => {
  // Mirror internal/protocol EncodeNodeStatus: u16 role, flags byte, 3x u64.
  const role = Buffer.from('follower', 'utf8');
  const buf = Buffer.alloc(2 + role.length + 25);
  buf.writeUInt16LE(role.length, 0);
  role.copy(buf, 2);
  let off = 2 + role.length;
  buf[off] = 0x02; // healthy, no leader flag
  buf.writeBigUInt64LE(4242n, off + 1);
  buf.writeBigInt64LE(-1n, off + 9);
  buf.writeBigUInt64LE(7n, off + 17);
  const st = decodeNodeStatus(buf);
  assert.equal(st.role, 'follower');
  assert.equal(st.hasLeader, false);
  assert.equal(st.healthy, true);
  assert.equal(st.appliedLSN, 4242n);
  assert.equal(st.lastContactMs, -1n);
  assert.equal(st.applyBacklog, 7n);
  assert.throws(() => decodeNodeStatus(buf.subarray(0, buf.length - 1)), NextSQLError);
});

test('connectCluster requires an address', async () => {
  await assert.rejects(() => connectCluster({ user: 'app', tls: {} }), NextSQLError);
});
