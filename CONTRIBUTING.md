# Contributing

Thank you for considering a contribution to NextSQL.

NextSQL is a correctness-first native database. Changes must preserve its
architecture, security model, durability guarantees, and native semantics. It
is not a PostgreSQL or MySQL compatibility project.

## Before you start

Read:

- [`README.md`](README.md) — product overview
- [`USAGE.md`](USAGE.md) — current operator surface
- [`AGENTS.md`](AGENTS.md) and [`SKILLS.md`](SKILLS.md) — engineering contract
- [`TODO.md`](TODO.md) — implementation status (authoritative)
- [`ARCHITECTURE.md`](ARCHITECTURE.md) and the relevant `docs/*`

## Ground rules

Please do not:

- add PostgreSQL/MySQL compatibility hacks
- bypass the parser, binder, or planner
- weaken WAL/`fsync` behavior for benchmarks
- weaken authorization or invent cryptography
- add unbounded workers, queues, or result buffers
- put an LLM in the query optimizer
- claim unsupported availability or security guarantees

## Development flow

1. Identify the owning phase in `TODO.md` and confirm it is not blocked.
2. Make the smallest coherent change.
3. Add tests for the behavior you changed.
4. Run race, fuzz, crash, or HA tests where they apply.
5. Update the relevant `docs/*` and `CHANGELOG.md` for notable shipped behavior.
6. Update `TODO.md` only when evidence supports the status change.

```bash
go test ./...
go test -race ./...                    # needs a C compiler
go test ./tests/integration ./tests/crash ./tests/ha
```

Add targeted fuzzing and benchmarks when you touch an untrusted decoder or a
hot path.

## Pull requests

A useful PR explains:

- the problem and the design
- affected subsystems
- impact on persistence, WAL/recovery, protocol, security, and RBAC
- tests and benchmarks run
- documentation changed

## Persistent formats and protocol

Any on-disk format change needs an explicit version, upgrade behavior,
corruption validation, encryption, recovery, backup/PITR behavior, and tests.

NSQL protocol changes need versioning, old/new driver behavior, capability
negotiation, malformed-packet handling, size limits, and cancellation.

SQL features should flow through parser → binder → catalog → planner →
optimizer → executor → WAL/MVCC → recovery → replication → RBAC → protocol →
EXPLAIN → tests → docs. Skip a layer only when it is genuinely unaffected.

## Performance

Do not trade correctness, durability, or security for speed. Vector and ANN
results must report recall@K alongside latency.

## Security

Do not commit secrets. Report vulnerabilities through [`SECURITY.md`](SECURITY.md).

## License

By contributing, you confirm you have the right to submit the work under this
repository's license.
