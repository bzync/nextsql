import {
  Kind,
  NextSQLError,
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

function assert(cond, msg) {
  if (!cond) {
    throw new Error(msg || 'assert failed');
  }
}

function assertThrows(fn) {
  try {
    fn();
  } catch (err) {
    if (err instanceof NextSQLError) {
      return;
    }
    throw err;
  }
  throw new Error('expected NextSQLError');
}

async function assertRejects(fn) {
  try {
    await fn();
  } catch (err) {
    if (err instanceof NextSQLError) {
      return;
    }
    throw err;
  }
  throw new Error('expected NextSQLError');
}

Deno.test('rejects keys and passwords in a URL', () => {
  assertThrows(() => validateConfig({
    address: 'nextsql://app:secret@db.example.com:7210/prod?key=deadbeef',
    user: 'app',
    password: 'x',
    tls: {},
  }));
});

Deno.test('requires TLS off loopback', () => {
  assertThrows(() => validateConfig({
    address: 'db.example.com:7210',
    user: 'app',
    password: 'x',
    insecureNoTLS: true,
  }));
  validateConfig({
    address: '127.0.0.1:7210',
    user: 'app',
    insecureNoTLS: true,
  });
});

Deno.test('loopback detection', () => {
  assert(isLoopback('127.0.0.1:7210'));
  assert(isLoopback('localhost:7210'));
  assert(isLoopback('[::1]:7210'));
  assert(!isLoopback('db.example.com:7210'));
});

Deno.test('connect requires address and user', async () => {
  await assertRejects(() => connect({ user: 'app', tls: {} }));
  await assertRejects(() => connect({ address: '127.0.0.1:1', tls: {} }));
});

Deno.test('hello encode / decode', () => {
  const payload = encodeHello({ version: 1, database: 'prod', user: 'app' });
  assert((payload[0] | (payload[1] << 8)) === 1);
  assert((payload[2] | (payload[3] << 8)) === 0);
  const ok = new Uint8Array(11);
  ok[0] = 1;
  ok[2] = 1;
  ok[3] = 99;
  const got = decodeHelloOK(ok);
  assert(got.authMethod === 1);
  assert(got.secret === 99n);
});

Deno.test('round-trip string and bool params', () => {
  const d = decodeValue(encodeParam('hello'), 0);
  assert(d.value === 'hello');
  assert(d.kind === Kind.String);
  assert(decodeValue(encodeParam(true), 0).value === true);
  assert(decodeValue(encodeParam(null), 0).value === null);
});

Deno.test('decimal encode / decode', () => {
  const body = encodeDecimalString('-12.50');
  assert(decodeDecimal(body.subarray(4)) === '-12.50');
  const enc = encodeParam({ kind: 'decimal', value: '1000.5' });
  assert(decodeValue(enc, 0).value === '1000.5');
});

Deno.test('uuid format', () => {
  const raw = new Uint8Array([
    0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
    0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
  ]);
  assert(formatUUID(raw) === '00112233-4455-6677-8899-aabbccddeeff');
});

Deno.test('NSJB true / object', () => {
  const t = new Uint8Array([0x4e, 0x53, 0x4a, 0x42, 1, 0x02]);
  const n = new Uint8Array([0x4e, 0x53, 0x4a, 0x42, 1, 0x00]);
  assert(decodeNSJB(t) === true);
  assert(decodeNSJB(n) === null);
});

Deno.test('point param', () => {
  const got = decodeValue(encodeParam({ lon: -73.98, lat: 40.75 }), 0);
  assert(got.kind === Kind.Point);
  assert(Math.abs(got.value.lon + 73.98) < 1e-9);
  assert(Math.abs(got.value.lat - 40.75) < 1e-9);
});
