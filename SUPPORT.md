# Support

NextSQL **0.0.1** is a preview release of a new database engine. It is actively
developed and under measurement. Do not treat preview behavior as a production
guarantee.

## What 0.0.1 includes

- The NextSQL engine, CLI, and native NSQL protocol
- Official drivers for Go, Node.js, Bun, PHP, Python, and Ruby
- Encrypted storage, WAL, MVCC, backup/restore, PITR, and Raft HA
- Relational SQL, JSON, full-text search, vectors, geospatial types, and hybrid queries
- Schema lifecycle, `WORKFLOW` / `TRIGGER` / `SCHEDULE` / `TASK`, and committed CDC
- The virtual `system` catalog and workload governance
- `nextsql setup` / `nextsql lifecycle` and NextSQL Admin (Setup, Operations, Studio)

Exact implementation status is in [`TODO.md`](TODO.md). Hard limits are in
[`docs/limits.md`](docs/limits.md).

## Platforms

Release packages are **Linux amd64** (`.deb`, `.tar.gz`, `.run`). Docker images
cover `linux/amd64` and `linux/arm64`.

On Windows, run those Linux packages inside **WSL 2**. Native Windows and WSL 1
are not supported. Keep the data directory on the Linux filesystem, not a
mounted Windows drive such as `/mnt/c`.

macOS packages are not a supported path.

## Reporting a bug

Please include:

- NextSQL version or commit
- OS and architecture
- CPU, RAM, filesystem, and storage
- The command or config that failed
- Steps to reproduce
- Logs with secrets removed
- Expected and observed behavior

Open an issue at <https://github.com/bzync/nextsql/issues>.

## Security issues

Do not file a public issue for an exploitable vulnerability. Follow
[`SECURITY.md`](SECURITY.md).

## Data safety

Before an upgrade, repair, or any destructive operation:

1. Take a verified backup.
2. Test a restore where you can.
3. Read [`COMPATIBILITY.md`](COMPATIBILITY.md).

## What support does not mean

Support for this preview does not imply guaranteed zero downtime, absolute
security, zero possibility of data loss outside tested assumptions, or
compatibility with PostgreSQL, MySQL, or MariaDB.
