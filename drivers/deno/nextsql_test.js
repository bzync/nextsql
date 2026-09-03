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
} from './mod.js';

function fieldKey(id, fill) {
  return { id, material: new Uint8Array(32).fill(fill) };
}

function stable(value) {
  if (typeof value === 'bigint') return value.toString();
  if (Array.isArray(value)) return value.map(stable);
  if (value && typeof value === 'object' && !(value instanceof Date)) {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, stable(value[key])]));
  }
  return value;
}

Deno.test('NSCE1 field encryption round-trip, rotation, and revocation', async () => {
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
    assert(sealed.startsWith('NSCE1.'));
    const plain = await decryptField(ring, 'app', 'accounts', 'secret', type, sealed);
    assert(JSON.stringify(stable(plain)) === JSON.stringify(stable(value)));
  }
  const old = await encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, 'old');
  const again = await encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, 'old');
  assert(old !== again);
  ring.rotate(fieldKey('v2', 2));
  assert(await decryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, old) === 'old');
  ring.revoke('v1');
  await assertRejects(() => decryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, old));
  await assertRejects(() => decryptField(new MemoryFieldKeyring(v1), 'app', 'accounts', 'other', FieldType.Text, old));
  await assertRejects(() => encryptField(ring, 'app', 'accounts', 'secret', FieldType.Text, 'x'.repeat(1 << 20)));
  const goCiphertext = 'NSCE1.AQECdjEDAAAAAABEeyxf_quGP5And9z0FmNijEp3uSiDspby_y1zIxe9L1R-llGtWQxh';
  assert(await decryptField(new MemoryFieldKeyring(v1), 'app', 'accounts', 'secret', FieldType.Text, goCiphertext) === 'portable');
});

Deno.test('FileFieldKeyring persists rotation and revocation across reopen', async () => {
  const dir = await Deno.makeTempDir({ prefix: 'nextsql-fk-' });
  const path = `${dir}/keyring.nsfk`;
  const kr = await FileFieldKeyring.create(path, fieldKey('v1', 1));
  const old = await encryptField(kr, 'app', 'accounts', 'secret', FieldType.Text, 'old');

  await assertRejects(() => FileFieldKeyring.create(path, fieldKey('v2', 2)));

  await kr.rotate(fieldKey('v2', 2));
  const reopenedAfterRotate = await FileFieldKeyring.open(path);
  assert(await decryptField(reopenedAfterRotate, 'app', 'accounts', 'secret', FieldType.Text, old) === 'old');
  const fresh = await encryptField(reopenedAfterRotate, 'app', 'accounts', 'secret', FieldType.Text, 'new');

  await kr.revoke('v1');
  const reopenedAfterRevoke = await FileFieldKeyring.open(path);
  await assertRejects(() => decryptField(reopenedAfterRevoke, 'app', 'accounts', 'secret', FieldType.Text, old));
  assert(await decryptField(reopenedAfterRevoke, 'app', 'accounts', 'secret', FieldType.Text, fresh) === 'new');

  const list = reopenedAfterRevoke.list();
  const v1Info = list.find((r) => r.id === 'v1');
  const v2Info = list.find((r) => r.id === 'v2');
  assert(v1Info.current === false && v1Info.revoked === true);
  assert(v2Info.current === true && v2Info.revoked === false);

  await assertRejects(() => kr.revoke('v2'));
  await assertRejects(() => kr.rotate(fieldKey('v1', 9)));

  const raw = await Deno.readFile(path);
  await Deno.writeTextFile(path, 'not a keyring');
  await assertRejects(() => kr.reload());
  const stillCurrent = await kr.currentFieldKey('app', 'accounts', 'secret');
  assert(stillCurrent.id === 'v2');
  await Deno.writeFile(path, raw);
});

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

Deno.test('hello realm is an opt-in trailing field (M2-2)', () => {
  const noRealm = encodeHello({ version: 1, database: 'prod', user: 'app' });
  const emptyRealm = encodeHello({ version: 1, database: 'prod', user: 'app', realm: '' });
  assert(noRealm.length === emptyRealm.length);
  assert(noRealm.every((b, i) => b === emptyRealm[i]));
  const withRealm = encodeHello({ version: 1, database: 'prod', user: 'app', realm: 'tenant-a' });
  assert(withRealm.length === noRealm.length + 2 + 'tenant-a'.length);
  assert(noRealm.every((b, i) => b === withRealm[i]));
});

Deno.test('round-trip string and bool params', () => {
  const d = decodeValue(encodeParam('hello'), 0);
  assert(d.value === 'hello');
  assert(d.kind === Kind.String);
  assert(decodeValue(encodeParam(true), 0).value === true);
  assert(decodeValue(encodeParam(null), 0).value === null);
});

Deno.test('blob param round-trip (D1)', () => {
  const raw = new Uint8Array([0x00, 0xff, 0xfe, 0x00, 0xde, 0xad, 0xbe, 0xef]);
  const dec = decodeValue(encodeParam(raw), 0);
  assert(dec.kind === Kind.Blob);
  assert(dec.value.length === raw.length && dec.value.every((b, i) => b === raw[i]));

  // A 16-byte Uint8Array keeps its pre-existing UUID meaning; the explicit
  // wrapper forces BLOB for that length.
  const raw16 = new Uint8Array(16).fill(7);
  assert(decodeValue(encodeParam(raw16), 0).kind === Kind.UUID);
  const wrapped = decodeValue(encodeParam({ kind: 'blob', value: raw16 }), 0);
  assert(wrapped.kind === Kind.Blob);
  assert(wrapped.value.length === 16 && wrapped.value.every((b) => b === 7));

  const empty = decodeValue(encodeParam(new Uint8Array(0)), 0);
  assert(empty.kind === Kind.Blob);
  assert(empty.value.length === 0);
});

Deno.test('fixed-width int param round-trip (D2)', () => {
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
    assert(dec.kind === kind);
    assert(dec.value.toString() === value.toString());
  }
  let threw = false;
  try {
    encodeParam({ kind: 'int8', value: 128 });
  } catch {
    threw = true;
  }
  assert(threw);
  assert(decodeValue(encodeParam(42), 0).kind === Kind.Decimal);
  assert(typeof decodeValue(encodeParam({ kind: 'int64', value: 5n }), 0).value === 'bigint');
});

Deno.test('fixed-width uint param round-trip (D3)', () => {
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
    assert(dec.kind === kind);
    assert(dec.value.toString() === value.toString());
  }
  let threw = false;
  try {
    encodeParam({ kind: 'uint8', value: 256 });
  } catch {
    threw = true;
  }
  assert(threw);
  assert(decodeValue(encodeParam(42), 0).kind === Kind.Decimal);
  assert(typeof decodeValue(encodeParam({ kind: 'uint64', value: 5n }), 0).value === 'bigint');
});

Deno.test('ENUM param round-trip (D11)', () => {
  const labels = ['small', 'medium', 'large'];
  const dec = decodeValue(encodeParam({ kind: 'enum', value: 'medium', labels }), 0);
  assert(dec.kind === Kind.Enum);
  assert(dec.value === 'medium');
  assert(JSON.stringify(dec.labels) === JSON.stringify(labels));
  let threw = false;
  try {
    encodeParam({ kind: 'enum', value: 'huge', labels });
  } catch {
    threw = true;
  }
  assert(threw);
});

Deno.test('DATE/TIME/TIMESTAMP/FLOAT32/FLOAT64 param round-trip (D5/D7/D8)', () => {
  const d = new Date(Date.UTC(2024, 0, 15));
  const decDate = decodeValue(encodeParam({ kind: 'date', value: d }), 0);
  assert(decDate.kind === Kind.Date);
  assert(decDate.value.getTime() === d.getTime());

  const nsInDay = (23 * 3600 + 59 * 60 + 59) * 1_000_000_000 + 999_000_000;
  const decTime = decodeValue(encodeParam({ kind: 'time', value: nsInDay }), 0);
  assert(decTime.kind === Kind.Time);
  assert(decTime.value === nsInDay);

  const ts = new Date('2024-06-15T10:30:00.000Z');
  const decTs = decodeValue(encodeParam({ kind: 'timestamp', value: ts }), 0);
  assert(decTs.kind === Kind.Timestamp);
  assert(decTs.value.getTime() === ts.getTime());
  assert(decodeValue(encodeParam(ts), 0).kind === Kind.TimestampTZ);

  const decF32 = decodeValue(encodeParam({ kind: 'float32', value: 1.5 }), 0);
  assert(decF32.kind === Kind.Float32);
  assert(decF32.value === 1.5);
  const decF64Nan = decodeValue(encodeParam({ kind: 'float64', value: NaN }), 0);
  assert(decF64Nan.kind === Kind.Float64);
  assert(Number.isNaN(decF64Nan.value));
});

Deno.test('INTERVAL param round-trip, including negative nanos (D6)', () => {
  const dec = decodeValue(encodeParam({ kind: 'interval', months: 14, days: 3, nanos: 4n * 3_600_000_000_000n }), 0);
  assert(dec.kind === Kind.Interval);
  assert(dec.value.months === 14 && dec.value.days === 3 && dec.value.nanos === 4n * 3_600_000_000_000n);
  const negDec = decodeValue(encodeParam({ kind: 'interval', months: 0, days: 0, nanos: -3_600_000_000_000n }), 0);
  assert(negDec.value.nanos === -3_600_000_000_000n);
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

Deno.test('follower-read routing classifiers', () => {
  for (const s of ['SELECT 1', '  select n from t', 'SHOW TASKS', 'WITH c AS (SELECT n FROM t) SELECT n FROM c']) {
    assert(isReadOnlySQL(s), s);
  }
  for (const s of ["INSERT INTO t (n) VALUES ('x')", 'UPDATE t SET n = 1', 'DELETE FROM t', 'EXPLAIN ANALYZE SELECT n FROM t', 'BEGIN']) {
    assert(!isReadOnlySQL(s), s);
  }
  const t = txnControl('  begin transaction ');
  assert(t.begin && !t.end);
  const c = txnControl('COMMIT');
  assert(!c.begin && c.end);
});

Deno.test('SetReadConsistency / NodeStatus wire codec', () => {
  const b = encodeSetReadConsistency(ReadConsistency.Bounded, 2500);
  assert(b.length === 9);
  assert(b[0] === 1);
  assert(new DataView(b.buffer, b.byteOffset).getBigUint64(1, true) === 2500n);
  assertThrows(() => encodeSetReadConsistency(9, 0));

  const role = new TextEncoder().encode('leader');
  const buf = new Uint8Array(2 + role.length + 25);
  const dv = new DataView(buf.buffer);
  dv.setUint16(0, role.length, true);
  buf.set(role, 2);
  const off = 2 + role.length;
  buf[off] = 0x03;
  dv.setBigUint64(off + 1, 7n, true);
  dv.setBigInt64(off + 9, 0n, true);
  dv.setBigUint64(off + 17, 0n, true);
  const st = decodeNodeStatus(buf);
  assert(st.role === 'leader');
  assert(st.healthy && st.hasLeader);
  assert(st.appliedLSN === 7n);
});

Deno.test('connectCluster requires an address', async () => {
  await assertRejects(() => connectCluster({ user: 'app', tls: {} }));
});
