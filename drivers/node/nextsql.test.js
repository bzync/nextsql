'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
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
  decodeRowDesc,
  decodeNSJB,
  formatUUID,
  decodeDecimal,
  encodeDecimalString,
  FieldType,
  MemoryFieldKeyring,
  FileFieldKeyring,
  decryptField,
  encryptField,
  struct,
} = require('./nextsql');

function fieldKey(id, fill) {
  return { id, material: Buffer.alloc(32, fill) };
}

test('NSCE1 field encryption round-trip, rotation, and revocation', async () => {
  const v1 = fieldKey('v1', 1);
  const ring = new MemoryFieldKeyring(v1);
  const values = [
    [FieldType.UUID, '00112233-4455-6677-8899-aabbccddeeff'],
    [FieldType.String, 'secret'],
    [FieldType.Text, 'long secret'],
    [FieldType.Decimal(8, 2), '-12.50'],
    [FieldType.TimestampTZ, 1234567890123456789n],
    [FieldType.JSON, { z: [true, null], a: 7 }],
    [FieldType.Bool, true],
    [FieldType.Blob, Buffer.from([0x00, 0xff, 0xde, 0xad, 0xbe, 0xef])],
    [FieldType.Int8, -128],
    [FieldType.Int16, -32768],
    [FieldType.Int32, -2147483648],
    [FieldType.Int64, -9223372036854775808n],
    [FieldType.Uint8, 255],
    [FieldType.Uint16, 65535],
    [FieldType.Uint32, 4294967295],
    [FieldType.Uint64, 18446744073709551615n],
  ];
  for (const [type, value] of values) {
    const sealed = await encryptField(ring, 'app', 'accounts', 'secret', type, value);
    assert.match(sealed, /^NSCE1\./);
    const plain = await decryptField(ring, 'app', 'accounts', 'secret', type, sealed);
    assert.deepEqual(plain, value);
  }
  const old = await encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, 'old');
  const again = await encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, 'old');
  assert.notEqual(old, again);
  ring.rotate(fieldKey('v2', 2));
  assert.equal(await decryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, old), 'old');
  ring.revoke('v1');
  await assert.rejects(() => decryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, old), (err) => err.code === 'crypto');
  await assert.rejects(() => decryptField(new MemoryFieldKeyring(v1), 'app', 'accounts', 'other', FieldType.Text, old), (err) => err.code === 'crypto');
  await assert.rejects(() => encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, 'x'.repeat(1 << 20)), (err) => err.code === 'exhausted');
  assert.equal(await encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, null), null);
  const decimal = await encryptField(ring, 'app', 'accounts', 'amount', FieldType.Decimal(4, 2), '1');
  assert.equal(await decryptField(ring, 'app', 'accounts', 'amount', FieldType.Decimal(4, 2), decimal), '1.00');
  await assert.rejects(() => encryptField(ring, 'app', 'accounts', 'amount', FieldType.Decimal(4, 2), '123.45'), (err) => err.code === 'invalid_argument');
  const goCiphertext = 'NSCE1.AQECdjEDAAAAAABEeyxf_quGP5And9z0FmNijEp3uSiDspby_y1zIxe9L1R-llGtWQxh';
  assert.equal(await decryptField(new MemoryFieldKeyring(v1), 'app', 'accounts', 'secret', FieldType.Text, goCiphertext), 'portable');
});

test('FileFieldKeyring persists rotation and revocation across reopen', async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'nextsql-fk-'));
  const p = path.join(dir, 'keyring.nsfk');
  const kr = await FileFieldKeyring.create(p, fieldKey('v1', 1));
  const old = await encryptField(kr, 'app', 'accounts', 'secret', FieldType.Text, 'old');

  await assert.rejects(() => FileFieldKeyring.create(p, fieldKey('v2', 2)), (err) => err.code === 'already_exists');

  await kr.rotate(fieldKey('v2', 2));
  const afterRotate = await FileFieldKeyring.open(p);
  assert.equal(await decryptField(afterRotate, 'app', 'accounts', 'secret', FieldType.Text, old), 'old');
  const fresh = await encryptField(afterRotate, 'app', 'accounts', 'secret', FieldType.Text, 'new');

  await kr.revoke('v1');
  const afterRevoke = await FileFieldKeyring.open(p);
  await assert.rejects(() => decryptField(afterRevoke, 'app', 'accounts', 'secret', FieldType.Text, old), (err) => err.code === 'crypto');
  assert.equal(await decryptField(afterRevoke, 'app', 'accounts', 'secret', FieldType.Text, fresh), 'new');

  const list = afterRevoke.list();
  assert.deepEqual(list.find((r) => r.id === 'v1'), { ...list.find((r) => r.id === 'v1'), current: false, revoked: true });
  assert.deepEqual(list.find((r) => r.id === 'v2'), { ...list.find((r) => r.id === 'v2'), current: true, revoked: false });

  await assert.rejects(() => kr.revoke('v2'), (err) => err.code === 'conflict');
  await assert.rejects(() => kr.rotate(fieldKey('v1', 9)), (err) => err.code === 'conflict');

  const raw = await fs.readFile(p);
  await fs.writeFile(p, 'not a keyring');
  await assert.rejects(() => kr.reload());
  const stillCurrent = await kr.currentFieldKey('app', 'accounts', 'secret');
  assert.equal(stillCurrent.id, 'v2');
  await fs.writeFile(p, raw);
});

test('FileFieldKeyring opens a Go-produced NSFK1 fixture', async () => {
  // Produced by drivers/go: one revoked key "v1" (zeroed material) and one
  // current key "v2" (material of 32 x 0x02 bytes) — proves the NSFK1 format
  // is byte-for-byte portable across drivers, not just same-language.
  const fixtureB64 =
    'TlNGSwEAAgACdjEA8VNlAAAAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAJ2MmTxU2UAAAAAAQICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgIC';
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'nextsql-fk-interop-'));
  const p = path.join(dir, 'go.nsfk');
  await fs.writeFile(p, Buffer.from(fixtureB64, 'base64'));

  const kr = await FileFieldKeyring.open(p);
  const list = kr.list();
  assert.deepEqual(list.find((r) => r.id === 'v1'), { ...list.find((r) => r.id === 'v1'), current: false, revoked: true });
  assert.deepEqual(list.find((r) => r.id === 'v2'), { ...list.find((r) => r.id === 'v2'), current: true, revoked: false });

  const current = await kr.currentFieldKey('app', 'accounts', 'secret');
  assert.equal(current.id, 'v2');
  assert.deepEqual(current.material, Buffer.alloc(32, 2));

  await assert.rejects(() => kr.fieldKey('app', 'accounts', 'secret', 'v1'), (err) => err.code === 'crypto');
});

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

test('hello realm is an opt-in trailing field (M2-2)', () => {
  const noRealm = encodeHello({ version: 1, database: 'prod', user: 'app' });
  const emptyRealm = encodeHello({ version: 1, database: 'prod', user: 'app', realm: '' });
  assert.ok(noRealm.equals(emptyRealm));
  const withRealm = encodeHello({ version: 1, database: 'prod', user: 'app', realm: 'tenant-a' });
  assert.equal(withRealm.length, noRealm.length + 2 + Buffer.byteLength('tenant-a'));
  assert.ok(withRealm.subarray(0, noRealm.length).equals(noRealm));
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

test('blob param round-trip (D1)', () => {
  const raw = Buffer.from([0x00, 0xff, 0xfe, 0x00, 0xde, 0xad, 0xbe, 0xef]);
  const dec = decodeValue(encodeParam(raw), 0);
  assert.equal(dec.kind, Kind.Blob);
  assert.ok(dec.value.equals(raw));

  // A 16-byte Buffer keeps its pre-existing UUID meaning; the explicit
  // wrapper forces BLOB for that length.
  const raw16 = Buffer.alloc(16, 7);
  assert.equal(decodeValue(encodeParam(raw16), 0).kind, Kind.UUID);
  const wrapped = decodeValue(encodeParam({ kind: 'blob', value: raw16 }), 0);
  assert.equal(wrapped.kind, Kind.Blob);
  assert.ok(wrapped.value.equals(raw16));

  const empty = decodeValue(encodeParam(Buffer.alloc(0)), 0);
  assert.equal(empty.kind, Kind.Blob);
  assert.equal(empty.value.length, 0);
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
    assert.equal(dec.kind, kind);
    assert.equal(dec.value.toString(), value.toString());
  }
  // Out-of-range values are rejected client-side too.
  assert.throws(() => encodeParam({ kind: 'int8', value: 128 }));
  assert.throws(() => encodeParam({ kind: 'int8', value: -129 }));
  // A bare number still defaults to Decimal (server coerces per column).
  assert.equal(decodeValue(encodeParam(42), 0).kind, Kind.Decimal);
  // int64 decodes as BigInt (does not fit safely in a JS number).
  assert.equal(typeof decodeValue(encodeParam({ kind: 'int64', value: 5n }), 0).value, 'bigint');
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
    assert.equal(dec.kind, kind);
    assert.equal(dec.value.toString(), value.toString());
  }
  // Out-of-range and negative values are rejected client-side too.
  assert.throws(() => encodeParam({ kind: 'uint8', value: 256 }));
  assert.throws(() => encodeParam({ kind: 'uint8', value: -1 }));
  // A bare number still defaults to Decimal (server coerces per column).
  assert.equal(decodeValue(encodeParam(42), 0).kind, Kind.Decimal);
  // uint64 decodes as BigInt (does not fit safely in a JS number).
  assert.equal(typeof decodeValue(encodeParam({ kind: 'uint64', value: 5n }), 0).value, 'bigint');
});

test('ENUM param round-trip and label-list wire framing (D11)', () => {
  const labels = ['small', 'medium', 'large'];
  const dec = decodeValue(encodeParam({ kind: 'enum', value: 'medium', labels }), 0);
  assert.equal(dec.kind, Kind.Enum);
  assert.equal(dec.value, 'medium');
  assert.deepEqual(dec.labels, labels);

  // A non-member label is rejected client-side too.
  assert.throws(() => encodeParam({ kind: 'enum', value: 'huge', labels }));

  // A NULL ENUM value still carries its label list on the wire.
  const raw = encodeParam({ kind: 'enum', value: 'small', labels });
  raw[1] = 1; // flip the null flag directly, keeping the same header/labels
  const nullDec = decodeValue(raw, 0);
  assert.equal(nullDec.value, null);
  assert.deepEqual(nullDec.labels, labels);

  // decodeRowDesc parses the same label-list framing for a column header
  // (kind byte, 5 bytes of Precision/Scale/VecElem meta, then the ENUM
  // label-count u16 + each u16-length-prefixed label) — hand-built to match
  // internal/protocol/value.go's appendType exactly, since clients never
  // encode a RowDesc themselves.
  const nameBuf = Buffer.concat([Buffer.from([2, 0]), Buffer.from('sz', 'utf8')]); // u16 LE name length + name
  const typeMeta = Buffer.concat([Buffer.from([Kind.Enum]), Buffer.alloc(5)]);
  const labelBytes = Buffer.concat([
    Buffer.from([labels.length, 0]),
    ...labels.map((l) => Buffer.concat([Buffer.from([l.length, 0]), Buffer.from(l, 'utf8')])),
  ]);
  const rowDesc = Buffer.concat([Buffer.from([1, 0]), nameBuf, typeMeta, labelBytes]);
  const cols = decodeRowDesc(rowDesc);
  assert.equal(cols.length, 1);
  assert.equal(cols[0].name, 'sz');
  assert.equal(cols[0].kind, Kind.Enum);
  assert.deepEqual(cols[0].labels, labels);
});

test('DATE/TIME/TIMESTAMP/CHAR/VARCHAR/FLOAT32/FLOAT64 param round-trip (D4/D5/D7/D8)', () => {
  // DATE: a Date's UTC calendar day round-trips to the same UTC-midnight Date.
  const d = new Date(Date.UTC(2024, 0, 15));
  const decDate = decodeValue(encodeParam({ kind: 'date', value: d }), 0);
  assert.equal(decDate.kind, Kind.Date);
  assert.equal(decDate.value.getTime(), d.getTime());
  // Pre-1970 dates must round-trip too (signed day count).
  const preEpoch = new Date(Date.UTC(1900, 0, 1));
  assert.equal(decodeValue(encodeParam({ kind: 'date', value: preEpoch }), 0).value.getTime(), preEpoch.getTime());

  // TIME: nanoseconds since midnight.
  const nsInDay = (23 * 3600 + 59 * 60 + 59) * 1_000_000_000 + 999_000_000;
  const decTime = decodeValue(encodeParam({ kind: 'time', value: nsInDay }), 0);
  assert.equal(decTime.kind, Kind.Time);
  assert.equal(decTime.value, nsInDay);

  // TIMESTAMP (naive): a bare Date defaults to TimestampTZ; the wrapper
  // selects the naive Kind while sharing the exact same wire shape.
  const ts = new Date('2024-06-15T10:30:00.000Z');
  const decTs = decodeValue(encodeParam({ kind: 'timestamp', value: ts }), 0);
  assert.equal(decTs.kind, Kind.Timestamp);
  assert.equal(decTs.value.getTime(), ts.getTime());
  assert.equal(decodeValue(encodeParam(ts), 0).kind, Kind.TimestampTZ);

  // FLOAT32/FLOAT64: NaN/Infinity are valid (unlike the bare-number -> Decimal path).
  for (const which of ['float32', 'float64']) {
    for (const value of [1.5, -0, Infinity, -Infinity, NaN]) {
      const dec = decodeValue(encodeParam({ kind: which, value }), 0);
      assert.equal(dec.kind, which === 'float32' ? Kind.Float32 : Kind.Float64);
      if (Number.isNaN(value)) {
        assert.ok(Number.isNaN(dec.value));
      } else {
        assert.equal(Math.sign(1 / dec.value), Math.sign(1 / value)); // distinguishes 0 from -0 too
      }
    }
  }

  // CHAR/VARCHAR decode as plain strings (same encoding as STRING/TEXT); the
  // server does the padding/length-ceiling validation, so no client-side
  // encode wrapper is needed — a plain string already round-trips as the
  // write path (server-side coercion covers it).
  const charRaw = Buffer.concat([Buffer.from([Kind.Char, 0]), Buffer.alloc(5), Buffer.from([5, 0, 0, 0]), Buffer.from('ab   ')]);
  assert.equal(decodeValue(charRaw, 0).value, 'ab   ');
});

test('INTERVAL param round-trip, including negative nanos (D6)', () => {
  const dec = decodeValue(encodeParam({ kind: 'interval', months: 14, days: 3, nanos: 4n * 3_600_000_000_000n }), 0);
  assert.equal(dec.kind, Kind.Interval);
  assert.deepEqual(dec.value, { months: 14, days: 3, nanos: 4n * 3_600_000_000_000n });

  // Regression test for a real bug found while implementing D6: putU64
  // (used for INTERVAL's nanosecond component, which is legitimately
  // negative, e.g. "-1 hour") called Buffer.writeBigUInt64LE directly on a
  // possibly-negative BigInt, which throws a RangeError instead of wrapping
  // to the unsigned two's-complement bit pattern — unlike the JS/Bun/Deno
  // driver's DataView.setBigUint64, which already wraps automatically. This
  // silently affected every pre-1970 TIMESTAMPTZ/TIMESTAMP too, since they
  // share the same helper; nothing in this suite exercised a negative value
  // through putU64 specifically until this interval test (the existing
  // pre-1970 DATE case above uses a completely different int32 encode path).
  const negDec = decodeValue(encodeParam({ kind: 'interval', months: 0, days: 0, nanos: -3_600_000_000_000n }), 0);
  assert.equal(negDec.value.nanos, -3_600_000_000_000n);
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

test('collections: ARRAY / MAP / STRUCT param round-trip (D9)', () => {
  const arr = decodeValue(encodeParam(['a', 'b', 'c']), 0);
  assert.equal(arr.kind, Kind.Array);
  assert.deepEqual(arr.value, ['a', 'b', 'c']);
  assert.deepEqual(decodeValue(encodeParam(['a', null, 'c']), 0).value, ['a', null, 'c']);
  const m = decodeValue(encodeParam(new Map([['x', 'hi']])), 0);
  assert.equal(m.kind, Kind.Map);
  assert.equal(m.value.get('x'), 'hi');
  const s = decodeValue(encodeParam(struct([['street', 'Main'], ['zip', '90210']])), 0);
  assert.equal(s.kind, Kind.Struct);
  assert.equal(s.value.street, 'Main');
  const nested = decodeValue(encodeParam(struct([['n', 'bob'], ['t', ['x', 'y']]])), 0);
  assert.deepEqual(nested.value.t, ['x', 'y']);
});

test('spatial: EWKB decode / encode round-trip (Spatial track S4)', () => {
  const u32le = (n) => { const b = Buffer.alloc(4); b.writeUInt32LE(n, 0); return b; };
  const f64le = (n) => { const b = Buffer.alloc(8); b.writeDoubleLE(n, 0); return b; };
  const ewkb = Buffer.concat([Buffer.from([1]), u32le(1 | 0x20000000), u32le(4326), f64le(1.5), f64le(2.5)]);
  const wire = Buffer.concat([Buffer.from([Kind.Geometry, 0, 0, 0, 0, 0, 0]), u32le(ewkb.length), ewkb]);
  const got = decodeValue(wire, 0);
  assert.equal(got.next, wire.length);
  assert.deepEqual(got.value, { type: 'Point', srid: 4326, coordinates: [1.5, 2.5] });
  const enc = encodeParam({ kind: 'geometry', wkt: 'POINT(1 2)', srid: 4326 });
  assert.equal(decodeValue(enc, 0).value, 'SRID=4326;POINT(1 2)');
});
