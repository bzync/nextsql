# NextSQL Compatibility Policy

NextSQL is a native database and does not target PostgreSQL/MySQL/MariaDB compatibility.

This document defines compatibility expectations for NextSQL itself.

---

## 1. Compatibility Domains

Compatibility applies independently to:

- storage format;
- WAL format;
- catalog format;
- backup format;
- export format;
- NSQL wire protocol;
- SQL dialect;
- official drivers;
- CLI behavior;
- system catalog/introspection;
- NextSQL Admin APIs (Setup/Operations/Studio modes).

---

## 2. Persistent Format Versioning

Every persistent format must be versioned.

Unknown unsupported versions fail closed.

Changes must define:

- reader compatibility;
- writer compatibility;
- upgrade path;
- downgrade restrictions;
- rollback strategy;
- backup requirements.

Raw Go memory layout is never a stable disk contract.

---

## 3. WAL Compatibility

WAL changes must preserve or explicitly version:

- record encoding;
- LSN behavior;
- checksums/authentication;
- encryption metadata;
- replay semantics;
- crash recovery.

A binary must not replay a WAL format it cannot safely interpret.

`RecChange` (type 12) carries the independently versioned `NSCD` v1 logical
CDC envelope. Binaries predating this record type reject it rather than
silently ignoring change history. Downgrade across the first emitted
`RecChange` therefore requires a pre-change backup/WAL boundary or an explicit
format-aware migration.

Catalog table descriptors are `NSCT` v13 when written by this version and the
current binary reads v1 through v13. Successive trailers cover CDC image policy
(v3), physical partitions/stable IDs (v4/v5), vector index quantization and
ANN method/IVF/IVF-PQ metadata (v6–v8), full-text analyzer metadata (v9),
client-encrypted column metadata (v10), `ENUM` labels (v11), recursive
`STRUCT`/`ARRAY`/`MAP` descriptors (v12), and per-column client-encryption mode
(v13). Any catalog rewrite upgrades a readable older descriptor to v13. Older binaries must not open a data directory
after such a rewrite; restore a pre-upgrade backup or use an explicit
format-aware migration. See `docs/storage-format.md` for the byte-level window.

---

## 4. Wire Protocol Compatibility

NSQL is authoritative.

Protocol evolution should preserve existing clients where practical through:

- protocol versions;
- capability negotiation;
- explicit unsupported-feature errors;
- backward-compatible field additions where safe.

Breaking changes require explicit versioning.

The wire frame version remains NSQL v1. Realm selection, read-consistency,
node-status, CDC, and idempotent-query support are additive v1 frames or
trailing fields and are capability-gated. The virtual `system` schema has its
own column-contract capability, currently `system_schema_v4`.

---

## 5. Driver Compatibility

Official drivers should track supported server versions.

Drivers must not silently emulate unsupported server behavior.

Capability-sensitive features should fail explicitly when the server cannot support them.

---

## 6. SQL Compatibility

NextSQL defines its own semantics.

Existing documented NextSQL behavior should remain stable unless a deliberate versioned breaking change is approved.

Do not add hidden PostgreSQL/MySQL semantics merely for familiarity.

---

## 7. Backup Compatibility

Backup/restore must document:

- originating version;
- target version;
- required migration path;
- incompatible format changes;
- verification requirements.

A backup is not considered valid until verification and restore testing succeed.

---

## 8. Upgrade Policy

Before upgrade:

1. verify backup;
2. verify compatibility;
3. review migration notes;
4. stop unsupported downgrade assumptions;
5. test representative workload.

Persistent-format upgrades must be crash-safe.

---

## 9. Downgrade Policy

Downgrade is only supported when explicitly documented.

If a newer binary writes a newer persistent format, restoring a pre-upgrade backup may be required for rollback.

Never assume an older binary can safely open a newer data directory.

---

## 10. Breaking Changes

A breaking change must include:

- rationale;
- affected surface;
- version boundary;
- migration path;
- rollback plan;
- documentation;
- tests.
