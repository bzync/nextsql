# Client-encrypted fields

Status: **experimental P25 increment**. The SQL/catalog/server path and helpers
for Go, Node.js/TypeScript, Bun, Deno, and PHP are implemented. PITR and
replication/failover are tested (exact-ciphertext restore-to-target-LSN; no
lost acknowledged ciphertext across leader failover). Every official driver
also ships a durable, atomic, file-backed keyring (`FileFieldKeyring`) so
rotation and revocation persist across process restarts — see "Rotation,
revocation, and recovery" below.

## Contract

```sql
CREATE TABLE accounts (
    id UUID PRIMARY KEY,
    ssn STRING ENCRYPTED CLIENT
);
```

The client encrypts a non-NULL value before binding it and decrypts the opaque
result after reading it. `nextsqld` never receives the field key or plaintext.
It stores the ciphertext as a physical `STRING`, while `system.columns` and the
catalog preserve the logical plaintext type.

The v1 logical types are `UUID`, `STRING`, `TEXT`, `BLOB`, `INT8`, `INT16`,
`INT32`, `INT64`, `UINT8`, `UINT16`, `UINT32`, `UINT64`, `DECIMAL`,
`TIMESTAMPTZ`, `JSON`, and `BOOL`. `INT8..64`/`UINT8..64` plaintext uses the
same fixed-width raw-byte shape as the server's own row encoding (not the
length-prefixed shape `STRING`/`BLOB`/`DECIMAL` use), so any official driver
can decrypt a field another driver encrypted. Plaintext is capped at 1 MiB.
SQL `NULL` remains SQL `NULL` and is not wrapped.

Only opaque storage, bare-column projection/`RETURNING`, NULL, parameters, and
direct ciphertext copies are allowed. Client-encrypted columns cannot be used
in predicates, joins, expressions, defaults, primary/foreign/partition keys,
indexes or `INCLUDE`, `SEARCH`, `FACET`, grouping, ordering, `DISTINCT`, or set
operations. Table/column rename, partition attach/detach, and legacy-tenant
migration fail closed because they would change the authenticated context.

There is no deterministic or searchable mode. Two encryptions of the same
value produce different ciphertexts. NextSQL will document and separately gate
any future searchable-encryption mode because equality or search tokens leak
information.

## `NSCE1` ciphertext

The portable value is ASCII `NSCE1.` followed by unpadded base64url of:

```text
version u8 (=1)
suite u8 (=1, AES-256-GCM)
key-id length u8 (1..64)
key id bytes ([A-Za-z0-9._-])
logical type kind u8 + precision u16le + scale u16le + vector tag u8
random nonce [12]byte
ciphertext || GCM tag [16]byte
```

AES-256-GCM associated data binds the `NSCE1.` prefix, length-delimited exact
database/table/column names, and the public header except the random nonce.
Consequently, a ciphertext copied to a different database, table, or column
does not authenticate. The server validates only the bounded structure and
logical type before persistence; only a client holding the key can
authenticate it. Wrong keys, revoked key ids, context changes, truncation, and
tampering fail closed without returning partial plaintext.

The server can still observe ciphertext length, public key id and logical type,
database/table/column names, row existence, NULLness, access patterns, and
which rows a client reads or rewrites. Database and WAL page encryption does
not hide this metadata from a live server. A compromised client process, field
key provider, or unlocked host can expose plaintext.

## Go driver

`Config.FieldKeys` is separate from the database root `Config.KeyProvider` and
is never sent over NSQL. Production applications should implement
`FieldKeyProvider` using their secret manager or KMS integration. The bounded
`MemoryFieldKeyring` is a convenience for already-loaded keys; it is not
durable key storage.

```go
fieldKey, err := nextsql.GenerateFieldKey("accounts-ssn-v1")
ring, err := nextsql.NewMemoryFieldKeyring(fieldKey)

conn, err := nextsql.Open(nextsql.Config{
    Address:   "127.0.0.1:7210",
    Database:  "app",
    User:      "app",
    Password:  password,
    FieldKeys: ring,
})

sealed, err := conn.EncryptField(ctx, "accounts", "ssn",
    types.StringValue("123-45-6789"))
_, err = conn.Exec(ctx,
    "INSERT INTO accounts (id, ssn) VALUES ($1, $2)", id, sealed)

// After selecting the opaque ssn value into sealedResult:
plain, err := conn.DecryptField(ctx, "accounts", "ssn",
    types.String(), sealedResult)
```

Do not log keys or put them in a URL. The application must back up field keys
separately from NextSQL data.

## JavaScript, TypeScript, Bun, and Deno

The JS-family drivers expose the same async provider contract. `fieldKeys` is
kept on the connection config and never encoded into a URL or NSQL frame.
`MemoryFieldKeyring` is bounded and non-durable.

```ts
import { FieldType, MemoryFieldKeyring, connect, generateFieldKey } from 'nextsql';

const ring = new MemoryFieldKeyring(generateFieldKey('accounts-ssn-v1'));
const conn = await connect({
  address: '127.0.0.1:7210', database: 'app', user: 'app', password,
  tls, fieldKeys: ring,
});

const sealed = await conn.encryptField(
  'accounts', 'ssn', FieldType.String, '123-45-6789',
);
await conn.exec('INSERT INTO accounts (id, ssn) VALUES ($1, $2)', [id, sealed]);

const plain = await conn.decryptField(
  'accounts', 'ssn', FieldType.String, sealedResult,
);
```

Application/KMS providers implement `currentFieldKey(database, table, column)`
and `fieldKey(database, table, column, keyID)`, returning `{id, material}` where
`material` is exactly 32 bytes. Both methods may return a value or a Promise.
Node.js uses `node:crypto`; Bun and Deno use Web Crypto.

## PHP

PHP applications implement `NextSQL\FieldKeyProvider` or use the bounded,
non-durable `MemoryFieldKeyring`. Key material is a 32-byte binary string.

```php
$ring = new NextSQL\MemoryFieldKeyring(
    NextSQL\FieldEncryption::generateKey('accounts-ssn-v1')
);
$conn = NextSQL\Client::connect([
    'address' => '127.0.0.1:7210', 'database' => 'app',
    'user' => 'app', 'password' => $password, 'tls' => $tls,
    'fieldKeys' => $ring,
]);

$sealed = $conn->encryptField(
    'accounts', 'ssn', NextSQL\FieldType::string(), '123-45-6789'
);
$conn->exec('INSERT INTO accounts (id, ssn) VALUES ($1, $2)', [$id, $sealed]);

$plain = $conn->decryptField(
    'accounts', 'ssn', NextSQL\FieldType::string(), $sealedResult
);
```

The PHP implementation uses OpenSSL AES-256-GCM. Cross-driver fixtures verify
that Go-produced ciphertext decrypts in Node.js, Bun, Deno, and PHP, and that
Node.js-produced ciphertext decrypts in Go.

## Rotation, revocation, and recovery

Rotation changes the provider's current key for new writes while retaining old
key ids for reads. Re-encrypt every existing non-NULL value through the client,
verify completion, and only then revoke the old id. Revocation means the
provider refuses to resolve that key id; ciphertext under it becomes
undecryptable.

Two provider implementations ship in every official driver:

- `MemoryFieldKeyring` is bounded and in-process. `Rotate`/`Revoke` change
  only the in-memory map — nothing is written to disk, so the lifecycle does
  not survive a process restart. Useful when an application's own secret
  manager or KMS integration already loads keys into memory each time it
  starts.
- `FileFieldKeyring` is the reference **durable** implementation: rotation
  and revocation persist across restarts in a versioned, atomically written,
  0600 local file, using the same current/retired-with-overlap shape as the
  server's own `NSTK` signing-key lifecycle (`docs/security.md` "Online
  rotation"). A revoked key's material is overwritten with zeros on disk
  before the file is closed, and a revoked id can never be reused — rotating
  a new key under a previously revoked id fails closed.

Production applications with an existing secret manager or KMS should still
prefer implementing the `FieldKeyProvider` contract directly against that
system; `FileFieldKeyring` is for the local/self-hosted case where no
external KMS is available.

### `NSFK1` keyring format

```text
magic "NSFK" (4 bytes)
version u16le (=1)
key count u16le
per record:
  id length u8 (1..64) | id bytes ([A-Za-z0-9._-])
  created u64le (unix seconds)
  flags u8 (bit0 = current, bit1 = revoked)
  material [32]byte (all-zero when revoked)
```

Exactly one record is current and unrevoked; ids are unique; a revoked
record's material is always all-zero, and decoding fails closed on a mismatch
(non-zero material on a revoked record, a current record marked revoked, more
or less than one current record, truncation, or a duplicate id). The format
is identical across every driver, so a keyring file created by one language
opens correctly in another — proven by a cross-driver interop test (a
Go-produced fixture opened by the Node driver).

```go
kr, err := nextsql.CreateFileFieldKeyring("/etc/nextsql/accounts-ssn.nsfk", fieldKey)
// ... later, online rotation:
next, err := nextsql.GenerateFieldKey("accounts-ssn-v2")
err = kr.Rotate(next)
// after every existing value has been re-encrypted under the new key:
err = kr.Revoke("accounts-ssn-v1")
```

The JavaScript-family and PHP drivers expose the same
`create`/`open`/`rotate`/`revoke`/`reload`/`list` shape as async methods
(`FileFieldKeyring.create(path, current)`, `FileFieldKeyring.open(path)`).
Bun and Deno each implement the file I/O with their native file API
(`node:fs/promises` for Bun, `Deno.readFile`/`writeFile`/`rename` for Deno)
over the shared, I/O-free `encodeFieldKeyring`/`decodeFieldKeyring` codec in
`drivers/js/client-encryption.mjs`; Node's independent copy in
`drivers/node/client-encryption.js` uses `node:fs/promises` directly; PHP's
`NextSQL\FileFieldKeyring` uses `pack`/`unpack` and `rename`.

### Backup and recovery

Physical backup/restore preserves the exact ciphertext and contains no field
keys. Restoring under the same logical database/table/column names works when
the application independently restores the required field keys — including a
`FileFieldKeyring` file, which should be backed up alongside (or independently
of) the database using the operator's normal file-backup process, since it is
never sent to `nextsqld`. Restoring or migrating under different names
requires client-side decrypt/re-encrypt. Physical backup/restore and logical
export/import under the same authenticated names are covered, as are PITR
(restore to a target LSN preserves the exact pre-target ciphertext and
excludes later archived writes) and HA/failover (no lost acknowledged
ciphertext across a three-voter leader failover, correct post-failover
replication and decrypt).

### Production-gating sign-off (Phase 25)

**2026-09-02.** Every item-level blocker for `ENCRYPTED CLIENT` field-level
encryption is closed: the SQL/catalog/server surface, official-driver
encrypt/decrypt helpers, server-opaque storage, wrong-key/tamper behavior,
and backup/restore/PITR/replication/failover were already tested (see
`docs/security.md` "P25 Security 2.0 audit"), and the last open item — a
durable key-rotation/revocation lifecycle — is closed by `FileFieldKeyring`
in every official driver: create/reopen persistence, rotation overlap-read
correctness, revocation zeroing material on disk and failing closed,
revoked-id reuse rejection, corrupt-file rejection, and cross-driver format
interop are all covered by automated tests (`drivers/go/nextsql_test.go`,
`drivers/bun/nextsql.test.js`, `drivers/deno/nextsql_test.js`,
`drivers/node/nextsql.test.js`, `drivers/php/tests/unit.php`). The
phase-wide P25 exit gate (`docs/security.md` "P25 security review sign-off")
closed the same day once password hashing and audit hardening also landed, so
`ENCRYPTED CLIENT` is now formally production-gated — still `experimental`
in `system.capabilities` because no searchable/deterministic mode ships (see
"Searchable encryption" above), not because of any open correctness,
durability, or key-lifecycle blocker.
