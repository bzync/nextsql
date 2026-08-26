# Architecture

NextSQL is **one engine**, not four loosely connected stores. Catalog mutations, secondary indexes, HNSW graphs, and inverted postings all go through the same WAL and transaction.

```text
Native wire protocol → TLS 1.3 → authn → authz
        → SQL parser → binder / catalog
        → logical planner → cost optimizer
        → vectorized executor
              ├── relational   clustered B+Tree
              ├── JSON         binary NSJB + path indexes
              ├── vector       VECTOR<F32,N>, flat + HNSW
              ├── full-text    inverted index, BM25
              └── geo          POINT / BOX / LINESTRING / POLYGON
        → MVCC + row/range locks + UNDO
        → REDO WAL (group commit, fsync)
        → buffer manager
        → AES-256-GCM sealed pages
```

## Hard rules

| Rule | Implication |
|---|---|
| NextSQL-native formats | Own page format, SQL dialect, wire protocol, drivers, optimizer, txn model, encryption |
| Encryption by default | Persistent user data is never readable plaintext in production configuration |
| Established crypto only | AES-256-GCM. No custom cipher, hash, MAC, KDF, or AEAD |
| Envelope encryption | Separate DEKs for pages, WAL, UNDO, backups, vector, full-text, temp, replication |
| Clustered B+Tree first | Primary leaves hold rows. Secondary indexes hold secondary key + primary key |
| Vectorized execution | Batches, not row-at-a-time as the primary model |
| Bounded resources | No unbounded goroutines, allocations, or result materialization |
| Raft, not a new consensus | HA only after single-node durability is proven |
| Deterministic optimizer | No LLM as the primary planner |
| Honest threat model | A live unlocked host with keys in RAM can expose plaintext |

Logical page size is 16 KiB. On-disk and on-wire formats are versioned from day one. Unknown versions fail closed.

## Repository

```text
cmd/nextsqld          server
cmd/nextsql           CLI
cmd/nextsql-bench     official benchmark tool
internal/             engine (storage, WAL, MVCC, SQL, crypto, HA, …)
drivers/              Go, Node, Bun, Deno, PHP + shared JS codec
tests/                integration, crash, HA
docs/                 format and operations notes
```

Format notes in the repository: [storage](https://github.com/bzync/nextsql/blob/main/docs/storage-format.md), [B+Tree](https://github.com/bzync/nextsql/blob/main/docs/btree.md), [WAL](https://github.com/bzync/nextsql/blob/main/docs/wal.md), [MVCC](https://github.com/bzync/nextsql/blob/main/docs/mvcc.md), [optimizer](https://github.com/bzync/nextsql/blob/main/docs/optimizer.md), [execution](https://github.com/bzync/nextsql/blob/main/docs/execution.md).
