# Architecture

NextSQL is **one engine**, not four loosely connected stores. Catalog mutations, secondary indexes, HNSW graphs, inverted postings, nested collections, workflows, and CDC all go through the same WAL and transaction.

```arch
Native wire protocol → TLS 1.3 → authn → RBAC
        → SQL parser → binder / catalog
        → logical planner → cost optimizer
        → vectorized executor
              ├── relational   clustered B+Tree, RANGE/HASH/LIST
              ├── JSON         binary NSJB + path indexes
              ├── collections  STRUCT / ARRAY / MAP
              ├── vector       F32/F16/I8, sparse, HNSW/IVF/IVF-PQ
              ├── full-text    inverted index, BM25, analyzers
              └── geo          WGS84 shapes + GEOMETRY / GEOGRAPHY
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

Hybrid queries (structured filter + full-text + ANN in one statement) are a single physical plan under one cost model.

## Repository

```files
cmd/nextsqld              server
cmd/nextsql               CLI
cmd/nextsql-bench         official benchmark tool
cmd/nextsql-auth-broker   OIDC broker
cmd/nextsql-admin         Setup / Operations / Studio
internal/                 engine (storage, WAL, MVCC, SQL, crypto, HA, hosting, …)
drivers/                  Go, Node, Bun, PHP, Python, Ruby + shared JS codec
tests/                    integration, crash, HA
docs/                     format and operations notes
```

Format notes in the repository: [storage](https://github.com/bzync/nextsql/blob/main/docs/storage-format.md), [B+Tree](https://github.com/bzync/nextsql/blob/main/docs/btree.md), [WAL](https://github.com/bzync/nextsql/blob/main/docs/wal.md), [MVCC](https://github.com/bzync/nextsql/blob/main/docs/mvcc.md), [optimizer](https://github.com/bzync/nextsql/blob/main/docs/optimizer.md), [execution](https://github.com/bzync/nextsql/blob/main/docs/execution.md).
