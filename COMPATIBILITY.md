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
- Studio/Manager APIs.

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

Catalog table descriptors are `NSCT` v3 when written by this version. V3 adds
one validated CDC image-policy byte; v1/v2 readers remain supported and map to
the key-only default. Older binaries that support only v2 must not be used for
downgrade after a v3 catalog write without a format-aware migration or a
pre-upgrade backup.

---

## 4. Wire Protocol Compatibility

NSQL is authoritative.

Protocol evolution should preserve existing clients where practical through:

- protocol versions;
- capability negotiation;
- explicit unsupported-feature errors;
- backward-compatible field additions where safe.

Breaking changes require explicit versioning.

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
