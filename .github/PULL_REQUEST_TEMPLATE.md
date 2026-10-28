## Description

<!-- Provide a concise description of what this PR does and why it is needed. -->

## Associated Issue / Task

<!-- Link to an issue or reference a task from TODO.md (e.g. Closes #123 or Reference TODO log #345). -->

## Type of Change

- [ ] `feat`: New feature or capability
- [ ] `fix`: Bug fix
- [ ] `perf`: Performance optimization
- [ ] `refactor`: Code reorganization with no behavior change
- [ ] `docs`: Documentation updates or additions
- [ ] `test`: New or updated tests
- [ ] `chore`: Build, CI, dependencies, or repository maintenance

## Subsystems Affected

- [ ] Storage & WAL (`internal/storage`, `internal/wal`, `internal/recovery`, `internal/backup`)
- [ ] SQL Engine & Optimizer (`internal/sql`, `internal/executor`, `internal/catalog`)
- [ ] Protocol, Auth & Security (`internal/protocol`, `internal/auth`, `internal/oidc`, `internal/security`)
- [ ] Replication & Clustering (`internal/replication`, `internal/ha`)
- [ ] Client Drivers (`drivers/go`, `drivers/node`, `drivers/bun`, `drivers/python`, `drivers/php`, `drivers/ruby`)
- [ ] NextSQL Admin & Studio (`internal/admin`, `cmd/nextsql-admin`)
- [ ] CLI & Tooling (`cmd/nextsql`, `cmd/nextsqld`, `cmd/nextsql-bench`)
- [ ] Documentation & Website (`docs/`)

## Engineering & Quality Checklist (per `SKILLS.md`)

- [ ] **Compilation & Format**: Code compiles cleanly (`make build`), `gofmt -s` formatted, and `go vet ./...` reports 0 issues.
- [ ] **Tests Pass**: Relevant unit, integration, and package tests pass (`make test`).
- [ ] **Race Detector**: Ran with `-race` where concurrency or background tasks are involved (`go test -race`).
- [ ] **Persistence & WAL Integrity**: If persistent state changed, format is explicitly versioned, checksummed, crash/restart verified, and passes `make test-upgrade`.
- [ ] **Fail-Closed Security**: Authentication, RBAC, realm isolation, and cryptographic envelopes fail closed. No plain-text passwords or keys logged or exposed in URLs.
- [ ] **Resource Bounds**: All queues, buffers, allocations, timeouts, and worker pools are strictly bounded (no unbounded goroutines or memory allocations).
- [ ] **Driver Parity**: If wire protocol or driver semantics changed, all official client drivers maintain behavioral parity.
- [ ] **Documentation**: Updated relevant documentation in `docs/` and tracked in `TODO.md`.
