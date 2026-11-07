# Client-side field encryption

NextSQL supports client-side column encryption with **randomized** (`NSCE1`) and **deterministic equality** (`NSCE2`) modes.

In client-side encryption, the application encrypts sensitive values before binding them over the wire, and decrypts the opaque results after reading them. **`nextsqld` never receives the field key or plaintext.**

The server stores the ciphertext as an opaque `STRING`, while the catalog preserves the logical plaintext type.

```sql
CREATE TABLE accounts (
    id UUID PRIMARY KEY,
    ssn STRING ENCRYPTED CLIENT,
    email TEXT ENCRYPTED CLIENT DETERMINISTIC
);
```

---

## Encryption modes

| Mode | Syntax | Cipher Suite | Predicate / Index Support |
|---|---|---|---|
| **Randomized** | `col TYPE ENCRYPTED CLIENT` | AES-256-GCM (12-byte random nonce) | Projection and parameter binding only. No WHERE filters, joins, or indexes. |
| **Deterministic** | `col TYPE ENCRYPTED CLIENT DETERMINISTIC` | RFC 5297 AES-SIV (HKDF-SHA256 derived) | Equality searches (`=`, `<>`, `!=`) and ordinary B-Tree / UNIQUE indexes. No ranges or substring search. |

### Supported logical types

The supported logical types for client encryption are:
`UUID`, `STRING`, `TEXT`, `BLOB`, `INT8`, `INT16`, `INT32`, `INT64`, `UINT8`, `UINT16`, `UINT32`, `UINT64`, `DECIMAL`, `TIMESTAMPTZ`, `JSON`, and `BOOL`.

Plaintext is capped at 1 MiB per field. SQL `NULL` values remain unencrypted SQL `NULL`.

---

## Cryptographic envelopes

### 1. `NSCE1` (Randomized Envelope)

Randomized fields produce an ASCII string beginning with `NSCE1.` followed by unpadded URL-safe base64 encoding of:
* Version `u8` (`= 1`)
* Suite `u8` (`= 1`, AES-256-GCM)
* Key ID (1–64 ASCII bytes)
* Logical type metadata
* 12-byte random nonce
* Ciphertext + 16-byte GCM authentication tag

**AEAD context binding**: The AES-GCM Associated Data cryptographically binds the `NSCE1.` prefix, exact database name, table name, column name, and header bytes. A ciphertext copied to another table, column, or database **fails to authenticate and will not decrypt**.

### 2. `NSCE2` (Deterministic Equality Envelope)

Deterministic fields produce an ASCII string beginning with `NSCE2.` followed by unpadded URL-safe base64 encoding of:
* Version `u8` (`= 2`)
* Suite `u8` (`= 2`, RFC 5297 AES-SIV)
* Key ID (1–64 ASCII bytes)
* Logical type metadata
* 16-byte synthetic IV / authentication tag
* AES-CTR ciphertext

> [!NOTE]
> `NSCE2` supports **equality-only matching** (`WHERE email = $1`). It deliberately leaks equality (frequency) within identical key IDs and contexts. Substring search, inequality ranges (`<`, `>`), and full-text searches fail closed.

---

## Driver implementations

All official NextSQL drivers ship built-in helpers for client-side field encryption.

### Go driver

```go
package main

import (
    "context"
    "github.com/bzync/nextsql/drivers/go"
    "github.com/bzync/nextsql/internal/sql/types"
)

func main() {
    ctx := context.Background()

    // Generate or load a 32-byte field key
    fieldKey, err := nextsql.GenerateFieldKey("accounts-ssn-v1")
    ring, err := nextsql.NewMemoryFieldKeyring(fieldKey)

    conn, err := nextsql.Open(nextsql.Config{
        Address:   "127.0.0.1:7210",
        Database:  "app",
        User:      "app",
        Password:  "secret",
        FieldKeys: ring,
    })

    // 1. Randomized encryption
    sealedSSN, err := conn.EncryptField(ctx, "accounts", "ssn", types.StringValue("123-45-6789"))
    _, err = conn.Exec(ctx, "INSERT INTO accounts (id, ssn) VALUES ($1, $2)", id, sealedSSN)

    // 2. Deterministic equality search
    emailToken, err := conn.EncryptFieldDeterministic(ctx, "accounts", "email", types.TextValue("user@example.com"))
    rows, err := conn.Query(ctx, "SELECT id FROM accounts WHERE email = $1", emailToken)
}
```

### Node.js, TypeScript, and Bun

```ts
import { FieldType, MemoryFieldKeyring, connect, generateFieldKey } from '@bzync/nextsql';

const ring = new MemoryFieldKeyring(generateFieldKey('accounts-ssn-v1'));
const conn = await connect({
  address: '127.0.0.1:7210',
  database: 'app',
  user: 'app',
  password: 'secret',
  tls: true,
  fieldKeys: ring,
});

// 1. Randomized encryption
const sealedSSN = await conn.encryptField('accounts', 'ssn', FieldType.String, '123-45-6789');
await conn.exec('INSERT INTO accounts (id, ssn) VALUES ($1, $2)', [id, sealedSSN]);

// 2. Deterministic equality search
const emailToken = await conn.encryptFieldDeterministic('accounts', 'email', FieldType.Text, 'user@example.com');
const rows = await conn.exec('SELECT id FROM accounts WHERE email = $1', [emailToken]);
```

### Python (3.10+)

```python
import nextsql
from nextsql import FieldType, MemoryFieldKeyring, generate_field_key

ring = MemoryFieldKeyring(generate_field_key("accounts-ssn-v1"))
conn = nextsql.connect(nextsql.Config(
    address="127.0.0.1:7210",
    database="app",
    user="app",
    password="secret",
    field_keys=ring,
))

# 1. Randomized encryption
sealed = conn.encrypt_field("accounts", "ssn", FieldType.STRING, "123-45-6789")
conn.exec("INSERT INTO accounts (id, ssn) VALUES ($1, $2)", [user_id, sealed])

# 2. Deterministic equality search
email_token = conn.encrypt_field_deterministic("accounts", "email", FieldType.TEXT, "user@example.com")
match = conn.exec("SELECT id FROM accounts WHERE email = $1", [email_token])
```

### PHP (8.1+)

```php
$ring = new NextSQL\MemoryFieldKeyring(
    NextSQL\FieldEncryption::generateKey('accounts-ssn-v1')
);
$conn = NextSQL\Client::connect([
    'address'   => '127.0.0.1:7210',
    'database'  => 'app',
    'user'      => 'app',
    'password'  => 'secret',
    'fieldKeys' => $ring,
]);

// 1. Randomized encryption
$sealed = $conn->encryptField('accounts', 'ssn', NextSQL\FieldType::string(), '123-45-6789');
$conn->exec('INSERT INTO accounts (id, ssn) VALUES ($1, $2)', [$id, $sealed]);

// 2. Deterministic equality search
$emailToken = $conn->encryptFieldDeterministic('accounts', 'email', NextSQL\FieldType::text(), 'user@example.com');
$match = $conn->exec('SELECT id FROM accounts WHERE email = $1', [$emailToken]);
```

### Ruby (3.0+)

```ruby
require "nextsql"

ring = NextSQL::MemoryFieldKeyring.new(
  NextSQL::FieldEncryption.generate_field_key("accounts-ssn-v1")
)
conn = NextSQL.connect(NextSQL::Config.new(
  address: "127.0.0.1:7210",
  database: "app",
  user: "app",
  password: "secret",
  field_keys: ring,
))

# 1. Randomized encryption
sealed = conn.encrypt_field("accounts", "ssn", NextSQL::FieldType::STRING, "123-45-6789")
conn.exec("INSERT INTO accounts (id, ssn) VALUES ($1, $2)", [user_id, sealed])

# 2. Deterministic equality search
email_token = conn.encrypt_field_deterministic("accounts", "email", NextSQL::FieldType::TEXT, "user@example.com")
match = conn.exec("SELECT id FROM accounts WHERE email = $1", [email_token])
```

---

## Key management (`FileFieldKeyring`)

Every official driver includes `FileFieldKeyring` — an atomic, durable, file-backed keyring implementation that stores keys in permission-`0600` files and survives process restarts.

```go
// 1. Create a persistent keyring file
ring, err := nextsql.CreateFileFieldKeyring("/etc/app/keys.nsfk", initialKey)

// 2. Online rotation (new writes use v2, reads resolve both v1 and v2)
newKey, err := nextsql.GenerateFieldKey("accounts-ssn-v2")
err = ring.Rotate(newKey)

// 3. Revocation (after re-encrypting existing rows, revoke v1; zeroes material on disk)
err = ring.Revoke("accounts-ssn-v1")
```

> [!IMPORTANT]
> **Backup requirement**:
> Field keys and `FileFieldKeyring` files are **never sent to the database**. You must back up your application's field keys independently from NextSQL physical backups. A database restore cannot decrypt client-encrypted columns without the corresponding field keys.
