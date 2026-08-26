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
- schema lifecycle/maintenance.

Exact status is authoritative in `TODO.md`.

---

## Planned but Not Yet Supported as Shipped Features

Until their phases close:

- WORKFLOW/TRIGGER/SCHEDULE/TASK;
- CDC;
- native physical partitioning;
- follower reads;
- Vector Engine 2.0;
- Full-text Search 2.0;
- Security 2.0 extensions;
- System Catalog 2.0;
- Workload Governance 2.0;
- NextSQL Manager;
- NextSQL Studio;
- NextSQL Intelligence/RAG.

---

## Platforms

Only platforms tested by the current release process should be described as supported.

Current packaging work includes Linux and Windows artifacts.

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
