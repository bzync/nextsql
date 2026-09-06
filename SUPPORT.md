# NextSQL Support Policy

## Current Status

NextSQL is currently:

```text
0.1.0-dev
```

It is an active development database engine under measurement.

Do not treat development-stage behavior as a general production guarantee.

---

## Supported Surfaces

Current supported/implemented surfaces include:

- NextSQL engine;
- CLI;
- native NSQL protocol;
- official drivers;
- backup/restore;
- PITR;
- Raft HA;
- relational SQL;
- JSON;
- full-text;
- vector;
- hybrid queries;
- geospatial;
- schema lifecycle/maintenance;
- WORKFLOW/TRIGGER/SCHEDULE/TASK and committed CDC;
- RANGE/HASH/LIST partitioning and follower-read routing;
- the virtual `system` schema and workload governance;
- seven official drivers (Go, Node.js/TypeScript, Bun, Deno, PHP, Python,
  Ruby);
- `nextsql setup`/`nextsql lifecycle` automation;
- the completed NextSQL Admin Operations-mode MVP;
- the NextSQL Admin Setup-mode M1 flow.

Exact status is authoritative in `TODO.md`.

---

## Planned but Not Yet Supported as Shipped Features

Current open product work includes:

- the remaining P28 installer gate: richer wizard flows, packaging integration,
  silent and cross-platform execution, and accessibility validation;
- production-gating the broader multi-database hosting track beyond completed
  M2 routing and landed M3 suspend/drop;
- NextSQL Admin's Studio mode (NextSQL Studio);
- NextSQL Intelligence/RAG.

---

## Platforms

Only platforms tested by the current release process should be described as supported.

Current packaging work includes Linux and Windows artifacts. Linux `.tar.gz`
and `.run` paths have been live-tested on amd64; `.deb` metadata/layout has
been inspected but not installed system-wide. `.rpm` and Windows artifacts
have not been executed in the current environment. Checksums are produced;
release signing is not yet implemented.

Production support should be declared per release, not assumed from build scripts alone.

---

## Go Version

Development baseline:

```text
Go 1.22+
```

Update this file when minimum supported Go versions change.

---

## Support Expectations

For bug reports, include:

- NextSQL version/commit;
- OS;
- architecture;
- CPU/RAM;
- filesystem/storage;
- command/config;
- reproduction;
- logs with secrets removed;
- expected behavior;
- observed behavior.

---

## Security Issues

Use `SECURITY.md`.

Do not publish active exploitable vulnerabilities before coordinated remediation.

---

## Data Safety

Before upgrade, repair, or destructive operations:

- make a verified backup;
- test restore where practical;
- review compatibility notes.

---

## Unsupported Claims

Support does not imply:

- guaranteed zero downtime;
- zero possibility of data loss outside tested assumptions;
- absolute security;
- compatibility with PostgreSQL/MySQL/MariaDB.
