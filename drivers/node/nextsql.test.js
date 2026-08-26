'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const {
  connect,
  NextSQLError,
  Kind,
  validateConfig,
  isLoopback,
  encodeParam,
  decodeValue,
  encodeHello,
  decodeHelloOK,
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
