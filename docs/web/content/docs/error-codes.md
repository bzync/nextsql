# Public error codes & taxonomy

NextSQL defines a stable public error taxonomy using uppercase `ERR_*` identifiers.

**Do not match error message strings in application code.** Always branch on the structured error code.

Every official NextSQL driver negotiates the `public error codes` capability and exposes the stable public error code alongside the internal error class:

| Driver | Legacy Class | Stable Public Code | Exception / Error Type |
|---|---|---|---|
| **Go** | `nerr.Error.Code` | `nerr.Error.Public` | `*nerr.Error` |
| **Node.js / Bun** | `err.code` | `err.publicCode` | `NextSQLError` |
| **PHP (8.1+)** | `$e->errorCode` | `$e->publicCode` | `NextSQL\NextSQLException` |
| **Python (3.10+)** | `err.error_code` | `err.public_code` | `nextsql.Error` |
| **Ruby (3.0+)** | `err.error_code` | `err.public_code` | `NextSQL::Error` |

---

## Error taxonomy reference

| Public Code | Internal Class | Meaning | Retry Guidance |
|---|---|---|---|
| **`ERR_SERIALIZATION`** | `serialization` | `SERIALIZABLE` or `SNAPSHOT` transaction conflict (concurrent write conflict). | **Retryable**: Rollback the entire transaction and retry with backoff. |
| **`ERR_DEADLOCK`** | `deadlock` | Deadlock detected across row or table locks; the victim transaction was aborted. | **Retryable**: Rollback and retry the transaction. |
| **`ERR_CONFLICT`** | `conflict` | Pipeline conflict on a single-flight connection (e.g. executing before previous rows closed). | Non-retryable without query flow fix. |
| **`ERR_UNAVAILABLE`** | `unavailable` | Cluster currently has no elected Raft leader or is undergoing failover. | **Retryable**: Wait briefly for leader election and retry. |
| **`ERR_CANCELED`** | `canceled` | Query exceeded statement timeout (`statement_timeout_ms`) or was explicitly killed. | Check query complexity or timeout budget. |
| **`ERR_FOREIGN_KEY`** | `foreign_key` | Constraint violation: missing parent row, `RESTRICT` children exist, or invalid `SET DEFAULT`. | Non-retryable (application bug or data conflict). |
| **`ERR_ALREADY_EXISTS`** | `already_exists` | Unique constraint violation (`PRIMARY KEY` or `UNIQUE` index collision) or duplicate table name. | Non-retryable. |
| **`ERR_NOT_FOUND`** | `not_found` | Table, column, index, partition, or database does not exist. | Non-retryable. |
| **`ERR_UNAUTHORIZED`** | `unauthorized` | Authentication failed: invalid password, expired token, or invalid certificate. | Refresh token / re-authenticate. |
| **`ERR_FORBIDDEN`** | `forbidden` | RBAC permission denied: principal lacks required grant for this table or action. | Non-retryable (permissions must be granted). |
| **`ERR_INVALID_ARGUMENT`**| `invalid_argument` | Parameter type mismatch, value out of range, or invalid function arguments. | Non-retryable. |
| **`ERR_SYNTAX`** | `syntax` | SQL syntax error or unquoted keyword conflict. | Fix SQL query. |
| **`ERR_EXHAUSTED`** | `exhausted` | Resource limit hit: FK cascade touched >100,000 rows, join spill limit reached, or result memory exceeded. | Optimize query or increase configured resource limit. |
| **`ERR_PAGE_FULL`** | `page_full` | Row or record exceeds slotted page capacity. | Use off-row types (e.g. TEXT, BLOB, VECTOR). |
| **`ERR_CORRUPTION`** | `corruption` | Page checksum mismatch, corrupted B+Tree invariant, or damaged disk file. | **Stop writes**: Restore from backup and inspect disk. |
| **`ERR_CRYPTO`** | `crypto` | AES-256-GCM authentication failure, invalid unlock key, or client encryption context mismatch. | Verify encryption key or keyring context. |
| **`ERR_IO`** | `io` | Operating system disk write/read failure or filesystem error. | Inspect OS disk and permissions. |
| **`ERR_PROTOCOL`** | `protocol` | Wire protocol packet framing error or unsupported packet length. | Driver version mismatch or network corruption. |
| **`ERR_INTERNAL`** | `internal` | Unhandled engine invariant error. | Report as a bug. |

---

## Handling retries in application code

### Transaction conflict retry pattern (JavaScript / TypeScript)

```ts
import { connect, NextSQLError } from '@bzync/nextsql';

async function runTransactionWithRetry(conn, maxAttempts = 5) {
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      await conn.exec('BEGIN TRANSACTION SERIALIZABLE;');
      
      // Perform transactional queries
      await conn.exec('UPDATE accounts SET balance = balance - 100 WHERE id = $1;', [fromId]);
      await conn.exec('UPDATE accounts SET balance = balance + 100 WHERE id = $1;', [toId]);
      
      await conn.exec('COMMIT;');
      return;
    } catch (err) {
      await conn.exec('ROLLBACK;').catch(() => {});
      
      if (err instanceof NextSQLError && 
         (err.publicCode === 'ERR_SERIALIZATION' || err.publicCode === 'ERR_DEADLOCK')) {
        // Exponential backoff before retrying
        await new Promise(res => setTimeout(res, Math.random() * (2 ** attempt) * 50));
        continue;
      }
      throw err; // Non-retryable error
    }
  }
  throw new Error('Transaction exceeded maximum retry attempts');
}
```

### Python retry pattern

```python
import time
import random
import nextsql

def execute_with_retry(conn, max_attempts=5):
    for attempt in range(max_attempts):
        try:
            conn.exec("BEGIN TRANSACTION SERIALIZABLE;")
            conn.exec("UPDATE accounts SET balance = balance - 100 WHERE id = $1;", [from_id])
            conn.exec("UPDATE accounts SET balance = balance + 100 WHERE id = $1;", [to_id])
            conn.exec("COMMIT;")
            return
        except nextsql.Error as e:
            try:
                conn.exec("ROLLBACK;")
            except Exception:
                pass

            if e.public_code in ("ERR_SERIALIZATION", "ERR_DEADLOCK", "ERR_UNAVAILABLE"):
                time.sleep((2 ** attempt) * 0.05 + random.uniform(0, 0.05))
                continue
            raise
    raise RuntimeError("Transaction retry budget exhausted")
```
