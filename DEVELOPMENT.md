# NextSQL Development Guide

> Local development, build, test, fuzz, benchmark, and debugging workflow.

---

## 1. Prerequisites

Primary implementation language:

```text
Go
```

Current baseline:

- Go 1.22+;
- supported C compiler when running race-enabled tooling that requires it;
- Git;
- Linux recommended for engine development and production-like testing.

---

## 2. Clone and Build

```bash
git clone https://github.com/bzync/nextsql.git
cd nextsql

go build ./...
```

Build primary binaries:

```bash
go build -o nextsql ./cmd/nextsql
go build -o nextsqld ./cmd/nextsqld
go build -o nextsql-bench ./cmd/nextsql-bench
```

---

## 3. Required Documents

Before non-trivial work, read:

```text
AGENTS.md
SKILLS.md
TODO.md
ROADMAP.md
PROJECT.md
ARCHITECTURE.md
```

Always verify the owning phase in `TODO.md`.

---

## 4. Development Priority

Use:

```text
Correctness
→ durability
→ security
→ integrity
→ availability
→ latency
→ throughput
→ efficiency
→ developer experience
→ features
```

Never trade an earlier property for a later one.

---

## 5. Run Locally

Initialize:

```bash
printf 'secret\n' > /tmp/nextsql.pw
chmod 600 /tmp/nextsql.pw

./nextsql init \
  --data-dir /tmp/nextsql-data \
  --key-file /tmp/nextsql-root.key \
  --user app \
  --password-file /tmp/nextsql.pw
```

Start server:

```bash
./nextsqld \
  --data-dir /tmp/nextsql-data \
  --key-file /tmp/nextsql-root.key \
  --listen 127.0.0.1:7210 \
  --user app \
  --password-file /tmp/nextsql.pw
```

Execute:

```bash
./nextsql exec \
  --addr 127.0.0.1:7210 \
  --user app \
  --password-file /tmp/nextsql.pw \
  --insecure \
  -c "SELECT 1"
```

---

## 6. Test Workflow

Baseline:

```bash
go test ./...
go test -race ./...
```

Run targeted integration suites where applicable:

```bash
go test ./tests/integration
go test ./tests/crash
go test ./tests/ha
```

A change may additionally require:

- restart tests;
- crash injection;
- WAL/recovery tests;
- transaction tests;
- concurrency tests;
- fuzz tests;
- Raft/failover tests;
- backup/restore tests;
- PITR tests;
- tenant-isolation tests;
- driver tests;
- protocol tests;
- resource-limit tests;
- benchmarks.

---

## 7. Fuzzing

Fuzz untrusted inputs such as:

- SQL parser inputs;
- protocol packets;
- page decoders;
- WAL records;
- JSON;
- persistent metadata formats.

Example:

```bash
go test -fuzz=Fuzz ./path/to/package
```

Never add an untrusted binary decoder without fuzz coverage.

---

## 8. Benchmarking

Use `nextsql-bench` for official measurements.

```bash
./nextsql-bench --quick
./nextsql-bench --slo
```

Official measurements keep:

- encryption;
- WAL;
- fsync;
- authentication;
- checksums;
- MVCC

enabled.

Record hardware/context with results.

Vector benchmarks must report recall with latency.

---

## 9. SQL Feature Development

Normally follow:

```text
grammar
→ lexer/parser/AST
→ binder
→ catalog
→ logical plan
→ optimizer
→ physical plan
→ executor
→ WAL/MVCC
→ recovery
→ replication
→ RBAC/tenant
→ protocol/drivers
→ EXPLAIN
→ metrics
→ tests
→ docs
```

---

## 10. Persistent Feature Development

Every new persistent structure must define:

- versioned format;
- validation;
- encryption;
- allocation;
- reclamation;
- WAL behavior;
- recovery;
- backup/restore;
- PITR;
- replication;
- upgrade behavior;
- fuzzing;
- diagnostics.

---

## 11. Concurrency Rules

Do not create:

- unbounded goroutine pools;
- hidden goroutine ownership;
- unbounded queues;
- unbounded result buffering.

Use:

- context cancellation;
- explicit ownership;
- bounded workers;
- backpressure;
- resource accounting.

---

## 12. Error Handling

Prefer:

- typed errors;
- explicit failure modes;
- fail-closed security behavior;
- deterministic errors for protocol/public paths.

Avoid string-matching error contracts.

---

## 13. Secrets

Never commit:

- root keys;
- passwords;
- TLS private keys;
- tokens.

Never log them.

Use local ignored files for developer secrets.

---

## 14. Documentation Updates

Change the correct document:

```text
TODO.md         status
PROJECT.md      intended product scope
TODO.md         status, sequencing, dependencies, and gates
ROADMAP.md      simplified non-authoritative roadmap
SKILLS.md       engineering contract
ARCHITECTURE.md architecture
USAGE.md        shipped user-facing behavior
README.md       overview
CHANGELOG.md    shipped notable change
```

---

## 15. Before Claiming Completion

- [ ] build succeeds;
- [ ] relevant tests pass;
- [ ] race tests pass;
- [ ] crash/restart behavior verified;
- [ ] WAL/recovery verified;
- [ ] RBAC/tenant checks verified;
- [ ] resource limits tested;
- [ ] benchmarks correct where applicable;
- [ ] docs updated;
- [ ] `TODO.md` status remains truthful.
